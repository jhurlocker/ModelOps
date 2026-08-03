import os
from flask import Flask


def create_app():
    app = Flask(
        __name__,
        template_folder="templates",
        static_folder="static",
        static_url_path="/static",
    )
    app.config.from_object("app.config")
    app.secret_key = os.environ.get("SECRET_KEY", os.urandom(24).hex())

    from app.routes.overview import overview_bp
    from app.routes.intake import intake_bp
    from app.routes.requests import requests_bp
    from app.routes.approvals import approvals_bp
    from app.routes.gpu_inventory import gpu_inventory_bp
    from app.routes.platform_health import platform_health_bp
    from app.routes.configuration import configuration_bp

    app.register_blueprint(overview_bp)
    app.register_blueprint(intake_bp)
    app.register_blueprint(requests_bp)
    app.register_blueprint(approvals_bp)
    app.register_blueprint(gpu_inventory_bp)
    app.register_blueprint(platform_health_bp)
    app.register_blueprint(configuration_bp)

    from app.status_display import (
        humanize_stage_name, status_label, status_badge_class, stage_progress_badge_class,
    )
    app.jinja_env.filters["humanize_stage_name"] = humanize_stage_name
    app.jinja_env.filters["status_label"] = status_label
    app.jinja_env.filters["status_badge_class"] = status_badge_class
    app.jinja_env.filters["stage_progress_badge_class"] = stage_progress_badge_class

    @app.route("/healthz")
    def healthz():
        from datetime import datetime, timezone
        return {"status": "ok", "time": datetime.now(timezone.utc).isoformat()}

    return app
