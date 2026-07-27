import json
import os
import sqlite3
import uuid
from datetime import datetime, timezone

from flask import Flask, g, jsonify, redirect, render_template_string, request, url_for

# --- Configuration -------------------------------------------------------------
DB_PATH = os.environ.get("DB_PATH", "/data/model-intake.db")
PIPELINE_NAMESPACE = os.environ.get("PIPELINE_NAMESPACE", "sandbox")
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


LIFECYCLE_PROFILE = os.environ.get(
    "LIFECYCLE_PROFILE", "standard-generative-onboarding"
)


def build_modelrequest_spec(params):
    spec = {
        "model": {
            "sourceType": params.get("model-source", "huggingface"),
            "uri": params.get("model-id"),
            "name": params.get("model-name"),
            "version": params.get("model-version", "v1"),
        },
        "lifecycleProfile": params.get("lifecycle-profile", LIFECYCLE_PROFILE),
        "requestedBy": params.get("requested-by", ""),
    }

    pipeline_override = params.get("pipeline-ref", "")
    if pipeline_override:
        spec["pipelineRef"] = pipeline_override

    requirements = {}

    ctx_len = params.get("context-length", "")
    if ctx_len:
        requirements["contextLength"] = int(ctx_len)

    conc = params.get("concurrency", "")
    if conc:
        requirements["expectedConcurrency"] = int(conc)

    allow_ts = params.get("allow-time-slicing", "")
    if allow_ts:
        requirements["allowTimeSlicing"] = allow_ts == "true"

    allow_mig = params.get("allow-mig", "")
    if allow_mig:
        requirements["allowMIG"] = allow_mig == "true"

    iso = params.get("gpu-isolation-policy", "")
    if iso:
        requirements["gpuIsolationPolicy"] = iso

    cve = params.get("artifact-cve-threshold", "")
    if cve:
        requirements["cveThreshold"] = cve

    sev = params.get("severity-threshold", "")
    if sev:
        requirements["securityThreshold"] = sev

    sandbox_ns = params.get("target-namespace", "")
    if sandbox_ns:
        requirements["sandboxNamespace"] = sandbox_ns

    staging_ns = params.get("staging-namespace", "")
    if staging_ns:
        requirements["stagingNamespace"] = staging_ns

    promo_namespace = params.get("promotion-namespace-0", "")
    if promo_namespace:
        namespaces = []
        i = 0
        while True:
            ns = params.get(f"promotion-namespace-{i}", "")
            if not ns:
                break
            namespaces.append(ns)
            i += 1
        if namespaces:
            requirements["promotionNamespaces"] = namespaces

    advisor = params.get("advisor-endpoint", "")
    if advisor:
        requirements["advisorEndpoint"] = advisor

    gpu_override = params.get("gpu-count-override", "")
    if gpu_override:
        requirements["gpuCountOverride"] = gpu_override

    values = params.get("values-content", "")
    if values:
        requirements["valuesContent"] = values

    console_domain = params.get("openshift-console-domain", "")
    if console_domain:
        requirements["openshiftConsoleDomain"] = console_domain

    custom_data = params.get("custom-data", "")
    if custom_data:
        requirements["customBenchmarkData"] = custom_data == "True"

    custom_file = params.get("custom-filename", "")
    if custom_file and custom_file != "no-file":
        requirements["customBenchmarkFile"] = custom_file

    if requirements:
        spec["requirements"] = requirements

    viewers = params.get("authorized-viewers", "")
    access_role = params.get("access-role", "")
    if viewers or access_role:
        spec["access"] = {
            "authorizedViewers": viewers,
            "accessRole": access_role,
        }

    evalhub_secret = params.get("evalhub-secret-name", "")
    if evalhub_secret:
        spec["evalhubSecretName"] = evalhub_secret

    hf_secret = params.get("huggingface-secret-name", "")
    if hf_secret:
        spec["huggingfaceSecretName"] = hf_secret

    scan_s3_secret = params.get("scan-s3-secret-name", "")
    if scan_s3_secret:
        spec["scanS3SecretName"] = scan_s3_secret

    result_s3_secret = params.get("result-s3-secret-name", "")
    if result_s3_secret:
        spec["resultS3SecretName"] = result_s3_secret

    deploy_maas = params.get("deploy-maas", "")
    if deploy_maas == "true":
        maas = {"enabled": True}
        maas_gpu = params.get("maas-gpu-count", "")
        if maas_gpu:
            maas["gpuCount"] = maas_gpu
        maas_img = params.get("maas-runtime-image", "")
        if maas_img:
            maas["runtimeImage"] = maas_img
        maas_group = params.get("maas-authorized-group", "")
        if maas_group:
            maas["authorizedGroup"] = maas_group
        spec["maas"] = maas

    return spec


# --- Pipeline parameter defaults (mirrors model-intake-pipeline.yaml) ------

