from flask import Blueprint, render_template
from app.services.inventory_service import build_overview_metrics

overview_bp = Blueprint("overview", __name__, url_prefix="/")


@overview_bp.context_processor
def inject_platform_health():
    try:
        metrics = build_overview_metrics()
        return {"platform_health_status": metrics.get("platform_health", "Healthy")}
    except Exception:
        return {"platform_health_status": "Healthy"}


@overview_bp.route("/")
def index():
    try:
        metrics = build_overview_metrics()
    except Exception:
        metrics = {
            "gpu_capacity": "0 / 0 allocated",
            "gpu_utilization": "0%",
            "models_deployed": 0,
            "active_requests": 0,
            "awaiting_approval": 0,
            "platform_health": "Degraded",
            "attention_items": [],
            "recent_pipeline_runs": [],
        }
    return render_template("overview/index.html", metrics=metrics, active_page="overview")
