import logging

from app.config import PIPELINE_NAMESPACE, MODELREQUEST_GROUP, MODELREQUEST_VERSION, MODELREQUEST_PLURAL
from app.kubernetes.client import custom_api, core_api

logger = logging.getLogger(__name__)


def _list_custom(group, version, plural, namespace=None, limit=100, label_selector=None, cluster=False):
    kwargs = {}
    if label_selector:
        kwargs["label_selector"] = label_selector
    if limit:
        kwargs["limit"] = limit
    try:
        if cluster:
            resp = custom_api().list_cluster_custom_object(
                group=group, version=version, plural=plural, **kwargs
            )
        else:
            resp = custom_api().list_namespaced_custom_object(
                group=group,
                version=version,
                namespace=namespace or PIPELINE_NAMESPACE,
                plural=plural,
                **kwargs,
            )
        return resp.get("items", []) or []
    except Exception as exc:
        logger.warning("list %s/%s failed: %s", group, plural, exc)
        return []


def list_model_requests(namespace=None, limit=100, label_selector=None):
    items = _list_custom(
        MODELREQUEST_GROUP,
        MODELREQUEST_VERSION,
        MODELREQUEST_PLURAL,
        namespace=namespace,
        limit=limit,
        label_selector=label_selector,
    )
    items.sort(key=lambda x: x.get("metadata", {}).get("creationTimestamp", ""), reverse=True)
    return items


def get_model_request(name, namespace=None):
    ns = namespace or PIPELINE_NAMESPACE
    return custom_api().get_namespaced_custom_object(
        group=MODELREQUEST_GROUP,
        version=MODELREQUEST_VERSION,
        namespace=ns,
        plural=MODELREQUEST_PLURAL,
        name=name,
    )


def create_model_request(name, spec, namespace=None):
    ns = namespace or PIPELINE_NAMESPACE
    body = {
        "apiVersion": f"{MODELREQUEST_GROUP}/{MODELREQUEST_VERSION}",
        "kind": "ModelRequest",
        "metadata": {
            "name": name,
            "namespace": ns,
            "labels": {"app.kubernetes.io/created-by": "model-intake-ui"},
        },
        "spec": spec,
    }
    return custom_api().create_namespaced_custom_object(
        group=MODELREQUEST_GROUP,
        version=MODELREQUEST_VERSION,
        namespace=ns,
        plural=MODELREQUEST_PLURAL,
        body=body,
    )


def get_pipeline_run(name, namespace=None):
    ns = namespace or PIPELINE_NAMESPACE
    return custom_api().get_namespaced_custom_object(
        group="tekton.dev",
        version="v1",
        namespace=ns,
        plural="pipelineruns",
        name=name,
    )


def list_pipeline_runs(namespace=None, limit=50, label_selector=None):
    items = _list_custom(
        "tekton.dev",
        "v1",
        "pipelineruns",
        namespace=namespace,
        limit=limit,
        label_selector=label_selector,
    )
    items.sort(key=lambda x: x.get("metadata", {}).get("creationTimestamp", ""), reverse=True)
    return items


def list_capacity_plans(namespace=None, limit=50, label_selector=None):
    return _list_custom(
        MODELREQUEST_GROUP,
        MODELREQUEST_VERSION,
        "capacityplans",
        namespace=namespace,
        limit=limit,
        label_selector=label_selector,
    )


def list_lifecycle_profiles(namespace=None, limit=50):
    return _list_custom(
        MODELREQUEST_GROUP,
        MODELREQUEST_VERSION,
        "modellifecycleprofiles",
        namespace=namespace,
        limit=limit,
    )


def get_lifecycle_profile(name, namespace=None):
    ns = namespace or PIPELINE_NAMESPACE
    return custom_api().get_namespaced_custom_object(
        group=MODELREQUEST_GROUP,
        version=MODELREQUEST_VERSION,
        namespace=ns,
        plural="modellifecycleprofiles",
        name=name,
    )


def list_platform_configs(namespace=None, limit=50):
    return _list_custom(
        MODELREQUEST_GROUP,
        MODELREQUEST_VERSION,
        "platformconfigs",
        namespace=namespace,
        limit=limit,
    )


def list_inference_services(limit=200):
    items = _list_custom(
        "serving.kserve.io",
        "v1beta1",
        "inferenceservices",
        limit=limit,
        cluster=True,
    )
    if items:
        return items
    # Fallback when cluster-scoped list is forbidden: check known serving namespaces.
    seen = {}
    for ns in (PIPELINE_NAMESPACE, "vllm-staging", "vllm", "demo"):
        for item in _list_custom(
            "serving.kserve.io",
            "v1beta1",
            "inferenceservices",
            namespace=ns,
            limit=limit,
        ):
            key = "{}/{}".format(item.get("metadata", {}).get("namespace"), item.get("metadata", {}).get("name"))
            seen[key] = item
    return list(seen.values())


