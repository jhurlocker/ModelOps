---
name: deploy-results-ui
description: Deploys the ModelOps Results Viewer (Flask) for Garak security scans and GuideLLM / lm-eval benchmarks. The app lists result files from MinIO and renders summaries. Use when setting up result visualization for model onboarding.
compatibility: Requires oc CLI, OpenShift cluster, and S3 storage already deployed (configure-s3-storage).
---

# Deploy Results UI

Deploys a Flask web application that reads pipeline artifacts from MinIO and renders:

- A landing page listing Garak and GuideLLM result files (no `?file=` required)
- Garak security-scan summaries (`security-scan-results`)
- GuideLLM benchmark summaries (`benchmark-results`)
- lm-eval quality tables when those files are present

## Prerequisites

S3 storage must be deployed. The viewer reads `benchmark-results` and `security-scan-results`.

## Deploy

Build from this repo (same pattern as the Model Intake UI), then apply:

```bash
oc new-build --binary --strategy=docker --name=benchmark-viewer -n vllm --to=benchmark-viewer:latest
oc start-build benchmark-viewer -n vllm --from-dir=model_onboarding_pipeline/results-ui --follow
oc apply -n vllm -f model_onboarding_pipeline/results-ui/deployment.yaml
oc wait -n vllm --for=condition=Ready pod -l app=benchmark-viewer --timeout=180s
```

`./deploy-all.sh` does this in phase 5.

## Access

```bash
BENCHMARK_ROUTE=$(oc get route benchmark-viewer -n vllm -o jsonpath='{.spec.host}')
echo "Results Viewer: https://$BENCHMARK_ROUTE"
```

Open the Route URL. You should see cards for Garak scans and GuideLLM benchmarks. Click **Open** on a card to view metrics.

Direct links (also written onto the Model Registry entry) look like:

```
https://$BENCHMARK_ROUTE/?file=20260909_023642_granite-2b-results.yaml&bucket=benchmark-results
https://$BENCHMARK_ROUTE/?file=20260909_023202_security_scan/scan_results.summary.json&bucket=security-scan-results
```

## Verification

After a pipeline run completes (`security-scan` and `upload-guide-llm-results`):

1. The home page lists both result types.
2. A GuideLLM card shows tokens/sec, TTFT, and ITL.
3. A Garak card shows pass/fail, overall attack-success rate, per-profile ASR (`quality`, `avid_security`, `cwe`), and per-probe rows.

## Gotchas

- The previous quay.io image only understood legacy GuideLLM YAML (`benchmarks[].metrics.*.total`). The pipeline now writes a flat EvalHub summary; the in-repo image handles that format.
- Garak reports live in `security-scan-results`, not `benchmark-results`. The viewer secret must include `S3_SECURITY_BUCKET`.
- The `quick` Garak profile is a single `dan.Dan_11_0` smoke probe and often stores no metrics. Rebuild after switching the pipeline to taxonomy profiles (`quality,avid_security,cwe`).
- lm-eval upload is disabled by default. Re-enable those pipeline tasks to see lm-eval tables.
