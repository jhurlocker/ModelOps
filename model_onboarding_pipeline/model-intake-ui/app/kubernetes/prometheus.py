import json
import logging
import os
import ssl
import urllib.error
import urllib.parse
import urllib.request

from app.config import PROMETHEUS_URL

logger = logging.getLogger(__name__)

TOKEN_PATH = "/var/run/secrets/kubernetes.io/serviceaccount/token"


def _bearer_token():
    try:
        with open(TOKEN_PATH) as f:
            return f.read().strip()
    except OSError:
        return ""


def query_prometheus(query, timeout=8):
    """Run an instant PromQL query against thanos-querier. Returns result samples."""
    if not PROMETHEUS_URL:
        return []
    token = _bearer_token()
    url = "{}/api/v1/query?{}".format(
        PROMETHEUS_URL.rstrip("/"), urllib.parse.urlencode({"query": query})
    )
    req = urllib.request.Request(url)
    if token:
        req.add_header("Authorization", "Bearer {}".format(token))
    ctx = ssl._create_unverified_context()
    try:
        with urllib.request.urlopen(req, context=ctx, timeout=timeout) as resp:
            payload = json.loads(resp.read().decode("utf-8"))
    except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as exc:
        logger.warning("Prometheus query failed (%s): %s", query, exc)
        return []
    if payload.get("status") != "success":
        logger.warning("Prometheus query not successful (%s): %s", query, payload.get("error"))
        return []
    return payload.get("data", {}).get("result", []) or []


def _sample_value(sample):
    try:
        return float(sample.get("value", [None, "nan"])[1])
    except (TypeError, ValueError, IndexError):
        return None


def _telemetry_key(metric):
    host = metric.get("Hostname") or metric.get("hostname") or metric.get("node") or ""
    gpu = str(metric.get("gpu", metric.get("device", "0"))).replace("nvidia", "")
    try:
        gpu_index = int(gpu)
    except ValueError:
        gpu_index = 0
    return host, gpu_index


def collect_gpu_telemetry():
    """Physical GPU telemetry keyed by (node hostname, gpu index).

    Values come from DCGM exporter via Prometheus. Missing metrics are omitted
    rather than filled with N/A so callers can fall back to scheduler data.
    """
    telemetry = {}

    def upsert(query, field):
        for sample in query_prometheus(query):
            metric = sample.get("metric") or {}
            key = _telemetry_key(metric)
            entry = telemetry.setdefault(
                key,
                {
                    "hostname": key[0],
                    "gpu_index": key[1],
                    "uuid": metric.get("UUID", ""),
                    "model": metric.get("modelName") or metric.get("model_name") or "",
                    "exported_pod": metric.get("exported_pod", ""),
                    "exported_namespace": metric.get("exported_namespace", ""),
                },
            )
            value = _sample_value(sample)
            if value is not None:
                entry[field] = value
            for label in ("UUID", "modelName", "exported_pod", "exported_namespace"):
                if metric.get(label) and not entry.get(label.lower().replace("modelname", "model")):
                    pass
            if metric.get("UUID"):
                entry["uuid"] = metric["UUID"]
            if metric.get("modelName"):
                entry["model"] = metric["modelName"]
            if metric.get("exported_pod"):
                entry["exported_pod"] = metric["exported_pod"]
            if metric.get("exported_namespace"):
                entry["exported_namespace"] = metric["exported_namespace"]

    upsert("DCGM_FI_DEV_GPU_UTIL", "util")
    upsert("DCGM_FI_DEV_FB_USED", "fb_used")
    upsert("DCGM_FI_DEV_FB_FREE", "fb_free")
    upsert("DCGM_FI_DEV_FB_RESERVED", "fb_reserved")
    upsert("DCGM_FI_DEV_GPU_TEMP", "temp")
    upsert("DCGM_FI_DEV_POWER_USAGE", "power")
    return telemetry


def match_telemetry(telemetry, node_name, gpu_index=0):
    """Find DCGM samples for a node. Hostname may be a prefix of the Kubernetes node name."""
    direct = telemetry.get((node_name, gpu_index))
    if direct:
        return direct
    for (host, idx), entry in telemetry.items():
        if idx != gpu_index:
            continue
        if host == node_name or node_name.startswith(host) or host.startswith(node_name.split(".")[0]):
            return entry
    return {}
