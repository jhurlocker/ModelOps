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


@pytest.fixture
def azure_env():
    os.environ["AZURE_SUBSCRIPTION_ID"] = "test-sub-id"
    os.environ["AZURE_RESOURCE_GROUP"] = "test-rg"
    os.environ["AZURE_WORKSPACE_NAME"] = "test-ws"
    yield
    del os.environ["AZURE_SUBSCRIPTION_ID"]
    del os.environ["AZURE_RESOURCE_GROUP"]
    del os.environ["AZURE_WORKSPACE_NAME"]


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
        resp = client.post("/jobs", json={"experimentName": "test"})
        assert resp.status_code == 401

    def test_submit_jobs_wrong_token_returns_401(self, client):
        resp = client.post(
            "/jobs",
            json={"experimentName": "test"},
            headers={"Authorization": "Bearer wrong-token"},
        )
        assert resp.status_code == 401

    def test_check_status_no_auth_header_returns_401(self, client):
        resp = client.get("/jobs/mock-job-1")
        assert resp.status_code == 401


class TestContractShape:
    def test_submit_response_has_exact_canonical_shape(self, client, monkeypatch):
        monkeypatch.setattr(
            app, "submit_to_platform",
            lambda payload: "mock-azure-job-1",
        )

        resp = client.post(
            "/jobs",
            json={"experimentName": "test", "pipelineDefinitionId": "test-pipeline"},
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
            lambda job_id: ("Running", "Job is running", "https://ml.azure.com/pipelines/mock-job-1?wsid=/subscriptions/sub/rg/rg/p/Microsoft.MachineLearningServices/workspaces/ws"),
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


@pytest.mark.usefixtures("azure_env")
class TestAzureAIIntegration:
    def test_submit_creates_pipeline_job(self, monkeypatch):
        mock_ml_client = _mock_ml_client(job_name="azure-job-abc123")
        monkeypatch.setattr(app, "_AZURE_ML_CLIENT", mock_ml_client)

        result = app.submit_to_platform({
            "experimentName": "model-validation",
            "displayName": "model-intake-test",
            "pipelineDefinitionId": "my-pipeline:1",
            "parameters": {"modelId": "foo/bar"},
        })

        assert result == "azure-job-abc123"
        mock_ml_client.jobs.create_or_update.assert_called_once()

    def test_submit_environment_variables_setup(self, monkeypatch):
        mock_ml_client = _mock_ml_client(job_name="azure-job-env")
        monkeypatch.setattr(app, "_AZURE_ML_CLIENT", mock_ml_client)

        result = app.submit_to_platform({
            "experimentName": "test-exp",
            "pipelineDefinitionId": "pipe:v1",
        })

        assert result == "azure-job-env"

    def test_submit_raises_on_missing_expected_fields(self):
        with pytest.raises(ValueError, match="experimentName"):
            app.submit_to_platform({"pipelineDefinitionId": "pipe:v1"})

        with pytest.raises(ValueError, match="experimentName"):
            app.submit_to_platform({"experimentName": "", "pipelineDefinitionId": "pipe:v1"})

        with pytest.raises(ValueError, match="pipelineDefinitionId"):
            app.submit_to_platform({"experimentName": "test"})

        with pytest.raises(ValueError, match="pipelineDefinitionId"):
            app.submit_to_platform({"experimentName": "test", "pipelineDefinitionId": ""})

    def test_check_status_not_started_maps_to_running(self, monkeypatch):
        mock_ml_client = _mock_ml_client(status="NotStarted")
        monkeypatch.setattr(app, "_AZURE_ML_CLIENT", mock_ml_client)

        phase, message, details_url = app.check_status_on_platform("mock-job-1")

        assert phase == "Running"
        assert message == "Job has not started"
        assert "mock-job-1" in details_url

    def test_check_status_queued_maps_to_running(self, monkeypatch):
        mock_ml_client = _mock_ml_client(status="Queued")
        monkeypatch.setattr(app, "_AZURE_ML_CLIENT", mock_ml_client)

        phase, _, _ = app.check_status_on_platform("mock-job-1")

        assert phase == "Running"

    def test_check_status_preparing_maps_to_running(self, monkeypatch):
        mock_ml_client = _mock_ml_client(status="Preparing")
        monkeypatch.setattr(app, "_AZURE_ML_CLIENT", mock_ml_client)

        phase, _, _ = app.check_status_on_platform("mock-job-1")

        assert phase == "Running"

    def test_check_status_running_maps_to_running(self, monkeypatch):
        mock_ml_client = _mock_ml_client(status="Running")
        monkeypatch.setattr(app, "_AZURE_ML_CLIENT", mock_ml_client)

        phase, _, _ = app.check_status_on_platform("mock-job-1")

        assert phase == "Running"

    def test_check_status_finalizing_maps_to_running(self, monkeypatch):
        mock_ml_client = _mock_ml_client(status="Finalizing")
        monkeypatch.setattr(app, "_AZURE_ML_CLIENT", mock_ml_client)

        phase, _, _ = app.check_status_on_platform("mock-job-1")

        assert phase == "Running"

    def test_check_status_completed_maps_to_succeeded(self, monkeypatch):
        mock_ml_client = _mock_ml_client(status="Completed")
        monkeypatch.setattr(app, "_AZURE_ML_CLIENT", mock_ml_client)

        phase, message, _ = app.check_status_on_platform("mock-job-1")

        assert phase == "Succeeded"
        assert "successfully" in message

    def test_check_status_failed_maps_to_failed(self, monkeypatch):
        mock_ml_client = _mock_ml_client(status="Failed")
        monkeypatch.setattr(app, "_AZURE_ML_CLIENT", mock_ml_client)

        phase, message, _ = app.check_status_on_platform("mock-job-1")

        assert phase == "Failed"
        assert message == ""

    def test_check_status_canceled_maps_to_failed(self, monkeypatch):
        mock_ml_client = _mock_ml_client(status="Canceled")
        monkeypatch.setattr(app, "_AZURE_ML_CLIENT", mock_ml_client)

        phase, message, _ = app.check_status_on_platform("mock-job-1")

        assert phase == "Failed"
        assert "canceled" in message.lower()

    def test_check_status_unrecognized_maps_to_running(self, monkeypatch):
        mock_ml_client = _mock_ml_client(status="BogusState")
        monkeypatch.setattr(app, "_AZURE_ML_CLIENT", mock_ml_client)

        phase, message, _ = app.check_status_on_platform("mock-job-1")

        assert phase == "Running"
        assert "Unrecognized" in message
        assert "BogusState" in message

    def test_check_status_resource_not_found_raises_job_not_found(self, monkeypatch):
        from azure.core.exceptions import ResourceNotFoundError as AzureResourceNotFoundError

        mock_ml_client = _mock_ml_client(status="Running")
        mock_ml_client.jobs.get.side_effect = AzureResourceNotFoundError("not found")
        monkeypatch.setattr(app, "_AZURE_ML_CLIENT", mock_ml_client)

        with pytest.raises(app.JobNotFoundError):
            app.check_status_on_platform("nonexistent-job")


def _mock_ml_client(job_name="mock-job-1", status="Running"):
    mock = MagicMock()
    mock.jobs = MagicMock()

    submitted_job = MagicMock()
    submitted_job.name = job_name
    mock.jobs.create_or_update.return_value = submitted_job

    status_job = MagicMock()
    status_job.status = status
    mock.jobs.get.return_value = status_job

    return mock
