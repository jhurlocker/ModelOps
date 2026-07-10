# Compliance / Artifact Scan Troubleshooting

## CVE Scan (Trivy)

- The task runs in a non-root pod: `HOME=/tmp` and `TRIVY_CACHE_DIR=/tmp/trivycache`.
- `artifact-scan-image` defaults to `registry.access.redhat.com/ubi9/python-311:latest` for fast demos. Point at the real serving-runtime image for production onboarding (needs a pull secret).
- `artifact-cve-threshold`: `critical` (default) blocks only on fixable CRITICAL CVEs. `high` blocks on HIGH+CRITICAL. `none` never blocks on CVEs.
- `ignore-unfixed` (default `true`): Only counts CVEs with an available fix toward the gate.

## Compliance Policy

- **Required**: Architecture must be in `allowed-architectures` (default: `amd64,x86_64`). Fails if the OCI artifact is unresolvable.
- **Warn-only**: License labels, source labels, provenance (many modelcar images lack these).

## Failure Behavior

On failure, the model is **still registered** (marked FAILED, with S3 report links) before the task exits non-zero. Always has an audit trail.
