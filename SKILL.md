---
name: modelops-tutorial
description: Deploys and validates the Enterprise AI Lifecycle Platform on OpenShift. Use when setting up the full pipeline, deploying any pipeline component, or troubleshooting pipeline execution. Covers the ModelOps Operator (controller), compliance scanning, GPU advisor, human approval, garak security scans, performance benchmarking, Model Registry registration, and MaaS production deployment. Sub-skills handle individual components.
license: Proprietary
metadata:
  version: "3.0"
---
# ModelOps Tutorial Pipeline Deployment

You are responsible for deploying and validating the ModelOps end-to-end LLM onboarding pipeline on an OpenShift cluster.

**Architecture (v3.0)**:
```
UI (model-intake-ui) → creates ModelRequest CR
                          ↓
ModelRequest controller (operator) → resolves ModelLifecycleProfile + PlatformConfig
                          ↓
                   creates CapacityPlan (GPU advisor runs as child resource)
                          ↓
                   creates Tekton PipelineRun (with profile-resolved params + secret values)
                          ↓
                   watches PipelineRun → syncs status back to ModelRequest
```

The pipeline runs on a single cluster with two namespaces simulating separate environments:
- `vllm` — sandbox for automated governance scans
- `vllm-staging` — staging for human-gated promotion + benchmarking

**Pipeline flow**: compliance/artifact scan → GPU advisor (sandbox) → GPU sharing → deploy → security scan → teardown → GPU advisor (staging) → human approval → GPU sharing (staging) → staging deploy → grant access → benchmark → register model → (optional) MaaS deploy.

## Sub-Skills

| Skill | File | When to Use |
|-------|------|-------------|
| `deploy-openshift-pipeline` | `skills/deploy-openshift-pipeline/SKILL.md` | Deploying the full pipeline: namespaces, RBAC, PVC, SA, all Tekton tasks/pipeline, triggering runs. Also: troubleshooting GPU advisor, GPU sharing, compliance scans. |
| `configure-s3-storage` | `skills/configure-s3-storage/SKILL.md` | Deploying MinIO S3 storage and creating buckets for pipeline scan reports and benchmark results. |
| `configure-evalhub` | `skills/configure-evalhub/SKILL.md` | Deploying EvalHub (TrustyAI) for garak security scans and GuideLLM benchmarks. Includes smoke tests. Also: garak troubleshooting. |
| `configure-model-registry` | `skills/configure-model-registry/SKILL.md` | Deploying the OpenShift AI Model Registry (MySQL backend + registry instance). Also: registry connectivity troubleshooting. |
| `configure-maas-platform` | `skills/configure-maas-platform/SKILL.md` | Full MaaS platform setup: Connectivity Link, Authorino TLS, PostgreSQL, monitoring, Gateway, DataScienceCluster enablement, namespaces, RBAC, routing. Also: DNS hijacking, MaaS troubleshooting. |
| `deploy-model-intake-ui` | `skills/deploy-model-intake-ui/SKILL.md` | Building and deploying the model intake web app for form-based ModelRequest submission and human approval. Also: approval workflow troubleshooting. |
| `deploy-results-ui` | `skills/deploy-results-ui/SKILL.md` | Deploying the benchmark results viewer (GuideLLM charts + lm-eval tables). |

## Deployment Order

Follow this order for a fresh cluster. Each phase is a separate decision point — load the corresponding skill when the previous phase completes.

- [ ] Phase 0: Deploy the ModelOps Operator → **NEW in v3.0** (see below)
- [ ] Phase 1: Deploy S3 storage → load `configure-s3-storage`
- [ ] Phase 2: Deploy EvalHub → load `configure-evalhub`
- [ ] Phase 3: Deploy Model Registry → load `configure-model-registry`
- [ ] Phase 4: Deploy MaaS platform → load `configure-maas-platform`
- [ ] Phase 5: Deploy results viewer → load `deploy-results-ui`
- [ ] Phase 6: Deploy model intake UI → load `deploy-model-intake-ui`
- [ ] Phase 7: Deploy pipeline → load `deploy-openshift-pipeline`
- [ ] Phase 8: Create credential Secrets and platform config → see Phase 0
- [ ] Phase 9: Submit a model via the UI → creates a ModelRequest CR; controller handles the rest

## Phase 0: Deploy the ModelOps Operator

The operator is the central controller for the new architecture. It watches ModelRequest CRs and orchestrates the full lifecycle.

### 0.1 Build and push the operator image

```bash
cd operator
podman build -t quay.io/jhurlocker/modelops-operator:v0.1.0 .
podman push quay.io/jhurlocker/modelops-operator:v0.1.0
```

### 0.2 Apply CRDs

```bash
oc apply -f operator/config/crd/bases/modelops.example.io_modelrequests.yaml
oc apply -f operator/config/crd/bases/modelops.example.io_modellifecycleprofiles.yaml
oc apply -f operator/config/crd/bases/modelops.example.io_platformconfigs.yaml
oc apply -f operator/config/crd/bases/modelops.example.io_capacityplans.yaml
```

