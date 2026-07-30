import os

PIPELINE_NAMESPACE = os.environ.get("PIPELINE_NAMESPACE", "sandbox")
LIFECYCLE_PROFILE = os.environ.get("LIFECYCLE_PROFILE", "standard-generative-onboarding")
DEFAULT_ADVISOR_ENDPOINT = os.environ.get("DEFAULT_ADVISOR_ENDPOINT", "")
SELF_INTERNAL_URL = os.environ.get("SELF_INTERNAL_URL", "http://model-intake.sandbox.svc.cluster.local:8080")

_default_db = "/data/model-intake.db" if os.path.exists("/data") else os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "model-intake.db")
DB_PATH = os.environ.get("DB_PATH", _default_db)

GPU_UTILIZATION_WARNING_THRESHOLD = float(os.environ.get("GPU_UTILIZATION_WARNING_THRESHOLD", "85"))
PROMETHEUS_URL = os.environ.get("PROMETHEUS_URL", "https://thanos-querier.openshift-monitoring.svc.cluster.local:9091")

MODELREQUEST_GROUP = "modelops.example.io"
MODELREQUEST_VERSION = "v1alpha1"
MODELREQUEST_PLURAL = "modelrequests"
CAPACITYPLAN_PLURAL = "capacityplans"
LIFECYCLEPROFILE_PLURAL = "modellifecycleprofiles"
PLATFORMCONFIG_PLURAL = "platformconfigs"
