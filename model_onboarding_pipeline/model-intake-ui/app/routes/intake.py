import hashlib
import time
import json

from flask import Blueprint, render_template, request, redirect, url_for

from app.config import PIPELINE_NAMESPACE, LIFECYCLE_PROFILE, DEFAULT_ADVISOR_ENDPOINT
from app.kubernetes.model_requests import create_model_request

intake_bp = Blueprint("intake", __name__, url_prefix="/intake")

FORM_STEP1_FIELDS = [
    "model-source", "model-id", "model-name", "model-version",
    "requested-by", "business-justification",
]
FORM_STEP2_FIELDS = [
    "context-length", "concurrency", "request-rate",
    "target-ttft", "target-throughput",
    "gpu-isolation-policy",
]
FORM_STEP3_FIELDS = [
    "lifecycle-profile", "promotion-namespace-0",
    "promotion-namespace-1", "promotion-namespace-2",
    "promotion-namespace-3", "promotion-namespace-4",
    "deploy-maas", "authorized-viewers", "access-role",
]
FORM_STEP4_FIELDS = []  # review only


@intake_bp.route("/")
def intake_form():
    defaults = {
        "model-source": "huggingface",
        "model-id": "ibm-granite/granite-3.3-2b-instruct",
        "model-tokenizer": "ibm-granite/granite-3.3-2b-instruct",
        "model-name": "granite-2b",
        "model-version": "v1",
        "lifecycle-profile": LIFECYCLE_PROFILE,
        "requested-by": "",
        "context-length": "32768",
        "concurrency": "4",
        "request-rate": "",
        "target-ttft": "",
        "target-throughput": "",
        "gpu-isolation-policy": "dedicated",
        "deploy-maas": "false",
        "authorized-viewers": "",
        "access-role": "view",
    }
    return render_template("intake/wizard.html", defaults=defaults, active_page="intake")


@intake_bp.route("/submit", methods=["POST"])
def submit():
    form_data = request.form.to_dict()

    model_source = form_data.get("model-source", "huggingface")
    model_uri = form_data.get("model-id", "").strip()

    if model_source == "oci":
        model_uri = _sanitize_oci_url(model_uri)

    spec = {
        "model": {
            "sourceType": model_source,
            "uri": model_uri,
            "name": form_data.get("model-name", ""),
            "version": form_data.get("model-version", "v1"),
            "tokenizer": form_data.get("model-tokenizer", ""),
        },
        "lifecycleProfile": form_data.get("lifecycle-profile", LIFECYCLE_PROFILE),
        "requestedBy": form_data.get("requested-by", ""),
    }

    reqs = {}
    ctx = form_data.get("context-length", "")
    if ctx:
        reqs["contextLength"] = int(ctx)
    conc = form_data.get("concurrency", "")
    if conc:
        reqs["expectedConcurrency"] = int(conc)
    iso = form_data.get("gpu-isolation-policy", "")
    if iso:
        reqs["gpuIsolationPolicy"] = iso

    promotion_namespaces = []
    for i in range(5):
        ns = form_data.get(f"promotion-namespace-{i}", "").strip()
        if ns:
            promotion_namespaces.append(ns)
    if promotion_namespaces:
        reqs["promotionNamespaces"] = promotion_namespaces

    reqs["sandboxNamespace"] = PIPELINE_NAMESPACE
    reqs["advisorEndpoint"] = form_data.get("advisor-endpoint", DEFAULT_ADVISOR_ENDPOINT)

    # Expert overrides
    gpu_override = form_data.get("gpu-count-override", "")
    if gpu_override:
        reqs["gpuCountOverride"] = int(gpu_override)
    values_content = form_data.get("values-content", "")
    if values_content:
        reqs["valuesContent"] = values_content
    console_domain = form_data.get("openshift-console-domain", "")
    if console_domain:
        reqs["openshiftConsoleDomain"] = console_domain

    # Thresholds (expert)
    cve_threshold = form_data.get("artifact-cve-threshold", "")
    if cve_threshold:
        reqs["cveThreshold"] = cve_threshold
    sev_threshold = form_data.get("severity-threshold", "")
    if sev_threshold:
        reqs["securityThreshold"] = sev_threshold

    if reqs:
        spec["requirements"] = reqs

    authorized = form_data.get("authorized-viewers", "")
    if authorized:
        spec["access"] = {
            "authorizedViewers": authorized,
            "accessRole": form_data.get("access-role", "view"),
        }

    if form_data.get("deploy-maas", "false") == "true":
        spec["maas"] = {
            "enabled": True,
            "gpuCount": form_data.get("maas-gpu-count", ""),
            "runtimeImage": form_data.get("maas-runtime-image", ""),
            "authorizedGroup": form_data.get("maas-authorized-group", ""),
        }

    # Expert secret references
    for key in ("evalhub-secret-name", "huggingface-secret-name", "scan-s3-secret-name", "result-s3-secret-name"):
        val = form_data.get(key, "")
        if val:
            spec[key.replace("-secret-name", "SecretName")] = val

    name_suffix = hashlib.sha256(f"{form_data.get('model-id','')}{time.time()}".encode()).hexdigest()[:10]
    req_name = f"model-intake-{name_suffix}"

    create_model_request(req_name, spec)

    return redirect(url_for("requests.request_detail", name=req_name))


def _sanitize_oci_url(url):
    for prefix in ("https://", "http://", "docker://"):
        if url.startswith(prefix):
            url = url[len(prefix):]
    modelcar_prefix = "quay.io/redhat-ai-services/modelcar-catalog:"
    if url.startswith(modelcar_prefix):
        url = url[len(modelcar_prefix):]
    return url
