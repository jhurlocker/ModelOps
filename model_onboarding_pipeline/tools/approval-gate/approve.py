"""
Approval gate tool - posts deployment plan to model intake UI, polls for
human approval/rejection decision.

Environment variables:
  MODEL_ID, MODEL_NAME, TARGET_NAMESPACE
  PLAN_ID            unique plan identifier
  REQUESTED_BY       who requested the onboarding
  APPROVAL_API_URL   model intake UI base URL
  POLL_INTERVAL      seconds between polls
  TIMEOUT_SECONDS    max wait before failing
"""

import os
import sys
import time
import json

import requests


def post_plan():
    """Read advisor workspace files and POST the plan to the intake UI."""
    workspace = os.environ.get("WORKSPACE_PATH", ".")

    # Read advisor outputs
    plan_id = os.environ["PLAN_ID"]
    pipelinerun_name = os.environ.get("PIPELINERUN_NAME", plan_id)
    model_id = os.environ["MODEL_ID"]
    model_name = os.environ["MODEL_NAME"]
    target_ns = os.environ["TARGET_NAMESPACE"]
    requested_by = os.environ.get("REQUESTED_BY", "")

    # Read summary from workspace
    try:
        with open(os.path.join(workspace, "gpu-advisor-summary.txt")) as f:
            recommendation_md = f.read()
    except Exception:
        recommendation_md = "No advisor summary found"

    try:
        with open(os.path.join(workspace, "deployment-options.json")) as f:
            deployment_options = json.load(f)
    except Exception:
        deployment_options = {}

    try:
        with open(os.path.join(workspace, "gpu-inventory.json")) as f:
            gpu_inventory = json.load(f)
    except Exception:
        gpu_inventory = {}

    try:
        with open(os.path.join(workspace, "recommended-vllm-command.sh")) as f:
            recommended_vllm_command = f.read()
    except Exception:
        recommended_vllm_command = ""

    try:
        with open(os.path.join(workspace, "plan-status.txt")) as f:
            plan_status = f.read().strip()
    except Exception:
        plan_status = "UNKNOWN"

    url = os.environ["APPROVAL_API_URL"].rstrip("/") + "/api/plans"
    payload = {
        "plan_id": plan_id,
        "pipelinerun_name": pipelinerun_name,
        "model_id": model_id,
        "model_name": model_name,
        "target_namespace": target_ns,
        "requested_by": requested_by,
        "plan_status": plan_status,
        "recommendation_md": recommendation_md,
        "deployment_options": deployment_options,
        "gpu_inventory": gpu_inventory,
        "recommended_vllm_command": recommended_vllm_command,
    }

    print(f"Posting deployment plan {plan_id} to {url}...")
    resp = requests.post(url, json=payload, timeout=30)
    resp.raise_for_status()
    print(f"Plan posted: {resp.json()}")


def poll_approval():
    """Poll the intake UI until a decision is made or timeout."""
    plan_id = os.environ["PLAN_ID"]
    poll_interval = int(os.environ.get("POLL_INTERVAL", "15"))
    timeout = int(os.environ.get("TIMEOUT_SECONDS", "3600"))
    url = os.environ["APPROVAL_API_URL"].rstrip("/") + f"/api/plans/{plan_id}"

    start = time.time()
    while time.time() - start < timeout:
        try:
            resp = requests.get(url, timeout=10)
            resp.raise_for_status()
            data = resp.json()
            status = data.get("status", "pending")
            print(f"Plan {plan_id} status: {status}")

            if status == "approved":
                print("APPROVED: human approved the deployment plan.")
                with open("approval-result.txt", "w") as f:
                    f.write("APPROVED")
                return True
            elif status == "rejected":
                print("REJECTED: human rejected the deployment plan.")
                with open("approval-result.txt", "w") as f:
                    f.write("REJECTED")
                sys.exit(1)
        except Exception as e:
            print(f"Poll error: {e}")

        time.sleep(poll_interval)

    print(f"TIMEOUT: no approval decision within {timeout}s")
    with open("approval-result.txt", "w") as f:
        f.write("TIMEOUT")
    sys.exit(1)


def auto_approve():
    """When no approval UI URL is set, auto-approve the plan."""
    print("No approval API URL configured - auto-approving.")
    with open("approval-result.txt", "w") as f:
        f.write("APPROVED")
    return True


def main():
    approval_api_url = os.environ.get("APPROVAL_API_URL", "").strip()

    if not approval_api_url:
        auto_approve()
        return

    cmd = sys.argv[1] if len(sys.argv) > 1 else "full"

    if cmd == "post":
        post_plan()
    elif cmd == "poll":
        poll_approval()
    elif cmd == "full":
        post_plan()
        poll_approval()
    else:
        print(f"Unknown command: {cmd}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
