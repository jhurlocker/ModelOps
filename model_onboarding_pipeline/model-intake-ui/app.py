import json
import os
import sqlite3
import uuid
from datetime import datetime, timezone

from flask import Flask, g, jsonify, redirect, render_template_string, request, url_for

# --- Configuration -------------------------------------------------------------
DB_PATH = os.environ.get("DB_PATH", "/data/model-intake.db")
PIPELINE_NAMESPACE = os.environ.get("PIPELINE_NAMESPACE", "vllm")
PIPELINE_NAME = os.environ.get("PIPELINE_NAME", "model-intake-pipeline")
SELF_INTERNAL_URL = os.environ.get(
    "SELF_INTERNAL_URL", "http://model-intake.vllm.svc.cluster.local:8080"
)
DEFAULT_S3_ENDPOINT = os.environ.get(
    "DEFAULT_S3_ENDPOINT", "http://minio-service.s3-storage.svc.cluster.local:9000"
)
DEFAULT_S3_ACCESS_KEY = os.environ.get("DEFAULT_S3_ACCESS_KEY", "minio")
DEFAULT_S3_SECRET_KEY = os.environ.get("DEFAULT_S3_SECRET_KEY", "minio123")
DEFAULT_ADVISOR_ENDPOINT = os.environ.get("DEFAULT_ADVISOR_ENDPOINT", "")

