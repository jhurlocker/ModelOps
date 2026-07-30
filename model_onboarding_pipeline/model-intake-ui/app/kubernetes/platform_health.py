from app.kubernetes.client import core_api, apps_api
from app.kubernetes.model_requests import list_model_requests, list_capacity_plans, list_pipeline_runs, list_lifecycle_profiles, list_platform_configs
from app.config import PIPELINE_NAMESPACE


def check_operators():
    operators = []

    operator_deployments = {
        "ModelOps Operator": ("modelops", "modelops-operator"),
    }

    subscriptions = [
        {"name": "OpenShift Pipelines", "ns": "openshift-pipelines", "deploy": ("openshift-pipelines", "tekton-pipelines-controller")},
        {"name": "NVIDIA GPU Operator", "ns": "nvidia-gpu-operator", "deploy": ("nvidia-gpu-operator", "gpu-operator")},
        {"name": "OpenShift AI", "ns": "redhat-ods-operator", "deploy": ("redhat-ods-operator", "rhods-operator")},
    ]

    all_checks = []
    for name, (ns, deploy_name) in operator_deployments.items():
        all_checks.append((name, ns, deploy_name))
    for sub in subscriptions:
        all_checks.append((sub["name"], sub["ns"], sub["deploy"][1]))

    for name, ns, deploy_name in all_checks:
        try:
            dep = apps_api().read_namespaced_deployment(name=deploy_name, namespace=ns)
            available = dep.status.available_replicas or 0
            desired = dep.status.replicas or 0
            ready = dep.status.ready_replicas or 0
            healthy = available >= desired and ready >= desired
            operators.append({
                "component": name,
                "namespace": ns,
                "available": f"{available}/{desired}",
                "state": "Healthy" if healthy else "Degraded",
                "message": "Reconciling" if healthy else "Replicas not ready",
            })
        except Exception:
            operators.append({
                "component": name,
                "namespace": ns,
                "available": "0/0",
                "state": "Not Found",
                "message": "Deployment not found",
            })

    return operators


def check_lifecycle_resources():
    resources = []

    for mr in list_model_requests(limit=100):
        metadata = mr.get("metadata", {})
        status = mr.get("status", {})
        conditions = status.get("conditions", [])
        ready = False
        phase = "Unknown"
        for c in conditions:
            if c.get("type") == "Ready":
                ready = c.get("status") == "True"
            if c.get("type") == "Accepted":
                phase = "Accepted" if c.get("status") == "True" else phase

        resources.append({
            "kind": "ModelRequest",
            "name": metadata.get("name", ""),
            "namespace": metadata.get("namespace", ""),
            "phase": status.get("phase", phase),
            "ready": str(ready),
            "age": metadata.get("creationTimestamp", ""),
            "owner": "",
        })

    for cp in list_capacity_plans(limit=100):
        metadata = cp.get("metadata", {})
        status = cp.get("status", {})
        conditions = status.get("conditions", [])
        ready = False
        phase = status.get("phase", "Unknown")
        for c in conditions:
            if c.get("type") == "Ready":
                ready = c.get("status") == "True"

        resources.append({
            "kind": "CapacityPlan",
            "name": metadata.get("name", ""),
            "namespace": metadata.get("namespace", ""),
            "phase": phase,
            "ready": str(ready),
            "age": metadata.get("creationTimestamp", ""),
            "owner": metadata.get("ownerReferences", [{}])[0].get("name", "") if metadata.get("ownerReferences") else "",
        })

    for lp in list_lifecycle_profiles(limit=50):
        metadata = lp.get("metadata", {})
        status = lp.get("status", {})
        conditions = status.get("conditions", [])
        ready = False
        for c in conditions:
            if c.get("type") == "Ready":
                ready = c.get("status") == "True"

        resources.append({
            "kind": "ModelLifecycleProfile",
            "name": metadata.get("name", ""),
            "namespace": metadata.get("namespace", ""),
            "phase": status.get("phase", "Valid"),
            "ready": str(ready),
            "age": metadata.get("creationTimestamp", ""),
            "owner": "",
        })

    for pc in list_platform_configs(limit=50):
        metadata = pc.get("metadata", {})
        status = pc.get("status", {})
        conditions = status.get("conditions", [])
        ready = False
        for c in conditions:
            if c.get("type") == "Ready":
                ready = c.get("status") == "True"

        resources.append({
            "kind": "PlatformConfig",
            "name": metadata.get("name", ""),
            "namespace": metadata.get("namespace", ""),
            "phase": status.get("phase", "Valid"),
            "ready": str(ready),
            "age": metadata.get("creationTimestamp", ""),
            "owner": "",
        })

    return resources


def check_pipeline_executions():
    runs = list_pipeline_runs(limit=50)
    results = []
    for pr in runs:
        metadata = pr.get("metadata", {})
        status = pr.get("status", {})
        conditions = status.get("conditions", [])
        status_text = "Unknown"
        for c in conditions:
            if c.get("type") == "Succeeded":
                status_text = c.get("reason", "Unknown")
        results.append({
            "name": metadata.get("name", ""),
            "namespace": metadata.get("namespace", ""),
            "status": status_text,
            "duration": status.get("completionTime", ""),
            "started": metadata.get("creationTimestamp", ""),
        })
    return results
