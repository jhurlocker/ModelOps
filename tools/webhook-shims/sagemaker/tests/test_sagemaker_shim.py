import os
import subprocess
import sys
from unittest.mock import MagicMock

import pytest

import app


@pytest.fixture
def client():
    app.app.config["TESTING"] = True
    os.environ["SHIM_AUTH_TOKEN"] = "test-token"
    with app.app.test_client() as c:
        yield c


class TestStartupGuard:
    def test_module_imports_when_token_set(self):
        assert app.app is not None

    def test_main_exits_when_token_unset(self):
        import pathlib
        app_path = pathlib.Path(__file__).resolve().parent.parent / "app.py"
        result = subprocess.run(
            [sys.executable, str(app_path)],
            capture_output=True,
            text=True,
            env={**os.environ, "SHIM_AUTH_TOKEN": ""},
            timeout=5,
        )
        assert result.returncode == 1
        assert "SHIM_AUTH_TOKEN is required and must not be empty" in result.stderr


class TestAuthMiddleware:
    def test_health_endpoint_no_auth_required(self, client):
        resp = client.get("/health")
        assert resp.status_code == 200

    def test_submit_jobs_no_auth_header_returns_401(self, client):
        resp = client.post("/jobs", json={"pipelineName": "test-pipeline"})
        assert resp.status_code == 401

    def test_submit_jobs_wrong_token_returns_401(self, client):
        resp = client.post(
            "/jobs",
            json={"pipelineName": "test-pipeline"},
            headers={"Authorization": "Bearer wrong-token"},
        )
        assert resp.status_code == 401

    def test_check_status_no_auth_header_returns_401(self, client):
        resp = client.get("/jobs/arn:aws:sagemaker:us-east-1:123:execution/test")
        assert resp.status_code == 401


class TestContractShape:
    def test_submit_response_has_exact_canonical_shape(self, client, monkeypatch):
        monkeypatch.setattr(
            app, "submit_to_platform",
            lambda payload: "arn:aws:sagemaker:us-east-1:123456789012:pipeline/test/execution/mock-1",
        )

        resp = client.post(
            "/jobs",
            json={"pipelineName": "test-pipeline"},
            headers={"Authorization": "Bearer test-token"},
        )
        assert resp.status_code == 201
        body = resp.get_json()
        assert set(body.keys()) == {"jobId"}
        assert isinstance(body["jobId"], str)
        assert len(body["jobId"]) > 0

    def test_status_response_has_exact_canonical_shape(self, client, monkeypatch):
        monkeypatch.setattr(
            app, "check_status_on_platform",
            lambda job_id: ("Running", "Pipeline execution is in progress", "https://console.aws.amazon.com/sagemaker/home?region=us-east-1#/pipelines/executions/mock-1"),
        )

        resp = client.get(
            "/jobs/mock-job-1",
            headers={"Authorization": "Bearer test-token"},
        )
        assert resp.status_code == 200
        body = resp.get_json()
        assert set(body.keys()) == {"phase", "message", "detailsUrl"}
        assert body["phase"] in ("Running", "Succeeded", "Failed")
        assert isinstance(body["message"], str)
        assert isinstance(body["detailsUrl"], str)


