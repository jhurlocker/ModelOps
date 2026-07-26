"""
Compliance scanner evaluator - extracted from the compliance-artifact-scan Tekton task.
Evaluates compliance policy checks AND artifact CVE scan results against thresholds,
uploads reports to S3, and updates the Model Registry.

Environment variables:
  WORKSPACE_PATH     path to shared workspace
  MODEL_ID           Hugging Face model ID
  MODEL_NAME         registered model name
  MODEL_VERSION      version label
  ARTIFACT_SCAN_IMAGE image scanned by Trivy
  CVE_THRESHOLD      critical, high, or none
  ALLOWED_ARCHES     comma-separated allowed CPU architectures
  MR_SERVER, MR_PORT, MR_AUTHOR  Model Registry connection
  S3_ENDPOINT_URL, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY, S3_BUCKET, S3_UI_ROUTE
  TIMESTAMP           run timestamp for S3 prefix
"""

import glob
import json
import os
import sys


def load_json(path, default=None):
    try:
        with open(path) as f:
            return json.load(f)
    except Exception:
        return default if default is not None else {}


def evaluate():
    ws = os.environ["WORKSPACE_PATH"]
    sandbox = os.path.join(ws, "compliance-artifact-sandbox")
    allowed_arches = {a.strip().lower() for a in os.environ.get("ALLOWED_ARCHES", "").split(",") if a.strip()}
    cve_threshold = os.environ.get("CVE_THRESHOLD", "critical").strip().lower()

    inspect = load_json(os.path.join(sandbox, "modelcar-inspect.json"), {})
    trivy = load_json(os.path.join(sandbox, "trivy-report.json"), {"Results": []})
    try:
        with open(os.path.join(sandbox, "modelcar-ref.txt")) as f:
            modelcar_ref = f.read().strip()
    except Exception:
        modelcar_ref = ""

    # Compliance checks
    checks = []
    labels = inspect.get("Labels") or {}
    arch = str(inspect.get("Architecture", "")).lower()
    os_name = str(inspect.get("Os", "")).lower()

    artifact_resolved = bool(modelcar_ref) and bool(inspect)
    checks.append(("artifact-resolvable", artifact_resolved, True,
                   modelcar_ref or "no modelcar OCI artifact found for model-id"))
    checks.append(("architecture-allowed",
                   (arch in allowed_arches) if arch else False, True,
                   f"architecture={arch or '?'} allowed={sorted(allowed_arches)}"))
    checks.append(("os-linux", (os_name == "linux") if os_name else False, False,
                   f"os={os_name or '?'}"))
    has_created = bool(inspect.get("Created"))
    checks.append(("provenance-created-date", has_created, False,
                   f"created={inspect.get('Created', '?')}"))
    license_val = labels.get("license") or labels.get("org.opencontainers.image.licenses")
    checks.append(("license-label-present", bool(license_val), False,
                   f"license={license_val or '<none>'}"))
    source_val = labels.get("org.opencontainers.image.source") or labels.get("io.openshift.build.source-location")
    checks.append(("source-provenance-label", bool(source_val), False,
                   f"source={source_val or '<none>'}"))

    compliance_required_failed = [c for c in checks if c[2] and not c[1]]
    compliance_passed = len(compliance_required_failed) == 0

    # CVE gate
    sev_counts = {"LOW": 0, "MEDIUM": 0, "HIGH": 0, "CRITICAL": 0}
    for r in trivy.get("Results", []) or []:
        for v in (r.get("Vulnerabilities") or []):
            s = v.get("Severity", "UNKNOWN").upper()
            if s in sev_counts:
                sev_counts[s] += 1
    scan_errored = bool(trivy.get("_scan_error"))

    if scan_errored:
        artifact_passed = False
        cve_reason = "trivy scan could not run (image pull/auth/db error)"
    elif cve_threshold == "none":
        artifact_passed = True
        cve_reason = "CVE gate disabled (threshold=none)"
    elif cve_threshold == "high":
        artifact_passed = (sev_counts["HIGH"] + sev_counts["CRITICAL"]) == 0
        cve_reason = f"HIGH={sev_counts['HIGH']} CRITICAL={sev_counts['CRITICAL']} (gate=high)"
    else:  # critical
        artifact_passed = sev_counts["CRITICAL"] == 0
        cve_reason = f"CRITICAL={sev_counts['CRITICAL']} (gate=critical)"

    overall_passed = compliance_passed and artifact_passed

    # Summary
    lines = []
    verdict = "PASSED" if overall_passed else "FAILED"
    lines.append(f"COMPLIANCE & ARTIFACT SCAN: {verdict}")
    lines.append(f"Model: {os.environ.get('MODEL_NAME')}  artifact: {modelcar_ref or '<unresolved>'}")
    lines.append("")
    lines.append("Compliance policy checks:")
    for name, ok, required, detail in checks:
        tag = "PASS" if ok else ("FAIL" if required else "WARN")
        req = " (required)" if required else ""
        lines.append(f"  [{tag}] {name}{req} - {detail}")
    lines.append(f"Compliance verdict: {'PASSED' if compliance_passed else 'FAILED'}")
    lines.append("")
    lines.append(f"Artifact security scan (Trivy) image: {os.environ.get('ARTIFACT_SCAN_IMAGE')}")
    lines.append(f"  CVE counts: CRITICAL={sev_counts['CRITICAL']} HIGH={sev_counts['HIGH']} MEDIUM={sev_counts['MEDIUM']} LOW={sev_counts['LOW']}")
    lines.append(f"  Gate: {cve_reason}")
    lines.append(f"Artifact verdict: {'PASSED' if artifact_passed else 'FAILED'}")

    summary = "\n".join(lines)
    with open(os.path.join(ws, "compliance-artifact-summary.txt"), "w") as f:
        f.write(summary + "\n")
    with open(os.path.join(ws, "compliance-artifact-passed.txt"), "w") as f:
        f.write("true" if overall_passed else "false")

    machine = {
        "timestamp": os.environ.get("TIMESTAMP"),
        "model_name": os.environ.get("MODEL_NAME"),
        "model_id": os.environ.get("MODEL_ID"),
        "modelcar_ref": modelcar_ref,
        "compliance_passed": compliance_passed,
        "compliance_checks": [
            {"name": n, "ok": ok, "required": req, "detail": d} for (n, ok, req, d) in checks
        ],
        "artifact_scan_image": os.environ.get("ARTIFACT_SCAN_IMAGE"),
        "artifact_passed": artifact_passed,
        "cve_counts": sev_counts,
        "cve_gate": cve_reason,
        "overall_passed": overall_passed,
    }
    with open(os.path.join(sandbox, "compliance-artifact-summary.json"), "w") as f:
        json.dump(machine, f, indent=2)

    print(summary)

    # Return env vars for downstream steps
    return {
        "COMPLIANCE_STATUS": "PASSED" if compliance_passed else "FAILED",
        "ARTIFACT_STATUS": "PASSED" if artifact_passed else "FAILED",
        "OVERALL_PASSED": "true" if overall_passed else "false",
        "CVE_CRITICAL": str(sev_counts["CRITICAL"]),
        "CVE_HIGH": str(sev_counts["HIGH"]),
        "MODELCAR_REF": modelcar_ref,
    }


