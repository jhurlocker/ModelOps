import json
import os
import sqlite3
import time
import uuid
from datetime import datetime, timezone

from flask import Flask, g, jsonify, redirect, render_template_string, request, url_for

# --- Configuration (all overridable via environment variables / Secret) ---
DB_PATH = os.environ.get("DB_PATH", "/data/model-intake.db")
PIPELINE_NAMESPACE = os.environ.get("PIPELINE_NAMESPACE", "vllm")
PIPELINE_NAME = os.environ.get("PIPELINE_NAME", "model-intake-pipeline")
PIPELINE_SERVICE_ACCOUNT = os.environ.get("PIPELINE_SERVICE_ACCOUNT", "pipeline")
SHARED_WORKSPACE_PVC = os.environ.get("SHARED_WORKSPACE_PVC", "guidellm-output-pvc")
MANIFESTS_CONFIGMAP = os.environ.get("MANIFESTS_CONFIGMAP", "mmlu-manifest")
CUSTOM_MMLU_CONFIGMAP = os.environ.get("CUSTOM_MMLU_CONFIGMAP", "custom-mmlu")
# In-cluster URL of THIS app, used as the default approval-api-url param so
# the wait-for-approval Task (running as a pod inside the cluster) can reach us.
SELF_INTERNAL_URL = os.environ.get(
    "SELF_INTERNAL_URL", "http://model-intake.vllm.svc.cluster.local:8080"
)
DEFAULT_S3_ENDPOINT = os.environ.get(
    "DEFAULT_S3_ENDPOINT", "http://minio-service.s3-storage.svc.cluster.local:9000"
)
DEFAULT_S3_ACCESS_KEY = os.environ.get("DEFAULT_S3_ACCESS_KEY", "minio")
DEFAULT_S3_SECRET_KEY = os.environ.get("DEFAULT_S3_SECRET_KEY", "minio123")
DEFAULT_ADVISOR_ENDPOINT = os.environ.get("DEFAULT_ADVISOR_ENDPOINT", "")

app = Flask(__name__)

# --- SQLite storage (single-replica app; PVC-backed file) -------------------


def get_db():
    if "db" not in g:
        os.makedirs(os.path.dirname(DB_PATH) or ".", exist_ok=True)
        g.db = sqlite3.connect(DB_PATH)
        g.db.row_factory = sqlite3.Row
    return g.db


@app.teardown_appcontext
def close_db(exception=None):
    db = g.pop("db", None)
    if db is not None:
        db.close()


def init_db():
    os.makedirs(os.path.dirname(DB_PATH) or ".", exist_ok=True)
    conn = sqlite3.connect(DB_PATH)
    conn.execute(
        """
        CREATE TABLE IF NOT EXISTS plans (
            plan_id TEXT PRIMARY KEY,
            pipelinerun_name TEXT,
            model_id TEXT,
            model_name TEXT,
            target_namespace TEXT,
            requested_by TEXT,
            plan_status TEXT,
            recommendation_md TEXT,
            deployment_options TEXT,
            gpu_inventory TEXT,
            recommended_vllm_command TEXT,
            status TEXT DEFAULT 'pending',
            decided_by TEXT,
            decision_comment TEXT,
            created_at TEXT,
            updated_at TEXT
        )
        """
    )
    conn.execute(
        """
        CREATE TABLE IF NOT EXISTS runs (
            run_name TEXT PRIMARY KEY,
            model_id TEXT,
            model_name TEXT,
            requested_by TEXT,
            params_json TEXT,
            created_at TEXT
        )
        """
    )
    conn.commit()
    conn.close()


init_db()


def now_iso():
    return datetime.now(timezone.utc).isoformat()


# --- Kubernetes / Tekton integration -----------------------------------------

_k8s_api = None


def k8s_custom_api():
    """Lazily initialise the Kubernetes CustomObjectsApi (in-cluster config)."""
    global _k8s_api
    if _k8s_api is None:
        from kubernetes import client, config

        try:
            config.load_incluster_config()
        except Exception:
            # Allow `oc port-forward` / local kubeconfig testing outside the cluster.
            config.load_kube_config()
        _k8s_api = client.CustomObjectsApi()
    return _k8s_api


TEKTON_GROUP = "tekton.dev"
TEKTON_VERSION = "v1"


def create_pipelinerun(run_name, params):
    api = k8s_custom_api()
    body = {
        "apiVersion": "tekton.dev/v1",
        "kind": "PipelineRun",
        "metadata": {
            "name": run_name,
            "labels": {"app.kubernetes.io/created-by": "model-intake-ui"},
        },
        "spec": {
            "pipelineRef": {"name": PIPELINE_NAME},
            "params": [{"name": k, "value": v} for k, v in params.items()],
            "taskRunTemplate": {"serviceAccountName": PIPELINE_SERVICE_ACCOUNT},
            "timeouts": {"pipeline": "2h0m0s"},
            "workspaces": [
                {
                    "name": "shared-workspace",
                    "persistentVolumeClaim": {"claimName": SHARED_WORKSPACE_PVC},
                },
                {"name": "manifests", "configMap": {"name": MANIFESTS_CONFIGMAP}},
                {"name": "custom-mmlu", "configMap": {"name": CUSTOM_MMLU_CONFIGMAP}},
            ],
        },
    }
    return api.create_namespaced_custom_object(
        group=TEKTON_GROUP,
        version=TEKTON_VERSION,
        namespace=PIPELINE_NAMESPACE,
        plural="pipelineruns",
        body=body,
    )


