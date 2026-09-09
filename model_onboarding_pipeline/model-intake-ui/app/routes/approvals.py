import json
from flask import Blueprint, render_template, request, redirect, url_for, jsonify

from app.services.approval_service import (
    init_db, list_plans, get_plan, upsert_plan, decide_plan, get_plan_by_pipelinerun,
)

approvals_bp = Blueprint("approvals", __name__, url_prefix="/approvals")


@approvals_bp.before_request
def _ensure_db():
    init_db()


@approvals_bp.route("/")
def list_page():
    plans = list_plans()
    return render_template("approvals/list.html", plans=plans, active_page="approvals")


@approvals_bp.route("/<plan_id>")
def detail(plan_id):
    plan = get_plan(plan_id)
    if not plan:
        return render_template("approvals/not_found.html", plan_id=plan_id, active_page="approvals"), 404

    def _try_parse(val):
        if not val:
            return None
        try:
            return json.loads(val)
        except (json.JSONDecodeError, TypeError):
            return val

    options = _try_parse(plan.get("deployment_options"))
    inventory = _try_parse(plan.get("gpu_inventory"))
    recommendation = plan.get("recommendation_md", "")
    vllm_cmd = plan.get("recommended_vllm_command", "")

    return render_template(
        "approvals/detail.html",
        plan=plan,
        options=options,
        inventory=inventory,
        recommendation=recommendation,
        vllm_cmd=vllm_cmd,
        active_page="approvals",
    )


@approvals_bp.route("/<plan_id>/approve", methods=["POST"])
def approve(plan_id):
    comment = request.form.get("comment", "")
    decide_plan(plan_id, "approved", "UI User", comment)
    return redirect(url_for("approvals.detail", plan_id=plan_id))


@approvals_bp.route("/<plan_id>/reject", methods=["POST"])
def reject(plan_id):
    comment = request.form.get("comment", "")
    decide_plan(plan_id, "rejected", "UI User", comment)
    return redirect(url_for("approvals.detail", plan_id=plan_id))


# JSON API routes for Tekton approval-gate tool
@approvals_bp.route("/api/plans", methods=["GET", "POST"])
def api_plans():
    if request.method == "GET":
        return jsonify(list_plans())

    data = request.get_json(force=True)
    plan_id = data.get("plan_id", "")
    if not plan_id:
        return jsonify({"error": "plan_id required"}), 400

    upsert_data = {
        "plan_id": plan_id,
        "pipelinerun_name": data.get("pipelinerun_name", ""),
        "model_id": data.get("model_id", ""),
        "model_name": data.get("model_name", ""),
        "target_namespace": data.get("target_namespace", ""),
        "requested_by": data.get("requested_by", ""),
        "plan_status": data.get("plan_status", ""),
        "recommendation_md": data.get("recommendation_md", ""),
        "deployment_options": json.dumps(data.get("deployment_options", {})),
        "gpu_inventory": json.dumps(data.get("gpu_inventory", {})),
        "recommended_vllm_command": data.get("recommended_vllm_command", ""),
    }
    upsert_plan(upsert_data)
    return jsonify({"status": "created", "plan_id": plan_id})


@approvals_bp.route("/api/plans/<plan_id>")
def api_plan_get(plan_id):
    plan = get_plan(plan_id)
    if not plan:
        return jsonify({"error": "not found"}), 404
    return jsonify({"plan_id": plan_id, "status": plan.get("status", "pending")})


@approvals_bp.route("/api/plans/<plan_id>/approve", methods=["POST"])
def api_approve(plan_id):
    decide_plan(plan_id, "approved", "API Caller")
    return jsonify({"status": "approved"})


@approvals_bp.route("/api/plans/<plan_id>/reject", methods=["POST"])
def api_reject(plan_id):
    decide_plan(plan_id, "rejected", "API Caller")
    return jsonify({"status": "rejected"})
