from flask import Blueprint, render_template

from app.kubernetes.gpu_inventory import list_gpu_nodes, get_node_gpu_info, list_gpu_pods
from app.config import GPU_UTILIZATION_WARNING_THRESHOLD

gpu_inventory_bp = Blueprint("gpu_inventory", __name__, url_prefix="/gpu-inventory")


@gpu_inventory_bp.route("/")
def index():
    try:
        nodes = list_gpu_nodes()
    except Exception:
        nodes = []

    gpu_rows = []
    total_allocated = 0
    total_capacity = 0

    for node in nodes:
        info = get_node_gpu_info(node)
        gpu_count = info["gpu_count"]
        total_capacity += gpu_count

        workloads = _find_workloads_on_node(node.metadata.name)
        for i in range(gpu_count):
            wl_for_gpu = workloads[i] if i < len(workloads) else None
            allocated = bool(wl_for_gpu)

            if allocated:
                total_allocated += 1

            gpu_rows.append({
                "node_name": info["node_name"],
                "gpu_index": i,
                "product": info["gpu_product"],
                "memory": info["gpu_memory"],
                "partitioning": _partition_label(info),
                "workloads": wl_for_gpu["workloads"] if wl_for_gpu else [],
                "utilization": wl_for_gpu.get("utilization", "0%") if wl_for_gpu else "0%",
                "vram": wl_for_gpu.get("vram", f"1/{info['gpu_memory']}") if wl_for_gpu else f"0/{info['gpu_memory']}",
                "health": "Healthy",
                "mig_enabled": info["mig_enabled"],
                "mig_config": info.get("mig_config", ""),
                "allocated": allocated,
                "time_slicing_replicas": info["time_slicing_replicas"],
            })

    return render_template(
        "gpu_inventory/index.html",
        gpu_rows=gpu_rows,
        total_allocated=total_allocated,
        total_capacity=total_capacity,
        warning_threshold=GPU_UTILIZATION_WARNING_THRESHOLD,
        active_page="gpu_inventory",
    )


def _partition_label(info):
    if info["mig_enabled"]:
        return f"MIG: {info.get('mig_config', 'enabled')}"
    if info["time_slicing_replicas"] > 1:
        return f"Time-sliced x{info['time_slicing_replicas']}"
    return "Dedicated"


def _find_workloads_on_node(node_name):
    try:
        all_pods = list_gpu_pods()
    except Exception:
        return []

    workloads = []
    for pod in all_pods:
        if pod.spec.node_name != node_name:
            continue
        if pod.status.phase not in ("Running", "Pending"):
            continue

        model_name = pod.metadata.labels.get("model-name", pod.metadata.name)
        ns = pod.metadata.namespace
        workloads.append(f"{model_name} ({ns})")

    return [{"workloads": workloads, "utilization": "N/A", "vram": "N/A"}] if workloads else []
