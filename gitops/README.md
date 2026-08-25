# Enterprise AI Lifecycle Platform — GitOps

This directory contains the GitOps configuration for deploying the Enterprise AI Lifecycle Platform on OpenShift using ArgoCD (OpenShift GitOps).

## Directory Structure

```
gitops/
├── root-app.yaml              # Root Application (App of Apps pattern)
├── appproject.yaml            # ArgoCD AppProject for the platform
├── applications/              # Child Application manifests
│   ├── evalhub.yaml
│   ├── maas-kuadrant.yaml
│   ├── maas-prereqs.yaml
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
│   ├── maas-kuadrant/         # Kuadrant CR (auth + rate-limiting infrastructure)
│   ├── maas-prereqs/          # RHCL operator, LeaderWorkerSet subscriptions
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
5. cert-manager Operator for Red Hat OpenShift (often pre-installed; verify with `oc get csv -n cert-manager-operator | grep cert-manager`)
6. Red Hat Connectivity Link operator (installed automatically by the `maas-prereqs` Application during deployment)
7. Leader Worker Set Operator (installed automatically by the `maas-prereqs` Application during deployment)

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

ArgoCD will automatically sync all 15 child applications:
1. MaaS Prereqs — RHCL operator + LeaderWorkerSet operator subscriptions
2. MaaS Kuadrant — Kuadrant CR (triggers Authorino + Limitador creation)
3. EvalHub — TrustyAI evaluation platform
4. MaaS — enables KServe Models-as-a-Service in DataScienceCluster
5. MaaS Subscriptions — KServe MaaS subscription tiers (free and premium) for serving runtimes
6. MinIO — S3-compatible in-cluster object storage for MLflow and EvalHub
7. MLflow — experiment tracking via MlflowOperator
8. Model Inference — example inference route (Granite 2B) deployed to staging
9. Model Intake UI — frontend UI for submitting and tracking model onboarding requests
10. Model Registry — MySQL-backed Model Registry instance for registered model metadata
11. Operator — ModelOps controller, CRDs, RBAC, and ServiceAccount
12. Pipelines — Tekton tasks and pipelines (sandbox and promotion)
13. RBAC — cluster roles and bindings for pipeline SA and namespace provisioner
14. Results UI — frontend UI for viewing scan and benchmark results
15. Runtime Config — PlatformConfig, ModelLifecycleProfile, IntakeProviderConfig, and sandbox secrets

### Application dependency chain and sync ordering

The child Applications have inter-dependencies that must be satisfied in order.

#### Sync wave ordering

ArgoCD sync-waves enforce a sequential ordering:
no wave-N Application syncs until all wave-(N-1) Applications are Synced.

| Wave | Applications | What it does / depends on |
|------|--------------|---------------------------|
| -1 | maas-prereqs | Installs RHCL + LWS operators (Subscriptions) |
| 0 | maas-kuadrant | Creates Kuadrant CR — depends on RHCL operator CRDs from wave -1 |
| 1 | maas | Patches DataScienceCluster → triggers async RHOAI reconciliation for MaaS CRDs |
| 2 | maas-subscriptions | Creates MaaSSubscription CRs — depends on CRDs registered by wave 1 |
| 3 | modelops-pipelines | Creates `sandbox`, `staging`, `vllm`, `vllm-staging` namespaces |
| 4 | modelops-runtime-config, results-ui, model-inference, modelops-rbac | Deploy into namespaces created by wave 3 |

All other Applications (evalhub, minio, mlflow, model-intake-ui, model-registry,
modelops-operator) have no sync-wave and run in the default wave (0), starting
alongside maas-kuadrant.

#### Retry policies

All Applications with sync-waves also carry a retry policy
(limit: 20, exponential backoff 10s → 5m max) so transient failures
(failure reasons outside this repo's control, such as async operator reconciliation
or namespace propagation) are handled automatically instead of failing fast.

#### maas-prereqs dependency chain (wave -1)

The **maas-prereqs** Application must sync before any MaaS resources because it
deploys the infrastructure that the maas Application's Tenant, Gateway, and
MaaSSubscription resources depend on:

1. **Red Hat Connectivity Link (RHCL) operator** (maas-prereqs, wave -1):
   Installed via Subscription in `openshift-operators`. The RHCL operator only
   supports `AllNamespaces` install mode, so it MUST go in `openshift-operators`
   — the global operator namespace that already has a cluster-wide OperatorGroup.
   Installing it in `kuadrant-system` (or any single namespace) will fail.

2. **Leader Worker Set (LWS) operator** (maas-prereqs, wave -1): Installed in
   `openshift-lws-operator` namespace (OwnNamespace install mode). The
   `cert-manager Operator for Red Hat OpenShift` must be pre-installed (verify
   with `oc get csv -n cert-manager-operator`).

3. **Kuadrant CR** (maas-kuadrant, wave 0): Created in `kuadrant-system`.
   When the RHCL operator reconciles this CR, it asynchronously creates an
   Authorino instance and Limitador instance for API authentication and rate
   limiting. The maas-kuadrant Application is a separate Application from
   maas-prereqs because the Kuadrant CRD (`kuadrants.kuadrant.io`) is only
   registered after the RHCL operator installs — ArgoCD can't validate the
   Kuadrant CR during the same sync operation as the operator Subscription.

The retry policy on maas-kuadrant (20 attempts, 10s→5m backoff) handles the
async operator installation: the RHCL operator may take several minutes to
install and register CRDs, during which the Kuadrant CR will fail validation.
Once the CRD is registered, the Kuadrant CR syncs automatically.

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
# ---- maas-prereqs ----

# Check that RHCL operator is installed
oc get csv rhcl-operator -n openshift-operators
# Expected: Succeeded phase

# Check that Kuadrant CR is ready
oc get kuadrant kuadrant -n kuadrant-system
oc get deployment authorino -n kuadrant-system

# If Kuadrant shows no status conditions after 2+ minutes, the operator may
# have a RESTMapping error ("cannot find RESTMapping for APIVersion
# kuadrant.io/v1beta1 Kind Kuadrant") — this happens when the operator starts
# before its own CRDs are fully registered. Restart the operator pod:
oc delete pod -n openshift-operators \
  $(oc get pods -n openshift-operators --no-headers | grep kuadrant-operator | awk '{print $1}')

# If Kuadrant reports MissingDependency (Istio race condition), restart the operator
# pod in the same way.

# If the Tenant still shows "no Authorino instances found" after Authorino is
# deployed, the Tenant controller hasn't re-reconciled. Annotate the Tenant to
# trigger a fresh reconciliation:
oc annotate tenant default-tenant -n models-as-a-service \
  force-reconcile="$(date +%s)" --overwrite

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

During a cold bootstrap of all 15 Applications with retry policies, the
ArgoCD application controller may exhaust its default 2Gi memory limit
and be OOMKilled (exit 137). Increase the memory limit before deploying:

```bash
oc patch argocd openshift-gitops -n openshift-gitops --type merge -p '
{"spec":{"controller":{"resources":{"limits":{"memory":"4Gi"},"requests":{"memory":"2Gi"}}}}}'
```

### Step 2a: Configure Authorino TLS (manual, one-time)

After the `maas-prereqs` Application is synced and the Kuadrant CR is ready
(`oc wait --for=condition=Ready kuadrant/kuadrant -n kuadrant-system`),
the Kuadrant operator auto-creates an Authorino instance. TLS must be
configured on that Authorino before MaaS API key operations will work.

These steps mirror [RHOAI 3.4 docs section 1.4 (Configure TLS for Models-as-a-Service)](https://docs.redhat.com/en/documentation/red_hat_openshift_ai_self-managed/3.4/html/govern_llm_access_with_models-as-a-service/deploy-and-manage-models-as-a-service_maas#configure-tls-for-maas_maas-deploy).

> **Why this can't be GitOps-managed**: The Authorino CR and its Service are
> dynamically created by the Kuadrant operator when it reconciles the Kuadrant
> CR. Annotating the Service, patching the Authorino CR, and setting deployment
> environment variables are all modifications of operator-owned, auto-created
> resources. Trying to pre-create them as Kustomize resources would either fail
> (resource doesn't exist yet) or conflict with the operator (race on ownership).

**Step 2a-1:** Verify Kuadrant is ready and Authorino exists:

```bash
oc wait --for=condition=Ready kuadrant/kuadrant -n kuadrant-system --timeout=300s
oc get deployment authorino -n kuadrant-system
```

**Step 2a-2:** Annotate the Authorino service for TLS certificate generation:

```bash
oc annotate service authorino-authorino-authorization \
  -n kuadrant-system \
  service.beta.openshift.io/serving-cert-secret-name=authorino-server-cert \
  --overwrite