def list_pipelineruns(limit=25):
    api = k8s_custom_api()
    try:
        resp = api.list_namespaced_custom_object(
            group=TEKTON_GROUP,
            version=TEKTON_VERSION,
            namespace=PIPELINE_NAMESPACE,
            plural="pipelineruns",
            label_selector="app.kubernetes.io/created-by=model-intake-ui",
        )
        items = resp.get("items", [])
        items.sort(
            key=lambda i: i.get("metadata", {}).get("creationTimestamp", ""),
            reverse=True,
        )
        return items[:limit]
    except Exception as e:
        app.logger.warning("Could not list PipelineRuns: %s", e)
        return []


def get_pipelinerun(name):
    api = k8s_custom_api()
    try:
        return api.get_namespaced_custom_object(
            group=TEKTON_GROUP,
            version=TEKTON_VERSION,
            namespace=PIPELINE_NAMESPACE,
            plural="pipelineruns",
            name=name,
        )
    except Exception as e:
        app.logger.warning("Could not get PipelineRun %s: %s", name, e)
        return None


def pipelinerun_status(pr):
    if not pr:
        return "Unknown"
    conditions = pr.get("status", {}).get("conditions", [])
    for c in conditions:
        if c.get("type") == "Succeeded":
            if c.get("status") == "True":
                return "Succeeded"
            if c.get("status") == "False":
                return "Failed: {}".format(c.get("reason", ""))
            return "Running: {}".format(c.get("reason", ""))
    return "Pending"


# --- Pipeline parameter defaults (mirrors model-intake-pipeline.yaml) ------

FORM_DEFAULTS = {
    "model-id": "ibm-granite/granite-3.3-2b-instruct",
    "model-name": "granite-2b",
    "model-version": "v1",
    "modelcar-image": "",
    "target-namespace": "vllm",
    "staging-namespace": "vllm-staging",
    # Gate 1 - Compliance & Artifact scan
    "modelcar-repo": "redhat-ai-services/modelcar-catalog",
    "artifact-scan-image": "registry.access.redhat.com/ubi9/python-311:latest",
    "artifact-cve-threshold": "critical",
    "ignore-unfixed": "true",
    "allowed-architectures": "amd64,x86_64",
    "context-length": "32768",
    "concurrency": "4",
    "allow-time-slicing": "true",
    "allow-mig": "false",
    "gpu-isolation-policy": "dedicated",
    "gpu-operator-namespace": "nvidia-gpu-operator",
    "clusterpolicy-name": "gpu-cluster-policy",
    "time-slicing-configmap": "modelops-time-slicing",
    "max-time-slices": "8",
    "advisor-endpoint": DEFAULT_ADVISOR_ENDPOINT,
    "advisor-secret-name": "gpu-advisor-credentials",
    "advisor-timeout-seconds": "180",
    "approval-api-url": SELF_INTERNAL_URL,
    "approval-poll-interval-seconds": "15",
    "approval-timeout-seconds": "3600",
    "release-name": "granite-2b",
    "chart-url": "https://redhat-ai-services.github.io/helm-charts/",
    "chart-version": "0.7.1",
    "values-content": "",
    "gpu-count-override": "",
    "hardware-profile-name": "gpu-profile",
    "hardware-profile-namespace": "redhat-ods-applications",
    "target": "http://granite-2b-predictor.vllm.svc.cluster.local:8080/v1",
    "staging-target": "http://granite-2b-predictor.vllm-staging.svc.cluster.local:8080/v1",
    "api-key": "",
    "garak-probes": "malwaregen.TopLevel",
    "garak-detectors": "",
    "max-seeds": "1",
    "parallel-attempts": "8",
    "severity-threshold": "block",
    "evalhub-url": "evalhub-redhat-ods-applications.apps.ocp.h64s4.sandbox324.opentlc.com",
    "evalhub-token": "",
    "guidellm-profile": "constant",
    "guidellm-rate": "4.0",
    "guidellm-max-seconds": "15",
    "guidellm-max-requests": "2",
    "huggingface-token": "",
    "custom-data": "False",
    "custom-filename": "no-file",
    "s3-api-endpoint": DEFAULT_S3_ENDPOINT,
    "s3-access-key-id": DEFAULT_S3_ACCESS_KEY,
    "s3-secret-access-key": DEFAULT_S3_SECRET_KEY,
    # Scan-result S3 (compliance/artifact + garak reports) - MinIO by default.
    "scan-s3-endpoint": DEFAULT_S3_ENDPOINT,
    "scan-s3-access-key-id": DEFAULT_S3_ACCESS_KEY,
    "scan-s3-secret-access-key": DEFAULT_S3_SECRET_KEY,
    "compliance-s3-bucket": "compliance-artifact-results",
    "security-s3-bucket": "security-scan-results",
    "s3-ui-route": "",
    "lm-eval-job-name": "mmlu-jurisprudence-eval-job",
    "lm-eval-custom": "False",
    "model-reg-author": "ModelOps Platform Team",
    "mr-server": "http://modelops-registry.rhoai-model-registries.svc.cluster.local",
    "mr-port": "8080",
    # Model access (RHOAI dashboard visibility via namespace RBAC).
    "authorized-viewers": "",
    "access-role": "view",
    # MaaS production deployment
    "maas-authorized-group": "system:authenticated",
}

