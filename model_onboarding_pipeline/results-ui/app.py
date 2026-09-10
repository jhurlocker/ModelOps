import json
import os
import re
from datetime import datetime, timezone

import yaml
from botocore.client import Config
from botocore.exceptions import ClientError, NoCredentialsError
from flask import Flask, jsonify, request

try:
    import boto3
except ImportError:  # pragma: no cover
    boto3 = None

S3_ACCESS_KEY = os.environ.get("S3_ACCESS_KEY")
S3_SECRET_KEY = os.environ.get("S3_SECRET_KEY")
S3_ENDPOINT_URL = os.environ.get("S3_ENDPOINT_URL")
S3_BUCKET_NAME = os.environ.get("S3_BUCKET_NAME", "benchmark-results")
S3_SECURITY_BUCKET = os.environ.get("S3_SECURITY_BUCKET", "security-scan-results")

BUCKET_ALIASES = {
    "benchmark-results": S3_BUCKET_NAME,
    "security-scan-results": S3_SECURITY_BUCKET,
    "benchmark": S3_BUCKET_NAME,
    "security": S3_SECURITY_BUCKET,
}

GUIDELLM_METRICS = (
    ("output_tokens_per_second", "Output tokens/sec", "tok/s", False),
    ("prompt_tokens_per_second", "Prompt tokens/sec", "tok/s", False),
    ("requests_per_second", "Requests/sec", "req/s", False),
    ("mean_ttft_ms", "Time to first token", "ms", True),
    ("mean_itl_ms", "Inter-token latency", "ms", True),
    ("time_to_first_token_ms", "Time to first token", "ms", True),
    ("time_per_output_token_ms", "Time per output token", "ms", True),
    ("inter_token_latency_ms", "Inter-token latency", "ms", True),
    ("request_latency", "Request latency", "s", True),
    ("request_concurrency", "Avg concurrency", "", False),
)

TIMESTAMP_RE = re.compile(r"(20\d{6}_\d{6})")

app = Flask(__name__)

_s3_client = None


def s3_client():
    global _s3_client
    if _s3_client is not None:
        return _s3_client
    if not boto3 or not S3_ACCESS_KEY or not S3_SECRET_KEY:
        return None
    _s3_client = boto3.client(
        "s3",
        endpoint_url=S3_ENDPOINT_URL or None,
        aws_access_key_id=S3_ACCESS_KEY,
        aws_secret_access_key=S3_SECRET_KEY,
        config=Config(
            s3={"addressing_style": "path"},
            connect_timeout=5,
            read_timeout=20,
            retries={"max_attempts": 2},
        ),
    )
    return _s3_client


def configured_buckets():
    buckets = []
    for name in (S3_BUCKET_NAME, S3_SECURITY_BUCKET):
        if name and name not in buckets:
            buckets.append(name)
    return buckets


def resolve_bucket(requested):
    if not requested:
        return None
    return BUCKET_ALIASES.get(requested, requested)


def first_present(*values):
    for value in values:
        if value is None or value == "":
            continue
        return value
    return None


def _num(value):
    if isinstance(value, bool) or value is None:
        return None
    if isinstance(value, (int, float)):
        return float(value)
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


def _metric(label, value, unit="", lower_is_better=False):
    number = _num(value)
    if number is None:
        return None
    if unit == "%":
        display = "{:.1f}".format(number)
    elif abs(number - round(number)) < 1e-9:
        display = str(int(round(number)))
    elif abs(number) < 10:
        display = "{:.3f}".format(number)
    else:
        display = "{:.2f}".format(number)
    return {
        "label": label,
        "value": number,
        "display": display,
        "unit": unit,
        "lowerIsBetter": lower_is_better,
    }


def _meta(label, value):
    if value is None or value == "":
        return None
    return {"label": label, "value": str(value)}


def classify_key(key, bucket=""):
    name = (key or "").lower()
    if "security_scan" in name or name.endswith("scan_results.summary.json") or "garak" in name:
        return "garak"
    if bucket == S3_SECURITY_BUCKET:
        return "garak"
    if "lm-eval" in name:
        return "lm-eval"
    if name.endswith("-results.yaml") or name.endswith("evalhub-job.json") or "guidellm" in name:
        return "guidellm"
    if name.endswith(".txt"):
        return "text"
    return "other"


def extract_timestamp(key):
    match = TIMESTAMP_RE.search(key or "")
    return match.group(1) if match else ""


def extract_model(key, data=None):
    if isinstance(data, dict):
        for field in ("model", "model_name", "model_name_sanitized"):
            value = data.get(field)
            if isinstance(value, str) and value:
                return value
            if isinstance(value, dict) and value.get("name"):
                return str(value.get("name"))
        config = data.get("config") if isinstance(data.get("config"), dict) else {}
        if config.get("model_name"):
            return str(config.get("model_name"))
    name = os.path.basename(key or "")
    match = re.search(r"\d{8}_\d{6}_(.+?)-results\.(?:yaml|json)$", name)
    if match:
        return match.group(1)
    match = re.search(r"\d{8}_\d{6}_(.+?)(?:_evalhub-job|_benchmark_info)", name)
    if match:
        return match.group(1)
    return ""


