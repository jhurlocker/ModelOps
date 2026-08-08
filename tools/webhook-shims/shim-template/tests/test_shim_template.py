import os
import subprocess
import sys

import pytest

from app import app as flask_app


@pytest.fixture
def client():
    flask_app.config["TESTING"] = True
    os.environ["SHIM_AUTH_TOKEN"] = "test-token"
    with flask_app.test_client() as c:
        yield c


@pytest.fixture(autouse=True)
def patch_submit_and_check(monkeypatch):
    monkeypatch.setattr("app.submit_to_platform", lambda payload: "mock-job-1")
    monkeypatch.setattr(
        "app.check_status_on_platform",
        lambda job_id: ("Running", "Job is executing", "https://example.com/jobs/mock-job-1"),
    )


class TestStartupGuard:
    def test_module_imports_when_token_set(self):
        """Proves the startup guard is in __main__, not at import time."""
        import app
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
        resp = client.post("/jobs", json={"modelId": "test"})
        assert resp.status_code == 401

    def test_submit_jobs_wrong_token_returns_401(self, client):
        resp = client.post(
            "/jobs",
            json={"modelId": "test"},
            headers={"Authorization": "Bearer wrong-token"},
        )
        assert resp.status_code == 401

    def test_submit_jobs_malformed_auth_header_returns_401(self, client):
        resp = client.post(
            "/jobs",
            json={"modelId": "test"},
            headers={"Authorization": "not-bearer-format"},
        )
        assert resp.status_code == 401

    def test_submit_jobs_correct_token_passes_auth(self, client):
        resp = client.post(
            "/jobs",
            json={"modelId": "test"},
            headers={"Authorization": "Bearer test-token"},
        )
        assert resp.status_code == 201

    def test_check_status_no_auth_header_returns_401(self, client):
        resp = client.get("/jobs/mock-job-1")
        assert resp.status_code == 401

    def test_check_status_correct_token_passes_auth(self, client):
        resp = client.get(
            "/jobs/mock-job-1",
            headers={"Authorization": "Bearer test-token"},
        )
        assert resp.status_code == 200


class TestContractShape:
    def test_submit_response_has_exact_canonical_shape(self, client):
        resp = client.post(
            "/jobs",
            json={"modelId": "test"},
            headers={"Authorization": "Bearer test-token"},
        )
        assert resp.status_code == 201
        body = resp.get_json()
        assert set(body.keys()) == {"jobId"}
        assert isinstance(body["jobId"], str)
        assert len(body["jobId"]) > 0

    def test_status_response_has_exact_canonical_shape(self, client):
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
