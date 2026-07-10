#!/usr/bin/env python3
"""Analyze Garak security scan results and determine pipeline pass/fail."""
import json
import os
import sys


def main():
    if len(sys.argv) < 3:
        print(f"Usage: {__file__} <results_file> <severity_threshold> [timestamp]")
        sys.exit(1)

    results_file = sys.argv[1]
    severity_threshold = sys.argv[2]
    timestamp = sys.argv[3] if len(sys.argv) > 3 else "unknown"

    severity_levels = {"info": 0, "low": 1, "medium": 2, "high": 3, "block": 4}
    threshold_level = severity_levels.get(severity_threshold, 3)

    if not os.path.exists(results_file):
        write_results("Scan failed: Results file not generated. Garak did not produce output.", [], "false", timestamp)
        sys.exit(1)

    with open(results_file, 'r') as f:
        results = json.load(f)

    findings = results.get("probes", results.get("probe_results", []))
    if not isinstance(findings, list):
        findings = [findings] if findings else []

    failures = []
    for finding in findings:
        probe_name = finding.get("probe", finding.get("name", "unknown"))
        status = str(finding.get("status", finding.get("result", "PASS"))).strip().upper()
        severity = str(finding.get("severity", finding.get("impact", "info"))).strip().lower()
        detail = finding.get("detail", finding.get("message", finding.get("log", "")))

        sev_code = severity_levels.get(severity, 0)
        if sev_code >= threshold_level:
            failures.append({
                "probe": probe_name,
                "severity": severity,
                "status": status,
                "detail": str(detail)[:1000]
            })

    if failures:
        summary = [f"SECURITY SCAN BLOCKED: {len(failures)} issue(s) at severity >= '{severity_threshold}'"]
        for i, f in enumerate(failures, 1):
            summary.append(f"  {i}. [{f['severity'].upper()}] {f['probe']}")
            if f.get('detail'):
                summary.append(f"     {f['detail']}")
        passed = "false"
    else:
        summary = [f"SECURITY SCAN PASSED: No issues at threshold '{severity_threshold}'. Model cleared."]
        passed = "true"

    write_results("\n".join(summary), failures, passed, timestamp)

    print("\n--- Security Scan Summary ---")
    print("\n".join(summary))
    print()
    if passed == "true":
        print("RESULT: PASSED - Model cleared for next pipeline step.")
        sys.exit(0)
    else:
        print("RESULT: BLOCKED - Security scan found issues above threshold.")
        print("Review: security-scan-failures.txt for details.")
        sys.exit(1)


def write_results(summary_text, failures, passed, timestamp):
    workspace = "/workspace/shared-workspace"

    with open(os.path.join(workspace, "security-scan-passed.txt"), "w") as f:
        f.write(passed)

    with open(os.path.join(workspace, "security-scan-summary.txt"), "w") as f:
        f.write(summary_text)

    with open(os.path.join(workspace, "security-scan-failures.txt"), "w") as f:
        f.write(json.dumps(failures, indent=2))

    with open(os.path.join(workspace, "security-timestamp.txt"), "w") as f:
        f.write(timestamp)


if __name__ == "__main__":
    main()