### 0.3 Deploy the operator

```bash
oc apply -f operator/config/rbac/role.yaml
oc apply -f operator/config/default/deployment.yaml
```

### 0.4 Create PlatformConfig (shared platform plumbing)

```bash
oc apply -f operator/config/samples/platformconfig-sample.yaml
```

### 0.5 Create ModelLifecycleProfile (binds pipeline + platform config)

```bash
oc apply -f operator/config/samples/lifecycleprofile-sample.yaml
```

### 0.6 Create credential Secrets

```bash
# EvalHub credentials (required by security scan task)
oc create secret generic evalhub-credentials \
  --from-literal=token=$(oc whoami -t) \
  --from-literal=url=evalhub-redhat-ods-applications.apps.REPLACE_WITH_CLUSTER_DOMAIN

# S3 credentials for scan results
oc create secret generic scan-s3-credentials \
  --from-literal=endpoint=http://minio-service.s3-storage.svc.cluster.local:9000 \
  --from-literal=accessKeyId=minio \
  --from-literal=secretAccessKey=minio123

# S3 credentials for benchmark/eval result uploads
oc create secret generic result-s3-credentials \
  --from-literal=endpoint=http://s3.openshift-storage.svc.cluster.local:80 \
  --from-literal=accessKeyId=YOUR_ACCESS_KEY \
  --from-literal=secretAccessKey=YOUR_SECRET_KEY

# Optional: HuggingFace token for gated models
oc create secret generic huggingface-credentials \
  --from-literal=token=YOUR_HF_TOKEN
```

### 0.7 Verify the operator is running

```bash
oc -n modelops get pods
oc -n modelops logs deployment/modelops-operator
```

## Submitting a Model (post-deployment)

With the operator running and UI deployed, submit a model through the UI form or directly via `kubectl`:

```bash
# Using the UI: browse to the model-intake route, fill the form, click Submit
# Or directly:
oc apply -f model_onboarding_pipeline/model-intake-pipeline/pipeline/sample-modelrequest.yaml
```

The operator will:
1. Create a CapacityPlan → GPU advisor runs, plan status set to Succeeded
2. Create a Tekton PipelineRun with all params resolved from the profile + secrets
3. Watch the PipelineRun and sync status back to the ModelRequest

Check status:
```bash
oc -n vllm get modelrequests
oc -n vllm get capacityplans
oc -n vllm get pipelineruns
```

## Architecture: What Changed in v3.0

| Aspect | v2.0 (old) | v3.0 (new) |
|--------|-----------|-----------|
| Entry point | UI directly creates Tekton PipelineRun | UI creates ModelRequest CR; controller creates PipelineRun |
| API surface | 80+ fields of platform plumbing exposed to users | Slim: model identity + requirements + profile ref |
| Credentials | Plaintext tokens in CRs and YAML defaults | K8s Secrets referenced by name |
| Pipeline selection | `pipelineRef` field | Resolved from `ModelLifecycleProfile` |
| Platform defaults | Hardcoded in controller and UI | `PlatformConfig` CR (admin-managed, one-time apply) |
| GPU capacity | Implicit pipeline task | `CapacityPlan` child CR with heuristic controller |
| Image tags | `:latest` (mutable) | `:v0.1.0` (pinned) |

## Required Inputs

Key form fields (set via the model intake UI or direct ModelRequest CR):

- `model-id`: HuggingFace model ID (default: `ibm-granite/granite-3.3-2b-instruct`)
- `model-name`: Kubernetes-safe deployment identifier (becomes Helm release name + endpoint hostname)
- `model-version`: Registry version label (e.g. `v1`)
- `lifecycle-profile`: ModelLifecycleProfile name (default: `standard-generative-onboarding`)
- `evalhub-secret-name`: K8s Secret with EvalHub credentials
- `scan-s3-secret-name`: K8s Secret with S3 credentials for scan results
- `result-s3-secret-name`: K8s Secret with S3 credentials for benchmark/eval uploads
- `authorized-viewers`: Comma-separated users/groups for RHOAI dashboard visibility
- `deploy-maas`: Set to `"true"` to include MaaS production deployment as the final phase

## Prerequisites

1. OpenShift cluster logged in via `oc login`
2. Tekton Pipelines operator installed
3. NVIDIA GPU Operator deployed (1+ GPU node), or a reachable remote GPU advisor endpoint
4. OpenShift AI with **trustyai** component `Managed` and **modelregistry** component `Managed` in the DataScienceCluster
5. Helm v3 available
6. Container build/push capability (podman or docker)
7. `operator/` built and pushed to `quay.io/jhurlocker/modelops-operator:v0.1.0`

## Gotchas

- **Operator image must be pushed before deployment**: The Deployment manifest references `quay.io/jhurlocker/modelops-operator:v0.1.0`. Build and push it first (Phase 0.1).
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
- Never store credentials in CRDs — always use K8s Secrets referenced by name.