# Grouping purely for form layout. Ordered to mirror the two-phase pipeline.
FORM_SECTIONS = [
    ("Model", ["model-id", "modelcar-image", "model-name", "model-version", "requested-by"]),
    ("Namespaces", ["target-namespace", "staging-namespace"]),
    (
        "Phase 1 Gate 1 - Compliance & Artifact Security Scan",
        [
            "modelcar-repo",
            "artifact-scan-image",
            "artifact-cve-threshold",
            "ignore-unfixed",
            "allowed-architectures",
        ],
    ),
    (
        "Phase 1 Gate 2 - Security Scan (Garak)",
        ["api-key", "garak-probes", "garak-detectors", "max-seeds", "parallel-attempts", "severity-threshold"],
    ),
    (
        "EvalHub",
        ["evalhub-url", "evalhub-token"],
    ),
    (
        "GPU Advisor + Time-Slicing (runs up-front)",
        [
            "context-length",
            "concurrency",
            "allow-time-slicing",
            "allow-mig",
            "gpu-isolation-policy",
            "gpu-operator-namespace",
            "clusterpolicy-name",
            "time-slicing-configmap",
            "max-time-slices",
            "advisor-endpoint",
            "advisor-secret-name",
            "advisor-timeout-seconds",
        ],
    ),
    (
        "Human Approval",
        ["approval-api-url", "approval-poll-interval-seconds", "approval-timeout-seconds"],
    ),
    (
        "Deploy",
        [
            "chart-url",
            "chart-version",
            "gpu-count-override",
            "hardware-profile-name",
            "hardware-profile-namespace",
            "values-content",
        ],
    ),
    (
        "Benchmark (GuideLLM, staging)",
        [
            "guidellm-profile",
            "guidellm-rate",
            "guidellm-max-seconds",
            "guidellm-max-requests",
            "huggingface-token",
            "custom-data",
            "custom-filename",
        ],
    ),
    ("Benchmark/Eval S3 Storage", ["s3-api-endpoint", "s3-access-key-id", "s3-secret-access-key"]),
    (
        "Scan-Result S3 Storage",
        [
            "scan-s3-endpoint",
            "scan-s3-access-key-id",
            "scan-s3-secret-access-key",
            "compliance-s3-bucket",
            "security-s3-bucket",
            "s3-ui-route",
        ],
    ),
    ("lm-eval", ["lm-eval-job-name", "lm-eval-custom"]),
    ("Model Registry", ["model-reg-author", "mr-server", "mr-port"]),
    (
        "Model Access (RHOAI dashboard visibility)",
        ["authorized-viewers", "access-role"],
    ),
    (
        "MaaS Production Deployment",
        ["maas-authorized-group"],
    ),
]

ALL_FORM_FIELDS = [f for _, fields in FORM_SECTIONS for f in fields] + ["requested-by"]

REQUIRED_FIELDS = {"model-id", "model-name", "evalhub-token"}


