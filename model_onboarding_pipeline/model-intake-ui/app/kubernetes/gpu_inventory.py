from app.kubernetes.client import core_api


def list_gpu_nodes():
    nodes = core_api().list_node(label_selector="nvidia.com/gpu.present=true")
    return nodes.items


def get_node(name):
    return core_api().read_node(name=name)


def list_gpu_pods(namespace=None):
    if namespace:
        pods = core_api().list_namespaced_pod(namespace=namespace)
    else:
        pods = core_api().list_pod_for_all_namespaces()
    gpu_pods = []
    for pod in pods.items:
        for container in pod.spec.containers:
            reqs = (container.resources.requests or {})
            if "nvidia.com/gpu" in reqs:
                gpu_pods.append(pod)
                break
    return gpu_pods


def parse_gpu_label_value(node, label_key, default="unknown"):
    return node.metadata.labels.get(label_key, default)


def get_node_gpu_info(node):
    labels = node.metadata.labels
    name = node.metadata.name
    status_capacity = node.status.capacity or {}
    status_allocatable = node.status.allocatable or {}

    gpu_count = int(status_allocatable.get("nvidia.com/gpu", 0))
    gpu_product = labels.get("nvidia.com/gpu.product", "unknown")
    gpu_memory = labels.get("nvidia.com/gpu.memory", "unknown")
    gpu_memory_mb = labels.get("nvidia.com/gpu.memory", "unknown")
    mig_enabled = labels.get("nvidia.com/mig.config", "")
    time_slicing_replicas = labels.get("nvidia.com/gpu.replicas", "1")
    is_shared = "-SHARED" in gpu_product

    return {
        "node_name": name,
        "gpu_count": gpu_count,
        "gpu_product": gpu_product,
        "gpu_memory": gpu_memory_mb,
        "mig_enabled": bool(mig_enabled),
        "mig_config": mig_enabled,
        "time_slicing_replicas": int(time_slicing_replicas),
        "is_shared": is_shared,
    }