def nested_total(metrics, key):
    block = (metrics or {}).get(key) or {}
    if isinstance(block, dict) and isinstance(block.get("total"), dict):
        return block["total"]
    return block if isinstance(block, dict) else {}


def metrics_from_mapping(mapping):
    items = []
    seen = set()
    source = mapping or {}
    for key, label, unit, lower in GUIDELLM_METRICS:
        if key in seen or key not in source:
            continue
        item = _metric(label, source.get(key), unit, lower)
        if item:
            items.append(item)
            seen.add(key)
            if key in ("mean_ttft_ms", "time_to_first_token_ms"):
                seen.update(("mean_ttft_ms", "time_to_first_token_ms"))
            if key in ("mean_itl_ms", "inter_token_latency_ms"):
                seen.update(("mean_itl_ms", "inter_token_latency_ms"))
    return items


def metrics_from_legacy_benchmark(benchmark):
    items = []
    metrics = benchmark.get("metrics") or {}
    for key, label, unit, lower in GUIDELLM_METRICS:
        total = nested_total(metrics, key)
        value = total.get("mean")
        if value is None:
            continue
        number = _num(value)
        if number is None:
            continue
        # Legacy GuideLLM stores some latency stats in microseconds.
        if unit == "ms" and number > 1000:
            number = number / 1000.0
        items.append(_metric(label, number, unit, lower))
    return [item for item in items if item]


def provider_from_job(data):
    benches = []
    results = data.get("results") if isinstance(data.get("results"), dict) else {}
    benches.extend(results.get("benchmarks") or [])
    benches.extend(data.get("benchmarks") or [])
    status = data.get("status") if isinstance(data.get("status"), dict) else {}
    benches.extend(status.get("benchmarks") or [])
    for bench in benches:
        if isinstance(bench, dict) and bench.get("provider_id"):
            return str(bench.get("provider_id"))
    name = str(data.get("name") or "")
    if "garak" in name:
        return "garak"
    if "guidellm" in name:
        return "guidellm"
    return ""


def flatten_evalhub_metrics(data):
    results = data.get("results") if isinstance(data.get("results"), dict) else {}
    benches = results.get("benchmarks") or []
    merged = {}
    for bench in benches:
        if isinstance(bench, dict) and isinstance(bench.get("metrics"), dict):
            merged.update(bench["metrics"])
    if merged:
        return merged
    if benches and isinstance(benches[0], dict) and isinstance(benches[0].get("metrics"), dict):
        return benches[0]["metrics"]
    status = data.get("status") if isinstance(data.get("status"), dict) else {}
    status_results = status.get("results")
    if isinstance(status_results, dict):
        return status_results
    return {}


def job_passed(data, metrics=None):
    if "passed" in data and data.get("passed") is not None:
        return bool(data.get("passed"))
    results = data.get("results") if isinstance(data.get("results"), dict) else {}
    test = results.get("test") if isinstance(results.get("test"), dict) else {}
    if "pass" in test:
        return bool(test.get("pass"))
    benches = results.get("benchmarks") or data.get("benchmarks") or []
    votes = []
    for bench in benches:
        if not isinstance(bench, dict):
            continue
        if "passed" in bench and bench.get("passed") is not None:
            votes.append(bool(bench.get("passed")))
            continue
        bench_test = bench.get("test") if isinstance(bench.get("test"), dict) else {}
        if "pass" in bench_test:
            votes.append(bool(bench_test.get("pass")))
    if votes:
        return all(votes)
    metrics = metrics or {}
    if "pass" in metrics:
        return bool(metrics.get("pass"))
    return None


def probes_from_mapping(results, profile=""):
    probes = []
    if not isinstance(results, dict):
        return probes
    raw = results.get("per_probe") or results.get("probes") or {}
    if isinstance(raw, list) and raw:
        for item in raw:
            if not isinstance(item, dict):
                continue
            name = item.get("name") or item.get("id") or item.get("probe") or "probe"
            fails = int(_num(item.get("fails") or item.get("attack_successes") or item.get("vulnerable_responses")) or 0)
            total = int(_num(item.get("total") or item.get("evaluated") or item.get("num_samples") or item.get("total_attempts")) or 0)
            rate = _num(item.get("rate") or item.get("attack_success_rate") or item.get("metric_value"))
            if rate is not None and rate > 1:
                rate = rate / 100.0
            if rate is None:
                rate = (fails / total) if total else 0.0
            probes.append({
                "name": str(name),
                "profile": str(item.get("profile") or profile or ""),
                "fails": fails,
                "total": total,
                "rate": rate,
            })
        return probes
    if isinstance(raw, dict) and raw:
        for name, item in raw.items():
            if isinstance(item, dict):
                fails = int(_num(item.get("fails") or item.get("attack_successes") or item.get("vulnerable_responses")) or 0)
                total = int(_num(item.get("total") or item.get("evaluated") or item.get("total_attempts")) or 0)
                rate = _num(item.get("rate") or item.get("attack_success_rate"))
            else:
                fails, total, rate = 0, 0, _num(item)
            if rate is not None and rate > 1:
                rate = rate / 100.0
            if rate is None:
                rate = (fails / total) if total else 0.0
            probes.append({
                "name": str(name),
                "profile": str(profile or ""),
                "fails": fails,
                "total": total,
                "rate": rate,
            })
        return probes
    for key, value in results.items():
        if not str(key).endswith("_asr") or key == "attack_success_rate":
            continue
        rate = _num(value)
        if rate is not None and rate > 1:
            rate = rate / 100.0
        probes.append({
            "name": str(key)[:-4],
            "profile": str(profile or ""),
            "fails": 0,
            "total": 0,
            "rate": rate or 0.0,
        })
    return probes


