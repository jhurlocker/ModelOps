# Enterprise AI Lifecycle Platform — GitOps

This directory contains the GitOps configuration for deploying the Enterprise AI Lifecycle Platform on OpenShift using ArgoCD (OpenShift GitOps).

## Directory Structure

```
gitops/
├── root-app.yaml              # Root Application (App of Apps pattern)
├── appproject.yaml            # ArgoCD AppProject for the platform
├── applications/              # Child Application manifests
│   ├── evalhub.yaml
│   ├── maas-subscriptions.yaml
│   ├── maas.yaml
│   ├── minio.yaml
│   ├── mlflow.yaml
│   ├── model-inference.yaml
│   ├── model-intake-ui.yaml
│   ├── model-registry.yaml
│   ├── operator.yaml
│   ├── pipelines.yaml
│   ├── rbac.yaml
│   ├── results-ui.yaml
│   └── runtime-config.yaml
├── components/                # Kustomize-based resource definitions
│   ├── evalhub/               # EvalHub (TrustyAI) evaluation
│   ├── maas-subscriptions/    # KServe MaaS subscription tiers (free/premium)
│   ├── maas/                  # KServe Models-as-a-Service enablement
│   ├── minio/                 # MinIO S3-compatible object storage
│   ├── mlflow/                # MLflow experiment tracking
│   ├── model-inference/       # Example model inference routes (Granite 2B)
│   ├── model-intake-ui/       # Model Intake frontend UI
│   ├── model-registry/        # MySQL-backed Model Registry instance
│   ├── operator/              # ModelOps controller deployment
│   ├── pipelines/             # Tekton tasks and pipelines
│   ├── rbac/                  # Cluster roles and bindings
│   ├── results-ui/            # Results UI for scan/benchmark results
│   └── runtime-config/        # PlatformConfig, LifecycleProfile, IntakeProviderConfig, sandbox secrets
└── README.md
```

## Quick Start

### Prerequisites

1. OpenShift cluster with cluster-admin access
2. OpenShift AI (RHOAI) operator installed
3. OpenShift Pipelines operator installed
4. OpenShift GitOps operator installed (see below)

### Step 1: Install OpenShift GitOps

```bash
# Create subscription (if operator not already installed)
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

# Wait for operator to be ready
oc wait --for=condition=Available deployment/openshift-gitops-server -n openshift-gitops --timeout=300s
```

#### Step 1a: Grant ArgoCD cluster-wide permissions (sandbox only)

The OpenShift GitOps operator creates an ArgoCD instance with a restricted
ServiceAccount. For a disposable sandbox cluster, grant it cluster-admin so it
can create CRDs, namespaces, and all resource types across the cluster:

```bash
oc adm policy add-cluster-role-to-user cluster-admin \
  -z openshift-gitops-argocd-application-controller \
  -n openshift-gitops
```

For production, scope this to the specific API groups and resources the
platform actually needs instead of using cluster-admin.

#### Step 1b: Configure ArgoCD UI RBAC

By default the ArgoCD UI is only visible to OpenShift users in the
`cluster-admins` group. Update the `ArgoCD` CR's `spec.rbac` so every
authenticated user can at least view applications (sandbox-default
`role:readonly`), and the local `admin` account works:

> **Important:** The OpenShift GitOps operator owns the `argocd-rbac-cm`
> ConfigMap and will revert any direct edits to it. Configure RBAC through
> the `ArgoCD` custom resource instead.

```bash
oc patch argocd openshift-gitops -n openshift-gitops --type merge -p '
{
  "spec": {
    "rbac": {
      "defaultPolicy": "role:readonly",
      "policy": "g, system:cluster-admins, role:admin\ng, cluster-admins, role:admin\ng, admin, role:admin\n",
      "scopes": "[groups]"
    }
  }
}'
```

#### Step 1c: Increase ArgoCD controller memory (recommended)

The default 2Gi memory limit can cause OOMKills during a cold bootstrap of
all 13 Applications with retry policies enabled:

```bash
oc patch argocd openshift-gitops -n openshift-gitops --type merge -p '
{"spec":{"controller":{"resources":{"limits":{"memory":"4Gi"},"requests":{"memory":"2Gi"}}}}}'
```

### Step 2: Deploy the platform

```bash
# Create the ArgoCD AppProject
oc apply -f gitops/appproject.yaml

# Deploy the root Application (App of Apps)
oc apply -f gitops/root-app.yaml
```

