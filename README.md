# Enterprise AI Lifecycle Platform

An open, Kubernetes-native platform for governing models and AI applications from intake through retirement.

## Vision

Traditional MLOps treats model deployment as a one-off event. This platform treats AI workloads as managed lifecycle products — with governed intake, auditable evidence chains, capacity planning, security assessment, human approval gates, and promotion between environments.

## Architecture

```
Enterprise AI Lifecycle Platform
├── model-intake (implemented)
├── catalog
├── application-development
├── application-promotion
├── production-operations
├── optimization
├── fine-tuning
├── governance
└── retirement
```

The platform follows a Kubernetes controller pattern:

**User declares intent** → **Operator reconciles lifecycle** → **Tekton executes governed pipeline**

Key architectural principles:
- **Declarative API** — ModelRequest CRD captures the *what*, not the *how*
- **Profile-driven configuration** — ModelLifecycleProfile + PlatformConfig separate user requirements from platform plumbing
- **Credential isolation** — Credentials are referenced via K8s Secrets, never stored in CRs
- **Durable lifecycle resources** — Each major stage (capacity plan, assessment, approval) becomes its own CRD with independent status and audit trail
- **Immutable tool images** — Python tooling is containerized with semantic version tags for reproducible, auditable execution
- **Evidence-oriented** — Every scan, benchmark, approval, and deployment decision produces artifacts retained for governance

## Module 1: Governed Model Intake (implemented)

The first implemented lifecycle module. Governs the process of bringing a foundation model into the platform:

1. **Request** — UI creates a ModelRequest CR
2. **Profile resolution** — Operator resolves lifecycle profile and platform configuration
3. **Capacity planning** — GPU advisor runs as a child resource (CapacityPlan CR)
4. **Governed pipeline** — Tekton PipelineRun executes with pinned images
5. **Status reflection** — Pipeline success/failure flows back to ModelRequest status

### Pipeline phases

| Phase | Stage | Description |
|---|---|---|
| 1 | Compliance & artifact scan | Skopeo + Trivy scanning, OCI metadata inspection, policy evaluation |
| 1 | GPU assessment | Capacity recommendation with time-slicing/MIG analysis |
| 1 | Sandbox deploy | Helm deployment to sandbox namespace |
| 1 | Security scan | Live LLM red-team scan via EvalHub (garak) |
| 2 | Approval gate | Human approval with timeout |
| 2 | Promote to staging | Teardown sandbox, re-deploy to staging |
| 2 | Benchmark | GuideLLM benchmarking via EvalHub |
| 2 | Register | Model Registry publication with evidence links |
| 3 | MaaS deploy (optional) | Production deployment via MaaS CRDs |

### Resource types

| CRD | Purpose |
|---|---|
| `ModelRequest` | User-facing lifecycle intent |
| `ModelLifecycleProfile` | Binds workflow engine, pipeline, policy, and platform config |
| `PlatformConfig` | Shared platform plumbing (S3 buckets, registry, defaults) |
| `CapacityPlan` | Per-model GPU capacity recommendation |

### Getting started (GitOps)

The platform is deployed via OpenShift GitOps (ArgoCD). All infrastructure is defined in `gitops/` and deployed as ArgoCD Applications.

```bash
# 1. Install OpenShift GitOps operator
oc apply -f - <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: openshift-gitops-operator
  namespace: openshift-operators
spec:
  channel: latest
  installPlanApproval: Automatic
  name: openshift-gitops-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
EOF

# 2. Wait for ArgoCD, then deploy platform
oc apply -f gitops/appproject.yaml
oc apply -f gitops/root-app.yaml

# 3. Apply platform configuration
oc apply -f operator/config/samples/platformconfig-sample.yaml
oc apply -f operator/config/samples/lifecycleprofile-sample.yaml

# 4. Create credential secrets
oc -n sandbox create secret generic evalhub-credentials \
  --from-literal=token=$(oc whoami -t) \
  --from-literal=url=<evalhub-url>
oc -n sandbox create secret generic scan-s3-credentials \
  --from-literal=endpoint=https://minio-modelops-storage.apps.<cluster-domain> \
  --from-literal=accessKeyId=minioadmin \
  --from-literal=secretAccessKey=minioadmin
oc -n sandbox create secret generic result-s3-credentials \
  --from-literal=endpoint=https://minio-modelops-storage.apps.<cluster-domain> \
  --from-literal=accessKeyId=minioadmin \
  --from-literal=secretAccessKey=minioadmin

# 5. Create a model request
kubectl apply -f model_onboarding_pipeline/model-intake-pipeline/pipeline/sample-modelrequest.yaml

# Check deployment status
oc get applications -n openshift-gitops
```

## Roadmap

| Capability | Status |
|---|---|
| Governed model intake | Implemented |
| Model catalog integration | Planned |
| Fine-tuning lifecycle | Planned |
| Application promotion (sandbox → staging → prod) | Planned |
| Drift detection & remediation | Planned |
| Model retirement | Planned |
| Agent evaluation framework | Planned |
