"""
Canonical model-registry upsert helper, extracted from inline Tekton scripts
into a standalone CLI tool. Used by compliance-artifact-scan, security-scan,
and model-registry tasks.

Driven entirely by environment variables so the same code works at every
onboarding stage:
  MR_SERVER          registry REST base
  MR_PORT            registry REST port (default 8080)
  MR_AUTHOR          author recorded on the entry
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


def main():
    from model_registry import ModelRegistry

    server = os.environ["MR_SERVER"].rstrip("/")
    port = int(os.environ.get("MR_PORT", "8080"))
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

    is_secure = server.lower().startswith("https")
    try:
        client = ModelRegistry(
            server_address=server, port=port, author=author, is_secure=is_secure
        )
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