class TestSageMakerIntegration:
    def test_submit_maps_pipeline_parameters(self, monkeypatch):
        mock_sm = _mock_sagemaker_client()
        monkeypatch.setattr(app, "_SM_CLIENT", mock_sm)

        result = app.submit_to_platform({
            "pipelineName": "my-pipeline",
            "pipelineExecutionDisplayName": "my-display-name",
            "pipelineParameters": {"modelId": "foo/bar", "instanceType": "ml.g5.xlarge"},
        })

        assert result == "arn:aws:sagemaker:us-east-1:123456789012:pipeline/my-pipeline/execution/test-exec-id"
        mock_sm.start_pipeline_execution.assert_called_once_with(
            PipelineName="my-pipeline",
            PipelineExecutionDisplayName="my-display-name",
            PipelineParameters=[
                {"Name": "modelId", "Value": "foo/bar"},
                {"Name": "instanceType", "Value": "ml.g5.xlarge"},
            ],
        )

    def test_submit_uses_defaults_when_fields_missing(self, monkeypatch):
        mock_sm = _mock_sagemaker_client()
        monkeypatch.setattr(app, "_SM_CLIENT", mock_sm)

        result = app.submit_to_platform({"pipelineName": "bare-pipeline"})

        assert "test-exec-id" in result
        call_kwargs = mock_sm.start_pipeline_execution.call_args.kwargs
        assert call_kwargs["PipelineName"] == "bare-pipeline"
        assert "PipelineExecutionDisplayName" not in call_kwargs
        assert "PipelineParameters" not in call_kwargs

    def test_submit_raises_on_missing_pipeline_name(self):
        with pytest.raises(ValueError, match="pipelineName"):
            app.submit_to_platform({"someOtherField": "value"})

    def test_submit_raises_on_empty_pipeline_name(self):
        with pytest.raises(ValueError, match="pipelineName"):
            app.submit_to_platform({"pipelineName": ""})

    def test_check_status_executing_maps_to_running(self, monkeypatch):
        mock_sm = _mock_sagemaker_client(status="Executing")
        monkeypatch.setattr(app, "_SM_CLIENT", mock_sm)

        phase, message, details_url = app.check_status_on_platform(
            "arn:aws:sagemaker:us-east-1:123:execution/test"
        )

        assert phase == "Running"
        assert message == "Pipeline execution is in progress"
        assert "us-east-1" in details_url

    def test_check_status_succeeded_maps_to_succeeded(self, monkeypatch):
        mock_sm = _mock_sagemaker_client(status="Succeeded")
        monkeypatch.setattr(app, "_SM_CLIENT", mock_sm)

        phase, message, _ = app.check_status_on_platform("arn:aws:sagemaker:us-east-1:123:execution/test")

        assert phase == "Succeeded"
        assert "completed successfully" in message

    def test_check_status_failed_with_failure_reason(self, monkeypatch):
        mock_sm = _mock_sagemaker_client(
            status="Failed",
            failure_reason="Step training failed: CUDA out of memory",
        )
        monkeypatch.setattr(app, "_SM_CLIENT", mock_sm)

        phase, message, _ = app.check_status_on_platform("arn:aws:sagemaker:us-east-1:123:execution/test")

        assert phase == "Failed"
        assert message == "Step training failed: CUDA out of memory"

    def test_check_status_failed_without_failure_reason(self, monkeypatch):
        mock_sm = _mock_sagemaker_client(status="Failed", failure_reason="")
        monkeypatch.setattr(app, "_SM_CLIENT", mock_sm)

        phase, message, _ = app.check_status_on_platform("arn:aws:sagemaker:us-east-1:123:execution/test")

        assert phase == "Failed"
        assert message == ""

    def test_check_status_stopped_maps_to_failed(self, monkeypatch):
        mock_sm = _mock_sagemaker_client(status="Stopped")
        monkeypatch.setattr(app, "_SM_CLIENT", mock_sm)

        phase, message, _ = app.check_status_on_platform("arn:aws:sagemaker:us-east-1:123:execution/test")

        assert phase == "Failed"
        assert "stopped" in message.lower()

    def test_check_status_stopping_maps_to_running(self, monkeypatch):
        mock_sm = _mock_sagemaker_client(status="Stopping")
        monkeypatch.setattr(app, "_SM_CLIENT", mock_sm)

        phase, message, _ = app.check_status_on_platform("arn:aws:sagemaker:us-east-1:123:execution/test")

        assert phase == "Running"
        assert "stopping" in message.lower()

    def test_check_status_unrecognized_maps_to_running(self, monkeypatch):
        mock_sm = _mock_sagemaker_client(status="BogusStatus")
        monkeypatch.setattr(app, "_SM_CLIENT", mock_sm)

        phase, message, _ = app.check_status_on_platform("arn:aws:sagemaker:us-east-1:123:execution/test")

        assert phase == "Running"
        assert "Unrecognized" in message
        assert "BogusStatus" in message

    def test_check_status_resource_not_found_raises_job_not_found(self, monkeypatch):
        mock_sm = _mock_sagemaker_client(status="Executing")
        mock_sm.exceptions.ResourceNotFoundError = type("ResourceNotFoundError", (Exception,), {})
        exc = mock_sm.exceptions.ResourceNotFoundError({}, "not found")
        mock_sm.describe_pipeline_execution.side_effect = exc
        monkeypatch.setattr(app, "_SM_CLIENT", mock_sm)

        with pytest.raises(app.JobNotFoundError):
            app.check_status_on_platform("arn:nonexistent")


def _mock_sagemaker_client(status="Executing", failure_reason=""):
    mock = MagicMock()
    mock.meta.region_name = "us-east-1"
    mock.exceptions = MagicMock()
    mock.exceptions.ResourceNotFoundError = type("ResourceNotFoundError", (Exception,), {})

    mock.start_pipeline_execution.return_value = {
        "PipelineExecutionArn": (
            "arn:aws:sagemaker:us-east-1:123456789012:pipeline/"
            "my-pipeline/execution/test-exec-id"
        ),
    }

    mock.describe_pipeline_execution.return_value = {
        "PipelineExecutionArn": (
            "arn:aws:sagemaker:us-east-1:123456789012:pipeline/"
            "my-pipeline/execution/test-exec-id"
        ),
        "PipelineExecutionStatus": status,
        "PipelineExecutionDisplayName": "test-display-name",
    }
    if failure_reason:
        mock.describe_pipeline_execution.return_value["FailureReason"] = failure_reason

    return mock
