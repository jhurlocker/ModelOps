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
    model_name = os.environ["MODEL_NAME"]
    profile = os.environ.get("GUIDELLM_PROFILE", "throughput")
    rate = float(os.environ.get("GUIDELLM_RATE", "2"))
    max_seconds = int(float(os.environ.get("GUIDELLM_MAX_SECONDS", "30")))

    base = evalhub_url.rstrip("/")
    if not base.startswith(("http://", "https://")):
        base = f"https://{base}"
    headers = {"Authorization": f"Bearer {token}", "Content-Type": "application/json", "X-Tenant": tenant_ns}

    job_name = f"guidellm-{uuid.uuid4().hex[:8]}"

    payload = {
        "name": job_name,
        "model": {
            "url": target,
            "name": model_name,
        },
        "benchmarks": [
            {
                "provider_id": "guidellm",
                "id": profile,
                "parameters": {
                    "rate": rate,
                    "max_seconds": max_seconds,
                },
            }
        ],
    }

    print(f"Submitting GuideLLM benchmark {job_name} to EvalHub...")
    resp = requests.post(
        f"{base}/api/v1/evaluations/jobs",
        json=payload, headers=headers,
    )
    resp.raise_for_status()
    result = resp.json()
    job_id = result.get("resource", {}).get("id", job_name)
    print(f"GuideLLM job submitted: {job_id}")
    return job_id


def poll_completion(job_id, max_attempts=30, poll_interval=10):
    evalhub_url = os.environ["EVALHUB_URL"]
    token = os.environ["EVALHUB_TOKEN"]
    tenant_ns = os.environ["TENANT_NS"]

    base = evalhub_url.rstrip("/")
    if not base.startswith(("http://", "https://")):
        base = f"https://{base}"
    headers = {"Authorization": f"Bearer {token}", "X-Tenant": tenant_ns}

    for attempt in range(1, max_attempts + 1):
        print(f"Polling GuideLLM job {job_id} (attempt {attempt}/{max_attempts})...")
        try:
            resp = requests.get(
                f"{base}/api/v1/evaluations/jobs/{job_id}",
                headers=headers,
            )
            resp.raise_for_status()
            job_data = resp.json()
            status_obj = job_data.get("status", {})
            job_state = status_obj.get("state", "unknown") if isinstance(status_obj, dict) else str(status_obj)
            state_lower = job_state.lower()
            if state_lower in ("completed", "finished", "succeeded"):
                print("GuideLLM job completed.")
                return job_data
            elif state_lower in ("failed", "error"):
                raise RuntimeError(f"GuideLLM job failed: {json.dumps(job_data, indent=2)}")
            else:
                print(f"  Status: {job_state}")
        except Exception as e:
            print(f"Poll error: {e}")

        if attempt < max_attempts:
            time.sleep(poll_interval)

    raise TimeoutError(f"GuideLLM job {job_id} did not complete within {max_attempts * poll_interval}s")


def extract_metrics(result):
    metrics = {}
    try:
        results_obj = result.get("results", {})
        benchmarks = results_obj.get("benchmarks", [])
        if not benchmarks:
            benchmarks = result.get("benchmarks", [])
        for bm in benchmarks:
            bm_metrics = bm.get("metrics", {})
            for key, val in bm_metrics.items():
                metrics[key] = val
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
