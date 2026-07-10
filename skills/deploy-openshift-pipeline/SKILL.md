---
name: deploy-openshift-pipeline
description: Deploys the ModelOps LLM onboarding Tekton pipeline and all its dependencies (namespaces, RBAC, ServiceAccount, PVC, ConfigMaps, Tasks, Pipeline) on an OpenShift cluster. Use when setting up the model onboarding pipeline or when any pipeline component needs to be redeployed.
compatibility: Requires oc CLI, OpenShift cluster with Tekton Pipelines operator, NVIDIA GPU Operator, and Helm v3.
---

# Deploy OpenShift Pipeline

Deploys the ModelOps LLM onboarding pipeline end-to-end:
compliance/artifact scan → GPU advisor → GPU sharing → deploy → security scan → teardown → staging advisor → human approval → staging deploy → benchmark → registry update → (optional) MaaS production deploy.

## Prerequisites

- OpenShift cluster logged in via `oc login`
- Tekton Pipelines operator installed
- NVIDIA GPU Operator deployed (1+ GPU node), or a reachable remote GPU advisor endpoint for GPU-less clusters
- OpenShift AI with **trustyai** component set to `Managed` in the DataScienceCluster (required for EvalHub)
- Other skills run first: `configure-s3-storage`, `configure-evalhub`, `configure-model-registry`, `configure-maas-platform`, `deploy-model-intake-ui`

## Deployment

### 1. Create Namespaces

```bash
oc new-project vllm || echo "vllm exists"
```

### 2. Create Staging Namespace + RBAC

```bash
oc apply -f model_onboarding_pipeline/model-intake-pipeline/pipeline/staging-namespace.yaml
```

### 3. GPU Time-Slicing RBAC

Required so the pipeline SA can patch the NVIDIA ClusterPolicy and manage the time-slicing ConfigMap:

```bash
oc apply -f model_onboarding_pipeline/model-intake-pipeline/pipeline/gpu-sharing-rbac.yaml
```

If your GPU Operator namespace is not `nvidia-gpu-operator`, edit the Role/RoleBinding namespace in that file and set pipeline params `gpu-operator-namespace`/`clusterpolicy-name` accordingly.

### 4. Create ConfigMaps for lm-eval

```bash
oc create configmap mmlu-manifest -n vllm --from-file=model_onboarding_pipeline/model-intake-pipeline/pipeline/mmlu.yaml --dry-run=client -o yaml | oc apply -f -
oc create configmap custom-mmlu -n vllm \
  --from-file=custom-mmlu.yaml=model_onboarding_pipeline/model-intake-pipeline/custom-lm-eval/custom-mmlu.yaml \
  --dry-run=client -o yaml | oc apply -f -
```

### 5. Create Pipeline PVC

```bash
oc apply -n vllm -f model_onboarding_pipeline/model-intake-pipeline/pipeline/pvc.yaml
```

### 6. Create ServiceAccount + RBAC

The pipeline SA needs permissions to deploy Helm charts, create LMEvalJobs, access the model registry, and perform GPU discovery:

```bash
oc create sa pipeline -n vllm --dry-run=client -o yaml | oc apply -f -
oc policy add-role-to-user edit -z pipeline -n vllm
oc adm policy add-cluster-role-to-user cluster-reader -z pipeline -n vllm
oc adm policy add-scc-to-user anyuid -z pipeline -n vllm
```

The `cluster-reader` ClusterRole is required for GPU inventory discovery (needs `nodes` get/list/watch cluster-wide). A namespaced `view` role is NOT sufficient.

### 7. (Optional) GPU Advisor Remote Endpoint Credentials

Only if pointing `advisor-endpoint` at an external agentic skill endpoint:

```bash
oc apply -f model_onboarding_pipeline/model-intake-pipeline/pipeline/gpu-advisor-credentials-secret.yaml
oc create secret generic gpu-advisor-credentials -n vllm \
  --from-literal=api-key='<your-api-key>' \
  --dry-run=client -o yaml | oc apply -f -
```

### 8. Deploy Tekton Tasks and Pipeline

