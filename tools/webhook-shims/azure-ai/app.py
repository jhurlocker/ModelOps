import hmac
import os
import sys

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
# Azure AI platform integration
# ═══════════════════════════════════════════════════════════

_AZURE_ML_CLIENT = None


def _azure_ml_client():
    global _AZURE_ML_CLIENT
    if _AZURE_ML_CLIENT is None:
        from azure.ai.ml import MLClient
        from azure.identity import DefaultAzureCredential

        subscription_id = os.environ["AZURE_SUBSCRIPTION_ID"]
        resource_group = os.environ["AZURE_RESOURCE_GROUP"]
        workspace = os.environ["AZURE_WORKSPACE_NAME"]

        credential = DefaultAzureCredential()
        _AZURE_ML_CLIENT = MLClient(
            credential=credential,
            subscription_id=subscription_id,
            resource_group_name=resource_group,
            workspace_name=workspace,
        )
    return _AZURE_ML_CLIENT


def _studio_url(job_id):
    subscription_id = os.environ["AZURE_SUBSCRIPTION_ID"]
    resource_group = os.environ["AZURE_RESOURCE_GROUP"]
    workspace = os.environ["AZURE_WORKSPACE_NAME"]
    return (
        f"https://ml.azure.com/pipelines/{job_id}"
        f"?wsid=/subscriptions/{subscription_id}"
        f"/resourceGroups/{resource_group}"
        f"/providers/Microsoft.MachineLearningServices/workspaces/{workspace}"
    )


def submit_to_platform(payload):
    from azure.ai.ml.entities import PipelineJob

    experiment_name = payload.get("experimentName", "")
    if not experiment_name:
        raise ValueError("payload must include a non-empty experimentName")
    display_name = payload.get("displayName", "")
    pipeline_definition_id = payload.get("pipelineDefinitionId", "")
    if not pipeline_definition_id:
        raise ValueError("payload must include a non-empty pipelineDefinitionId")
    parameters = payload.get("parameters") or {}

    ml_client = _azure_ml_client()

    pipeline_job = PipelineJob(
        experiment_name=experiment_name,
        display_name=display_name,
        component=pipeline_definition_id,
        inputs=parameters,
    )
    submitted = ml_client.jobs.create_or_update(pipeline_job)
    return submitted.name


def check_status_on_platform(job_id):
    from azure.core.exceptions import ResourceNotFoundError as AzureResourceNotFoundError

    ml_client = _azure_ml_client()
    try:
        job = ml_client.jobs.get(name=job_id)
    except AzureResourceNotFoundError:
        raise JobNotFoundError(job_id)
    status = job.status or ""
    details_url = _studio_url(job_id)
    return _map_status(status, details_url)


_STATUS_MAP = {
    "NotStarted": ("Running", "Job has not started"),
    "Queued": ("Running", "Job is queued"),
    "Preparing": ("Running", "Job is preparing"),
    "Running": ("Running", "Job is running"),
    "Finalizing": ("Running", "Job is finalizing"),
    "Completed": ("Succeeded", "Job completed successfully"),
    "Failed": ("Failed", ""),
    "Canceled": ("Failed", "Job was canceled"),
}


def _map_status(status, details_url):
    mapped = _STATUS_MAP.get(status)
    if mapped is None:
        return ("Running", f"Unrecognized Azure ML status: {status}", details_url)
    phase, default_message = mapped
    if phase == "Failed" or default_message:
        return (phase, default_message, details_url)
    return (phase, default_message, details_url)


if __name__ == "__main__":
    token = os.environ.get("SHIM_AUTH_TOKEN", "")
    if not token:
        print("SHIM_AUTH_TOKEN is required and must not be empty.", file=sys.stderr)
        sys.exit(1)
    app.run(host="0.0.0.0", port=8080)