def inference_service_ready(item):
    for cond in item.get("status", {}).get("conditions", []) or []:
        if cond.get("type") == "Ready":
            return cond.get("status") == "True"
    return False


def _pipelinerun_params(pr):
    params = {}
    for item in pr.get("spec", {}).get("params", []) or []:
        name = item.get("name")
        if name:
            params[name] = item.get("value", "")
    return params


def _pipelinerun_phase(pr):
    for cond in pr.get("status", {}).get("conditions", []) or []:
        if cond.get("type") == "Succeeded":
            status = cond.get("status")
            if status == "True":
                return "Succeeded"
            if status == "False":
                return cond.get("reason") or "Failed"
            return cond.get("reason") or "Running"
    return "Pending"


def pipelinerun_as_request(pr):
    """Present a Tekton PipelineRun as a ModelRequest-shaped dict for the UI."""
    meta = pr.get("metadata", {})
    params = _pipelinerun_params(pr)
    phase = _pipelinerun_phase(pr)
    ready = phase == "Succeeded"
    model_uri = params.get("model-id") or params.get("model-name") or meta.get("name", "")
    return {
        "metadata": {
            "name": meta.get("name", ""),
            "namespace": meta.get("namespace", PIPELINE_NAMESPACE),
            "creationTimestamp": meta.get("creationTimestamp", ""),
        },
        "spec": {
            "model": {
                "sourceType": "huggingface",
                "uri": model_uri,
                "name": params.get("model-name") or model_uri,
            },
            "requestedBy": params.get("requested-by") or "pipeline",
            "lifecycleProfile": "standard-generative-onboarding",
        },
        "status": {
            "phase": phase,
            "conditions": [
                {
                    "type": "Ready",
                    "status": "True" if ready else "False",
                    "reason": phase,
                    "message": "PipelineRun {}".format(phase),
                    "lastTransitionTime": pr.get("status", {}).get("completionTime")
                    or meta.get("creationTimestamp", ""),
                }
            ],
        },
        "kind": "PipelineRun",
    }


def get_capacity_plan(name, namespace=None):
    ns = namespace or PIPELINE_NAMESPACE
    return custom_api().get_namespaced_custom_object(
        group=MODELREQUEST_GROUP,
        version=MODELREQUEST_VERSION,
        namespace=ns,
        plural="capacityplans",
        name=name,
    )


def resolve_evalhub_host():
    """Live EvalHub route host (no scheme). Empty if the route is missing."""
    from app.config import EVALHUB_NAMESPACE, EVALHUB_ROUTE_NAME

    try:
        route = custom_api().get_namespaced_custom_object(
            group="route.openshift.io",
            version="v1",
            namespace=EVALHUB_NAMESPACE,
            plural="routes",
            name=EVALHUB_ROUTE_NAME,
        )
        return (route.get("spec") or {}).get("host") or ""
    except Exception as exc:
        logger.warning("could not resolve EvalHub route: %s", exc)
        return ""


def create_pipeline_run(name, params, namespace=None):
    """Create a Tekton PipelineRun for model-intake-pipeline."""
    from app.config import (
        PIPELINE_NAME,
        PIPELINE_SERVICE_ACCOUNT,
        SHARED_WORKSPACE_PVC,
        MANIFESTS_CONFIGMAP,
        CUSTOM_MMLU_CONFIGMAP,
    )

    ns = namespace or PIPELINE_NAMESPACE
    body = {
        "apiVersion": "tekton.dev/v1",
        "kind": "PipelineRun",
        "metadata": {
            "name": name,
            "namespace": ns,
            "labels": {"app.kubernetes.io/created-by": "model-intake-ui"},
        },
        "spec": {
            "pipelineRef": {"name": PIPELINE_NAME},
            "params": [{"name": k, "value": v} for k, v in params.items() if v is not None],
            "taskRunTemplate": {"serviceAccountName": PIPELINE_SERVICE_ACCOUNT},
            "timeouts": {"pipeline": "6h0m0s"},
            "workspaces": [
                {
                    "name": "shared-workspace",
                    "persistentVolumeClaim": {"claimName": SHARED_WORKSPACE_PVC},
                },
                {"name": "manifests", "configMap": {"name": MANIFESTS_CONFIGMAP}},
                {"name": "custom-mmlu", "configMap": {"name": CUSTOM_MMLU_CONFIGMAP}},
            ],
        },
    }
    return custom_api().create_namespaced_custom_object(
        group="tekton.dev",
        version="v1",
        namespace=ns,
        plural="pipelineruns",
        body=body,
    )