ArgoCD will automatically sync all 13 child applications:
1. EvalHub — TrustyAI evaluation platform
2. MaaS — enables KServe Models-as-a-Service in DataScienceCluster
3. MaaS Subscriptions — KServe MaaS subscription tiers (free and premium) for serving runtimes
4. MinIO — S3-compatible in-cluster object storage for MLflow and EvalHub
5. MLflow — experiment tracking via MlflowOperator
6. Model Inference — example inference route (Granite 2B) deployed to staging
7. Model Intake UI — frontend UI for submitting and tracking model onboarding requests
8. Model Registry — MySQL-backed Model Registry instance for registered model metadata
9. Operator — ModelOps controller, CRDs, RBAC, and ServiceAccount
10. Pipelines — Tekton tasks and pipelines (sandbox and promotion)
11. RBAC — cluster roles and bindings for pipeline SA and namespace provisioner
12. Results UI — frontend UI for viewing scan and benchmark results
13. Runtime Config — PlatformConfig, ModelLifecycleProfile, IntakeProviderConfig, and sandbox secrets

### Application dependency chain and sync ordering

The child Applications have inter-dependencies that must be satisfied in order.

#### Sync wave ordering

ArgoCD sync-waves enforce a sequential ordering:
no wave-N Application syncs until all wave-(N-1) Applications are Synced.

| Wave | Applications | What it does / depends on |
|------|--------------|---------------------------|
| 1 | maas | Patches DataScienceCluster → triggers async RHOAI reconciliation for MaaS CRDs |
| 2 | maas-subscriptions | Creates MaaSSubscription CRs — depends on CRDs registered by wave 1 |
| 3 | modelops-pipelines | Creates `sandbox`, `staging`, `vllm`, `vllm-staging` namespaces |
| 4 | modelops-runtime-config, results-ui, model-inference, modelops-rbac | Deploy into namespaces created by wave 3 |

All other Applications (evalhub, minio, mlflow, model-intake-ui, model-registry,
modelops-operator) have no sync-wave and run in the default wave (0), starting
immediately alongside wave 1.

#### Retry policies

All Applications with sync-waves also carry a retry policy
(limit: 20, exponential backoff 10s → 5m max) so transient failures
(failure reasons outside this repo's control, such as async operator reconciliation
or namespace propagation) are handled automatically instead of failing fast.

#### maas / maas-subscriptions dependency chain

The **maas** Application patches the `DataScienceCluster` to enable KServe
Models-as-a-Service (`kserve.modelsAsService.managementState: Managed`).
When RHOAI's operator asynchronously reconciles this change, it registers
the `MaaSSubscription` and `Tenant` CRDs. The **maas-subscriptions** Application
then creates `MaaSSubscription` resources that depend on those CRDs being available.

The DataScienceCluster patch must be a Kustomize **resource** (not a `patches`
entry) because it targets a `DataScienceCluster` not owned by the maas
kustomization. A Kustomize `patches` target with no matching resource in the
kustomization's `resources` list is silently dropped — the patch is never
applied. As a resource, ArgoCD applies it as a strategic merge patch against
the existing cluster object.

#### pipelines / namespace dependency chain

The **modelops-pipelines** Application (wave 3) creates the `sandbox` and
`staging` namespaces that **modelops-runtime-config**, **results-ui**,
**model-inference**, and **modelops-rbac** (wave 4) deploy into. Without
sync-wave ordering, wave-4 apps exhaust their retries before the namespaces
are ready, especially during a cold bootstrap.

The namespace YAML files live alongside the Tekton resources in
`model_onboarding_pipeline/model-intake-pipeline/pipeline/`:
- `platform-sandbox-ns.yaml` — creates the `sandbox` namespace
- `platform-staging-ns.yaml` — creates the `staging` namespace
- `sandbox-namespace.yaml` — creates the `vllm` namespace
- `staging-namespace.yaml` — creates the `vllm-staging` namespace

The Kuadrant CRDs (`authpolicies.kuadrant.io`, `tokenratelimitpolicies.kuadrant.io`)
deployed by the maas Application are self-contained CustomResourceDefinition
resources (not instances of CRDs registered by an external operator), so they
do not have a similar async timing dependency. The Gateway (`data-science-gateway-class`)
and Tenant resources within maas also depend on RHOAI being installed, but
their CRDs are registered during RHOAI installation — not gated behind
`modelsAsService: Managed` — and are therefore unaffected by this race
condition. The maas Application's retry policy covers any transient failures
on those resources as well.

#### Troubleshooting exhausted retries

If any Application's retry policy is ever exhausted (all 20 attempts fail),
verify the dependency and force a re-sync:

