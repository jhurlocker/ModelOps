"""Unit tests for the Model Registry connection derivation shared by the
registry-writing helpers (register.py, evaluate.py, and the inline
registry_helper.py embedded in model-registry-task.yaml).

These cover the behavior introduced for the https/in-cluster-8443 + SA-token
change: token and CA resolution and the ModelRegistry() kwargs assembled from
them. They deliberately do NOT import model_registry, so they run without the
registry client installed.
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

import register  # noqa: E402


def test_https_server_derives_secure_and_adds_ca():
    kwargs = register.connection_kwargs(
        "https://modelops-registry.rhoai-model-registries.svc.cluster.local",
        8443,
        "tok-abc",
        "/ca/service-ca.crt",
    )
    assert kwargs["server_address"] == "https://modelops-registry.rhoai-model-registries.svc.cluster.local"
    assert kwargs["port"] == 8443
    assert kwargs["is_secure"] is True
    assert kwargs["user_token"] == "tok-abc"
    assert kwargs["custom_ca"] == "/ca/service-ca.crt"


def test_http_server_disables_secure_and_omits_ca():
    kwargs = register.connection_kwargs(
        "http://registry:8080", 8080, "tok-abc", "/ca/service-ca.crt"
    )
    assert kwargs["is_secure"] is False
    # CA is only relevant over TLS; it must not be passed for plain HTTP.
    assert "custom_ca" not in kwargs
    assert kwargs["user_token"] == "tok-abc"


def test_empty_token_is_omitted():
    kwargs = register.connection_kwargs("https://registry", 8443, "", "/ca/service-ca.crt")
    assert "user_token" not in kwargs


def test_empty_ca_is_omitted_even_over_https():
    kwargs = register.connection_kwargs("https://registry", 8443, "tok", "")
    assert kwargs["is_secure"] is True
    assert "custom_ca" not in kwargs


def test_read_secret_file_prefers_first_nonempty(tmp_path):
    empty = tmp_path / "empty"
    empty.write_text("")
    full = tmp_path / "full"
    full.write_text("hello-token")
    assert register.read_secret_file([str(empty), str(full)]) == "hello-token"


def test_read_secret_file_all_missing_returns_empty():
    assert register.read_secret_file(["/nonexistent/a", "/nonexistent/b"]) == ""


def test_resolve_ca_path_only_when_file_exists(tmp_path, monkeypatch):
    ca = tmp_path / "service-ca.crt"
    ca.write_text("PEM")
    monkeypatch.setenv("MR_CA_PATH", str(ca))
    assert register.resolve_ca_path(os.environ) == str(ca)

    monkeypatch.setenv("MR_CA_PATH", str(tmp_path / "missing.crt"))
    assert register.resolve_ca_path(os.environ) == ""


def test_resolve_user_token_uses_explicit_path(tmp_path, monkeypatch):
    token_file = tmp_path / "token"
    token_file.write_text("projected-token")
    monkeypatch.setenv("MR_USER_TOKEN_PATH", str(token_file))
    assert register.resolve_user_token(os.environ) == "projected-token"


def test_resolve_user_token_empty_when_no_token_files(monkeypatch):
    monkeypatch.setenv("MR_USER_TOKEN_PATH", "/nonexistent/token")
    assert register.resolve_user_token(os.environ) == ""


def test_resolve_ca_path_prefers_primary(monkeypatch, tmp_path):
    primary = tmp_path / "primary.crt"
    fallback = tmp_path / "fallback.crt"
    primary.write_text("PRIMARY")
    fallback.write_text("FALLBACK")
    monkeypatch.setattr(
        register, "DEFAULT_CA_PATHS", (str(primary), str(fallback))
    )
    monkeypatch.delenv("MR_CA_PATH", raising=False)
    assert register.resolve_ca_path(os.environ) == str(primary)


def test_resolve_ca_path_falls_back_to_secondary(monkeypatch, tmp_path):
    primary = tmp_path / "primary.crt"
    fallback = tmp_path / "fallback.crt"
    fallback.write_text("FALLBACK")
    monkeypatch.setattr(
        register, "DEFAULT_CA_PATHS", (str(primary), str(fallback))
    )
    monkeypatch.delenv("MR_CA_PATH", raising=False)
    assert register.resolve_ca_path(os.environ) == str(fallback)


def test_resolve_ca_path_explicit_env_takes_precedence(monkeypatch, tmp_path):
    primary = tmp_path / "primary.crt"
    fallback = tmp_path / "fallback.crt"
    explicit = tmp_path / "explicit.crt"
    primary.write_text("PRIMARY")
    fallback.write_text("FALLBACK")
    explicit.write_text("EXPLICIT")
    monkeypatch.setattr(
        register, "DEFAULT_CA_PATHS", (str(primary), str(fallback))
    )
    monkeypatch.setenv("MR_CA_PATH", str(explicit))
    assert register.resolve_ca_path(os.environ) == str(explicit)


def test_evaluate_matches_register_connection_logic():
    """The three registry-writing sites carry identical connection logic;
    this pins register.py (canonical) and evaluate.py (compliance-scanner)
    to the same derived kwargs."""
    import importlib.util

    eval_path = os.path.join(
        os.path.dirname(__file__), "..", "..", "compliance-scanner", "evaluate.py"
    )
    spec = importlib.util.spec_from_file_location("evaluate", eval_path)
    evaluate = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(evaluate)

    assert register.connection_kwargs(
        "https://registry", 8443, "tok", "/ca.crt"
    ) == evaluate.connection_kwargs("https://registry", 8443, "tok", "/ca.crt")
    assert register.connection_kwargs(
        "http://registry", 8080, "tok", "/ca.crt"
    ) == evaluate.connection_kwargs("http://registry", 8080, "tok", "/ca.crt")