MODELOPS_GROUP = "modelops.example.io"
MODELOPS_VERSION = "v1alpha1"
MODELOPS_PLURAL = "modelrequests"

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
        CREATE TABLE IF NOT EXISTS model_requests (
            request_name TEXT PRIMARY KEY,
            model_source TEXT,
            model_uri TEXT,
            model_name TEXT,
            target_environment TEXT,
            requested_by TEXT,
            spec_json TEXT,
            created_at TEXT
        )
        """
    )
    conn.commit()
    conn.close()


init_db()


def now_iso():
    return datetime.now(timezone.utc).isoformat()


# --- Kubernetes integration (ModelRequest CRD) ------------------------------


_k8s_custom_api = None
_k8s_core_api = None


def k8s_custom_api():
    global _k8s_custom_api
    if _k8s_custom_api is None:
        from kubernetes import client, config

        try:
            config.load_incluster_config()
        except Exception:
            config.load_kube_config()
        _k8s_custom_api = client.CustomObjectsApi()
    return _k8s_custom_api


def k8s_core_api():
    global _k8s_core_api
    if _k8s_core_api is None:
        from kubernetes import client, config

        try:
            config.load_incluster_config()
        except Exception:
            config.load_kube_config()
        _k8s_core_api = client.CoreV1Api()
    return _k8s_core_api


def create_model_request(request_name, spec):
    api = k8s_custom_api()
    body = {
        "apiVersion": "{}/{}".format(MODELOPS_GROUP, MODELOPS_VERSION),
        "kind": "ModelRequest",
        "metadata": {
            "name": request_name,
            "labels": {"app.kubernetes.io/created-by": "model-intake-ui"},
        },
        "spec": spec,
    }
    return api.create_namespaced_custom_object(
        group=MODELOPS_GROUP,
        version=MODELOPS_VERSION,
        namespace=PIPELINE_NAMESPACE,
        plural=MODELOPS_PLURAL,
        body=body,
    )


def list_model_requests(limit=25):
    api = k8s_custom_api()
    try:
        resp = api.list_namespaced_custom_object(
            group=MODELOPS_GROUP,
            version=MODELOPS_VERSION,
            namespace=PIPELINE_NAMESPACE,
            plural=MODELOPS_PLURAL,
            label_selector="app.kubernetes.io/created-by=model-intake-ui",
        )
        items = resp.get("items", [])
        items.sort(
            key=lambda i: i.get("metadata", {}).get("creationTimestamp", ""),
            reverse=True,
        )
        return items[:limit]
    except Exception as e:
        app.logger.warning("Could not list ModelRequests: %s", e)
        return []


def get_model_request(name):
    api = k8s_custom_api()
    try:
        return api.get_namespaced_custom_object(
            group=MODELOPS_GROUP,
            version=MODELOPS_VERSION,
            namespace=PIPELINE_NAMESPACE,
            plural=MODELOPS_PLURAL,
            name=name,
        )
    except Exception as e:
        app.logger.warning("Could not get ModelRequest %s: %s", name, e)
        return None


def modelrequest_status(mr):
    if not mr:
        return "Unknown"
    status_block = mr.get("status", {})
    phase = status_block.get("phase", "")
    if phase:
        return phase
    conditions = status_block.get("conditions", [])
    for c in conditions:
        if c.get("type") == "Ready":
            if c.get("status") == "True":
                return "Succeeded"
            return "Failed: {}".format(c.get("reason", ""))
    return "Pending"


# --- Build ModelRequest spec from form params --------------------------------


def build_modelrequest_spec(params):
    spec = {
        "modelSource": params.get("model-source", "huggingface"),
        "modelURI": params.get("model-id"),
        "targetEnvironment": params.get("target-environment", "sandbox"),
        "pipelineRef": params.get("pipeline-ref", PIPELINE_NAME),
        "modelName": params.get("model-name"),
        "modelVersion": params.get("model-version", "v1"),
        "requestedBy": params.get("requested-by", ""),
    }

    if params.get("modelcar-repo") or params.get("modelcar-image"):
        spec["modelCar"] = {
            "repo": params.get("modelcar-repo", "redhat-ai-services/modelcar-catalog"),
            "image": params.get("modelcar-image", ""),
        }

    spec["namespaces"] = {
        "sandbox": params.get("target-namespace", "vllm"),
        "staging": params.get("staging-namespace", "vllm-staging"),
    }

    if params.get("artifact-cve-threshold") or params.get("artifact-scan-image"):
        spec["compliance"] = {
            "artifactScanImage": params.get("artifact-scan-image", "registry.access.redhat.com/ubi9/python-311:latest"),
            "cveThreshold": params.get("artifact-cve-threshold", "critical"),
            "ignoreUnfixed": params.get("ignore-unfixed", "true"),
            "allowedArchitectures": [
                a.strip() for a in params.get("allowed-architectures", "amd64,x86_64").split(",") if a.strip()
            ],
        }

    spec["gpuAdvisor"] = {
        "contextLength": int(params.get("context-length", 32768)),
        "concurrency": int(params.get("concurrency", 4)),
        "allowTimeSlicing": params.get("allow-time-slicing", "true") == "true",
        "allowMIG": params.get("allow-mig", "false") == "true",
        "gpuIsolationPolicy": params.get("gpu-isolation-policy", "dedicated"),
        "gpuOperatorNamespace": params.get("gpu-operator-namespace", "nvidia-gpu-operator"),
        "clusterPolicyName": params.get("clusterpolicy-name", "gpu-cluster-policy"),
        "timeSlicingConfigMap": params.get("time-slicing-configmap", "modelops-time-slicing"),
        "maxTimeSlices": int(params.get("max-time-slices", 8)),
        "advisorEndpoint": params.get("advisor-endpoint", ""),
        "advisorSecretName": params.get("advisor-secret-name", "gpu-advisor-credentials"),
        "advisorTimeoutSeconds": int(params.get("advisor-timeout-seconds", 300)),
    }

    spec["deploy"] = {
        "chartUrl": params.get("chart-url", "https://redhat-ai-services.github.io/helm-charts/"),
        "chartVersion": params.get("chart-version", "0.7.1"),
        "gpuCountOverride": params.get("gpu-count-override", ""),
        "hardwareProfileName": params.get("hardware-profile-name", "gpu-profile"),
        "hardwareProfileNamespace": params.get("hardware-profile-namespace", "redhat-ods-applications"),
        "valuesContent": params.get("values-content", ""),
        "releaseName": params.get("model-name", ""),
    }

    spec["securityScan"] = {
        "severityThreshold": params.get("severity-threshold", "block"),
        "evalhubUrl": params.get("evalhub-url", ""),
        "evalhubToken": params.get("evalhub-token", ""),
        "tenantNamespace": params.get("target-namespace", "vllm"),
        "openshiftConsoleDomain": params.get("openshift-console-domain", ""),
    }

    spec["approval"] = {
        "apiUrl": params.get("approval-api-url", SELF_INTERNAL_URL),
        "pollIntervalSeconds": int(params.get("approval-poll-interval-seconds", 15)),
        "timeoutSeconds": int(params.get("approval-timeout-seconds", 3600)),
    }

    spec["benchmark"] = {
        "profile": params.get("guidellm-profile", "constant"),
        "rate": float(params.get("guidellm-rate", 4.0)),
        "maxSeconds": int(params.get("guidellm-max-seconds", 15)),
        "maxRequests": int(params.get("guidellm-max-requests", 2)),
        "customData": params.get("custom-data", "False") == "True",
        "customFilename": params.get("custom-filename", "no-file"),
        "huggingfaceToken": params.get("huggingface-token", ""),
    }

    spec["s3Storage"] = {
        "endpoint": params.get("s3-api-endpoint", DEFAULT_S3_ENDPOINT),
        "accessKeyId": params.get("s3-access-key-id", DEFAULT_S3_ACCESS_KEY),
        "secretAccessKey": params.get("s3-secret-access-key", DEFAULT_S3_SECRET_KEY),
    }

    spec["scanS3Storage"] = {
        "endpoint": params.get("scan-s3-endpoint", DEFAULT_S3_ENDPOINT),
        "accessKeyId": params.get("scan-s3-access-key-id", DEFAULT_S3_ACCESS_KEY),
        "secretAccessKey": params.get("scan-s3-secret-access-key", DEFAULT_S3_SECRET_KEY),
        "bucket": "compliance-artifact-results",
        "uiRoute": params.get("s3-ui-route", ""),
    }

    spec["modelRegistry"] = {
        "server": params.get("mr-server", "http://modelops-registry.rhoai-model-registries.svc.cluster.local"),
        "port": params.get("mr-port", "8080"),
        "author": params.get("model-reg-author", "ModelOps Platform Team"),
    }

    spec["modelAccess"] = {
        "authorizedViewers": params.get("authorized-viewers", ""),
        "accessRole": params.get("access-role", "view"),
    }

    spec["maas"] = {
        "enabled": False,
        "servingNamespace": "llm",
        "policyNamespace": "models-as-a-service",
        "gpuCount": "1",
        "runtimeImage": "registry.redhat.io/rhaiis/vllm-cuda-rhel9:3.3.0",
        "authorizedGroup": params.get("maas-authorized-group", "system:authenticated"),
    }

    spec["lmEval"] = {
        "enabled": False,
        "jobName": params.get("lm-eval-job-name", "mmlu-jurisprudence-eval-job"),
        "useCustom": params.get("lm-eval-custom", "False") == "True",
    }

    return spec


# --- Pipeline parameter defaults (mirrors model-intake-pipeline.yaml) ------

FORM_DEFAULTS = {
    "model-id": "ibm-granite/granite-3.3-2b-instruct",
    "model-name": "granite-2b",
    "model-version": "v1",
    "model-source": "huggingface",
    "target-environment": "sandbox",
    "pipeline-ref": PIPELINE_NAME,
    "modelcar-image": "",
    "target-namespace": "vllm",
    "staging-namespace": "vllm-staging",
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
    "authorized-viewers": "",
    "access-role": "view",
    "maas-authorized-group": "system:authenticated",
    "openshift-console-domain": "",
}

FORM_SECTIONS = [
    ("Model Identity", ["model-source", "model-id", "modelcar-image", "model-name", "model-version",
                         "target-environment", "pipeline-ref", "requested-by"]),
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
        "EvalHub & Security Scan",
        ["evalhub-url", "evalhub-token", "severity-threshold", "openshift-console-domain"],
    ),
    (
        "GPU Advisor + Time-Slicing",
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
    ("S3 Storage", ["s3-api-endpoint", "s3-access-key-id", "s3-secret-access-key"]),
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

ALL_FORM_FIELDS = [f for _, fields in FORM_SECTIONS for f in fields]

REQUIRED_FIELDS = {"model-id", "model-name", "evalhub-token"}

FIELD_HELP = {
    "model-source": "Source of the model (e.g., huggingface, s3, oci).<br><b>Default:</b> huggingface",
    "model-id": "Hugging Face model ID to onboard.<br><b>Example:</b> <code>ibm-granite/granite-3.3-2b-instruct</code>",
    "target-environment": "Target environment label.<br><b>Default:</b> sandbox",
    "pipeline-ref": "Name of the Tekton pipeline to execute.<br><b>Default:</b> model-intake-pipeline",
    "model-name": "Kubernetes-safe deployment name. Lowercase, digits, <code>-</code> only.",
    "model-version": "Version label recorded in the model registry.<br><b>Example:</b> <code>v1</code>",
    "requested-by": "Your name / username for audit trail.",
    "modelcar-image": "Optional full OCI/ModelCar image reference.",
    "target-namespace": "Sandbox namespace.<br><b>Default:</b> <code>vllm</code>",
    "staging-namespace": "Staging namespace.<br><b>Default:</b> <code>vllm-staging</code>",
    "modelcar-repo": "Quay repo for ModelCar images.<br><b>Default:</b> <code>redhat-ai-services/modelcar-catalog</code>",
    "artifact-scan-image": "Container image for Trivy CVE scan.",
    "artifact-cve-threshold": "CVE severity gate: <code>critical</code>, <code>high</code>, <code>none</code>",
    "ignore-unfixed": "Ignore CVEs without available fixes.",
    "allowed-architectures": "Comma-separated permitted archs.<br><b>Example:</b> <code>amd64,x86_64</code>",
    "evalhub-url": "EvalHub base URL (FQDN only, no scheme).",
    "evalhub-token": "OpenShift OAuth token for EvalHub API (<code>oc whoami -t</code>).<br><b>Required</b>",
    "severity-threshold": "Garak gate strictness: <code>block</code>, <code>warn</code>, <code>off</code>",
    "openshift-console-domain": "OpenShift AI console domain for dashboard links.",
    "context-length": "Max context length for vLLM (<code>--max-model-len</code>).",
    "concurrency": "Expected concurrent requests.",
    "allow-time-slicing": "Allow NVIDIA time-slicing when no free GPU.<br><b>Values:</b> <code>true</code>/<code>false</code>",
    "gpu-isolation-policy": "GPU isolation policy.<br><b>Default:</b> <code>dedicated</code>",
    "advisor-endpoint": "Optional LLM endpoint for remote GPU advisor.",
    "advisor-secret-name": "Secret with API key for advisor endpoint.",
    "approval-api-url": "In-cluster URL of this UI that the approval task polls.",
    "approval-poll-interval-seconds": "Seconds between approval polls.",
    "approval-timeout-seconds": "Max seconds to wait for approval.",
    "chart-url": "Helm chart repo URL for vLLM deployment.",
    "chart-version": "vllm-kserve Helm chart version.<br><b>Default:</b> <code>0.7.1</code>",
    "gpu-count-override": "Force GPU count (leave blank for advisor plan).",
    "hardware-profile-name": "RHOAI hardware profile for deployment.",
    "values-content": "Optional extra Helm values (YAML).",
    "guidellm-profile": "Benchmark profile (<code>constant</code>, <code>sweep</code>, <code>throughput</code>).",
    "guidellm-rate": "Request rate for benchmark.",
    "guidellm-max-seconds": "Max benchmark duration per rate step.",
    "guidellm-max-requests": "Max benchmark requests per rate step.",
    "huggingface-token": "Hugging Face token for gated models.",
    "custom-data": "Use custom benchmark dataset.",
    "s3-api-endpoint": "S3/MinIO endpoint for benchmark results.",
    "s3-access-key-id": "S3 access key ID.",
    "s3-secret-access-key": "S3 secret access key.",
    "scan-s3-endpoint": "S3 endpoint for scan reports.",
    "scan-s3-access-key-id": "S3 access key for scan results.",
    "scan-s3-secret-access-key": "S3 secret key for scan results.",
    "compliance-s3-bucket": "Bucket for compliance scan reports.",
    "security-s3-bucket": "Bucket for security scan reports.",
    "s3-ui-route": "Optional S3 browser UI route.",
    "model-reg-author": "Author recorded in Model Registry.",
    "mr-server": "Model Registry REST base URL.",
    "mr-port": "Model Registry REST port.<br><b>Default:</b> <code>8080</code>",
    "authorized-viewers": "Comma-separated users/groups for RHOAI dashboard visibility.",
    "access-role": "Kubernetes role for authorized viewers.<br><b>Default:</b> <code>view</code>",
    "maas-authorized-group": "OpenShift group for MaaS access.<br><b>Default:</b> <code>system:authenticated</code>",
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
  <a href="{{ url_for('list_requests') }}">Model Requests</a>
  <a href="{{ url_for('list_plans') }}">Deployment Plans</a>
</nav>
"""

