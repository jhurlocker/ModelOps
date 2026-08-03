import logging

from flask import Blueprint, render_template, jsonify
from kubernetes.client.exceptions import ApiException

from app.kubernetes.model_requests import (
    list_model_requests, get_model_request, list_pipeline_runs, get_lifecycle_profile,
)
from app.config import PIPELINE_NAMESPACE
from app.status_display import resolve_stage_providers

requests_bp = Blueprint("requests", __name__, url_prefix="/requests")

logger = logging.getLogger(__name__)


@requests_bp.route("/")
def list_page():
    items = list_model_requests(limit=200)
    return render_template("requests/list.html", items=items, active_page="requests")


@requests_bp.route("/<name>")
def request_detail(name):
    mr = get_model_request(name)

    # Read-only "which provider does this request's profile resolve
    # to" lookup (see status_display.resolve_stage_providers) -- best
    # effort: a request whose profile was deleted/renamed should still
    # render the rest of the page.
    provider_rows = []
    profile_name = (mr.get("spec") or {}).get("lifecycleProfile")
    if profile_name:
        try:
            profile = get_lifecycle_profile(profile_name, namespace=mr.get("metadata", {}).get("namespace"))
            provider_rows = resolve_stage_providers(profile)
        except ApiException as exc:
            logger.warning("could not resolve lifecycle profile %r for provider display: %s", profile_name, exc)

    return render_template(
        "requests/detail.html", request=mr, provider_rows=provider_rows, active_page="requests",
    )


@requests_bp.route("/api")
def api_list():
    items = list_model_requests(limit=200)
    return jsonify(items)