FORM_DEFAULTS = {
    "model-id": "ibm-granite/granite-3.3-2b-instruct",
    "model-name": "granite-2b",
    "model-version": "v1",
    "model-source": "huggingface",
    "lifecycle-profile": LIFECYCLE_PROFILE,
    "pipeline-ref": "",
    "requested-by": "",
    "target-namespace": "sandbox",
    "promotion-namespace-0": "staging",
    "promotion-namespace-1": "",
    "promotion-namespace-2": "",
    "promotion-namespace-3": "",
    "promotion-namespace-4": "",
    "context-length": "32768",
    "concurrency": "4",
    "allow-time-slicing": "true",
    "allow-mig": "false",
    "gpu-isolation-policy": "dedicated",
    "artifact-cve-threshold": "critical",
    "severity-threshold": "block",
    "gpu-count-override": "",
    "advisor-endpoint": DEFAULT_ADVISOR_ENDPOINT,
    "values-content": "",
    "authorized-viewers": "",
    "access-role": "view",
    "evalhub-secret-name": "evalhub-credentials",
    "huggingface-secret-name": "",
    "scan-s3-secret-name": "scan-s3-credentials",
    "result-s3-secret-name": "result-s3-credentials",
    "custom-data": "False",
    "custom-filename": "no-file",
    "deploy-maas": "false",
    "maas-gpu-count": "",
    "maas-runtime-image": "",
    "maas-authorized-group": "",
    "openshift-console-domain": "",
}

FORM_SECTIONS = [
    ("Model Identity", [
        "model-source", "model-id", "model-name", "model-version",
        "lifecycle-profile", "pipeline-ref", "requested-by",
    ]),
    ("Namespaces - Sandbox", ["target-namespace"]),
    ("Namespaces - Promotion Environments", [
        "promotion-namespace-0", "promotion-namespace-1",
        "promotion-namespace-2", "promotion-namespace-3",
        "promotion-namespace-4",
    ]),
    ("GPU Requirements", [
        "context-length", "concurrency", "allow-time-slicing", "allow-mig",
        "gpu-isolation-policy", "gpu-count-override", "advisor-endpoint",
    ]),
    ("Governance Thresholds", [
        "artifact-cve-threshold", "severity-threshold",
    ]),
    ("Deploy Override", ["values-content"]),
    ("Model Access", ["authorized-viewers", "access-role"]),
    ("Credential Secret References", [
        "evalhub-secret-name", "huggingface-secret-name",
        "scan-s3-secret-name", "result-s3-secret-name",
    ]),
    ("Benchmark Custom Data", ["custom-data", "custom-filename"]),
    ("MaaS (optional production deploy)", [
        "deploy-maas", "maas-gpu-count", "maas-runtime-image",
        "maas-authorized-group",
    ]),
    ("OpenShift Console", ["openshift-console-domain"]),
]

ALL_FORM_FIELDS = [f for _, fields in FORM_SECTIONS for f in fields]
PROMOTION_NS_FIELDS = [f for f in ALL_FORM_FIELDS if f.startswith("promotion-namespace-")]

REQUIRED_FIELDS = {"model-id", "model-name"}