```

The `service-ca-operator` generates a TLS certificate signed by the cluster
service CA and stores it in the `authorino-server-cert` Secret.

**Step 2a-3:** Verify the TLS secret was created:

```bash
oc get secret authorino-server-cert -n kuadrant-system
# Expected: TYPE kubernetes.io/tls, DATA 2
```

**Step 2a-4:** Patch the Authorino CR to enable the TLS listener:

```bash
oc patch authorino authorino -n kuadrant-system --type=merge --patch '{
  "spec": {
    "listener": {
      "tls": {
        "enabled": true,
        "certSecretRef": {
          "name": "authorino-server-cert"
        }
      }
    }
  }
}'
```

**Step 2a-5:** Configure Authorino deployment with TLS certificate environment variables:

```bash
oc -n kuadrant-system set env deployment/authorino \
  SSL_CERT_FILE=/etc/ssl/certs/openshift-service-ca/service-ca-bundle.crt \
  REQUESTS_CA_BUNDLE=/etc/ssl/certs/openshift-service-ca/service-ca-bundle.crt
```

Wait for the Authorino deployment to roll out:

```bash
oc wait --for=condition=Available deployment/authorino -n kuadrant-system --timeout=300s
```

**Step 2a-6:** Annotate the Gateway for Authorino TLS bootstrap:

```bash
oc annotate gateway maas-default-gateway -n openshift-ingress \
  security.opendatahub.io/authorino-tls-bootstrap="true" --overwrite
