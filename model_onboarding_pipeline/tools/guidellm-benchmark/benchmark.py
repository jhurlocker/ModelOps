"""
GuideLLM benchmark tool - submits GuideLLM benchmark via EvalHub, polls for
completion, saves results to workspace.

Environment variables:
  TARGET             model endpoint URL
  MODEL_NAME         registered model name
  EVALHUB_URL        EvalHub base URL
  EVALHUB_TOKEN      OAuth bearer token
  TENANT_NS          EvalHub tenant namespace
  GUIDELLM_PROFILE   benchmark profile (constant, sweep, throughput)
  GUIDELLM_RATE      request rate
  GUIDELLM_MAX_SECONDS max duration
  GUIDELLM_MAX_REQUESTS max requests
  CUSTOM_DATA        use custom dataset ("True"/"False")
  CUSTOM_FILENAME    custom dataset filename
"""

import json
import os
import sys
import time
import uuid

import requests


def submit_benchmark():
    target = os.environ["TARGET"]
    evalhub_url = os.environ["EVALHUB_URL"]
    token = os.environ["EVALHUB_TOKEN"]
    tenant_ns = os.environ["TENANT_NS"]
    profile = os.environ.get("GUIDELLM_PROFILE", "constant")
    rate = float(os.environ.get("GUIDELLM_RATE", "4.0"))
    max_seconds = int(os.environ.get("GUIDELLM_MAX_SECONDS", "15"))
    max_requests = int(os.environ.get("GUIDELLM_MAX_REQUESTS", "2"))

    base = f"https://{evalhub_url}"
    headers = {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}

    job_id = f"guidellm-{uuid.uuid4().hex[:8]}"

    payload = {
        "apiVersion": "trustyai.opendatahub.io/v1alpha1",
        "kind": "EvalJob",
        "metadata": {
            "name": job_id,
            "namespace": tenant_ns,
            "labels": {"job-type": "guidellm", "model-name": os.environ["MODEL_NAME"]},
        },
        "spec": {
            "provider": "guidellm",
            "model": target,
            "arguments": {
                "profile": profile,
                "rate": rate,
                "max_seconds": max_seconds,
                "max_requests": max_requests,
            },
        },
    }

    print(f"Submitting GuideLLM benchmark {job_id} to EvalHub...")
    resp = requests.post(
        f"{base}/api/v1/evaluations/jobs",
        json=payload, headers=headers,
    )
    resp.raise_for_status()
    print(f"GuideLLM job submitted: {job_id}")
    return job_id


def poll_completion(job_id, max_attempts=60, poll_interval=15):
    evalhub_url = os.environ["EVALHUB_URL"]
    token = os.environ["EVALHUB_TOKEN"]
    tenant_ns = os.environ["TENANT_NS"]

    base = f"https://{evalhub_url}"
    headers = {"Authorization": f"Bearer {token}"}

    for attempt in range(1, max_attempts + 1):
        print(f"Polling GuideLLM job {job_id} (attempt {attempt}/{max_attempts})...")
        try:
            resp = requests.get(
                f"{base}/api/v1/evaluations/jobs/{tenant_ns}/{job_id}",
                headers=headers,
            )
            resp.raise_for_status()
            status = resp.json()
            job_status = (status.get("status", {}) or {}).get("conditions", [{}])[0].get("type", "Running")
            if job_status == "Complete":
                print("GuideLLM job completed.")
                return status
        except Exception as e:
            print(f"Poll error: {e}")

        if attempt < max_attempts:
            time.sleep(poll_interval)

    raise TimeoutError(f"GuideLLM job {job_id} did not complete within {max_attempts * poll_interval}s")


def extract_metrics(result):
    """Extract key metrics from GuideLLM results."""
    metrics = {}
    try:
        data = result.get("status", {}).get("results", {})
        for key in ["tps", "average_tps", "time_to_first_token", "inter_token_latency",
                     "requests_per_second", "latency", "concurrency"]:
            if key in data:
                metrics[key] = data[key]
    except Exception:
        pass
    return metrics


def main():
    cmd = sys.argv[1] if len(sys.argv) > 1 else "full"

    if cmd == "submit":
        job_id = submit_benchmark()
        print(f"GUIDELLM_JOB_ID={job_id}")
        with open("guidellm-job-id.txt", "w") as f:
            f.write(job_id)
    elif cmd == "poll":
        with open("guidellm-job-id.txt") as f:
            job_id = f.read().strip()
        result = poll_completion(job_id)
        timestamp = time.strftime("%Y%m%d_%H%M%S", time.gmtime())
        with open("guidellm-timestamp.txt", "w") as f:
            f.write(timestamp)
        with open("guidellm-results.json", "w") as f:
            json.dump(result, f, indent=2)
        metrics = extract_metrics(result)
        with open("guidellm-metrics.json", "w") as f:
            json.dump(metrics, f, indent=2)
    elif cmd == "full":
        job_id = submit_benchmark()
        with open("guidellm-job-id.txt", "w") as f:
            f.write(job_id)
        result = poll_completion(job_id)
        timestamp = time.strftime("%Y%m%d_%H%M%S", time.gmtime())
        with open("guidellm-timestamp.txt", "w") as f:
            f.write(timestamp)
        with open("guidellm-results.json", "w") as f:
            json.dump(result, f, indent=2)
    else:
        print(f"Unknown command: {cmd}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