```bash
oc apply -n vllm -f model_onboarding_pipeline/model-intake-pipeline/pipeline/compliance-artifact-scan-task.yaml
oc apply -n vllm -f model_onboarding_pipeline/model-intake-pipeline/pipeline/gpu-advisor-task.yaml
oc apply -n vllm -f model_onboarding_pipeline/model-intake-pipeline/pipeline/approval-gate-task.yaml
oc apply -n vllm -f model_onboarding_pipeline/model-intake-pipeline/pipeline/apply-gpu-sharing-task.yaml
oc apply -n vllm -f model_onboarding_pipeline/model-intake-pipeline/pipeline/deploy-model-task.yaml
oc apply -n vllm -f model_onboarding_pipeline/model-intake-pipeline/pipeline/security-scan-task.yaml
oc apply -n vllm -f model_onboarding_pipeline/model-intake-pipeline/pipeline/teardown-model-task.yaml
oc apply -n vllm -f model_onboarding_pipeline/model-intake-pipeline/pipeline/grant-model-access-task.yaml
oc apply -n vllm -f model_onboarding_pipeline/model-intake-pipeline/pipeline/guidellm-benchmark-task.yaml
oc apply -n vllm -f model_onboarding_pipeline/model-intake-pipeline/pipeline/upload-guidellm-results-task.yaml
oc apply -n vllm -f model_onboarding_pipeline/model-intake-pipeline/pipeline/model-registry-task.yaml
oc apply -n vllm -f model_onboarding_pipeline/model-intake-pipeline/pipeline/deploy-maas-task.yaml
oc apply -n vllm -f model_onboarding_pipeline/model-intake-pipeline/pipeline/model-intake-pipeline.yaml
```

Verify:

```bash
oc get tasks -n vllm
oc get pipeline -n vllm
```

### 9. Trigger Pipeline Run

Preferred: use the model-intake web app's "Submit Model" form.

Manual alternative:

```bash
oc apply -n vllm -f model_onboarding_pipeline/model-intake-pipeline/pipeline/model-intake-pipelinerun.yaml
```

Monitor:

```bash
oc get pipelinerun -n vllm -w
```

To enable the MaaS production deployment, set `deploy-maas: 'true'` in the PipelineRun params.

## Post-Deployment Verification

Check all TaskRuns succeed in order:

```bash
oc get taskrun -n vllm --sort-by=.metadata.creationTimestamp
```

Expected order: `compliance-artifact-scan`, `gpu-advisor-sandbox`, `apply-gpu-sharing-sandbox`, `deploy-model`, `security-scan`, `teardown-model`, `gpu-advisor-staging`, `wait-for-approval`, `apply-gpu-sharing-staging`, `deploy-model-staging`, `benchmark`, `upload-guide-llm-results`, `register-model-and-results`, `deploy-maas` (if enabled).

Inspect model registry:

```bash
oc run -n vllm mr-show --image=registry.access.redhat.com/ubi9/ubi-minimal --rm -i --restart=Never -- \
  curl -s http://modelops-registry.rhoai-model-registries.svc.cluster.local:8080/api/model_registry/v1alpha3/registered_models
```

## Gotchas

- **GPU Advisor BLOCKED**: Expected when the cluster lacks enough free GPU memory. Options: add GPUs, lower `context-length`/`concurrency`, enable `allow-time-slicing`, or use a remote advisor endpoint.
- **GPU Advisor discovers no GPUs**: The `pipeline` SA needs cluster-scoped `nodes` read access (the `cluster-reader` ClusterRole in step 6).
- **Remote GPU advisor fails**: Falls back to local heuristic. Check TaskRun logs for `ERROR calling advisor endpoint:`. Increase `advisor-timeout-seconds` if the agent needs more reasoning time.
- **Compliance/artifact scan fails with Trivy errors**: The task sets `HOME=/tmp` + `TRIVY_CACHE_DIR=/tmp/trivycache`. If scanning a real serving-runtime image (e.g. `registry.redhat.io/...`), ensure a pull secret exists on the node.
- **wait-for-approval stuck**: Confirm `approval-api-url` points to the in-cluster Service DNS (`http://model-intake.<ns>.svc.cluster.local:8080`), NOT the public Route.
- **Time-slicing persists cluster-wide** after the pipeline. To revert: see `references/gpu-sharing.md`.
- **lm-eval tasks are disabled by default** (commented out in the Pipeline). Re-enable by uncommenting in `model-intake-pipeline.yaml`.

## References

- [GPU sharing details and rollback](references/gpu-sharing.md)
- [Compliance/artifact scan troubleshooting](references/troubleshooting-compliance.md)
- [GPU advisor troubleshooting](references/troubleshooting-gpu-advisor.md)
