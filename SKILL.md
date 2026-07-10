---
name: modelops-tutorial
description: Deploys and validates the ModelOps LLM onboarding pipeline on OpenShift. Use when setting up the full pipeline, deploying any pipeline component, or troubleshooting pipeline execution. Covers compliance scanning, GPU advisor, human approval, garak security scans, performance benchmarking, Model Registry registration, and MaaS production deployment. Sub-skills handle individual components.
license: Proprietary
metadata:
  version: "2.0"
---

# ModelOps Tutorial Pipeline Deployment

You are responsible for deploying and validating the ModelOps end-to-end LLM onboarding pipeline on an OpenShift cluster. The pipeline simulates onboarding an LLM: governance scans in a sandbox → Model Registry registration → human approval → promotion to staging → benchmarking → (optional) MaaS production deployment.

The pipeline runs on a single cluster with two namespaces simulating separate environments:
- `vllm` — sandbox for automated governance scans
- `vllm-staging` — staging for human-gated promotion + benchmarking

**Pipeline flow**: compliance/artifact scan → GPU advisor (sandbox) → GPU sharing → deploy → security scan → teardown → GPU advisor (staging) → human approval → GPU sharing (staging) → staging deploy → grant access → benchmark → register model → (optional) MaaS deploy.

## Sub-Skills

Each component is a separate skill loaded on demand. Start with `deploy-openshift-pipeline` which orchestrates deployment of all Tekton tasks. Load other skills as their components are needed.

| Skill | File | When to Use |
|-------|------|-------------|
| `deploy-openshift-pipeline` | `skills/deploy-openshift-pipeline/SKILL.md` | **Start here.** Deploying the full pipeline: namespaces, RBAC, PVC, SA, all Tekton tasks/pipeline, triggering runs. Also: troubleshooting GPU advisor, GPU sharing, compliance scans. |
| `configure-s3-storage` | `skills/configure-s3-storage/SKILL.md` | Deploying MinIO S3 storage and creating buckets for pipeline scan reports and benchmark results. |
| `configure-evalhub` | `skills/configure-evalhub/SKILL.md` | Deploying EvalHub (TrustyAI) for garak security scans and GuideLLM benchmarks. Includes smoke tests. Also: garak troubleshooting. |
| `configure-model-registry` | `skills/configure-model-registry/SKILL.md` | Deploying the OpenShift AI Model Registry (MySQL backend + registry instance). Also: registry connectivity troubleshooting. |
| `configure-maas-platform` | `skills/configure-maas-platform/SKILL.md` | Full MaaS platform setup: Connectivity Link, Authorino TLS, PostgreSQL, monitoring, Gateway, DataScienceCluster enablement, namespaces, RBAC, routing. Also: DNS hijacking, MaaS troubleshooting. |
| `deploy-model-intake-ui` | `skills/deploy-model-intake-ui/SKILL.md` | Building and deploying the model intake web app for form-based pipeline run submission and human approval. Also: approval workflow troubleshooting. |
| `deploy-results-ui` | `skills/deploy-results-ui/SKILL.md` | Deploying the benchmark results viewer (GuideLLM charts + lm-eval tables). |

## Deployment Order

Follow this order for a fresh cluster. Each phase is a separate decision point — load the corresponding skill when the previous phase completes.

- [ ] Phase 1: Deploy S3 storage → load `configure-s3-storage`
- [ ] Phase 2: Deploy EvalHub → load `configure-evalhub`
- [ ] Phase 3: Deploy Model Registry → load `configure-model-registry`
- [ ] Phase 4: Deploy MaaS platform → load `configure-maas-platform`
- [ ] Phase 5: Deploy results viewer → load `deploy-results-ui`
- [ ] Phase 6: Deploy model intake UI → load `deploy-model-intake-ui`
- [ ] Phase 7: Deploy pipeline → load `deploy-openshift-pipeline`
- [ ] Phase 8: Trigger pipeline run → covered in `deploy-openshift-pipeline`

## Required Inputs

Key pipeline parameters (set via the model intake UI form or PipelineRun):

- `model-id`: HuggingFace model ID (default: `ibm-granite/granite-3.3-2b-instruct`)
- `modelcar-image`: Optional explicit modelcar OCI image ref
- `model-name`: Kubernetes-safe deployment identifier (becomes Helm release name + endpoint hostname)
- `model-version`: Registry version label (e.g. `v1`)
- `authorized-viewers`: Comma-separated users/groups for RHOAI dashboard visibility. Prefix with `group:` for OpenShift Groups.
- `maas-authorized-group`: OpenShift Group authorized to access the production MaaS deployment. Default `system:authenticated`. Set to a specific group like `ml-team` to restrict access.
- `approval-api-url`: In-cluster URL of the model intake app for human approval. Empty = auto-approve (dev only).
- `deploy-maas`: Set to `"true"` to include MaaS production deployment as the final phase.

## Prerequisites

1. OpenShift cluster logged in via `oc login`
2. Tekton Pipelines operator installed
3. NVIDIA GPU Operator deployed (1+ GPU node), or a reachable remote GPU advisor endpoint
4. OpenShift AI with **trustyai** component `Managed` and **modelregistry** component `Managed` in the DataScienceCluster
5. Helm v3 available
6. Container build/push capability for the model intake UI

## Gotchas

- **MaaS Gateway can hijack `*.apps` DNS**: The `maas-default-gateway` creates a wildcard DNS record that overwrites Route53. See `configure-maas-platform` → references for recovery. Always omit `hostname` in the Gateway listener.
- **Time-slicing is cluster-wide and persistent**: After the pipeline, the GPU time-slicing config remains. Revert with the steps in `deploy-openshift-pipeline` → references.
- **approval-api-url must use in-cluster DNS**: The `wait-for-approval` Task runs as a pod. Use `http://model-intake.vllm.svc.cluster.local:8080`, not the public Route.
- **lm-eval tasks are disabled**: Scientific quality evaluation is left to AI engineers after onboarding.
- **`deploy-maas` requires MaaS platform**: The optional Phase 3 only runs when `deploy-maas: 'true'` AND the full platform is set up.

## Safety Rules

- Do not delete PVCs containing benchmark data without confirmation.
- Do not change model registry MySQL credentials without updating all dependent resources.
- Do not leave `approval-api-url` empty in shared/production clusters.
- Do not deploy a model whose GPU advisor plan-status is `BLOCKED`.
- Treat the model-intake app's SQLite data PVC as the source of truth for approval history.
