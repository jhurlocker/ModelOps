"""
Canonical model-registry upsert helper, extracted from inline Tekton scripts
into a standalone CLI tool. Used by compliance-artifact-scan, security-scan,
and model-registry tasks.

Driven entirely by environment variables so the same code works at every
onboarding stage:
  MR_SERVER          registry REST base (https in-cluster service, port 8443)
  MR_PORT            registry REST port (default 8443)
  MR_AUTHOR          author recorded on the entry
  MR_USER_TOKEN_PATH optional path to the bearer token file (projected SA token)
  MR_CA_PATH         optional path to the service-CA bundle (TLS verification)
  MODEL_NAME         registered-model name (identity across the whole pipeline)
  MODEL_VERSION      version name (default v1)
  MODEL_URI          artifact URI (used only when first registering)
  MODEL_DESCRIPTION  optional description (set on first registration)
  MODEL_FORMAT       model format name (default vLLM)
  MR_STAGE           furthest onboarding stage reached
  MR_PROPS_JSON      JSON object of custom properties to MERGE onto the version
"""

import json
import os
import time
import sys


def log(msg):
    print(f"[model-registry] {msg}", flush=True)


DEFAULT_REGISTRY_PORT = "8443"
DEFAULT_TOKEN_PATH = "/var/run/model-registry/token"
LEGACY_TOKEN_PATH = "/var/run/secrets/kubernetes.io/serviceaccount/token"
# CA trust sources in preference order: the pod's own injected SA CA bundle
# (primary), then the dedicated inject-cabundle ConfigMap mounted at
# /etc/model-registry-ca (fallback). Mirrors Zot's /etc/zot-ca pattern because
# the SA-dir service-ca.crt is not guaranteed on every cluster
# (see docs/PHASE_LOG.md: the EvalHub CA fix and the Zot TLS phase entry).
DEFAULT_CA_PATHS = (
    "/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt",
    "/etc/model-registry-ca/service-ca.crt",
)


def read_secret_file(paths):
    """Return the first non-empty file content from `paths`, else ""."""
    for path in paths:
        try:
            with open(path, "r") as f:
                value = f.read().strip()
            if value:
                return value
        except OSError:
            continue
    return ""


def resolve_user_token(env):
    """Resolve the bearer token: explicit path, then the projected token
    mount, then the pod's default serviceaccount token."""
    explicit = env.get("MR_USER_TOKEN_PATH", "").strip()
    paths = (
        [explicit, DEFAULT_TOKEN_PATH, LEGACY_TOKEN_PATH]
        if explicit
        else [DEFAULT_TOKEN_PATH, LEGACY_TOKEN_PATH]
    )
    return read_secret_file(paths)


def resolve_ca_path(env):
    """Return the first existing CA bundle path in preference order."""
    explicit = env.get("MR_CA_PATH", "").strip()
    candidates = list(DEFAULT_CA_PATHS)
    if explicit:
        candidates.insert(0, explicit)
    for path in candidates:
        if path and os.path.exists(path):
            return path
    return ""


def connection_kwargs(server, port, token, ca_path):
    """Assemble ModelRegistry() constructor kwargs from resolved values.

    user_token is only included when a token was resolved; custom_ca is only
    included over TLS (https) and only when a CA bundle path was resolved.
    """
    is_secure = server.lower().startswith("https")
    kwargs = {"server_address": server, "port": port, "is_secure": is_secure}
    if token:
        kwargs["user_token"] = token
    if is_secure and ca_path:
        kwargs["custom_ca"] = ca_path
    return kwargs


def main():
    from model_registry import ModelRegistry

    server = os.environ["MR_SERVER"].rstrip("/")
    port = int(os.environ.get("MR_PORT", DEFAULT_REGISTRY_PORT))
    author = os.environ.get("MR_AUTHOR", "ModelOps Platform")
    name = os.environ["MODEL_NAME"]
    version = os.environ.get("MODEL_VERSION", "v1")
    uri = os.environ.get("MODEL_URI", "") or f"oci://unknown/{name}:{version}"
    description = os.environ.get("MODEL_DESCRIPTION", "")
    fmt = os.environ.get("MODEL_FORMAT", "vLLM")
    stage = os.environ.get("MR_STAGE", "")

    try:
        props = json.loads(os.environ.get("MR_PROPS_JSON", "{}") or "{}")
    except Exception as e:
        log(f"WARNING: MR_PROPS_JSON not valid JSON ({e}); ignoring.")
        props = {}

    merged = {str(k): ("" if v is None else str(v)) for k, v in props.items()}
    if stage:
        merged["onboarding-stage"] = stage
    merged["last-updated"] = time.strftime("%Y-%m-%d %H:%M:%S UTC", time.gmtime())

    token = resolve_user_token(os.environ)
    ca_path = resolve_ca_path(os.environ)
    kwargs = connection_kwargs(server, port, token, ca_path)
    kwargs["author"] = author
    try:
        client = ModelRegistry(**kwargs)
    except Exception as e:
        log(f"WARNING: could not connect to model registry at {server}:{port} ({e}). Skipping registry update.")
        return

    try:
        existing = {m.name for m in client.get_registered_models()}
    except Exception as e:
        log(f"WARNING: could not list registered models ({e}). Skipping.")
        return

    if name in existing:
        log(f"Model '{name}' already registered - updating version '{version}'.")
        try:
            v = client.get_model_version(name, version)
        except Exception:
            v = None
        if v is None:
            try:
                client.register_model(
                    name=name, uri=uri, version=version,
                    model_format_name=fmt, model_format_version="1",
                    description=description or "Registered by ModelOps pipeline",
                    metadata=merged,
                )
                log(f"Created new version '{version}' with {len(merged)} properties.")
                return
            except Exception as e:
                log(f"WARNING: could not create version '{version}' ({e}).")
                return
        current = dict(v.custom_properties) if v.custom_properties else {}
        current.update(merged)
        v.custom_properties = current
        try:
            client.update(v)
            log(f"Updated version '{version}' - merged {len(merged)} properties.")
        except Exception as e:
            log(f"WARNING: could not update version '{version}' ({e}).")
    else:
        log(f"Model '{name}' not found - registering new entry (version '{version}').")
        try:
            client.register_model(
                name=name, uri=uri, version=version,
                model_format_name=fmt, model_format_version="1",
                description=description or "Onboarding via ModelOps pipeline",
                metadata=merged,
            )
            log(f"Registered '{name}' with {len(merged)} properties.")
        except Exception as e:
            log(f"WARNING: could not register model '{name}' ({e}).")


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        log(f"WARNING: unexpected registry error ({e}); continuing (non-fatal).")
        sys.exit(0)
