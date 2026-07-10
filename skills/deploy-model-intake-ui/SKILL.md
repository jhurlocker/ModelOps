---
name: deploy-model-intake-ui
description: Deploys the Model Intake web application (Flask + SQLite). This app lets users submit model onboarding requests via a form and approve/reject GPU advisor deployment plans through a browser. Use when setting up the human approval workflow for the ModelOps pipeline.
compatibility: Requires oc CLI and an OpenShift cluster. The prebuilt image is pulled from quay.io/jhurlocker/model-intake-ui.
---

# Deploy Model Intake UI

A Flask web app backed by SQLite (PVC-persisted) that provides:
- A model submission form (creates PipelineRuns via the Kubernetes API)
- A deployment plan approval queue (human review of GPU advisor recommendations)
- Pipeline run status tracking
- JSON API used by the `wait-for-approval` Tekton Task

## Deploy

The deployment YAML references `quay.io/jhurlocker/model-intake-ui:latest` — no build step needed.

```bash
oc apply -n vllm -f model_onboarding_pipeline/model-intake-ui/deployment.yaml
oc wait -n vllm --for=condition=Ready pod -l app=model-intake --timeout=120s
```

## Access

```bash
oc get route model-intake -n vllm -o jsonpath='{.spec.host}'
```

## Pipeline Integration

The pipeline's `approval-api-url` param should point to the **in-cluster Service DNS name** (not the public Route) so the `wait-for-approval` Task pod can reach it:

`http://model-intake.vllm.svc.cluster.local:8080`

This is set as the default in the pipeline params and in the app's ConfigMap.

## Verification

1. Open the app's Route URL in a browser
2. The "Submit Model" form should appear with all pipeline parameters pre-filled
3. After submitting, the run should appear on the "Pipeline Runs" page
4. When `gpu-advisor-staging` runs, the plan should appear on the "Deployment Plans" page with Approve/Reject buttons

## Gotchas

- **App can't create PipelineRuns**: Check the `model-intake` ServiceAccount's RBAC (Role `model-intake-tekton`) grants `create`/`get`/`list`/`watch` on `pipelineruns.tekton.dev`. Check logs: `oc logs -n vllm deployment/model-intake`.
- **approval-api-url must be in-cluster DNS**: The Task runs as a pod, not in your browser. Use `http://model-intake.<ns>.svc.cluster.local:8080`, not the public Route.
- **SQLite is single-replica**: The app uses a PVC-backed SQLite file. Do not scale beyond 1 replica.
- **Auto-approval**: If `approval-api-url` is left empty, the pipeline skips the human gate entirely. This is for dev/test only — never use in shared/production clusters.
- **Plan stuck in pending**: A human must click Approve/Reject at `/plans/<plan_id>`. Nothing approves automatically. If `approval-timeout-seconds` expires, the pipeline fails.

## References

- [Approval workflow troubleshooting](references/troubleshooting-approval.md)