FIELD_HELP = {
    "model-source": "Source of the model (e.g., huggingface, s3, oci).<br><b>Default:</b> huggingface",
    "model-id": "Hugging Face model ID to onboard.<br><b>Example:</b> <code>ibm-granite/granite-3.3-2b-instruct</code>",
    "model-name": "Kubernetes-safe deployment name. Lowercase, digits, <code>-</code> only.",
    "model-version": "Version label recorded in the model registry.<br><b>Example:</b> <code>v1</code>",
    "lifecycle-profile": "ModelLifecycleProfile that defines workflow, policy, and platform config.<br><b>Default:</b> standard-generative-onboarding",
    "pipeline-ref": "Optional override for the pipeline defined in the lifecycle profile.",
    "requested-by": "Your name / username for audit trail.",
    "target-namespace": "Sandbox namespace for compliance and security scans.<br><b>Default:</b> <code>sandbox</code>",
    "promotion-namespace-0": "Primary promotion namespace (e.g., <code>staging</code>).",
    "promotion-namespace-1": "Optional second promotion namespace (e.g., <code>test</code>).",
    "promotion-namespace-2": "Optional third promotion namespace (e.g., <code>production</code>).",
    "promotion-namespace-3": "Optional fourth promotion namespace.",
    "promotion-namespace-4": "Optional fifth promotion namespace.",
    "context-length": "Max context length for vLLM (<code>--max-model-len</code>).",
    "concurrency": "Expected concurrent requests.",
    "allow-time-slicing": "Allow NVIDIA time-slicing when no free GPU.<br><b>Values:</b> <code>true</code>/<code>false</code>",
    "allow-mig": "Allow Multi-Instance GPU partitioning.<br><b>Values:</b> <code>true</code>/<code>false</code>",
    "gpu-isolation-policy": "GPU isolation policy.<br><b>Default:</b> <code>dedicated</code>",
    "gpu-count-override": "Force GPU count (leave blank for capacity plan).",
    "advisor-endpoint": "Optional LLM endpoint for remote GPU advisor.",
    "artifact-cve-threshold": "CVE severity gate: <code>critical</code>, <code>high</code>, <code>none</code>",
    "severity-threshold": "Garak gate strictness: <code>block</code>, <code>warn</code>, <code>off</code>",
    "values-content": "Optional extra Helm values (YAML).",
    "authorized-viewers": "Comma-separated users/groups for RHOAI dashboard visibility.",
    "access-role": "Kubernetes role for authorized viewers.<br><b>Default:</b> <code>view</code>",
    "evalhub-secret-name": "K8s Secret name with EvalHub credentials (keys: <code>token</code>, <code>url</code>).",
    "huggingface-secret-name": "K8s Secret name with HuggingFace token (key: <code>token</code>).",
    "scan-s3-secret-name": "K8s Secret name for scan-result S3 (keys: <code>endpoint</code>, <code>accessKeyId</code>, <code>secretAccessKey</code>).",
    "result-s3-secret-name": "K8s Secret name for benchmark/eval S3 (keys: <code>endpoint</code>, <code>accessKeyId</code>, <code>secretAccessKey</code>).",
    "custom-data": "Use custom benchmark dataset (<code>True</code>/<code>False</code>).",
    "custom-filename": "Name of custom prompt file if custom-data is <code>True</code>.",
    "deploy-maas": "Deploy model via MaaS as final production step (<code>true</code>/<code>false</code>).",
    "maas-gpu-count": "Number of GPUs for MaaS deployment (override platform default).",
    "maas-runtime-image": "Runtime container image for MaaS deployment (override platform default).",
    "maas-authorized-group": "OpenShift group for MaaS access (override platform default).",
    "openshift-console-domain": "OpenShift AI console domain for EvalHub result links.",
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
  <fieldset{% if section == 'Namespaces - Promotion Environments' %} id="promotion-namespaces-fieldset"{% endif %}>
    <legend>{{ section }}</legend>
    {% for field in fields %}
    <div class="field-row{% if field.startswith('promotion-namespace-') %} promotion-ns-row{% endif %}"{% if field.startswith('promotion-namespace-') and loop.index > 1 %} style="display:none"{% endif %}>
    <label for="{{ field }}">{{ field }}{% if help.get(field) %}<span class="help">?<span class="tip">{{ help[field]|safe }}</span></span>{% endif %}</label>
    {% if field in ('values-content',) %}
    <textarea id="{{ field }}" name="{{ field }}">{{ defaults[field] }}</textarea>
    {% else %}
    <input type="text" id="{{ field }}" name="{{ field }}" value="{{ defaults[field] }}"{% if field in required_fields %} required{% endif %}>
    {% endif %}
    </div>
    {% endfor %}
    {% if section == 'Namespaces - Promotion Environments' %}
    <button type="button" id="add-namespace-btn" style="margin-top:8px; font-size:0.85em; padding:4px 12px;">+ Add Environment Namespace</button>
    {% endif %}
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
  var nextPromoIndex = 1;
  var promoContainer = document.getElementById("promotion-namespaces-fieldset");
  var addBtn = document.getElementById("add-namespace-btn");
  if (addBtn && promoContainer) {
    var promoRows = promoContainer.querySelectorAll(".promotion-ns-row");
    promoRows.forEach(function (row, i) {
      if (i > 0) { nextPromoIndex = Math.max(nextPromoIndex, i + 1); }
    });

    addBtn.addEventListener("click", function () {
      var newDiv = document.createElement("div");
      newDiv.className = "field-row promotion-ns-row";
      var fieldName = "promotion-namespace-" + nextPromoIndex;
      var label = document.createElement("label");
      label.htmlFor = fieldName;
      label.textContent = fieldName + " ";
      var helpSpan = document.createElement("span");
      helpSpan.className = "help";
      helpSpan.textContent = "?";
      var tipSpan = document.createElement("span");
      tipSpan.className = "tip";
      tipSpan.textContent = "Additional promotion namespace.";
      helpSpan.appendChild(tipSpan);
      label.appendChild(helpSpan);
      newDiv.appendChild(label);
      var input = document.createElement("input");
      input.type = "text";
      input.id = fieldName;
      input.name = fieldName;
      input.value = "";
      newDiv.appendChild(input);
      promoContainer.insertBefore(newDiv, addBtn);
      nextPromoIndex++;
    });
  }

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
<p><strong>Model:</strong> {{ model_uri }} ({{ model_source }})</p>
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
    model_info = spec.get("model", {})
    db.execute(
        "INSERT INTO model_requests (request_name, model_source, model_uri, model_name, target_environment, requested_by, spec_json, created_at) "
        "VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
        (
            request_name,
            model_info.get("sourceType", ""),
            model_info.get("uri", ""),
            model_info.get("name", ""),
            (spec.get("requirements", {}) or {}).get("targetEnvironment", ""),
            spec.get("requestedBy", ""),
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
                "model_source": r["model_source"] or "",
                "model_uri": r["model_uri"] or "",
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
    model_info = spec.get("model", {}) if spec else {}
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
        model_uri=model_info.get("uri", ""),
        model_source=model_info.get("sourceType", ""),
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