# --- Per-field help text (shown in a hover tooltip next to each field) --------
# Values may contain simple HTML (<b>, <code>, <br>) and are rendered with |safe.
FIELD_HELP = {
    # Model
    "model-id": "Hugging Face model ID to onboard. Used by the GPU advisor to "
    "size the model and as the served model name.<br><b>Example:</b> "
    "<code>ibm-granite/granite-3.3-2b-instruct</code>",
    "modelcar-image": "Optional. Full OCI/ModelCar image that holds the model "
    "weights. If set, it overrides the image derived from "
    "<code>modelcar-repo</code> + <code>model-id</code>. "
    "<code>oci://</code>/<code>docker://</code> prefixes are stripped."
    "<br><b>Example:</b> "
    "<code>quay.io/redhat-ai-services/modelcar-catalog:llama-3.2-1b-instruct</code>"
    "<br>Leave blank to auto-derive.",
    "model-name": "Deployment identity - single source of truth. Becomes the "
    "InferenceService / predictor name and the endpoint URLs "
    "(<code>&lt;model-name&gt;-predictor.&lt;ns&gt;.svc</code>).<br>"
    "<b>Must be a valid Kubernetes name:</b> lowercase, digits and "
    "<code>-</code> only, no dots. Auto-suggested from the model ID.",
    "model-version": "Version label recorded in the model registry for this "
    "onboarding run.<br><b>Example:</b> <code>v1</code>",
    "requested-by": "Your name / username. Recorded on the run and deployment "
    "plan for audit and shown in the approval queue.",
    # Namespaces
    "target-namespace": "Sandbox namespace where the model is deployed for the "
    "Phase 1 security scan, then torn down.<br><b>Default:</b> <code>vllm</code>",
    "staging-namespace": "Staging namespace where the model is deployed in "
    "Phase 2 (after approval) for benchmarking and registration."
    "<br><b>Default:</b> <code>vllm-staging</code>",
    # Phase 1 Gate 1 - Compliance & Artifact scan
    "modelcar-repo": "Quay repo (without tag) that holds ModelCar images. The "
    "image tag is derived from <code>model-id</code> unless "
    "<code>modelcar-image</code> is set.<br><b>Default:</b> "
    "<code>redhat-ai-services/modelcar-catalog</code>",
    "artifact-scan-image": "Container image used to run the Trivy-based "
    "compliance/artifact scan step.",
    "artifact-cve-threshold": "Highest CVE severity allowed before the scan gate "
    "fails.<br><b>Values:</b> <code>critical</code>, <code>high</code>, "
    "<code>medium</code>, <code>low</code>",
    "ignore-unfixed": "Ignore vulnerabilities that have no fix available yet."
    "<br><b>Values:</b> <code>true</code> / <code>false</code>",
    "allowed-architectures": "Comma-separated CPU architectures the model image "
    "is allowed to target.<br><b>Example:</b> <code>amd64,x86_64</code>",
    # Phase 1 Gate 2 - Security scan (Garak)
    "api-key": "Optional API key/token for the model endpoint being scanned by "
    "Garak. Leave blank if the in-cluster endpoint needs no auth.",
    "garak-probes": "Comma-separated Garak probes (attack modules) to run against "
    "the model.<br><b>Example:</b> <code>malwaregen.TopLevel</code>, "
    "<code>promptinject</code>, <code>dan.Dan_11_0</code>",
    "garak-detectors": "Optional explicit Garak detectors. Leave blank to use each "
    "probe's default detectors.",
    "max-seeds": "Number of prompt seeds per probe. Higher = more thorough but "
    "slower.<br><b>Example:</b> <code>1</code>",
    "parallel-attempts": "How many probe attempts Garak runs in parallel.<br>"
    "<b>Example:</b> <code>8</code>",
    "severity-threshold": "Garak result severity that halts the pipeline."
    "<br><b>Values:</b> <code>block</code>, <code>warn</code>, <code>off</code>",
    "evalhub-url": "Base URL of the EvalHub instance (FQDN only, no scheme)."
    "<br><b>Example:</b> <code>evalhub-redhat-ods-applications.apps.&lt;cluster&gt;</code>",
    "evalhub-token": "OpenShift OAuth token used to authenticate with the EvalHub "
    "API. Obtain with <code>oc whoami -t</code>."
    "<br><b>Required</b> for EvalHub requests.",
    # GPU advisor + time-slicing
    "context-length": "Max context (tokens) the advisor sizes the KV cache for and "
    "that vLLM serves with (<code>--max-model-len</code>).<br><b>Example:</b> "
    "<code>32768</code>",
    "concurrency": "Expected concurrent requests. Used by the advisor to size the "
    "KV cache.<br><b>Example:</b> <code>4</code>",
    "allow-time-slicing": "Allow the advisor to recommend sharing one physical GPU "
    "across models via NVIDIA time-slicing when no whole GPU is free."
    "<br><b>Values:</b> <code>true</code> / <code>false</code>",
    "allow-mig": "Allow the advisor to recommend MIG (hardware GPU partitioning)."
    "<br><b>Values:</b> <code>true</code> / <code>false</code>",
    "gpu-isolation-policy": "Preferred GPU isolation. <code>dedicated</code> avoids "
    "sharing unless <code>allow-time-slicing</code>/<code>allow-mig</code> is set "
    "and nothing else fits.<br><b>Values:</b> <code>dedicated</code>, "
    "<code>shared</code>",
    "gpu-operator-namespace": "Namespace of the NVIDIA GPU Operator, where the "
    "time-slicing ConfigMap is created.<br><b>Default:</b> "
    "<code>nvidia-gpu-operator</code>",
    "clusterpolicy-name": "Name of the NVIDIA ClusterPolicy CR patched to enable "
    "time-slicing.<br><b>Default:</b> <code>gpu-cluster-policy</code>",
    "time-slicing-configmap": "Name of the device-plugin time-slicing ConfigMap the "
    "advisor generates and the apply-gpu-sharing task applies.<br><b>Default:</b> "
    "<code>modelops-time-slicing</code>",
    "max-time-slices": "Upper bound on time-slice replicas the advisor will "
    "recommend for a single physical GPU.<br><b>Example:</b> <code>8</code>",
    "advisor-endpoint": "Optional URL of an external agentic GPU-advisor "
    "(OpenAI-compatible) endpoint. Leave blank to use the built-in local "
    "heuristic sizing.",
    "advisor-secret-name": "Name of a Secret in this namespace with an "
    "<code>api-key</code> key, used as a Bearer token for "
    "<code>advisor-endpoint</code>. Ignored if it does not exist.",
    "advisor-timeout-seconds": "HTTP timeout (seconds) when calling "
    "<code>advisor-endpoint</code>. Needs headroom for reasoning models."
    "<br><b>Example:</b> <code>180</code>",
    # Human approval
    "approval-api-url": "In-cluster URL of this intake app that the "
    "wait-for-approval task polls for the approve/reject decision. Usually leave "
    "as the default.",
    "approval-poll-interval-seconds": "How often (seconds) the pipeline polls for an "
    "approval decision.<br><b>Example:</b> <code>15</code>",
    "approval-timeout-seconds": "How long (seconds) the pipeline waits at the "
    "approval gate before timing out.<br><b>Example:</b> <code>3600</code> (1h)",
    # Deploy
    "chart-url": "Helm chart repository URL used to deploy the vLLM model server.",
    "chart-version": "Version of the vLLM Helm chart to deploy.<br><b>Example:</b> "
    "<code>0.7.1</code>",
    "gpu-count-override": "Force a specific GPU count for the deployment instead of "
    "the advisor's recommendation. Leave blank to use the plan.",
    "hardware-profile-name": "RHOAI hardware profile applied to the serving "
    "deployment.<br><b>Default:</b> <code>gpu-profile</code>",
    "hardware-profile-namespace": "Namespace of the hardware profile.<br>"
    "<b>Default:</b> <code>redhat-ods-applications</code>",
    "values-content": "Optional extra Helm <code>values.yaml</code> content "
    "(YAML) merged into the deployment. Leave blank for defaults.",
    # Benchmark (GuideLLM)
    "guidellm-profile": "GuideLLM load profile.<br><b>Values:</b> <code>constant</code>, "
    "<code>sweep</code>, <code>throughput</code>"
    "<br><b>Default:</b> <code>constant</code>",
    "guidellm-rate": "GuideLLM request rate (requests per second)."
    "<br><b>Example:</b> <code>4.0</code>",
    "guidellm-max-seconds": "Max benchmark duration (seconds) per rate step."
    "<br><b>Default:</b> <code>15</code>",
    "guidellm-max-requests": "Max benchmark requests per rate step."
    "<br><b>Default:</b> <code>2</code>",
    "huggingface-token": "Optional Hugging Face token, needed only if the "
    "target model endpoint requires auth. Leave blank otherwise.",
    "custom-data": "Use a custom benchmark dataset file instead of synthetic data."
    "<br><b>Values:</b> <code>True</code> / <code>False</code>",
    "custom-filename": "Filename of the custom benchmark dataset on the shared "
    "workspace. Used only when <code>custom-data</code> is <code>True</code>.",
    # Benchmark/Eval S3
    "s3-api-endpoint": "S3/MinIO endpoint where GuideLLM benchmark results are "
    "uploaded.",
    "s3-access-key-id": "Access key ID for the benchmark-results S3/MinIO bucket.",
    "s3-secret-access-key": "Secret access key for the benchmark-results S3/MinIO "
    "bucket.",
    # Scan-result S3
    "scan-s3-endpoint": "S3/MinIO endpoint where compliance and Garak security scan "
    "reports are uploaded.",
    "scan-s3-access-key-id": "Access key ID for the scan-results S3/MinIO bucket.",
    "scan-s3-secret-access-key": "Secret access key for the scan-results S3/MinIO "
    "bucket.",
    "compliance-s3-bucket": "Bucket for compliance/artifact scan reports.<br>"
    "<b>Default:</b> <code>compliance-artifact-results</code>",
    "security-s3-bucket": "Bucket for Garak security scan reports.<br>"
    "<b>Default:</b> <code>security-scan-results</code>",
    "s3-ui-route": "Optional override for the S3 browser UI route link recorded in "
    "the model registry. Leave blank to auto-resolve.",
    # lm-eval
    "lm-eval-job-name": "Name of the lm-eval (LM Evaluation Harness) Job. Only used "
    "if the lm-eval task is enabled.",
    "lm-eval-custom": "Use a custom MMLU task ConfigMap for lm-eval.<br>"
    "<b>Values:</b> <code>True</code> / <code>False</code>",
    # Model registry
    "model-reg-author": "Author/owner recorded on the model version in the RHOAI "
    "model registry.",
    "mr-server": "RHOAI model registry server URL (without port).<br><b>Example:</b> "
    "<code>http://modelops-registry.rhoai-model-registries.svc.cluster.local</code>",
    "mr-port": "Model registry server port.<br><b>Default:</b> <code>8080</code>",
    # Model access
    "authorized-viewers": "Comma-separated users/groups granted visibility of the "
    "deployed model in the RHOAI dashboard (via namespace RBAC). Prefix a group "
    "with <code>group:</code>.<br><b>Example:</b> "
    "<code>alice, bob, group:ml-team</code><br>Leave blank for no extra access.",
    "access-role": "Kubernetes role granted to the authorized viewers in the "
    "staging namespace.<br><b>Values:</b> <code>view</code> (read-only, no "
    "secrets), <code>edit</code>, <code>admin</code>",
    "maas-authorized-group": "OpenShift Group authorized to access the production "
    "MaaS deployment. Used in the MaaSAuthPolicy and MaaSSubscription owners. "
    "Leave as <code>system:authenticated</code> for all users, or specify a "
    "specific group to restrict access.<br><b>Example:</b> "
    "<code>ml-team</code>, <code>data-scientists</code>",
}


