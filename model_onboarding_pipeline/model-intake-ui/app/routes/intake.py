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


def _base_form_defaults():
    return {
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
        "s3-endpoint": "http://minio.modelops-storage.svc.cluster.local:9000",
        "s3-bucket": "compliance-artifact-results",
        "scan-s3-secret-name": "scan-s3-credentials",
        "result-s3-secret-name": "result-s3-credentials",
    }


# These two fields have no server-side credential fallback:
# internal/controller.resolveSecrets (Phase 1 of REFACTOR_PLAN.md)
# removed the old hardcoded minioadmin default, so a ModelRequest
# submitted with either one blank is guaranteed to reach
# SecretLookupFailed once the sandbox stage's secrets are resolved --
# it will never succeed on its own. Validated here so the wizard blocks
# submission instead of creating a request that's already doomed.
REQUIRED_SECRET_FIELDS = [
    ("scan-s3-secret-name", "Scan S3 Secret Name"),
    ("result-s3-secret-name", "Result S3 Secret Name"),
]


def _validate_required_secrets(form_data):
    errors = []
    for field, label in REQUIRED_SECRET_FIELDS:
        if not form_data.get(field, "").strip():
            errors.append(
                f"{label} is required (Governance step -> Show expert overrides -> "
                "S3 Connection Override). No default credential is used if this is "
                "left blank -- the request will fail once it reaches the sandbox stage."
            )
    return errors


@intake_bp.route("/")
def intake_form():
    return render_template(
        "intake/wizard.html", defaults=_base_form_defaults(), errors=[], active_page="intake",
    )


@intake_bp.route("/submit", methods=["POST"])
def submit():
    form_data = request.form.to_dict()

    errors = _validate_required_secrets(form_data)
    if errors:
        # Re-render with exactly what was submitted (not silently
        # repopulated with defaults) so the user can see which field(s)
        # are actually empty, rather than just being told "something is
        # wrong" -- overlaying onto _base_form_defaults() only fills in
        # keys the form itself never sends (e.g. an unchecked checkbox).
        defaults = {**_base_form_defaults(), **form_data}
        return render_template(
            "intake/wizard.html", defaults=defaults, errors=errors, active_page="intake",
        ), 400

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
        "displayName": form_data.get("display-name", ""),
        "businessJustification": form_data.get("business-justification", ""),
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
    rr = form_data.get("request-rate", "")
    if rr:
        reqs["requestRate"] = rr
    ttft = form_data.get("target-ttft", "")
    if ttft:
        reqs["targetTTFT"] = ttft
    tp = form_data.get("target-throughput", "")
    if tp:
        reqs["targetThroughput"] = tp

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
    # gpuCountOverride is a string field on the CRD (ModelRequirements.
    # GPUConfig.GPUCountOverride), not an int -- sending int() here was
    # silently rejected by the API server (422, "must be of type
    # string") on every submission that set this field. Send the raw
    # string value straight through.
    gpu_override = form_data.get("gpu-count-override", "")
    if gpu_override:
        reqs["gpuCountOverride"] = gpu_override
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

    # S3 connection overrides (expert). Credentials are never accepted as
    # raw form values -- only a non-secret endpoint/bucket override here;
    # actual credentials are always resolved via the *-secret-name fields
    # below (a Kubernetes Secret reference), never inline.
    for field, json_key in [("s3-endpoint", "resultS3Endpoint"), ("s3-bucket", "resultS3Bucket")]:
        val = form_data.get(field, "")
        if val:
            spec[json_key] = val

    # Expert secret references. An explicit map, not a generic
    # "-secret-name" -> "SecretName" string replace: the latter silently
    # produced the wrong key for the two S3 fields (e.g.
    # "scan-s3-secret-name" -> "scan-s3SecretName", not the CRD's actual
    # "scanS3SecretName", because ".replace()" only strips the literal
    # "-secret-name" suffix and leaves the embedded "-s3" hyphen alone).
    # The API server silently pruned the resulting unrecognized field on
    # every write -- confirmed live: a request submitted with both S3
    # secret-name fields correctly filled in still ended up with
    # spec.scanS3SecretName/resultS3SecretName unset, every time,
    # regardless of what was typed into the form.
    SECRET_NAME_FIELD_MAP = {
        "evalhub-secret-name": "evalhubSecretName",
        "huggingface-secret-name": "huggingfaceSecretName",
        "scan-s3-secret-name": "scanS3SecretName",
        "result-s3-secret-name": "resultS3SecretName",
    }
    for key, json_key in SECRET_NAME_FIELD_MAP.items():
        val = form_data.get(key, "")
        if val:
            spec[json_key] = val

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
