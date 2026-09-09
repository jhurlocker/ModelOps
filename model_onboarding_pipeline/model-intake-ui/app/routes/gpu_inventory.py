from flask import Blueprint, render_template

from app.kubernetes.gpu_inventory import list_gpu_nodes, get_node_gpu_info, list_gpu_pods
from app.kubernetes.prometheus import collect_gpu_telemetry, match_telemetry
from app.config import GPU_UTILIZATION_WARNING_THRESHOLD

gpu_inventory_bp = Blueprint("gpu_inventory", __name__, url_prefix="/gpu-inventory")


def _fmt_mib(value):
    try:
        n = float(value)
    except (TypeError, ValueError):
        return None
    if n >= 1024:
        return "{:.1f} GiB".format(n / 1024)
    return "{} MiB".format(int(round(n)))


def _format_vram(telem, fallback_memory):
    used = telem.get("fb_used")
    free = telem.get("fb_free")
    reserved = telem.get("fb_reserved") or 0
    if used is not None and free is not None:
        total = used + free + reserved
        used_s = _fmt_mib(used)
        total_s = _fmt_mib(total)
        pct = int(round(used / total * 100)) if total else 0
        return "{} / {} ({}%)".format(used_s, total_s, pct)
    if fallback_memory not in (None, "", "0", "unknown"):
        return "0 / {} MiB (0%)".format(fallback_memory)
    return "0 / 0 MiB (0%)"


def _format_util(telem, allocated):
    if telem.get("util") is not None:
        return "{}%".format(int(round(telem["util"])))
    return "0%"


def _format_health(telem, node_ready):
    temp = telem.get("temp")
    if temp is not None and temp >= 85:
        return "Warning"
    if node_ready:
        return "Healthy"
    return "Not Ready"


@gpu_inventory_bp.route("/")
def index():
    try:
        nodes = list_gpu_nodes()
    except Exception:
        nodes = []

    telemetry = collect_gpu_telemetry()
    gpu_rows = []
    total_allocated = 0
    total_capacity = 0

    for node in nodes:
        info = get_node_gpu_info(node)
        physical_count = info["physical_gpu_count"]
        total_capacity += info["schedulable_gpu_count"]
        workloads_by_gpu = _find_workloads_on_node(node.metadata.name, physical_count)

        for i in range(physical_count):
            telem = match_telemetry(telemetry, info["node_name"], i)
            workloads = workloads_by_gpu.get(i, [])
            allocated = bool(workloads)
            if allocated:
                total_allocated += len(workloads)

            product = telem.get("model") or info["display_product"]
            utilization = _format_util(telem, allocated)
            vram = _format_vram(telem, info["gpu_memory"])
            temp = telem.get("temp")
            power = telem.get("power")

            gpu_rows.append({
                "node_name": info["node_name"],
                "gpu_index": i,
                "product": product,
                "memory": "{} MiB".format(info["gpu_memory"]) if info["gpu_memory"] else "0 MiB",
                "partitioning": _partition_label(info),
                "workloads": workloads,
                "utilization": utilization,
                "vram": vram,
                "health": _format_health(telem, info["node_ready"]),
                "mig_enabled": info["mig_enabled"],
                "mig_config": info.get("mig_config", ""),
                "allocated": allocated,
                "time_slicing_replicas": info["time_slicing_replicas"],
                "temp": "{}°C".format(int(round(temp))) if temp is not None else "—",
                "power": "{} W".format(int(round(power))) if power is not None else "—",
                "uuid": telem.get("uuid") or "—",
            })

    if total_allocated == 0:
        # Fall back to scheduler slice count so the summary is never empty.
        try:
            total_allocated = _count_running_gpu_requests()
        except Exception:
            total_allocated = 0

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
        return "MIG: {}".format(info.get("mig_config", "enabled"))
    if info["time_slicing_replicas"] > 1:
        return "Time-sliced x{}".format(info["time_slicing_replicas"])
    return "Dedicated"


def _pod_gpu_request(pod):
    total = 0
    for container in pod.spec.containers:
        reqs = (container.resources.requests or {})
        try:
            total += int(reqs.get("nvidia.com/gpu", 0))
        except (TypeError, ValueError):
            pass
    return total


def _find_workloads_on_node(node_name, physical_count):
    by_gpu = {i: [] for i in range(max(physical_count, 1))}
    try:
        all_pods = list_gpu_pods()
    except Exception:
        return by_gpu

    round_robin = 0
    for pod in all_pods:
        if pod.spec.node_name != node_name:
            continue
        if pod.status.phase not in ("Running", "Pending"):
            continue
        model_name = (pod.metadata.labels or {}).get("serving.kserve.io/inferenceservice") or (
            (pod.metadata.labels or {}).get("model-name") or pod.metadata.name
        )
        ns = pod.metadata.namespace
        label = "{} ({})".format(model_name, ns)
        gpu_index = round_robin % max(physical_count, 1)
        by_gpu[gpu_index].append(label)
        round_robin += 1
    return by_gpu


def _count_running_gpu_requests():
    count = 0
    for pod in list_gpu_pods():
        if pod.status.phase not in ("Running", "Pending"):
            continue
        count += _pod_gpu_request(pod)
    return count