# --- Shared page chrome -------------------------------------------------------

BASE_STYLE = """
<style>
  body { font-family: sans-serif; margin: 20px; background: #f4f4f4; color: #333; }
  .container { background: #fff; padding: 20px; border-radius: 8px; box-shadow: 0 0 10px rgba(0,0,0,0.1); max-width: 1100px; margin: 0 auto; }
  h1, h2, h3 { color: #0056b3; }
  nav a { margin-right: 16px; font-weight: 600; text-decoration: none; color: #0056b3; }
  fieldset { border: 1px solid #ddd; border-radius: 6px; margin-bottom: 16px; padding: 12px 16px; }
  legend { font-weight: 700; color: #003366; padding: 0 6px; }
  label { display: block; margin-top: 10px; font-size: 0.9em; font-weight: 600; }
  input[type=text], textarea, select { width: 100%; padding: 6px; margin-top: 3px; box-sizing: border-box; font-family: monospace; }
  textarea { min-height: 60px; }
  button, .btn { background: #0056b3; color: #fff; border: none; padding: 10px 18px; border-radius: 4px; cursor: pointer; font-size: 1em; margin-top: 16px; }
  button.reject, .btn.reject { background: #dc3545; }
  button.approve, .btn.approve { background: #28a745; }
  table { width: 100%; border-collapse: collapse; margin-top: 12px; }
  th, td { border: 1px solid #ddd; padding: 8px; text-align: left; font-size: 0.9em; }
  th { background: #f2f2f2; }
  .badge { padding: 3px 8px; border-radius: 10px; font-size: 0.85em; color: #fff; }
  .badge.pending { background: #fd7e14; }
  .badge.approved { background: #28a745; }
  .badge.rejected { background: #dc3545; }
  .badge.auto-approved { background: #6c757d; }
  .badge.blocked { background: #dc3545; }
  .badge.plan_ready { background: #28a745; }
  pre { background: #f6f8fa; padding: 12px; border-radius: 6px; overflow-x: auto; white-space: pre-wrap; }
  .help { position: relative; display: inline-block; margin-left: 6px; width: 15px; height: 15px;
          line-height: 15px; text-align: center; font-size: 0.72em; font-weight: 700; cursor: help;
          color: #fff; background: #0056b3; border-radius: 50%; vertical-align: middle; }
  .help .tip { visibility: hidden; opacity: 0; transition: opacity 0.12s ease-in;
               position: absolute; z-index: 20; left: 22px; top: -6px; width: 320px;
               background: #003366; color: #fff; padding: 8px 11px; border-radius: 6px;
               font-weight: 400; font-size: 1.15em; font-family: sans-serif; line-height: 1.4;
               white-space: normal; text-align: left; box-shadow: 0 3px 10px rgba(0,0,0,0.35); }
  .help .tip code { background: rgba(255,255,255,0.15); padding: 0 3px; border-radius: 3px; }
  .help .tip b { color: #9ec5ff; }
  .help:hover .tip { visibility: visible; opacity: 1; }
</style>
"""