def upload_to_s3():
    import boto3
    from botocore.client import Config

    ws = os.environ["WORKSPACE_PATH"]
    sandbox = os.path.join(ws, "compliance-artifact-sandbox")
    bucket = os.environ["S3_BUCKET"]
    prefix = os.environ["S3_PREFIX"]

    try:
        s3 = boto3.client(
            "s3", endpoint_url=os.environ["S3_ENDPOINT_URL"],
            aws_access_key_id=os.environ["S3_ACCESS_KEY_ID"],
            aws_secret_access_key=os.environ["S3_SECRET_ACCESS_KEY"],
            config=Config(s3={"addressing_style": "path"}),
        )
        existing = [b["Name"] for b in s3.list_buckets().get("Buckets", [])]
        if bucket not in existing:
            s3.create_bucket(Bucket=bucket)
            print(f"Created bucket '{bucket}'.")
    except Exception as e:
        print(f"WARN: could not init S3/bucket, skipping upload: {e}")
        return

    files = [p for p in glob.glob(os.path.join(sandbox, "*")) if os.path.isfile(p)]
    p = os.path.join(ws, "compliance-artifact-summary.txt")
    if os.path.isfile(p):
        files.append(p)
    for path in files:
        key = f"{prefix}/{os.path.basename(path)}"
        try:
            s3.upload_file(path, bucket, key)
            print(f"  uploaded s3://{bucket}/{key}")
        except Exception as e:
            print(f"  WARN: failed to upload {path}: {e}")


def main():
    cmd = sys.argv[1] if len(sys.argv) > 1 else "evaluate"

    if cmd == "evaluate":
        env_vars = evaluate()
        for k, v in env_vars.items():
            print(f"EXPORT {k}={v}")
        # Write env file for shell sourcing
        with open("/tmp/scan_env.sh", "w") as f:
            for k, v in env_vars.items():
                f.write(f'{k}={v}\n')
    elif cmd == "upload":
        upload_to_s3()
    else:
        print(f"Unknown command: {cmd}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
