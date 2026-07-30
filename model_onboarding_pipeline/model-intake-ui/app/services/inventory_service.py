from app.kubernetes.model_requests import list_model_requests, list_capacity_plans, list_pipeline_runs
from app.kubernetes.gpu_inventory import list_gpu_nodes, get_node_gpu_info, list_gpu_pods
from app.services.approval_service import list_plans
from app.config import PIPELINE_NAMESPACE


def build_overview_metrics():
    model_requests = list_model_requests(limit=200)
    capacity_plans = list_capacity_plans(limit=200)
    pipeline_runs = list_pipeline_runs(limit=100)
    plans = list_plans()

    total_requests = len(model_requests)
    active_requests = len([r for r in model_requests if r.get("status", {}).get("phase", "") in ("Pending", "Evaluating", "Deploying")])

    failed_requests = len([r for r in model_requests if r.get("status", {}).get("phase", "") == "Failed"])

    awaiting_approval = len([p for p in plans if p.get("status") == "pending"])

    succeeded = len([pr for pr in pipeline_runs if _get_pipelinerun_status(pr) == "Succeeded"])
    failed = len([pr for pr in pipeline_runs if _get_pipelinerun_status(pr) == "Failed"])
    running = len([pr for pr in pipeline_runs if _get_pipelinerun_status(pr) == "Running"])

    models_deployed = 0
    succeeded_phases = ("Succeeded", "Promoting", "Deploying")
    for mr in model_requests:
        phase = mr.get("status", {}).get("phase", "")
        if phase in succeeded_phases:
            models_deployed += 1

    gpu_nodes = list_gpu_nodes()
    total_gpus = 0
    allocated_gpus = 0
    for node in gpu_nodes:
        info = get_node_gpu_info(node)
        total_gpus += info["gpu_count"]

    try:
        gpu_pods = list_gpu_pods()
        allocated_gpus = _count_allocated_gpus(gpu_pods)
    except Exception:
        allocated_gpus = 0

    if total_gpus > 0:
        gpu_capacity = f"{allocated_gpus} / {total_gpus} allocated"
        gpu_utilization = f"{int(allocated_gpus / total_gpus * 100)}%"
    else:
        gpu_capacity = "0 / 0 allocated"
        gpu_utilization = "N/A"

    attention_items = []

    for mr in model_requests:
        phase = mr.get("status", {}).get("phase", "Unknown")
        if phase == "Failed":
            name = mr.get("metadata", {}).get("name", "")
            attention_items.append({
                "type": "failed_request",
                "title": f"Failed request: {name}",
                "description": f"ModelRequest {name} is in Failed phase",
                "link": f"/requests/{name}",
            })

    for plan in plans:
        if plan.get("status") == "pending":
            attention_items.append({
                "type": "pending_approval",
                "title": f"Awaiting approval: {plan.get('model_name', plan['plan_id'])}",
                "description": f"Plan {plan['plan_id']} needs review",
                "link": f"/approvals/{plan['plan_id']}",
            })

    unhealthy_requests = [r for r in model_requests if r.get("status", {}).get("phase") == "Failed"]
    unhealthy_plans = [p for p in capacity_plans if _get_plan_phase(p) == "Failed"]
    has_unhealthy = bool(unhealthy_requests or unhealthy_plans)
    platform_health = "Degraded" if (has_unhealthy or failed > 0) else "Healthy"

    return {
        "gpu_capacity": gpu_capacity,
        "gpu_utilization": gpu_utilization,
        "models_deployed": models_deployed,
        "active_requests": active_requests,
        "awaiting_approval": awaiting_approval,
        "platform_health": platform_health,
        "attention_items": attention_items,
        "recent_pipeline_runs": [
            {
                "name": pr.get("metadata", {}).get("name", ""),
                "status": _get_pipelinerun_status(pr),
                "started": pr.get("metadata", {}).get("creationTimestamp", ""),
            }
            for pr in pipeline_runs[:5]
        ],
    }


def _get_pipelinerun_status(pr):
    conditions = pr.get("status", {}).get("conditions", [])
    for c in conditions:
        if c.get("type") == "Succeeded":
            return c.get("reason", "Unknown")
    return "Unknown"


def _get_plan_phase(plan):
    conditions = plan.get("status", {}).get("conditions", [])
    for c in conditions:
        if c.get("type") == "Ready" and c.get("status") == "False":
            return "Failed"
    return plan.get("status", {}).get("phase", "Unknown")


def _count_allocated_gpus(pods):
    count = 0
    for pod in pods:
        for container in pod.spec.containers:
            requests = (container.resources.requests or {})
            gpu_str = requests.get("nvidia.com/gpu", "0")
            try:
                count += int(gpu_str)
            except (ValueError, TypeError):
                pass
    return count