def _eval_rows(bench):
    rows = []
    if not isinstance(bench, dict):
        return rows
    for key in ("results", "evaluations", "metrics"):
        raw = bench.get(key)
        if isinstance(raw, list):
            rows.extend(item for item in raw if isinstance(item, dict) and item.get("metric_name"))
    return rows


def _profile_from_evalhub_bench(bench):
    profile_id = str(bench.get("id") or bench.get("benchmark_id") or "garak")
    metrics = bench.get("metrics") if isinstance(bench.get("metrics"), dict) else {}
    overall = bench.get("evaluation_metadata") if isinstance(bench.get("evaluation_metadata"), dict) else {}
    overall = overall.get("overall") if isinstance(overall.get("overall"), dict) else {}
    probes = probes_from_mapping(bench, profile_id) or probes_from_mapping(metrics, profile_id)
    for row in _eval_rows(bench):
        meta = row.get("metadata") if isinstance(row.get("metadata"), dict) else {}
        name = str(row.get("metric_name") or "")
        probe = meta.get("probe")
        if not probe and name.endswith("_asr") and name != "attack_success_rate":
            probe = name[:-4]
        if not probe:
            continue
        total = int(_num(row.get("num_samples") or meta.get("total_attempts") or meta.get("total")) or 0)
        fails = int(_num(meta.get("vulnerable_responses") or meta.get("fails")) or 0)
        rate = _num(row.get("metric_value") or meta.get("attack_success_rate"))
        if rate is not None and rate > 1:
            rate = rate / 100.0
        if rate is None:
            rate = (fails / total) if total else 0.0
        probes.append({
            "name": str(probe),
            "profile": profile_id,
            "fails": fails,
            "total": total,
            "rate": rate,
        })
    deduped = []
    seen = set()
    for probe in probes:
        marker = probe.get("name")
        if marker in seen:
            continue
        seen.add(marker)
        deduped.append(probe)
    probes = deduped
    total = int(_num(first_present(
        bench.get("total_evaluated"),
        bench.get("num_examples_evaluated"),
        metrics.get("total_evaluated"),
        metrics.get("total_attempts"),
        overall.get("total_attempts"),
    )) or 0)
    fails = int(_num(first_present(
        bench.get("total_attack_successes"),
        metrics.get("total_attack_successes"),
        metrics.get("vulnerable_responses"),
        overall.get("vulnerable_responses"),
    )) or 0)
    rate = _num(first_present(
        bench.get("attack_success_rate"),
        bench.get("overall_score"),
        metrics.get("attack_success_rate"),
        overall.get("attack_success_rate"),
    ))
    if rate is not None and rate > 1:
        rate = rate / 100.0
    if not total:
        total = sum(p.get("total") or 0 for p in probes)
    if not fails:
        fails = sum(p.get("fails") or 0 for p in probes)
    if rate is None:
        rate = (fails / total) if total else 0.0
    passed = bench.get("passed")
    test = bench.get("test") if isinstance(bench.get("test"), dict) else {}
    if passed is None and "pass" in test:
        passed = bool(test.get("pass"))
    return {
        "id": profile_id,
        "attack_success_rate": rate or 0.0,
        "total_evaluated": total,
        "total_attack_successes": fails,
        "passed": passed,
        "probes": probes,
    }


def garak_profiles(data):
    profiles = []
    seen = set()
    candidates = []
    if isinstance(data.get("benchmarks"), list):
        candidates.extend(data.get("benchmarks") or [])
    results = data.get("results") if isinstance(data.get("results"), dict) else {}
    if isinstance(results.get("benchmarks"), list):
        candidates.extend(results.get("benchmarks") or [])
    for bench in candidates:
        if not isinstance(bench, dict):
            continue
        parsed = _profile_from_evalhub_bench(bench)
        key = parsed.get("id")
        if key in seen:
            continue
        seen.add(key)
        profiles.append(parsed)
    return profiles


