import hmac
import os
import sys

import boto3
from flask import Flask, jsonify, request


class JobNotFoundError(Exception):
    pass


def _bearer_token_from_header():
    auth = request.headers.get("Authorization", "")
    if not auth.startswith("Bearer "):
        return None
    return auth[len("Bearer "):]


def _auth_required():
    if request.path == "/health":
        return None
    token = _bearer_token_from_header()
    if token is None:
        return jsonify({}), 401
    expected = os.environ["SHIM_AUTH_TOKEN"]
    if not hmac.compare_digest(token, expected):
        return jsonify({}), 401
    return None


app = Flask(__name__)
app.before_request(_auth_required)


@app.route("/health")
def health():
    return "", 200


@app.route("/jobs", methods=["POST"])
def submit_job():
    payload = request.get_json(force=True)
    try:
        job_id = submit_to_platform(payload)
    except Exception:
        return "", 500
    return jsonify({"jobId": job_id}), 201


@app.route("/jobs/<job_id>", methods=["GET"])
def check_status(job_id):
    try:
        phase, message, details_url = check_status_on_platform(job_id)
    except JobNotFoundError:
        return "", 404
    except Exception:
        return "", 500
    return jsonify({
        "phase": phase,
        "message": message,
        "detailsUrl": details_url,
    })


# ═══════════════════════════════════════════════════════════
# SageMaker platform integration
# ═══════════════════════════════════════════════════════════

_SM_CLIENT = None


def _sagemaker_client():
    global _SM_CLIENT
    if _SM_CLIENT is None:
        region = os.environ.get("AWS_DEFAULT_REGION", "us-east-1")
        _SM_CLIENT = boto3.client("sagemaker", region_name=region)
    return _SM_CLIENT


def submit_to_platform(payload):
    pipeline_name = payload.get("pipelineName", "")
    if not pipeline_name:
        raise ValueError("payload must include a non-empty pipelineName")
    display_name = payload.get("pipelineExecutionDisplayName", "")
    params = payload.get("pipelineParameters") or {}
    kwargs = {"PipelineName": pipeline_name}
    if display_name:
        kwargs["PipelineExecutionDisplayName"] = display_name
    if params:
        kwargs["PipelineParameters"] = [
            {"Name": str(k), "Value": str(v)} for k, v in params.items()
        ]
    sm = _sagemaker_client()
    resp = sm.start_pipeline_execution(**kwargs)
    return resp["PipelineExecutionArn"]


def check_status_on_platform(job_id):
    sm = _sagemaker_client()
    try:
        resp = sm.describe_pipeline_execution(PipelineExecutionArn=job_id)
    except sm.exceptions.ResourceNotFoundError:
        raise JobNotFoundError(job_id)
    status = resp.get("PipelineExecutionStatus", "")
    failure_reason = resp.get("FailureReason", "")
    region = sm.meta.region_name
    details_url = (
        f"https://console.aws.amazon.com/sagemaker/home"
        f"?region={region}#/pipelines/executions/{job_id}"
    )
    return _map_status(status, failure_reason, details_url)


_STATUS_MAP = {
    "Executing": ("Running", "Pipeline execution is in progress"),
    "Stopping": ("Running", "Pipeline execution is stopping"),
    "Succeeded": ("Succeeded", "Pipeline execution completed successfully"),
    "Failed": ("Failed", ""),
    "Stopped": ("Failed", "Pipeline execution was stopped"),
}


def _map_status(status, failure_reason, details_url):
    mapped = _STATUS_MAP.get(status)
    if mapped is None:
        return ("Running", f"Unrecognized SageMaker status: {status}", details_url)
    phase, default_message = mapped
    if phase == "Failed" and failure_reason:
        return (phase, failure_reason, details_url)
    return (phase, default_message, details_url)


if __name__ == "__main__":
    token = os.environ.get("SHIM_AUTH_TOKEN", "")
    if not token:
        print("SHIM_AUTH_TOKEN is required and must not be empty.", file=sys.stderr)
        sys.exit(1)
    app.run(host="0.0.0.0", port=8080)
