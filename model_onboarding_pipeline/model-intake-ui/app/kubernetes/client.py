from kubernetes import client, config

_custom_api = None
_core_api = None
_apps_api = None
_dynamic_client = None


def _load_config():
    try:
        config.load_incluster_config()
    except config.ConfigException:
        config.load_kube_config()


def custom_api():
    global _custom_api
    if _custom_api is None:
        _load_config()
        _custom_api = client.CustomObjectsApi()
    return _custom_api


def core_api():
    global _core_api
    if _core_api is None:
        _load_config()
        _core_api = client.CoreV1Api()
    return _core_api


def apps_api():
    global _apps_api
    if _apps_api is None:
        _load_config()
        _apps_api = client.AppsV1Api()
    return _apps_api