def normalize_guidellm(data, key):
    metrics_map = {}
    strategies = []
    if isinstance(data.get("benchmarks"), list) and data["benchmarks"]:
        first = data["benchmarks"][0] if isinstance(data["benchmarks"][0], dict) else {}
        if isinstance(first.get("metrics"), dict) and any(
            isinstance((first["metrics"].get(k) or {}).get("total"), dict)
            for k in first["metrics"]
        ):
            for index, bench in enumerate(data["benchmarks"]):
                if not isinstance(bench, dict):
                    continue
                strategy = (
                    ((bench.get("args") or {}).get("strategy") or {}).get("type_")
                    or ((bench.get("args") or {}).get("profile") or {}).get("strategy_type")
                    or bench.get("id")
                    or "strategy {}".format(index)
                )
                strategies.append({
                    "name": str(strategy),
                    "metrics": metrics_from_legacy_benchmark(bench),
                })
            metrics_map = {}
        else:
            metrics_map = first.get("metrics") or {}
    if not metrics_map and not strategies:
        metrics_map = flatten_evalhub_metrics(data) or {
            k: data[k] for k in (
                "output_tokens_per_second", "prompt_tokens_per_second",
                "requests_per_second", "mean_ttft_ms", "mean_itl_ms",
            ) if k in data
        }
    model = extract_model(key, data)
    target = data.get("target") or ((data.get("model") or {}) if isinstance(data.get("model"), dict) else {}).get("url")
    passed = job_passed(data, metrics_map)
    meta = list(filter(None, [
        _meta("Model", model),
        _meta("Profile", data.get("profile") or ((data.get("benchmarks") or [{}])[0] or {}).get("id")),
        _meta("Rate", data.get("rate")),
        _meta("Max seconds", data.get("max_seconds")),
        _meta("Max requests", data.get("max_requests")),
        _meta("Prompt tokens", data.get("prompt_tokens")),
        _meta("Output tokens", data.get("output_tokens")),
        _meta("Target", target),
        _meta("EvalHub job", data.get("evalhub_job_id") or ((data.get("resource") or {}) if isinstance(data.get("resource"), dict) else {}).get("id")),
        _meta("Timestamp", data.get("timestamp") or extract_timestamp(key)),
    ]))
    return {
        "fileType": "guidellm",
        "title": "GuideLLM benchmark",
        "model": model,
        "passed": passed,
        "meta": meta,
        "metrics": metrics_from_mapping(metrics_map),
        "strategies": strategies,
        "probes": [],
        "table": [],
        "text": "",
    }


def normalize_garak(data, key, text=""):
    results = data.get("results") if isinstance(data.get("results"), dict) else {}
    status = data.get("status") if isinstance(data.get("status"), dict) else {}
    status_results = status.get("results") if isinstance(status.get("results"), dict) else {}
    metrics = flatten_evalhub_metrics(data)
    merged = {}
    for src in (status_results, results, metrics, data):
        if isinstance(src, dict):
            merged.update({k: v for k, v in src.items() if not isinstance(v, (list, dict)) or k in (
                "per_probe", "probes", "attack_success_rate", "total_evaluated", "total_attack_successes",
            )})
    profiles = garak_profiles(data)
    probes = []
    seen_probes = set()
    for profile in profiles:
        for probe in profile.get("probes") or []:
            marker = (probe.get("profile"), probe.get("name"))
            if marker in seen_probes:
                continue
            seen_probes.add(marker)
            probes.append(probe)
    if not probes:
        probes = (
            probes_from_mapping(data)
            or probes_from_mapping(merged)
            or probes_from_mapping(results)
            or probes_from_mapping(status_results)
            or probes_from_mapping(metrics)
        )
    total_evaluated = int(_num(merged.get("total_evaluated") or merged.get("total") or merged.get("evaluated") or merged.get("num_examples_evaluated")) or 0)
    total_attacks = int(_num(
        merged.get("total_attack_successes") or merged.get("attack_successes") or merged.get("fails") or merged.get("vulnerable_responses")
    ) or 0)
    if not total_evaluated:
        total_evaluated = sum(p.get("total_evaluated") or 0 for p in profiles) or sum(p.get("total") or 0 for p in probes)
    if not total_attacks:
        total_attacks = sum(p.get("total_attack_successes") or 0 for p in profiles) or sum(p.get("fails") or 0 for p in probes)
    rate = _num(merged.get("attack_success_rate") or merged.get("asr") or merged.get("overall_score"))
    if rate is not None and rate > 1:
        rate = rate / 100.0
    if rate is None and total_evaluated:
        rate = total_attacks / total_evaluated
    if rate is None and profiles:
        rate = max(p.get("attack_success_rate") or 0.0 for p in profiles)
    passed = data.get("passed")
    if passed is None:
        passed = job_passed(data, merged)
    model = extract_model(key, data)
    job_id = data.get("evalhub_job_id") or ((data.get("resource") or {}) if isinstance(data.get("resource"), dict) else {}).get("id")
    if not job_id:
        job_id = data.get("id")
    message = ""
    status_message = status.get("message")
    if isinstance(status_message, dict):
        message = str(status_message.get("message") or "")
    elif status_message:
        message = str(status_message)
    profile_names = data.get("profiles")
    if isinstance(profile_names, list):
        profile_label = ", ".join(str(item) for item in profile_names)
    else:
        profile_label = ", ".join(p.get("id") for p in profiles if p.get("id"))
    meta = list(filter(None, [
        _meta("Model", model),
        _meta("Target", data.get("target") or ((data.get("model") or {}) if isinstance(data.get("model"), dict) else {}).get("url")),
        _meta("EvalHub job", job_id),
        _meta("Profiles", profile_label),
        _meta("Severity threshold", data.get("severity_threshold") or merged.get("severity_threshold")),
        _meta("Tolerated rate", data.get("tolerated_rate") or merged.get("tolerated_rate")),
        _meta("Timestamp", data.get("timestamp") or extract_timestamp(key)),
        _meta("Message", message),
    ]))
    table = []
    return {
        "fileType": "garak",
        "title": "Garak security scan",
        "model": model,
        "passed": passed,
        "meta": meta,
        "metrics": list(filter(None, [
            _metric("Attack success rate", (rate or 0) * (100 if (rate or 0) <= 1 else 1), "%", True),
            _metric("Attempts evaluated", total_evaluated, "", False),
            _metric("Successful attacks", total_attacks, "", True),
            _metric("Profiles", len(profiles), "", False) if profiles else None,
        ])),
        "strategies": [],
        "profiles": profiles,
        "probes": probes,
        "table": table,
        "text": text,
    }


