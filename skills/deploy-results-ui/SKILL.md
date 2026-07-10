---
name: deploy-results-ui
description: Deploys the Benchmark Results Viewer (Flask + Chart.js) for the ModelOps pipeline. This web app reads GuideLLM and lm-eval benchmark results from S3 and renders performance charts and evaluation tables. Use when setting up the results visualization for model onboarding.
compatibility: Requires oc CLI, OpenShift cluster, and S3 storage already deployed (configure-s3-storage).
---

# Deploy Results UI

Deploys a Flask web application that reads benchmark YAML/JSON from the S3 bucket and renders:
- GuideLLM performance charts (throughput, latency via Chart.js)
- lm-eval quality evaluation tables with qualitative ratings (good/moderate/poor)

## Prerequisites

S3 storage must be deployed with the `benchmark-results` bucket populated (or empty).

## Deploy

```bash
oc apply -n vllm -f model_onboarding_pipeline/results-ui/deployment.yaml
oc wait -n vllm --for=condition=Ready pod -l app=benchmark-viewer --timeout=120s
```

## Access

```bash
BENCHMARK_ROUTE=$(oc get route benchmark-viewer -n vllm -o jsonpath='{.spec.host}')
echo "Benchmark Viewer: https://$BENCHMARK_ROUTE"
```

## Verification

Open the Route URL. After a pipeline run completes (with `upload-guide-llm-results` succeeding), the viewer shows throughput charts and result summaries.

## Gotchas

- The results viewer displays data from S3 that was uploaded by the pipeline's `upload-guidellm-benchmark-results` task. If no pipeline has run yet, the viewer will show an empty state.
- The `upload-lm-eval-results` task is disabled by default (lm-eval evaluations are commented out in the pipeline). To see lm-eval results, re-enable those pipeline tasks.