```bash
# ---- maas / maas-subscriptions ----

# Check that the DataScienceCluster patch was applied
oc get dsc default-dsc -o jsonpath='{.spec.components.kserve.modelsAsService.managementState}'
# Expected: Managed

# Confirm the MaaS CRDs are registered
oc get crd maassubscriptions.maas.opendatahub.io tenants.maas.opendatahub.io

# If "not found", RHOAI has not finished reconciling — check the operator:
oc get deployment rhods-operator -n redhat-ods-operator

# Once the CRDs are confirmed present, force a manual re-sync:
oc patch application maas-subscriptions -n openshift-gitops --type merge \
  -p '{"operation":{"initiatedBy":{"username":"admin"},"sync":{"prune":true}}}'

# ---- pipelines / namespace dependents ----

# Check that namespaces exist
oc get ns sandbox staging vllm vllm-staging

# If missing, sync pipelines first:
oc patch application modelops-pipelines -n openshift-gitops --type merge \
  -p '{"operation":{"initiatedBy":{"username":"admin"},"sync":{"prune":true}}}'

# Then sync namespace-dependent apps:
for app in modelops-runtime-config results-ui model-inference modelops-rbac; do
  oc patch application $app -n openshift-gitops --type merge \
    -p '{"operation":{"initiatedBy":{"username":"admin"},"sync":{"prune":true}}}'
done
```

#### ArgoCD controller memory

During a cold bootstrap of all 13 Applications with retry policies, the
ArgoCD application controller may exhaust its default 2Gi memory limit
and be OOMKilled (exit 137). Increase the memory limit before deploying:

```bash
oc patch argocd openshift-gitops -n openshift-gitops --type merge -p '
{"spec":{"controller":{"resources":{"limits":{"memory":"4Gi"},"requests":{"memory":"2Gi"}}}}}'
```

### Step 3: Platform configuration (automatic)

No manual step is needed. The **Runtime Config** Application (application #13 above)
is already synced automatically by the root Application and deploys the actual live
platform configuration from `gitops/components/runtime-config/`:

- `PlatformConfig` (model registry, S3 buckets, GPU advisor, EvalHub, benchmark defaults)
- `ModelLifecycleProfile` (declared stage sequence: capacity → sandbox → promotion)
- `IntakeProviderConfig` (Tekton pipeline names, timeout/workspace defaults)
- Sandbox secrets (scan/result S3 credentials, EvalHub endpoint)

The sample files under `operator/config/samples/` are for **local development and
reference only** — they are not the deployed configuration and must not be applied
directly to any cluster managed by GitOps.

### Step 4: Check status

```bash
# ArgoCD UI
open https://openshift-gitops-server-openshift-gitops.apps.<cluster-domain>

# Or CLI
oc get applications -n openshift-gitops
```

## Adding a New Promotion Namespace

Promotion namespaces are declared per-stage on `ModelLifecycleProfile.Spec.Stages`
with `perNamespace: true`. The operator automatically creates the necessary RBAC
(`pipeline` ServiceAccount, RoleBindings) in those namespaces when a ModelRequest
is submitted.

Example lifecycle profile:
```yaml
apiVersion: modelops.example.io/v1alpha1
kind: ModelLifecycleProfile
spec:
  providerConfigRef:
    name: rhoai-default
    kind: IntakeProviderConfig
  stages:
    - name: capacity
      kind: CapacityPlan
      required: true
    - name: sandbox
      kind: PipelineRun
      providerConfigRef:
        name: rhoai-sandbox
        kind: IntakeProviderConfig
      required: true
    - name: promote
      kind: PipelineRun
      providerConfigRef:
        name: rhoai-promotion
        kind: IntakeProviderConfig
      perNamespace: true
      namespaceSetup:
        ensureRBAC: true
```

The operator will ensure each promotion namespace has:
- A `pipeline` ServiceAccount
- RoleBindings granting the pipeline SA access in that namespace

## Customization

Edit files in `gitops/components/` and push to the repo. ArgoCD automatically detects changes and syncs (when `automated.selfHeal` is enabled).

To customize MinIO credentials, edit `gitops/components/minio/kustomization.yaml` (the `secretGenerator` section). **For production, use a sealed-secret or external secret provider.** This recommendation exists *because* `gitops/components/runtime-config/secrets.yaml` currently commits MinIO's default credentials (`minioadmin`/`minioadmin`) as plaintext Secrets — acceptable only for a disposable sandbox cluster with an in-cluster MinIO instance, not a pattern to follow beyond that. See `docs/REFACTOR_PLAN.md`'s backlog for the tracked item to introduce a real secrets-management pattern before this project leaves sandbox use.