def normalize_lm_eval(data, key):
    rows = []
    results = data.get("results") if isinstance(data.get("results"), dict) else {}
    for task_name, task_data in results.items():
        if not isinstance(task_data, dict):
            continue
        alias = task_data.get("alias") or task_name
        metric_keys = [k for k in task_data.keys() if k != "alias" and not k.endswith("_stderr")]
        for metric_key in metric_keys:
            value = task_data.get(metric_key)
            number = _num(value)
            stderr_key = metric_key.replace(",", "_stderr,") if "," in metric_key else metric_key + "_stderr"
            stderr = task_data.get(stderr_key)
            rating = "n/a"
            if number is not None and str(metric_key).startswith("acc") and "stderr" not in str(metric_key):
                rating = "good" if number > 0.6 else "moderate" if number > 0.3 else "poor"
            rows.append({
                "task": "{} ({})".format(alias, task_name),
                "metric": metric_key,
                "value": "{:.4f}".format(number) if number is not None else str(value),
                "stderr": "{:.4f}".format(_num(stderr)) if _num(stderr) is not None else "—",
                "rating": rating,
            })
    model = extract_model(key, data)
    return {
        "fileType": "lm-eval",
        "title": "LM Evaluation Harness",
        "model": model,
        "passed": None,
        "meta": list(filter(None, [
            _meta("Model", model),
            _meta("Evaluation time (s)", data.get("total_evaluation_time_seconds")),
        ])),
        "metrics": [],
        "strategies": [],
        "probes": [],
        "table": rows,
        "text": "",
    }


def normalize_payload(key, bucket, content):
    stripped = (content or "").strip()
    if not stripped:
        raise ValueError("File is empty.")

    data = None
    try:
        data = json.loads(stripped)
    except json.JSONDecodeError:
        try:
            data = yaml.safe_load(stripped)
        except yaml.YAMLError as exc:
            if key.lower().endswith(".txt"):
                return {
                    "fileType": "text",
                    "title": os.path.basename(key),
                    "model": extract_model(key),
                    "passed": None,
                    "meta": [_meta("File", key)],
                    "metrics": [],
                    "strategies": [],
                    "probes": [],
                    "table": [],
                    "text": stripped,
                }
            raise ValueError("Could not parse file as JSON or YAML: {}".format(exc))

    kind = classify_key(key, bucket)
    if isinstance(data, dict):
        provider = provider_from_job(data)
        if provider == "garak" or "attack_success_rate" in data or "total_attack_successes" in data:
            kind = "garak"
        elif provider == "guidellm" or "benchmarks" in data or "mean_ttft_ms" in data or "output_tokens_per_second" in data:
            kind = "guidellm"
        elif "results" in data and "config" in data:
            kind = "lm-eval"

    if kind == "garak" and isinstance(data, dict):
        text = stripped if key.lower().endswith(".txt") else ""
        return normalize_garak(data, key, text=text)
    if kind == "lm-eval" and isinstance(data, dict):
        return normalize_lm_eval(data, key)
    if kind == "guidellm" and isinstance(data, dict):
        return normalize_guidellm(data, key)
    if isinstance(data, dict) and ("mean_ttft_ms" in data or "output_tokens_per_second" in data):
        return normalize_guidellm(data, key)
    if key.lower().endswith(".txt"):
        return {
            "fileType": "text",
            "title": os.path.basename(key),
            "model": extract_model(key, data if isinstance(data, dict) else None),
            "passed": None,
            "meta": [_meta("File", key)],
            "metrics": [],
            "strategies": [],
            "probes": [],
            "table": [],
            "text": stripped,
        }
    raise ValueError(
        "Unrecognized result file. Expected a GuideLLM summary, EvalHub job JSON, "
        "Garak scan summary, or lm-eval results."
    )


def list_bucket_objects(client, bucket):
    objects = []
    token = None
    while True:
        kwargs = {"Bucket": bucket}
        if token:
            kwargs["ContinuationToken"] = token
        resp = client.list_objects_v2(**kwargs)
        for item in resp.get("Contents") or []:
            key = item.get("Key") or ""
            if not key or key.endswith("/"):
                continue
            modified = item.get("LastModified")
            if isinstance(modified, datetime):
                if modified.tzinfo is None:
                    modified = modified.replace(tzinfo=timezone.utc)
                modified_iso = modified.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
            else:
                modified_iso = str(modified or "")
            objects.append({
                "bucket": bucket,
                "key": key,
                "size": int(item.get("Size") or 0),
                "lastModified": modified_iso,
                "fileType": classify_key(key, bucket),
                "timestamp": extract_timestamp(key),
                "model": extract_model(key),
            })
        if not resp.get("IsTruncated"):
            break
        token = resp.get("NextContinuationToken")
        if not token:
            break
    return objects