NAV = """
<nav>
  <a href="{{ url_for('index') }}">Submit Model</a>
  <a href="{{ url_for('list_runs') }}">Pipeline Runs</a>
  <a href="{{ url_for('list_plans') }}">Deployment Plans</a>
</nav>
"""

PAGE_HEAD = "<!DOCTYPE html><html><head><meta charset='utf-8'><title>ModelOps Intake</title>" + BASE_STYLE + "</head><body><div class='container'>" + NAV


FORM_TEMPLATE = (
    PAGE_HEAD
    + """
<h1>Model Intake</h1>
<p>Submit a new model onboarding run. It will run the GPU advisor first, pause for human
approval of the deployment plan, then deploy, security-scan, benchmark, evaluate, and
register the model.</p>
<form method="post" action="{{ url_for('submit') }}">
  {% for section, fields in sections %}
  <fieldset>
    <legend>{{ section }}</legend>
    {% for field in fields %}
    <label for="{{ field }}">{{ field }}{% if help.get(field) %}<span class="help">?<span class="tip">{{ help[field]|safe }}</span></span>{% endif %}</label>
    {% if field in ('values-content',) %}
    <textarea id="{{ field }}" name="{{ field }}">{{ defaults[field] }}</textarea>
    {% else %}
    <input type="text" id="{{ field }}" name="{{ field }}" value="{{ defaults[field] }}"{% if field in required_fields %} required{% endif %}>
    {% endif %}
    {% endfor %}
  </fieldset>
  {% endfor %}
  <button type="submit">Submit Model for Onboarding</button>
</form>
<script>
/* Suggest a k8s-safe model-name and the GuideLLM tokenizer (processor) from the
 * "Model" section. The deployment name, and the garak/benchmark endpoint URLs
 * are derived from model-name by the PIPELINE itself (Tekton param interpolation
 * as http://<model-name>-predictor.<namespace>.svc.cluster.local:8080/v1), so
 * they can never drift from model-name and are no longer form fields.
 * Note: model-name must be a valid Kubernetes name (lowercase, no dots) since it
 * becomes the InferenceService/predictor name - dots are stripped here. A field
 * stops auto-updating once the user edits it by hand. */
(function () {
  function byId(id) { return document.getElementById(id); }
  ["model-name"].forEach(function (id) {
    var el = byId(id);
    if (el) { el.addEventListener("input", function () { el.dataset.dirty = "1"; }); }
  });
  function setIfClean(id, val) {
    var el = byId(id);
    if (el && el.dataset.dirty !== "1" && val) { el.value = val; }
  }
  function k8sName(s) {
    return (s.split("/").pop() || "").toLowerCase()
      .replace(/[._]/g, "-").replace(/[^a-z0-9-]/g, "").replace(/^-+|-+$/g, "");
  }
  function tagFromImage(img) {
    img = img.replace(/^(oci|docker):\/\//, "");
    var i = img.lastIndexOf(":");
    return i >= 0 ? img.substring(i + 1) : "";
  }
  function recompute() {
    var mid = (byId("model-id") || {}).value || "";
    var img = (byId("modelcar-image") || {}).value || "";
    setIfClean("model-name", k8sName(tagFromImage(img) || mid));
  }
  ["model-id", "modelcar-image"].forEach(function (id) {
    var el = byId(id);
    if (el) { el.addEventListener("input", recompute); }
  });

  /* Validate required fields on submit and highlight empty ones. */
  var form = document.querySelector("form");
  var requiredFields = {{ required_fields_json|safe }};
  form.addEventListener("submit", function (e) {
    var missing = [];
    requiredFields.forEach(function (f) {
      var el = byId(f);
      if (el) {
        if (!el.value.trim()) {
          el.style.borderColor = "#dc3545";
          el.style.borderWidth = "2px";
          missing.push(f);
        } else {
          el.style.borderColor = "";
          el.style.borderWidth = "";
        }
      }
    });
    if (missing.length) {
      e.preventDefault();
      alert("Required fields missing: " + missing.join(", "));
    }
  });
})();
</script>
"""
    + "</div></body></html>"
)


RUNS_TEMPLATE = (
    PAGE_HEAD
    + """
<h1>Pipeline Runs</h1>
<table>
  <thead><tr><th>Run</th><th>Model</th><th>Requested By</th><th>Status</th><th>Created</th></tr></thead>
  <tbody>
  {% for r in runs %}
    <tr>
      <td><a href="{{ url_for('run_detail', name=r.name) }}">{{ r.name }}</a></td>
      <td>{{ r.model_id }}</td>
      <td>{{ r.requested_by }}</td>
      <td>{{ r.status }}</td>
      <td>{{ r.created }}</td>
    </tr>
  {% else %}
    <tr><td colspan="5">No pipeline runs submitted yet.</td></tr>
  {% endfor %}
  </tbody>
</table>
"""
    + "</div></body></html>"
)


RUN_DETAIL_TEMPLATE = (
    PAGE_HEAD
    + """
<h1>Pipeline Run: {{ name }}</h1>
<p><strong>Status:</strong> {{ status }}</p>
{% if plan %}
<p><strong>Deployment plan:</strong> <a href="{{ url_for('plan_detail', plan_id=plan.plan_id) }}">{{ plan.plan_id }}</a>
   (<span class="badge {{ plan.status }}">{{ plan.status }}</span>)</p>
{% else %}
<p>No deployment plan submitted yet for this run (gpu-advisor / wait-for-approval may still be running).</p>
{% endif %}
<h3>Raw PipelineRun params</h3>
<pre>{{ params_json }}</pre>
"""
    + "</div></body></html>"
)