PAGE_HEAD = "<!DOCTYPE html><html><head><meta charset='utf-8'><title>ModelOps Intake</title>" + BASE_STYLE + "</head><body><div class='container'>" + NAV

FORM_TEMPLATE = (
    PAGE_HEAD
    + """
<h1>Model Intake</h1>
<p>Submit a new model onboarding request. The ModelOps controller will create a
Tekton PipelineRun, monitor execution, and report status back here.</p>
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
  function recompute() {
    var mid = (byId("model-id") || {}).value || "";
    setIfClean("model-name", k8sName(mid));
  }
  ["model-id"].forEach(function (id) {
    var el = byId(id);
    if (el) { el.addEventListener("input", recompute); }
  });
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

REQUESTS_TEMPLATE = (
    PAGE_HEAD
    + """
<h1>Model Requests</h1>
<table>
  <thead><tr><th>Request</th><th>Model</th><th>Source</th><th>Phase</th><th>Pipeline Run</th><th>Created</th></tr></thead>
  <tbody>
  {% for r in requests %}
    <tr>
      <td><a href="{{ url_for('request_detail', name=r.name) }}">{{ r.name }}</a></td>
      <td>{{ r.model_uri }}</td>
      <td>{{ r.model_source }}</td>
      <td><span class="badge {{ r.phase|lower }}">{{ r.phase }}</span></td>
      <td>{{ r.pipeline_run_name }}</td>
      <td>{{ r.created }}</td>
    </tr>
  {% else %}
    <tr><td colspan="6">No model requests submitted yet.</td></tr>
  {% endfor %}
  </tbody>
</table>
"""
    + "</div></body></html>"
)

REQUEST_DETAIL_TEMPLATE = (
    PAGE_HEAD
    + """
<h1>Model Request: {{ name }}</h1>
<p><strong>Model:</strong> {{ spec.get('modelURI', '') }} ({{ spec.get('modelSource', '') }})</p>
<p><strong>Phase:</strong> <span class="badge {{ phase|lower }}">{{ phase }}</span></p>
{% if pipeline_run_name %}
<p><strong>Pipeline Run:</strong> {{ pipeline_run_name }}</p>
{% endif %}
{% if message %}
<p><strong>Message:</strong> {{ message }}</p>
{% endif %}
{% if plan %}
<p><strong>Deployment plan:</strong> <a href="{{ url_for('plan_detail', plan_id=plan.plan_id) }}">{{ plan.plan_id }}</a>
   (<span class="badge {{ plan.status }}">{{ plan.status }}</span>)</p>
{% else %}
<p>No deployment plan submitted yet for this request (gpu-advisor / wait-for-approval may still be running).</p>
{% endif %}
<h3>ModelRequest Spec</h3>
<pre>{{ spec_json }}</pre>
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

    spec = build_modelrequest_spec(params)
    request_name = "model-intake-{}".format(uuid.uuid4().hex[:10])

    try:
        create_model_request(request_name, spec)
    except Exception as e:
        return (
            "Failed to create ModelRequest: {}".format(e),
            500,
        )

    db = get_db()
    db.execute(
        "INSERT INTO model_requests (request_name, model_source, model_uri, model_name, target_environment, requested_by, spec_json, created_at) "
        "VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
        (
            request_name,
            spec.get("modelSource"),
            spec.get("modelURI"),
            spec.get("modelName"),
            spec.get("targetEnvironment"),
            spec.get("requestedBy"),
            json.dumps(spec),
            now_iso(),
        ),
    )
    db.commit()

    return redirect(url_for("request_detail", name=request_name))


@app.route("/requests")
def list_requests():
    db = get_db()
    local_requests = {r["request_name"]: r for r in db.execute("SELECT * FROM model_requests").fetchall()}

    rows = []
    live = {i["metadata"]["name"]: i for i in list_model_requests()}
    for name, r in local_requests.items():
        mr = live.get(name)
        rows.append(
            {
                "name": name,
                "model_source": r.get("model_source", ""),
                "model_uri": r.get("model_uri", ""),
                "phase": modelrequest_status(mr) if mr else "Unknown",
                "pipeline_run_name": (mr.get("status", {}).get("pipelineRunName", "") if mr else ""),
                "created": r["created_at"],
            }
        )
    rows.sort(key=lambda r: r["created"], reverse=True)
    return render_template_string(REQUESTS_TEMPLATE, requests=rows)


@app.route("/requests/<name>")
def request_detail(name):
    mr = get_model_request(name)
    spec = mr.get("spec", {}) if mr else {}
    status_block = mr.get("status", {}) if mr else {}
    phase = status_block.get("phase", "Pending")
    pipeline_run_name = status_block.get("pipelineRunName", "")
    message = status_block.get("message", "")

    db = get_db()
    plan = db.execute(
        "SELECT * FROM plans WHERE pipelinerun_name = ?", (pipeline_run_name or name,)
    ).fetchone()

    return render_template_string(
        REQUEST_DETAIL_TEMPLATE,
        name=name,
        spec=spec,
        phase=phase,
        pipeline_run_name=pipeline_run_name,
        message=message,
        plan=plan,
        spec_json=json.dumps(spec, indent=2),
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
