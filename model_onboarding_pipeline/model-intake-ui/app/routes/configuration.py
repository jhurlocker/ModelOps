from flask import Blueprint, render_template, jsonify, request as flask_request

import requests

from app.config import (
    PIPELINE_NAMESPACE, LIFECYCLE_PROFILE, DEFAULT_ADVISOR_ENDPOINT,
    SELF_INTERNAL_URL, PROMETHEUS_URL, DB_PATH,
)
from app.kubernetes.model_requests import (
    list_webhook_provider_configs, list_intake_provider_configs,
)

configuration_bp = Blueprint("configuration", __name__, url_prefix="/configuration")


@configuration_bp.route("/")
def index():
    config = {
        "pipeline_namespace": PIPELINE_NAMESPACE,
        "lifecycle_profile": LIFECYCLE_PROFILE,
        "advisor_endpoint": DEFAULT_ADVISOR_ENDPOINT or "Not set",
        "self_url": SELF_INTERNAL_URL,
        "prometheus_url": PROMETHEUS_URL,
        "db_path": DB_PATH,
    }
    webhook_providers = list_webhook_provider_configs()
    intake_providers = list_intake_provider_configs()
    return render_template(
        "configuration/index.html",
        config=config,
        webhook_providers=webhook_providers,
        intake_providers=intake_providers,
        active_page="configuration",
    )


@configuration_bp.route("/providers/health")
def provider_health():
    url = flask_request.args.get("url", "")
    if not url:
        return jsonify({"reachable": False, "error": "no url parameter"})
    try:
        resp = requests.get(url, timeout=5)
        return jsonify({"reachable": resp.ok, "status": resp.status_code})
    except requests.RequestException as e:
        return jsonify({"reachable": False, "error": str(e)})