PLANS_TEMPLATE = (
    PAGE_HEAD
    + """
<h1>Deployment Plans</h1>
<table>
  <thead><tr><th>Plan ID</th><th>Model</th><th>Namespace</th><th>Plan Status</th><th>Decision</th><th>Updated</th></tr></thead>
  <tbody>
  {% for p in plans %}
    <tr>
      <td><a href="{{ url_for('plan_detail', plan_id=p.plan_id) }}">{{ p.plan_id }}</a></td>
      <td>{{ p.model_id }}</td>
      <td>{{ p.target_namespace }}</td>
      <td><span class="badge {{ p.plan_status|lower }}">{{ p.plan_status }}</span></td>
      <td><span class="badge {{ p.status }}">{{ p.status }}</span></td>
      <td>{{ p.updated_at }}</td>
    </tr>
  {% else %}
    <tr><td colspan="6">No deployment plans submitted yet.</td></tr>
  {% endfor %}
  </tbody>
</table>
"""
    + "</div></body></html>"
)


PLAN_DETAIL_TEMPLATE = (
    PAGE_HEAD
    + """
<h1>Deployment Plan: {{ plan.plan_id }}</h1>
<p>
  <strong>Model:</strong> {{ plan.model_id }} ({{ plan.model_name }})<br>
  <strong>Target namespace:</strong> {{ plan.target_namespace }}<br>
  <strong>Requested by:</strong> {{ plan.requested_by }}<br>
  <strong>GPU advisor status:</strong> <span class="badge {{ plan.plan_status|lower }}">{{ plan.plan_status }}</span><br>
  <strong>Approval status:</strong> <span class="badge {{ plan.status }}">{{ plan.status }}</span>
  {% if plan.decided_by %} by {{ plan.decided_by }}{% endif %}
</p>

<h3>Recommendation</h3>
<pre>{{ plan.recommendation_md }}</pre>

<h3>Recommended vLLM command</h3>
<pre>{{ plan.recommended_vllm_command }}</pre>

<h3>Deployment options (raw JSON)</h3>
<pre>{{ plan.deployment_options }}</pre>

<h3>GPU inventory (raw JSON)</h3>
<pre>{{ plan.gpu_inventory }}</pre>

{% if plan.status == 'pending' %}
<h3>Decision</h3>
<form method="post" action="{{ url_for('approve_plan_ui', plan_id=plan.plan_id) }}" style="display:inline-block; margin-right: 10px;">
  <label for="approved_by">Your name</label>
  <input type="text" name="approved_by" required>
  <label for="comment">Comment (optional)</label>
  <input type="text" name="comment">
  <button class="approve" type="submit">Approve &amp; Continue Pipeline</button>
</form>
<form method="post" action="{{ url_for('reject_plan_ui', plan_id=plan.plan_id) }}" style="display:inline-block;">
  <label for="rejected_by">Your name</label>
  <input type="text" name="rejected_by" required>
  <label for="comment">Reason</label>
  <input type="text" name="comment">
  <button class="reject" type="submit">Reject &amp; Halt Pipeline</button>
</form>
{% endif %}
"""
    + "</div></body></html>"
)


# --- Web UI routes ------------------------------------------------------------


@app.route("/")
def index():
    return render_template_string(
        FORM_TEMPLATE, sections=FORM_SECTIONS, defaults=FORM_DEFAULTS, help=FIELD_HELP,
        required_fields=REQUIRED_FIELDS, required_fields_json=json.dumps(sorted(REQUIRED_FIELDS))
    )


@app.route("/submit", methods=["POST"])
def submit():
    params = {}
    for field in ALL_FORM_FIELDS:
        params[field] = request.form.get(field, FORM_DEFAULTS.get(field, ""))

    run_name = "model-intake-{}".format(uuid.uuid4().hex[:10])
    try:
        create_pipelinerun(run_name, params)
    except Exception as e:
        return (
            "Failed to create PipelineRun: {}".format(e),
            500,
        )

    db = get_db()
    db.execute(
        "INSERT INTO runs (run_name, model_id, model_name, requested_by, params_json, created_at) "
        "VALUES (?, ?, ?, ?, ?, ?)",
        (
            run_name,
            params.get("model-id"),
            params.get("model-name"),
            params.get("requested-by"),
            json.dumps(params),
            now_iso(),
        ),
    )
    db.commit()

    return redirect(url_for("run_detail", name=run_name))


@app.route("/runs")
def list_runs():
    db = get_db()
    local_runs = {r["run_name"]: r for r in db.execute("SELECT * FROM runs").fetchall()}

    rows = []
    live = {i["metadata"]["name"]: i for i in list_pipelineruns()}
    for name, r in local_runs.items():
        pr = live.get(name)
        rows.append(
            {
                "name": name,
                "model_id": r["model_id"],
                "requested_by": r["requested_by"],
                "status": pipelinerun_status(pr) if pr else "Unknown (not found in cluster)",
                "created": r["created_at"],
            }
        )
    rows.sort(key=lambda r: r["created"], reverse=True)
    return render_template_string(RUNS_TEMPLATE, runs=rows)


@app.route("/runs/<name>")
def run_detail(name):
    db = get_db()
    row = db.execute("SELECT * FROM runs WHERE run_name = ?", (name,)).fetchone()
    pr = get_pipelinerun(name)
    status = pipelinerun_status(pr) if pr else "Unknown"
    plan = db.execute(
        "SELECT * FROM plans WHERE pipelinerun_name = ?", (name,)
    ).fetchone()
    params_json = row["params_json"] if row else "{}"
    return render_template_string(
        RUN_DETAIL_TEMPLATE,
        name=name,
        status=status,
        plan=plan,
        params_json=json.dumps(json.loads(params_json), indent=2),
    )


