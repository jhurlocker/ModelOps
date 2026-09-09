import logging

from app.kubernetes.model_requests import (
    list_model_requests,
    list_capacity_plans,
    list_pipeline_runs,
    list_inference_services,
    inference_service_ready,
)
from app.kubernetes.gpu_inventory import list_gpu_nodes, get_node_gpu_info, list_gpu_pods
from app.kubernetes.prometheus import collect_gpu_telemetry
from app.services.approval_service import list_plans
from app.config import PIPELINE_NAMESPACE

logger = logging.getLogger(__name__)


def _safe(fn, default):
    try:
        return fn()
    except Exception as exc:
        logger.warning("%s failed: %s", getattr(fn, "__name__", "call"), exc)
        return default


def build_overview_metrics():
    model_requests = _safe(lambda: list_model_requests(limit=200), [])
    capacity_plans = _safe(lambda: list_capacity_plans(limit=200), [])
    pipeline_runs = _safe(lambda: list_pipeline_runs(limit=100), [])
    plans = _safe(list_plans, [])
    inference_services = _safe(lambda: list_inference_services(limit=200), [])

    active_phases = ("Pending", "Evaluating", "Deploying", "Running")
    active_requests = len(
        [r for r in model_requests if r.get("status", {}).get("phase", "") in active_phases]
    )
    running_prs = [
        pr for pr in pipeline_runs if _get_pipelinerun_status(pr) in ("Running", "Started", "Pending")
    ]
    if not active_requests:
        active_requests = len(running_prs)

    awaiting_approval = len([p for p in plans if p.get("status") == "pending"])

    succeeded = len([pr for pr in pipeline_runs if _get_pipelinerun_status(pr) == "Succeeded"])
    failed = len([pr for pr in pipeline_runs if _get_pipelinerun_status(pr) == "Failed"])

    ready_services = [isvc for isvc in inference_services if inference_service_ready(isvc)]
    models_deployed = len(ready_services)
    if models_deployed == 0:
        succeeded_phases = ("Succeeded", "Promoting", "Deploying")
        models_deployed = len(
            [mr for mr in model_requests if mr.get("status", {}).get("phase", "") in succeeded_phases]
        )

    gpu_nodes = _safe(list_gpu_nodes, [])
    total_gpus = 0
    physical_gpus = 0
    for node in gpu_nodes:
        info = get_node_gpu_info(node)
        total_gpus += info["schedulable_gpu_count"]
        physical_gpus += info["physical_gpu_count"]

    allocated_gpus = 0
    try:
        gpu_pods = list_gpu_pods()
        allocated_gpus = _count_allocated_gpus(gpu_pods)
    except Exception as exc:
        logger.warning("count allocated GPUs failed: %s", exc)
        allocated_gpus = 0

    telemetry = _safe(collect_gpu_telemetry, {})
    util_samples = [entry["util"] for entry in telemetry.values() if entry.get("util") is not None]
    if util_samples:
        gpu_utilization = "{}%".format(int(round(sum(util_samples) / len(util_samples))))
    elif total_gpus > 0:
        gpu_utilization = "{}%".format(int(round(allocated_gpus / total_gpus * 100)))
    else:
        gpu_utilization = "0%"

    if total_gpus > 0:
        gpu_capacity = "{} / {} allocated".format(allocated_gpus, total_gpus)
    else:
        gpu_capacity = "0 / 0 allocated"

    attention_items = []

    for mr in model_requests:
        phase = mr.get("status", {}).get("phase", "Unknown")
        if phase == "Failed":
            name = mr.get("metadata", {}).get("name", "")
            attention_items.append({
                "type": "failed_request",
                "title": "Failed request: {}".format(name),
                "description": "ModelRequest {} is in Failed phase".format(name),
                "link": "/requests/{}".format(name),
            })

    for plan in plans:
        if plan.get("status") == "pending":
            attention_items.append({
                "type": "pending_approval",
                "title": "Awaiting approval: {}".format(plan.get("model_name", plan["plan_id"])),
                "description": "Plan {} needs review".format(plan["plan_id"]),
                "link": "/approvals/{}".format(plan["plan_id"]),
            })

    unhealthy_requests = [r for r in model_requests if r.get("status", {}).get("phase") == "Failed"]
    unhealthy_plans = [p for p in capacity_plans if _get_plan_phase(p) == "Failed"]
    running_failed = any(_get_pipelinerun_status(pr) in ("Failed", "TaskRunFailed") for pr in running_prs)
    if not gpu_nodes:
        platform_health = "Degraded"
    elif unhealthy_requests or unhealthy_plans or running_failed:
        platform_health = "Degraded"
    else:
        platform_health = "Healthy"

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
        "physical_gpus": physical_gpus,
        "namespace": PIPELINE_NAMESPACE,
        "succeeded_runs": succeeded,
    }


def _get_pipelinerun_status(pr):
    conditions = pr.get("status", {}).get("conditions", [])
    for c in conditions:
        if c.get("type") == "Succeeded":
            status = c.get("status")
            if status == "True":
                return "Succeeded"
            if status == "False":
                return c.get("reason", "Failed")
            return c.get("reason", "Running")
    return "Pending"


def _get_plan_phase(plan):
    conditions = plan.get("status", {}).get("conditions", [])
    for c in conditions:
        if c.get("type") == "Ready" and c.get("status") == "False":
            return "Failed"
    return plan.get("status", {}).get("phase", "Unknown")


def _count_allocated_gpus(pods):
    count = 0
    for pod in pods:
        if getattr(pod.status, "phase", "") not in ("Running", "Pending"):
            continue
        for container in pod.spec.containers:
            requests = (container.resources.requests or {})
            gpu_str = requests.get("nvidia.com/gpu", "0")
            try:
                count += int(gpu_str)
            except (ValueError, TypeError):
                pass
    return count