def fetch_object(client, bucket, key):
    response = client.get_object(Bucket=bucket, Key=key)
    return response["Body"].read().decode("utf-8", errors="replace")


def find_object(client, key, requested_bucket=None):
    buckets = []
    resolved = resolve_bucket(requested_bucket)
    if resolved:
        buckets.append(resolved)
    for bucket in configured_buckets():
        if bucket not in buckets:
            buckets.append(bucket)
    last_error = None
    for bucket in buckets:
        try:
            return bucket, fetch_object(client, bucket, key)
        except ClientError as exc:
            code = exc.response.get("Error", {}).get("Code", "")
            if code in ("NoSuchKey", "404", "NotFound"):
                last_error = exc
                continue
            raise
    if last_error:
        raise last_error
    raise FileNotFoundError(key)


HTML_TEMPLATE = r"""<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>ModelOps Results Viewer</title>
    <style>
        :root {
            --bg: #f5f7fa;
            --surface: #fff;
            --primary: #2563eb;
            --primary-light: #eff6ff;
            --success: #16a34a;
            --success-light: #f0fdf4;
            --warning: #d97706;
            --warning-light: #fffbeb;
            --danger: #dc2626;
            --danger-light: #fef2f2;
            --text: #1e293b;
            --muted: #64748b;
            --border: #e2e8f0;
            --radius: 12px;
            --shadow: 0 1px 3px rgba(15,23,42,.08), 0 1px 2px rgba(15,23,42,.06);
        }
        * { box-sizing: border-box; }
        body {
            margin: 0;
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            background: var(--bg);
            color: var(--text);
            line-height: 1.5;
        }
        header {
            background: #1e293b;
            color: #e2e8f0;
            padding: 20px 28px;
        }
        header h1 { margin: 0 0 4px; font-size: 22px; color: #fff; }
        header p { margin: 0; color: #94a3b8; font-size: 14px; }
        header a { color: #93c5fd; }
        main { max-width: 1100px; margin: 0 auto; padding: 24px; }
        .toolbar { display: flex; gap: 8px; flex-wrap: wrap; margin-bottom: 20px; }
        .chip {
            border: 1px solid var(--border);
            background: var(--surface);
            color: var(--muted);
            border-radius: 999px;
            padding: 6px 14px;
            cursor: pointer;
            font-size: 13px;
        }
        .chip.active { background: var(--primary); border-color: var(--primary); color: #fff; }
        .card {
            background: var(--surface);
            border-radius: var(--radius);
            box-shadow: var(--shadow);
            padding: 20px;
            margin-bottom: 16px;
        }
        .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 16px; }
        .result-card {
            background: var(--surface);
            border-radius: var(--radius);
            box-shadow: var(--shadow);
            padding: 18px;
            display: flex;
            flex-direction: column;
            gap: 10px;
            min-height: 150px;
        }
        .result-card h3 { margin: 0; font-size: 16px; }
        .result-card p { margin: 0; color: var(--muted); font-size: 13px; word-break: break-all; }
        .badge {
            display: inline-flex;
            align-items: center;
            border-radius: 999px;
            padding: 2px 10px;
            font-size: 12px;
            font-weight: 600;
        }
        .badge-garak { background: #fdf2f8; color: #be185d; }
        .badge-guidellm { background: var(--primary-light); color: var(--primary); }
        .badge-lm-eval { background: #f5f3ff; color: #6d28d9; }
        .badge-text, .badge-other { background: #f1f5f9; color: #475569; }
        .badge-success { background: var(--success-light); color: var(--success); }
        .badge-danger { background: var(--danger-light); color: var(--danger); }
        .badge-neutral { background: #f1f5f9; color: #475569; }
        .btn {
            display: inline-block;
            background: var(--primary);
            color: #fff;
            text-decoration: none;
            border-radius: 8px;
            padding: 8px 12px;
            font-size: 13px;
            font-weight: 600;
            border: 0;
            cursor: pointer;
            margin-top: auto;
            width: fit-content;
        }
        .metrics {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
            gap: 12px;
            margin: 16px 0;
        }
        .metric {
            background: var(--primary-light);
            border-radius: 10px;
            padding: 12px;
        }
        .metric .label { color: var(--muted); font-size: 12px; font-weight: 600; }
        .metric .value { font-size: 24px; font-weight: 700; margin-top: 4px; }
        .metric .hint { color: var(--muted); font-size: 11px; }
        table { width: 100%; border-collapse: collapse; font-size: 14px; }
        th, td { border-bottom: 1px solid var(--border); padding: 10px; text-align: left; }
        th { color: var(--muted); font-size: 12px; text-transform: uppercase; letter-spacing: .03em; }
        .meta-table td:first-child { width: 180px; font-weight: 600; color: var(--muted); }
        .error { color: var(--danger); font-weight: 600; }
        .empty { color: var(--muted); padding: 32px 0; text-align: center; }
        pre {
            background: #0f172a;
            color: #e2e8f0;
            padding: 16px;
            border-radius: 8px;
            overflow: auto;
            font-size: 12px;
        }
        .back { color: var(--primary); text-decoration: none; font-size: 14px; }
    </style>
</head>
<body>
    <header>
        <h1>ModelOps Results Viewer</h1>
        <p>Garak security scans and GuideLLM benchmarks uploaded by the onboarding pipeline.</p>
    </header>
    <main>
        <p id="error" class="error"></p>
        <div id="app"><p>Loading results from object storage...</p></div>
    </main>
    <script>
        const app = document.getElementById('app');
        const errorEl = document.getElementById('error');
        const params = new URLSearchParams(window.location.search);
        const fileKey = params.get('file');
        const bucket = params.get('bucket') || '';
        let allFiles = [];
        let activeFilter = 'all';

        function isPrimary(item) {
            const k = item.key || '';
            return k.endsWith('-results.yaml')
                || k.endsWith('scan_results.summary.json')
                || k.includes('lm-eval');
        }

        function esc(value) {
            return String(value == null ? '' : value)
                .replace(/&/g, '&amp;')
                .replace(/</g, '&lt;')
                .replace(/>/g, '&gt;')
                .replace(/"/g, '&quot;');
        }

        function badgeForType(type) {
            const labels = {garak: 'Garak', guidellm: 'GuideLLM', 'lm-eval': 'lm-eval', text: 'Log', other: 'File'};
            return `<span class="badge badge-${esc(type)}">${esc(labels[type] || type)}</span>`;
        }

        function passBadge(passed) {
            if (passed === true) return '<span class="badge badge-success">Passed</span>';
            if (passed === false) return '<span class="badge badge-danger">Failed</span>';
            return '';
        }

        function ratingBadge(rating) {
            if (rating === 'good') return '<span class="badge badge-success">Good</span>';
            if (rating === 'moderate') return '<span class="badge" style="background:#fff7ed;color:#c2410c">Moderate</span>';
            if (rating === 'poor') return '<span class="badge badge-danger">Poor</span>';
            return '<span class="badge badge-neutral">N/A</span>';
        }

        function showError(message) {
            errorEl.textContent = message || '';
        }

        function fileUrl(item) {
            const q = new URLSearchParams({file: item.key});
            if (item.bucket) q.set('bucket', item.bucket);
            return `/?${q.toString()}`;
        }

        function renderList() {
            const filters = [
                ['all', 'All'],
                ['garak', 'Garak'],
                ['guidellm', 'Benchmark'],
                ['lm-eval', 'lm-eval'],
                ['raw', 'Raw files'],
            ];
            let visible;
            if (activeFilter === 'raw') {
                visible = allFiles.filter(item => !isPrimary(item));
            } else if (activeFilter === 'all') {
                visible = allFiles.filter(isPrimary);
            } else {
                visible = allFiles.filter(item => item.fileType === activeFilter && isPrimary(item));
            }
            const chips = filters.map(([id, label]) =>
                `<button class="chip ${activeFilter === id ? 'active' : ''}" data-filter="${id}">${label}</button>`
            ).join('');
            if (!visible.length) {
                app.innerHTML = `<div class="toolbar">${chips}</div><div class="empty">No matching result files in S3 yet.</div>`;
                bindFilters();
                return;
            }
            const cards = visible.map(item => `
                <article class="result-card">
                    <div>${badgeForType(item.fileType)}</div>
                    <h3>${esc(item.model || item.timestamp || 'Result')}</h3>
                    <p>${esc(item.key)}</p>
                    <p>${esc(item.lastModified || '')}${item.size ? ' · ' + item.size + ' bytes' : ''}</p>
                    <a class="btn" href="${esc(fileUrl(item))}">Open</a>
                </article>
            `).join('');
            app.innerHTML = `<div class="toolbar">${chips}</div><div class="grid">${cards}</div>`;
            bindFilters();
        }

        function bindFilters() {
            app.querySelectorAll('[data-filter]').forEach(btn => {
                btn.addEventListener('click', () => {
                    activeFilter = btn.getAttribute('data-filter');
                    renderList();
                });
            });
        }

        function renderDetail(payload) {
            const metrics = (payload.metrics || []).map(m => `
                <div class="metric">
                    <div class="label">${esc(m.label)}</div>
                    <div class="value">${esc(m.display)}${m.unit ? ' ' + esc(m.unit) : ''}</div>
                    ${m.unit ? `<div class="hint">${m.lowerIsBetter ? 'Lower is better' : 'Higher is better'}</div>` : ''}
                </div>
            `).join('');
            const meta = (payload.meta || []).map(row =>
                `<tr><td>${esc(row.label)}</td><td>${esc(row.value)}</td></tr>`
            ).join('');
            const profiles = (payload.profiles || []).map(p =>
                `<tr><td>${esc(p.id)}</td><td>${((p.attack_success_rate || 0) * 100).toFixed(1)}%</td><td>${esc(p.total_attack_successes || 0)} / ${esc(p.total_evaluated || 0)}</td><td>${passBadge(p.passed)}</td></tr>`
            ).join('');
            const probes = (payload.probes || []).map(p =>
                `<tr><td>${esc(p.profile || '—')}</td><td>${esc(p.name)}</td><td>${esc(p.fails)} / ${esc(p.total)}</td><td>${((p.rate || 0) * 100).toFixed(1)}%</td></tr>`
            ).join('');
            const table = (payload.table || []).map(row =>
                `<tr><td>${esc(row.task)}</td><td>${esc(row.metric)}</td><td>${esc(row.value)}</td><td>${ratingBadge(row.rating)}</td><td>${esc(row.stderr)}</td></tr>`
            ).join('');
            const strategies = (payload.strategies || []).map(s => `
                <div class="card">
                    <h3>${esc(s.name)}</h3>
                    <div class="metrics">${(s.metrics || []).map(m => `
                        <div class="metric"><div class="label">${esc(m.label)}</div><div class="value">${esc(m.display)}${m.unit ? ' ' + esc(m.unit) : ''}</div></div>
                    `).join('')}</div>
                </div>
            `).join('');
            app.innerHTML = `
                <p><a class="back" href="/">← All results</a></p>
                <div class="card">
                    <div>${badgeForType(payload.fileType)} ${passBadge(payload.passed)}</div>
                    <h2 style="margin:12px 0 8px">${esc(payload.title || 'Result')}</h2>
                    ${payload.model ? `<p style="color:var(--muted);margin:0 0 12px">${esc(payload.model)}</p>` : ''}
                    ${meta ? `<table class="meta-table">${meta}</table>` : ''}
                    ${metrics ? `<div class="metrics">${metrics}</div>` : ''}
                    ${payload.text ? `<pre>${esc(payload.text)}</pre>` : ''}
                </div>
                ${profiles ? `<div class="card"><h3>Profiles</h3><table><thead><tr><th>Profile</th><th>Attack success rate</th><th>Attacks</th><th>Gate</th></tr></thead><tbody>${profiles}</tbody></table></div>` : ''}
                ${probes ? `<div class="card"><h3>Probes</h3><table><thead><tr><th>Profile</th><th>Probe</th><th>Attacks</th><th>Success rate</th></tr></thead><tbody>${probes}</tbody></table></div>` : ''}
                ${table ? `<div class="card"><h3>Tasks</h3><table><thead><tr><th>Task</th><th>Metric</th><th>Value</th><th>Rating</th><th>StdErr</th></tr></thead><tbody>${table}</tbody></table></div>` : ''}
                ${strategies}
            `;
        }

        function loadList() {
            fetch('/api/files')
                .then(r => r.json().then(body => ({ok: r.ok, body})))
                .then(({ok, body}) => {
                    if (!ok) throw new Error(body.error || 'Could not list result files');
                    allFiles = body.files || [];
                    renderList();
                })
                .catch(err => {
                    showError(err.message);
                    app.innerHTML = '<div class="empty">Could not load the result index.</div>';
                });
        }

        function loadFile() {
            const q = new URLSearchParams({file: fileKey});
            if (bucket) q.set('bucket', bucket);
            fetch('/data?' + q.toString())
                .then(r => r.json().then(body => ({ok: r.ok, body})))
                .then(({ok, body}) => {
                    if (!ok) throw new Error(body.error || 'Could not load result file');
                    renderDetail(body);
                })
                .catch(err => {
                    showError(err.message);
                    app.innerHTML = '<p><a class="back" href="/">← All results</a></p><div class="empty">Could not render this result file.</div>';
                });
        }

        if (fileKey) loadFile();
        else loadList();
    </script>
</body>
</html>
"""


