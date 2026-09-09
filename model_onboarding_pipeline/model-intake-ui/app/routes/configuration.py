from flask import Blueprint, render_template
from app.config import (
    PIPELINE_NAMESPACE, LIFECYCLE_PROFILE, DEFAULT_ADVISOR_ENDPOINT,
    SELF_INTERNAL_URL, PROMETHEUS_URL, DB_PATH,
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
    return render_template("configuration/index.html", config=config, active_page="configuration")
