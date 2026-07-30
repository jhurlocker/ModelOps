from flask import Blueprint, render_template, jsonify

from app.kubernetes.model_requests import list_model_requests, get_model_request, list_pipeline_runs
from app.config import PIPELINE_NAMESPACE

requests_bp = Blueprint("requests", __name__, url_prefix="/requests")


@requests_bp.route("/")
def list_page():
    items = list_model_requests(limit=200)
    return render_template("requests/list.html", items=items, active_page="requests")


@requests_bp.route("/<name>")
def request_detail(name):
    mr = get_model_request(name)
    return render_template("requests/detail.html", request=mr, active_page="requests")


@requests_bp.route("/api")
def api_list():
    items = list_model_requests(limit=200)
    return jsonify(items)