@app.route("/plans")
def list_plans():
    db = get_db()
    plans = db.execute(
        "SELECT * FROM plans ORDER BY updated_at DESC"
    ).fetchall()
    return render_template_string(PLANS_TEMPLATE, plans=plans)


@app.route("/plans/<plan_id>")
def plan_detail(plan_id):
    db = get_db()
    plan = db.execute("SELECT * FROM plans WHERE plan_id = ?", (plan_id,)).fetchone()
    if plan is None:
        return "Plan not found: {}".format(plan_id), 404
    return render_template_string(PLAN_DETAIL_TEMPLATE, plan=plan)


@app.route("/plans/<plan_id>/approve-ui", methods=["POST"])
def approve_plan_ui(plan_id):
    _decide_plan(plan_id, "approved", request.form.get("approved_by", ""), request.form.get("comment", ""))
    return redirect(url_for("plan_detail", plan_id=plan_id))


@app.route("/plans/<plan_id>/reject-ui", methods=["POST"])
def reject_plan_ui(plan_id):
    _decide_plan(plan_id, "rejected", request.form.get("rejected_by", ""), request.form.get("comment", ""))
    return redirect(url_for("plan_detail", plan_id=plan_id))


# --- JSON API used by the wait-for-approval Tekton Task ----------------------


@app.route("/api/plans", methods=["POST", "GET"])
def api_plans():
    if request.method == "GET":
        db = get_db()
        plans = db.execute("SELECT * FROM plans ORDER BY updated_at DESC").fetchall()
        return jsonify([dict(p) for p in plans])

    data = request.get_json(force=True, silent=True) or {}
    plan_id = data.get("plan_id")
    if not plan_id:
        return jsonify({"error": "plan_id is required"}), 400

    db = get_db()
    existing = db.execute("SELECT * FROM plans WHERE plan_id = ?", (plan_id,)).fetchone()
    ts = now_iso()
    fields = {
        "plan_id": plan_id,
        "pipelinerun_name": data.get("pipelinerun_name", plan_id),
        "model_id": data.get("model_id", ""),
        "model_name": data.get("model_name", ""),
        "target_namespace": data.get("target_namespace", ""),
        "requested_by": data.get("requested_by", ""),
        "plan_status": data.get("plan_status", "UNKNOWN"),
        "recommendation_md": data.get("recommendation_md", ""),
        "deployment_options": json.dumps(data.get("deployment_options", {})),
        "gpu_inventory": json.dumps(data.get("gpu_inventory", {})),
        "recommended_vllm_command": data.get("recommended_vllm_command", ""),
    }

    if existing is None:
        db.execute(
            "INSERT INTO plans (plan_id, pipelinerun_name, model_id, model_name, target_namespace, "
            "requested_by, plan_status, recommendation_md, deployment_options, gpu_inventory, "
            "recommended_vllm_command, status, created_at, updated_at) "
            "VALUES (:plan_id, :pipelinerun_name, :model_id, :model_name, :target_namespace, "
            ":requested_by, :plan_status, :recommendation_md, :deployment_options, :gpu_inventory, "
            ":recommended_vllm_command, 'pending', :created_at, :updated_at)",
            {**fields, "created_at": ts, "updated_at": ts},
        )
    else:
        db.execute(
            "UPDATE plans SET pipelinerun_name=:pipelinerun_name, model_id=:model_id, "
            "model_name=:model_name, target_namespace=:target_namespace, requested_by=:requested_by, "
            "plan_status=:plan_status, recommendation_md=:recommendation_md, "
            "deployment_options=:deployment_options, gpu_inventory=:gpu_inventory, "
            "recommended_vllm_command=:recommended_vllm_command, updated_at=:updated_at "
            "WHERE plan_id=:plan_id",
            {**fields, "updated_at": ts},
        )
    db.commit()
    return jsonify({"plan_id": plan_id, "status": "ok"}), 201


@app.route("/api/plans/<plan_id>")
def api_plan_get(plan_id):
    db = get_db()
    plan = db.execute("SELECT * FROM plans WHERE plan_id = ?", (plan_id,)).fetchone()
    if plan is None:
        return jsonify({"error": "not found"}), 404
    return jsonify(
        {
            "plan_id": plan["plan_id"],
            "status": plan["status"],
            "decided_by": plan["decided_by"],
            "decision_comment": plan["decision_comment"],
            "plan_status": plan["plan_status"],
        }
    )


@app.route("/api/plans/<plan_id>/approve", methods=["POST"])
def api_plan_approve(plan_id):
    data = request.get_json(force=True, silent=True) or {}
    ok = _decide_plan(
        plan_id, "approved", data.get("approved_by", "api"), data.get("comment", "")
    )
    if not ok:
        return jsonify({"error": "not found"}), 404
    return jsonify({"plan_id": plan_id, "status": "approved"})


@app.route("/api/plans/<plan_id>/reject", methods=["POST"])
def api_plan_reject(plan_id):
    data = request.get_json(force=True, silent=True) or {}
    ok = _decide_plan(
        plan_id, "rejected", data.get("rejected_by", "api"), data.get("comment", "")
    )
    if not ok:
        return jsonify({"error": "not found"}), 404
    return jsonify({"plan_id": plan_id, "status": "rejected"})


def _decide_plan(plan_id, status, decided_by, comment):
    db = get_db()
    existing = db.execute("SELECT * FROM plans WHERE plan_id = ?", (plan_id,)).fetchone()
    if existing is None:
        return False
    db.execute(
        "UPDATE plans SET status=?, decided_by=?, decision_comment=?, updated_at=? WHERE plan_id=?",
        (status, decided_by, comment, now_iso(), plan_id),
    )
    db.commit()
    return True


@app.route("/healthz")
def healthz():
    return jsonify({"status": "ok", "time": now_iso()})


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=8080)
