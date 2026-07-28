# Enterprise AI Lifecycle Platform — GitOps

This directory contains the GitOps configuration for deploying the Enterprise AI Lifecycle Platform on OpenShift using ArgoCD (OpenShift GitOps).

## Directory Structure

```
gitops/
├── root-app.yaml              # Root Application (App of Apps pattern)
├── appproject.yaml            # ArgoCD AppProject for the platform
├── applications/              # Child Application manifests
│   ├── maas.yaml
│   ├── minio.yaml
│   ├── mlflow.yaml
│   ├── evalhub.yaml
│   ├── operator.yaml
│   ├── pipelines.yaml
│   └── rbac.yaml
├── components/                # Kustomize-based resource definitions
│   ├── maas/                  # KServe Models-as-a-Service enablement
│   ├── minio/                 # MinIO S3-compatible object storage
│   ├── mlflow/                # MLflow experiment tracking
│   ├── evalhub/               # EvalHub (TrustyAI) evaluation
│   ├── operator/              # ModelOps controller deployment
│   ├── pipelines/             # Tekton tasks and pipelines
│   └── rbac/                  # Cluster roles and bindings
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

### Step 2: Deploy the platform

```bash
# Create the ArgoCD AppProject
oc apply -f gitops/appproject.yaml

# Deploy the root Application (App of Apps)
oc apply -f gitops/root-app.yaml
```

ArgoCD will automatically sync all child applications:
1. MaaS — enables KServe Models-as-a-Service in DataScienceCluster
2. MinIO — S3-compatible object storage for MLflow and EvalHub
3. MLflow — experiment tracking via MlflowOperator
4. EvalHub — TrustyAI evaluation platform
5. Operator — ModelOps controller
6. Pipelines — Tekton tasks and pipelines
7. RBAC — cluster roles and bindings for pipeline SA

### Step 3: Create platform configuration

After the operator is running, apply the platform configuration:

```bash
oc apply -f operator/config/samples/platformconfig-sample.yaml
oc apply -f operator/config/samples/lifecycleprofile-sample.yaml
```

### Step 4: Check status

```bash
# ArgoCD UI
open https://openshift-gitops-server-openshift-gitops.apps.<cluster-domain>

# Or CLI
oc get applications -n openshift-gitops
```

## Adding a New Promotion Namespace

The `promotionNamespaces` field in `ModelLifecycleProfile` can reference namespaces that don't exist yet. The operator automatically creates the necessary RBAC (`pipeline` ServiceAccount, RoleBindings) in those namespaces when a ModelRequest is submitted.

Example lifecycle profile:
```yaml
apiVersion: modelops.example.io/v1alpha1
kind: ModelLifecycleProfile
spec:
  pipelineRef:
    sandbox: sandbox-pipeline
    promotion: promotion-pipeline
  promotionNamespaces:
    - staging
    - production
```

The operator will ensure each promotion namespace has:
- A `pipeline` ServiceAccount
- RoleBindings granting the pipeline SA access in that namespace

## Customization

Edit files in `gitops/components/` and push to the repo. ArgoCD automatically detects changes and syncs (when `automated.selfHeal` is enabled).

To customize MinIO credentials, edit `gitops/components/minio/kustomization.yaml` (the `secretGenerator` section). For production, use a sealed-secret or external secret provider.