@app.route("/healthz")
def healthz():
    return jsonify({"status": "ok"})


@app.route("/")
def index():
    return HTML_TEMPLATE


@app.route("/api/files")
def list_files():
    client = s3_client()
    if client is None:
        return jsonify({"error": "Server is not configured for S3 access."}), 500
    files = []
    errors = []
    for bucket in configured_buckets():
        try:
            files.extend(list_bucket_objects(client, bucket))
        except ClientError as exc:
            errors.append("{}: {}".format(bucket, exc.response.get("Error", {}).get("Message", str(exc))))
        except Exception as exc:
            errors.append("{}: {}".format(bucket, exc))
    files.sort(key=lambda item: item.get("lastModified") or "", reverse=True)
    payload = {"files": files}
    if errors and not files:
        return jsonify({"error": "; ".join(errors)}), 500
    if errors:
        payload["warnings"] = errors
    return jsonify(payload)


@app.route("/data")
def get_data():
    file_key = (request.args.get("file") or "").lstrip("/")
    if not file_key:
        return jsonify({"error": "No 'file' parameter specified in URL."}), 400
    if ".." in file_key:
        return jsonify({"error": "Invalid file key."}), 400

    client = s3_client()
    if client is None:
        return jsonify({"error": "Server is not configured for S3 access."}), 500

    try:
        bucket, content = find_object(client, file_key, request.args.get("bucket"))
        payload = normalize_payload(file_key, bucket, content)
        payload["fileName"] = file_key
        payload["bucket"] = bucket
        return jsonify(payload)
    except NoCredentialsError:
        return jsonify({"error": "Server S3 credentials are invalid or missing."}), 500
    except ClientError as exc:
        code = exc.response.get("Error", {}).get("Code", "")
        message = exc.response.get("Error", {}).get("Message", str(exc))
        if code in ("NoSuchKey", "404", "NotFound"):
            return jsonify({"error": "File not found in S3: {}".format(file_key)}), 404
        return jsonify({"error": "S3 error: {}".format(message)}), 500
    except ValueError as exc:
        return jsonify({"error": str(exc)}), 400
    except Exception as exc:
        return jsonify({"error": "Unexpected error: {}".format(exc)}), 500
