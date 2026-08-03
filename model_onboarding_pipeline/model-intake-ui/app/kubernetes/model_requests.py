from app.config import PIPELINE_NAMESPACE, MODELREQUEST_GROUP, MODELREQUEST_VERSION, MODELREQUEST_PLURAL
from app.kubernetes.client import custom_api, core_api


def list_model_requests(namespace=None, limit=100, label_selector=None):
    ns = namespace or PIPELINE_NAMESPACE
    kwargs = {}
    if label_selector:
        kwargs["label_selector"] = label_selector
    resp = custom_api().list_namespaced_custom_object(
        group=MODELREQUEST_GROUP,
        version=MODELREQUEST_VERSION,
        namespace=ns,
        plural=MODELREQUEST_PLURAL,
        limit=limit,
        **kwargs,
    )
    items = resp.get("items", [])
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
    ns = namespace or PIPELINE_NAMESPACE
    kwargs = {}
    if label_selector:
        kwargs["label_selector"] = label_selector
    resp = custom_api().list_namespaced_custom_object(
        group="tekton.dev",
        version="v1",
        namespace=ns,
        plural="pipelineruns",
        limit=limit,
        **kwargs,
    )
    items = resp.get("items", [])
    items.sort(key=lambda x: x.get("metadata", {}).get("creationTimestamp", ""), reverse=True)
    return items


def list_capacity_plans(namespace=None, limit=50, label_selector=None):
    ns = namespace or PIPELINE_NAMESPACE
    kwargs = {}
    if label_selector:
        kwargs["label_selector"] = label_selector
    resp = custom_api().list_namespaced_custom_object(
        group=MODELREQUEST_GROUP,
        version=MODELREQUEST_VERSION,
        namespace=ns,
        plural="capacityplans",
        limit=limit,
        **kwargs,
    )
    return resp.get("items", [])


def list_lifecycle_profiles(namespace=None, limit=50):
    ns = namespace or PIPELINE_NAMESPACE
    resp = custom_api().list_namespaced_custom_object(
        group=MODELREQUEST_GROUP,
        version=MODELREQUEST_VERSION,
        namespace=ns,
        plural="modellifecycleprofiles",
        limit=limit,
    )
    return resp.get("items", [])


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
    ns = namespace or PIPELINE_NAMESPACE
    resp = custom_api().list_namespaced_custom_object(
        group=MODELREQUEST_GROUP,
        version=MODELREQUEST_VERSION,
        namespace=ns,
        plural="platformconfigs",
        limit=limit,
    )
    return resp.get("items", [])


def get_capacity_plan(name, namespace=None):
    ns = namespace or PIPELINE_NAMESPACE
    return custom_api().get_namespaced_custom_object(
        group=MODELREQUEST_GROUP,
        version=MODELREQUEST_VERSION,
        namespace=ns,
        plural="capacityplans",
        name=name,
    )
