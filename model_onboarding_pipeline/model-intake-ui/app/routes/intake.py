import hashlib
import logging
import re
import time

from flask import Blueprint, render_template, request, redirect, url_for

from app.config import (
    PIPELINE_NAMESPACE,
    LIFECYCLE_PROFILE,
    DEFAULT_ADVISOR_ENDPOINT,
    SELF_INTERNAL_URL,
    DEFAULT_S3_ACCESS_KEY,
    DEFAULT_S3_SECRET_KEY,
)
from app.kubernetes.model_requests import create_pipeline_run, resolve_evalhub_host

logger = logging.getLogger(__name__)

intake_bp = Blueprint("intake", __name__, url_prefix="/intake")


def _base_form_defaults():
    return {
        "model-source": "huggingface",
        "model-id": "ibm-granite/granite-3.3-2b-instruct",
        "model-tokenizer": "ibm-granite/granite-3.3-2b-instruct",
        "model-name": "granite-2b",
        "model-version": "v1",
        "display-name": "",
        "lifecycle-profile": LIFECYCLE_PROFILE,
        "requested-by": "",
        "business-justification": "",
        "context-length": "32768",
        "concurrency": "4",
        "request-rate": "",
        "target-ttft": "",
        "target-throughput": "",
        "gpu-isolation-policy": "dedicated",
        "deploy-maas": "false",
        "authorized-viewers": "",
        "access-role": "view",
        "promotion-namespace-0": "vllm-staging",
        "promotion-namespace-1": "",
        "s3-endpoint": "http://minio-service.s3-storage.svc.cluster.local:9000",
        "s3-bucket": "benchmark-results",
        "scan-s3-secret-name": "scan-s3-credentials",
        "result-s3-secret-name": "result-s3-credentials",
        "advisor-endpoint": DEFAULT_ADVISOR_ENDPOINT,
        "sandbox-namespace": PIPELINE_NAMESPACE,
    }


def _validate_model_source_uri(model_source, model_uri):
    errors = []
    if not model_uri:
        errors.append(("model-id", "Model URL is required."))
        return errors
    if model_source == "huggingface" and ("://" in model_uri or ":" in model_uri):
        errors.append(("model-id", (
            "Model ID (\"{}\") looks like a URL or OCI image reference, "
            "not a Hugging Face repo id (e.g. \"ibm-granite/granite-3.3-2b-instruct\"). "
            "If you're referencing a container image, switch Model Source to "
            "\"OCI Container Registry\" first."
        ).format(model_uri)))
    return errors


def _k8s_name(value, fallback="model"):
    name = (value.split("/")[-1] if value else fallback).lower()
    name = re.sub(r"[._]", "-", name)
    name = re.sub(r"[^a-z0-9-]", "", name)
    name = re.sub(r"-+", "-", name).strip("-")
    return name or fallback


def _sanitize_oci_url(url):
    for prefix in ("https://", "http://", "docker://", "oci://"):
        if url.startswith(prefix):
            url = url[len(prefix):]
    modelcar_prefix = "quay.io/redhat-ai-services/modelcar-catalog:"
    if url.startswith(modelcar_prefix):
        url = url[len(modelcar_prefix):]
    return url


def _pipeline_params_from_form(form_data):
    model_source = form_data.get("model-source", "huggingface")
    model_uri = form_data.get("model-id", "").strip()
    if model_source == "oci":
        model_uri = _sanitize_oci_url(model_uri)

    model_name = _k8s_name(form_data.get("model-name") or model_uri, "model")
    staging_ns = (
        form_data.get("promotion-namespace-0", "").strip()
        or "vllm-staging"
    )

    params = {
        "model-id": model_uri,
        "model-name": model_name,
        "model-version": form_data.get("model-version", "v1").strip() or "v1",
        "requested-by": form_data.get("requested-by", "").strip(),
        "target-namespace": PIPELINE_NAMESPACE,
        "staging-namespace": staging_ns,
        "context-length": form_data.get("context-length", "").strip() or "32768",
        "concurrency": form_data.get("concurrency", "").strip() or "4",
        "gpu-isolation-policy": form_data.get("gpu-isolation-policy", "dedicated") or "dedicated",
        "authorized-viewers": form_data.get("authorized-viewers", "").strip(),
        "access-role": form_data.get("access-role", "view") or "view",
        "release-name": model_name,
        "approval-api-url": SELF_INTERNAL_URL,
        "deploy-maas": "true" if form_data.get("deploy-maas") == "true" else "false",
        "s3-access-key-id": DEFAULT_S3_ACCESS_KEY,
        "s3-secret-access-key": DEFAULT_S3_SECRET_KEY,
    }

    tokenizer = form_data.get("model-tokenizer", "").strip()
    if model_source == "oci" and model_uri:
        params["modelcar-image"] = model_uri
        if tokenizer:
            params["model-id"] = tokenizer

    for form_key, param_key in (
        ("advisor-endpoint", "advisor-endpoint"),
        ("gpu-count-override", "gpu-count-override"),
        ("values-content", "values-content"),
        ("artifact-cve-threshold", "artifact-cve-threshold"),
        ("severity-threshold", "severity-threshold"),
        ("garak-benchmarks", "garak-benchmarks"),
        ("openshift-console-domain", "openshift-console-domain"),
        ("s3-endpoint", "s3-api-endpoint"),
        ("maas-gpu-count", "maas-gpu-count"),
        ("maas-runtime-image", "maas-runtime-image"),
        ("maas-authorized-group", "maas-authorized-group"),
    ):
        val = form_data.get(form_key, "").strip()
        if val:
            params[param_key] = val

    evalhub = resolve_evalhub_host()
    if evalhub:
        params["evalhub-url"] = evalhub

    return params


@intake_bp.route("/")
def intake_form():
    return render_template(
        "intake/wizard.html", defaults=_base_form_defaults(),
        errors=[], error_fields=set(), start_step=0, active_page="intake",
    )


@intake_bp.route("/submit", methods=["POST"])
def submit():
    form_data = {**_base_form_defaults(), **request.form.to_dict()}

    model_source = form_data.get("model-source", "huggingface")
    model_uri = form_data.get("model-id", "").strip()
    model_name = form_data.get("model-name", "").strip()

    field_errors = _validate_model_source_uri(model_source, model_uri)
    if not model_name:
        field_errors.append(("model-name", "Model Name is required."))

    if field_errors:
        error_fields = {field for field, _ in field_errors}
        errors = [message for _, message in field_errors]
        start_step = 0 if "model-id" in error_fields or "model-name" in error_fields else 3
        return render_template(
            "intake/wizard.html",
            defaults=form_data,
            errors=errors,
            error_fields=error_fields,
            start_step=start_step,
            active_page="intake",
        ), 400

    params = _pipeline_params_from_form(form_data)
    suffix = hashlib.sha256("{}{}".format(model_uri, time.time()).encode()).hexdigest()[:6]
    run_name = "{}-onboard-{}".format(params["model-name"], suffix)[:63]

    try:
        create_pipeline_run(run_name, params)
    except Exception as exc:
        logger.exception("failed to create PipelineRun")
        return render_template(
            "intake/wizard.html",
            defaults=form_data,
            errors=["Could not create pipeline run: {}".format(exc)],
            error_fields=set(),
            start_step=3,
            active_page="intake",
        ), 500

    return redirect(url_for("requests.request_detail", name=run_name))
