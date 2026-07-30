from flask import Blueprint, render_template


from app.kubernetes.platform_health import check_operators, check_lifecycle_resources, check_pipeline_executions

platform_health_bp = Blueprint("platform_health", __name__, url_prefix="/platform-health")


@platform_health_bp.route("/")
def index():
    try:
        operators = check_operators()
    except Exception:
        operators = []
    try:
        resources = check_lifecycle_resources()
    except Exception:
        resources = []
    try:
        pipelines = check_pipeline_executions()
    except Exception:
        pipelines = []

    return render_template(
        "platform_health/index.html",
        operators=operators,
        resources=resources,
        pipelines=pipelines,
        active_page="platform_health",
    )