```

The MaaS controller detects this annotation and creates an `EnvoyFilter`
resource that configures the Envoy proxy to use TLS when communicating
with Authorino.

### Step 2b: Restart model controllers (manual, one-time)

After Authorino TLS is configured, the RHOAI model controllers need to
discover the updated MaaS infrastructure. Restart them:

```bash
oc delete pod -n redhat-ods-applications -l app=odh-model-controller
oc delete pod -n redhat-ods-applications -l control-plane=kserve-controller-manager
```

Wait for both to be ready:

```bash
oc wait --for=condition=Ready pod -l app=odh-model-controller -n redhat-ods-applications --timeout=120s
oc wait --for=condition=Ready pod -l control-plane=kserve-controller-manager -n redhat-ods-applications --timeout=120s
```

> **Why this can't be GitOps-managed**: Restarting a pod is not idempotent
> or declarative — a `Job` that deletes pods would re-execute on every sync
> cycle, causing unnecessary disruption. This is a one-time post-install step.

### Step 2c: Verify Authorino TLS end-to-end

Confirm the full Authorino TLS chain:

```bash
# Authorino service has the serving cert annotation
oc get service authorino-authorino-authorization -n kuadrant-system \
  -o jsonpath='{.metadata.annotations.service\.beta\.openshift\.io/serving-cert-secret-name}'
# Expected: authorino-server-cert

# Authorino CR has TLS enabled
oc get authorino authorino -n kuadrant-system \
  -o jsonpath='{.spec.listener.tls.enabled}'
# Expected: true

# Authorino deployment has TLS env vars
oc get deployment/authorino -n kuadrant-system \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="SSL_CERT_FILE")].value}'
# Expected: /etc/ssl/certs/openshift-service-ca/service-ca-bundle.crt

# Gateway has TLS bootstrap annotation
oc get gateway maas-default-gateway -n openshift-ingress \
  -o jsonpath='{.metadata.annotations.security\.opendatahub\.io/authorino-tls-bootstrap}'
# Expected: true

# Tenant Degraded condition should clear
oc get tenant default-tenant -n models-as-a-service \
  -o jsonpath='{.status.conditions[?(@.type=="Degraded")].message}'
# Should no longer mention "no Authorino instances found"

# MaaS API key search should return non-500
CLUSTER_DOMAIN=$(oc get ingresses.config.openshift.io cluster -o jsonpath='{.spec.domain}')
curl -sk "https://maas.${CLUSTER_DOMAIN}/maas/api/v1/user"
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
