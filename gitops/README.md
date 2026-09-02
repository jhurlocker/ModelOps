# Enterprise AI Lifecycle Platform — GitOps

This directory contains the GitOps configuration for deploying the Enterprise AI Lifecycle Platform on OpenShift using ArgoCD (OpenShift GitOps).

## Directory Structure

```
gitops/
├── root-app.yaml              # Root Application (App of Apps pattern)
├── appproject.yaml            # ArgoCD AppProject for the platform
├── applications/              # Child Application manifests
│   ├── argocd-config.yaml
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
│   ├── runtime-config.yaml
│   ├── sealed-secrets.yaml
│   └── zot.yaml
├── components/                # Kustomize-based resource definitions
│   ├── argocd-config/         # Patches the operator-owned ArgoCD CR (UI RBAC + controller memory)
│   ├── evalhub/               # EvalHub (TrustyAI) evaluation
│   ├── maas-kuadrant/         # Kuadrant CR (auth + rate-limiting infrastructure)
│   ├── maas-prereqs/          # cert-manager, RHCL, and LeaderWorkerSet operator subscriptions
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
│   ├── runtime-config/        # PlatformConfig, LifecycleProfile, IntakeProviderConfig, SealedSecrets + EvalHub endpoint
│   ├── sealed-secrets/        # Sealed Secrets controller (vendored, pinned v0.39.1)
│   └── zot/                   # Zot OCI container image registry (in-cluster, no external UI)
├── scripts/
│   └── seal-secrets.py        # Per-cluster credential generation + kubeseal script
└── README.md
```

## Quick Start

### Prerequisites

1. OpenShift cluster with cluster-admin access
2. OpenShift AI (RHOAI) operator installed
3. OpenShift Pipelines operator installed
4. OpenShift GitOps operator installed (see below)
5. cert-manager Operator for Red Hat OpenShift (installed automatically by the `maas-prereqs` Application; also commonly pre-installed — verify with `oc get csv -n cert-manager-operator | grep cert-manager`)
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

#### Step 1b: Configure the operator-owned ArgoCD instance (automatic)

The `openshift-gitops` `ArgoCD` CR — created and owned by the OpenShift GitOps
operator, not by this repo — is patched automatically by the `argocd-config`
Application (sync-wave -1, synced ahead of everything else). It sets:

- **`spec.rbac`** so every authenticated user can at least view applications
  (sandbox-default `role:readonly`), the OpenShift cluster-admin groups
  (`system:cluster-admins` / `cluster-admins`) get `role:admin`, and the local
  `admin` account works.
- **`spec.controller.resources`** so the application controller's default 2Gi
  memory limit does not OOMKill it during a cold bootstrap of all Applications
  with retry policies enabled.

No manual action is needed. See `gitops/components/argocd-config/patch-argocd.yaml`.

> **Important:** The OpenShift GitOps operator owns the `argocd-rbac-cm`
> ConfigMap and will revert any direct edits to it. Configure RBAC through the
> `ArgoCD` custom resource instead — which is exactly what the committed
> `argocd-config` component does.

> **Why this used to be manual (and caused a real problem):** Steps 1b and 1c
> were previously `oc patch` commands that had to be remembered and re-run by
> hand on every new cluster. Skipping them was invisible on an existing lab
> cluster (already patched), but failed on a fresh cluster: a cluster-admin who
> logged into the ArgoCD UI saw an **empty** Application list — and the
> controller was at risk of OOMKilling during the cold bootstrap — despite every
> Application otherwise being Synced. They are now committed GitOps that apply
> on every new cluster automatically, closing the same hand-run-post-install gap
> that the Authorino TLS and gateway-memory entries below already document.

### Step 2: Deploy the platform

```bash
# Create the ArgoCD AppProject
oc apply -f gitops/appproject.yaml

# Deploy the root Application (App of Apps)
oc apply -f gitops/root-app.yaml
```

ArgoCD will automatically sync all 18 child applications:
1. ArgoCD Config — patches the operator-owned `openshift-gitops` ArgoCD CR (UI RBAC + controller memory), so cluster-admins can view Applications in the UI and the controller does not OOMKill during bootstrap
2. MaaS Prereqs — cert-manager + RHCL + LeaderWorkerSet operator subscriptions
3. MaaS Kuadrant — Kuadrant CR (triggers Authorino + Limitador creation) + Authorino CR (TLS listener enabled)
4. EvalHub — TrustyAI evaluation platform
5. MaaS — enables KServe Models-as-a-Service in DataScienceCluster
6. MaaS Subscriptions — KServe MaaS subscription tiers (free and premium) for serving runtimes
7. MinIO — S3-compatible in-cluster object storage for MLflow and EvalHub
8. Zot — OCI container image registry (in-cluster, built-in UI)
9. MLflow — experiment tracking via MlflowOperator
10. Model Inference — example inference route (Granite 2B) deployed to staging
11. Model Intake UI — frontend UI for submitting and tracking model onboarding requests
12. Model Registry — MySQL-backed Model Registry instance for registered model metadata
13. Operator — ModelOps controller, CRDs, RBAC, and ServiceAccount
14. Pipelines — Tekton tasks and pipelines (sandbox and promotion)
15. RBAC — cluster roles and bindings for pipeline SA and namespace provisioner
16. Results UI — frontend UI for viewing scan and benchmark results
17. Runtime Config — PlatformConfig, ModelLifecycleProfile, IntakeProviderConfig, SealedSecrets, and EvalHub endpoint Secret
18. Sealed Secrets — the Sealed Secrets controller (wave -1), which decrypts every committed SealedSecret into its target Secret

### Application dependency chain and sync ordering

The child Applications have inter-dependencies that must be satisfied in order.

#### Sync wave ordering

ArgoCD sync-waves enforce a sequential ordering:
no wave-N Application syncs until all wave-(N-1) Applications are Synced.

| Wave | Applications | What it does / depends on |
|------|--------------|---------------------------|
| -1 | argocd-config, maas-prereqs, sealed-secrets | argocd-config patches the operator-owned ArgoCD CR (RBAC + controller memory); maas-prereqs installs RHCL + LWS operators (Subscriptions); sealed-secrets installs the controller that decrypts every committed SealedSecret, so it MUST be up before any wave-0+ app syncs |
| 0 | maas-kuadrant | Creates Kuadrant CR — depends on RHCL operator CRDs from wave -1 |
| 1 | maas | Patches DataScienceCluster → triggers async RHOAI reconciliation for MaaS CRDs |
| 2 | maas-subscriptions | Creates MaaSSubscription CRs — depends on CRDs registered by wave 1 |
| 3 | modelops-pipelines | Creates `sandbox`, `staging`, `vllm`, `vllm-staging` namespaces |
| 4 | modelops-runtime-config, results-ui, model-inference, modelops-rbac | Deploy into namespaces created by wave 3 |

All other Applications (evalhub, minio, zot, mlflow, model-intake-ui, model-registry,
modelops-operator) have no sync-wave and run in the default wave (0), starting
alongside maas-kuadrant.

#### argocd-config sequencing (no chicken-and-egg)

`argocd-config` patches the `openshift-gitops` ArgoCD CR. That CR is created by
the OpenShift GitOps operator itself in Step 1 — it must already exist for
`root-app.yaml` to be applicable at all, because ArgoCD's own CRDs and the
running ArgoCD instance are a hard prerequisite for the App-of-Apps pattern.
By the time any child Application (including `argocd-config`) syncs, the ArgoCD
CR is guaranteed to be present, so there is no sequencing conflict: this patch
targets a CR that necessarily predates the first sync.

It is placed at sync-wave -1 (alongside `maas-prereqs`, ahead of the wave-0
Applications) so the controller memory bump lands *before* the bulk of the 17
Applications begin their cold bootstrap, and before anyone relies on the UI
showing them.

#### Retry policies

All Applications with sync-waves also carry a retry policy
(limit: 20, exponential backoff 10s → 5m max) so transient failures
(failure reasons outside this repo's control, such as async operator reconciliation
or namespace propagation) are handled automatically instead of failing fast.

#### Zot registry access

Zot is an OCI container image registry deployed in the `modelops-zot` namespace
(PVC-backed storage, Deployment, Service). Its built-in UI is enabled via
`extensions.ui.enable: true` in the ConfigMap, but there is **no external Route**
to it: Zot serves the UI and the registry API on the same port (5000), so a Route
would expose the push/pull API externally too — and nothing needs Zot reachable
from outside the cluster. Zot still enforces htpasswd basic auth (the credential
path is referenced in the ConfigMap; the credential itself is the `zot-htpasswd`
SealedSecret, mounted as `/etc/zot/htpasswd`) because anonymous pull is left open
by design. In-cluster consumers (Tekton pipeline tasks, future controllers)
target the internal Service DNS — `zot.modelops-zot.svc.cluster.local:5000` — never
a Route. The `zot` Application carries the same generous retry policy as
`maas-subscriptions` since it is operator-adjacent infrastructure.

#### maas-prereqs dependency chain (wave -1)

The **maas-prereqs** Application must sync before any MaaS resources because it
deploys the infrastructure that the maas Application's Tenant, Gateway, and
MaaSSubscription resources depend on:

1. **cert-manager Operator for Red Hat OpenShift** (maas-prereqs, wave -1):
   Installed via Subscription in `cert-manager-operator` (package
   `openshift-cert-manager-operator`, channel `stable-v1`). Required by RHCL,
   LeaderWorkerSet, and MaaS TLS. If the cluster already has it pre-installed,
   ArgoCD reconciles the Subscription to the same shape (no duplicate).

2. **Red Hat Connectivity Link (RHCL) operator** (maas-prereqs, wave -1):
   Installed via Subscription in `openshift-operators`. The RHCL operator only
   supports `AllNamespaces` install mode, so it MUST go in `openshift-operators`
   — the global operator namespace that already has a cluster-wide OperatorGroup.
   Installing it in `kuadrant-system` (or any single namespace) will fail.

3. **Leader Worker Set (LWS) operator** (maas-prereqs, wave -1): Installed in
   `openshift-lws-operator` namespace (OwnNamespace install mode).

4. **Kuadrant CR** (maas-kuadrant, wave 0): Created in `kuadrant-system`.
   When the RHCL operator reconciles this CR, it asynchronously creates an
   Authorino instance and Limitador instance for API authentication and rate
   limiting. The maas-kuadrant Application is a separate Application from
   maas-prereqs because the Kuadrant CRD (`kuadrants.kuadrant.io`) is only
   registered after the RHCL operator installs — ArgoCD can't validate the
   Kuadrant CR during the same sync operation as the operator Subscription.

5. **Authorino CR** (maas-kuadrant, wave 0): Committed with the RHOAI
   MaaS-specific TLS shape (`listener.tls.enabled: true` + `certSecretRef:
   authorino-server-cert`). The Kuadrant operator auto-creates the base
   `authorino` instance; this CR updates it to the MaaS shape. `authorino-server-cert`
   is generated by the OpenShift service-ca-operator after the operator-owned
   Service is annotated (see Step 2a-2 — the cert *generation* remains a
   one-time manual step, not the CR shape).

The retry policy on maas-kuadrant (20 attempts, 10s→5m backoff) handles the
async operator installation: the RHCL operator may take several minutes to
install and register CRDs, during which the Kuadrant CR and Authorino CR will
fail validation. Once the CRD is registered, they sync automatically.

#### maas / maas-subscriptions dependency chain

The **maas** Application patches the `DataScienceCluster` to enable the AI
Gateway module and Models-as-a-Service
(`components.aigateway.managementState: Managed` together with
`components.aigateway.modelsAsAService.managementState: Managed`).
When RHOAI's operator asynchronously reconciles this change, it registers
the `MaaSSubscription` and `Tenant` CRDs. The **maas-subscriptions** Application
then creates `MaaSSubscription` resources that depend on those CRDs being available.

> **RHOAI 3.5 field rename**: the legacy `components.kserve.modelsAsService`
> path is deprecated and one-directional — it can go `Managed` → `Removed`
> (cleanup) but NOT `Removed` → `Managed`, so a fresh 3.5 cluster rejects the
> old field with "cannot re-enable once Removed". MaaS must be enabled via
> `components.aigateway.modelsAsAService` (note `AsAService`, three capitals,
> matching the ai-gateway-operator CRD field) alongside enabling the
> `aigateway` module itself.

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
`modelsAsAService: Managed` — and are therefore unaffected by this race
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
oc get dsc default-dsc -o jsonpath='{.spec.components.aigateway.modelsAsAService.managementState}'
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

Handled automatically by the `argocd-config` Application (wave -1), which
patches `spec.controller.resources` (4Gi limit / 2Gi request) on the
operator-owned `openshift-gitops` ArgoCD CR. See Step 1b — no manual action
is needed.

### Step 2a: Configure Authorino TLS (manual, one-time)

After the `maas-prereqs` Application is synced and the Kuadrant CR is ready
(`oc wait --for=condition=Ready kuadrant/kuadrant -n kuadrant-system`),
the Kuadrant operator auto-creates an Authorino instance. TLS must be
configured on that Authorino before MaaS API key operations will work.

These steps mirror [RHOAI 3.4 docs section 1.4 (Configure TLS for Models-as-a-Service)](https://docs.redhat.com/en/documentation/red_hat_openshift_ai_self-managed/3.4/html/govern_llm_access_with_models-as-a-service/deploy-and-manage-models-as-a-service_maas#configure-tls-for-maas_maas-deploy).

> **What is GitOps-managed vs manual**: The Authorino CR itself (TLS listener
> enabled, `certSecretRef: authorino-server-cert`) is now committed as
> `gitops/components/maas-kuadrant/authorino.yaml` and synced with the
> maas-kuadrant Application. It updates the base `authorino` instance that the
> Kuadrant operator auto-creates. Two steps remain genuinely manual because they
> mutate operator-owned, auto-created sub-resources (the Service and the
> Deployment) that the operator would otherwise race to overwrite:
> (a) annotating the Authorino *Service* to trigger cert generation (Step 2a-2)
> and (b) setting TLS env vars on the authorino *Deployment* (Step 2a-5).

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

**Step 2a-4:** (GitOps-managed, no action needed) The Authorino CR is committed
with the TLS listener enabled via `gitops/components/maas-kuadrant/authorino.yaml`.
For reference, the equivalent manual patch is:

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

# MaaS Gateway health check
CLUSTER_DOMAIN=$(oc get ingresses.config.openshift.io cluster -o jsonpath='{.spec.domain}')
curl -sk "https://maas.${CLUSTER_DOMAIN}/maas-api/health"
# Expected: {"status":"healthy"}

# MaaS API key search should require auth (not 500/503)
curl -sk -X POST "https://maas.${CLUSTER_DOMAIN}/maas-api/v1/api-keys/search" -H "Content-Type: application/json" -d '{}'
# Expected: 401 (requires valid user token)
```

### Step 2d: Gateway namespace label (manual, one-time)

The `maas-default-gateway` restricts HTTPRoutes to namespaces labeled with
`maas.opendatahub.io/gateway-access=true`. On RHOAI 3.5 the MaaS API HTTPRoute
lives in the `redhat-ai-gateway-infra` namespace (the maas-api deployment also
moved there from `redhat-ods-applications`); RHOAI auto-labels that namespace,
so no manual label is needed for the base MaaS API route.

Any additional namespaces serving models through the MaaS Gateway must be
labeled manually (`maas.opendatahub.io/gateway-access=true`):

```bash
oc label namespace <serving-namespace> \
  maas.opendatahub.io/gateway-access=true --overwrite
```

> RHOAI manages these namespaces, so this label cannot be added via GitOps
> (ArgoCD would fight with the RHOAI operator). It is a one-time manual step
> per cluster. The promotion stage's `namespaceSetup.labels` already sets
> `maas.opendatahub.io/gateway-access=true` for operator-provisioned namespaces.

### Step 2e: MaaS Gateway architecture

The `maas-prereqs` and `maas-kuadrant` Applications deploy the auth
infrastructure (RHCL operator, Kuadrant, Authorino). The **maas** Application
deploys the Gateway that routes API traffic:

```
Dashboard (rhods-dashboard)
  └─ maas-ui (standalone deployment, :8243)
     └─ https://maas.apps.<cluster>/maas-api/v1/*
        └─ OpenShift Router (passthrough Route)
           └─ maas-default-gateway (HTTPS :443, TLS terminate)
              └─ HTTPRoute (hostname: maas.apps.<cluster>)
                 └─ maas-api Service (:8443)
```

The Gateway requires:
1. An HTTPS listener with a **`hostname`** set to `maas.apps.<cluster-domain>`
   and TLS termination using the OpenShift **wildcard ingress certificate**
   (`cert-manager-ingress-cert`, covers `*.apps.<domain>`). RHOAI 3.5's maas-api
   reads `spec.listeners[].hostname` to build the external API URL; without it,
   `GET /v1/tenants` returns 500 and the dashboard shows "maas-api is not
   available".
2. An OpenShift **passthrough** Route for `maas.apps.<cluster-domain>` pointing
   to the Gateway's Service. Do NOT use `reencrypt` with the
   `service-ca-certificate` annotation here — that makes the Router present SNI
   = the internal service name, which conflicts with the hostname-filtered
   HTTPS listener (`filter_chain_not_found`).
3. The serving namespaces labeled for Gateway access (Step 2d); on RHOAI 3.5
   the MaaS API HTTPRoute and `maas-api` deployment live in
   `redhat-ai-gateway-infra` (auto-labeled by RHOAI)

The Gateway/Router hostname is cluster-specific, so it is not hand-typed in
`gateway.yaml` or `gateway-route.yaml`. It is derived at build time from the
single sourced value in `gitops/components/maas/cluster-config.yaml` via the
kustomize `replacements` in `gitops/components/maas/kustomization.yaml`. For a
new cluster, update that one ConfigMap (see the bootstrap checklist), never the
Gateway/Route manifests directly.

### Step 2f: Gateway memory (RHOAI 3.5+ AI Gateway)

RHOAI 3.5.0 introduced an "AI Gateway" that serves the dashboard through a
Sail/Istio gateway (`data-science-gateway` in `openshift-ingress`) alongside the
MaaS gateway. Both gateways are `istio-proxy` deployments with a default **1Gi**
memory limit that is too small once the Kuadrant WASM filters and Authorino auth
load — the pods OOMKill (exit 137) and `rh-ai.apps.<cluster-domain>` goes down.

The **maas** Application already patches gateway memory to 2Gi via:
- `gitops/components/maas/gateway-config.yaml` (`maas-gw-options` → MaaS gateway)
- `gitops/components/maas/patch-data-science-gateway-config.yaml` (`data-science-gateway-config` → dashboard gateway)

The `data-science-gateway-config` ConfigMap is operator-owned, but the RHOAI
operator preserves extra `data` keys, so the GitOps `deployment` key merges
safely without a reconciliation fight.

Verify after sync:

```bash
oc get deploy -n openshift-ingress data-science-gateway-data-science-gateway-class \
  -o jsonpath='{.spec.template.spec.containers[0].resources.limits.memory}{"\n"}'   # 2Gi
oc get deploy -n openshift-ingress maas-default-gateway-data-science-gateway-class \
  -o jsonpath='{.spec.template.spec.containers[0].resources.limits.memory}{"\n"}'   # 2Gi
curl -sk -o /dev/null -w "%{http_code}\n" https://rh-ai.apps.<cluster-domain>/    # 302 (login redirect), not 503
```

> **Auto-upgrade gotcha**: the `rhods-operator` Subscription is `Automatic` on
> the `stable-3.x` channel, so RHOAI can upgrade (e.g. 3.4.3 → 3.5.0) on a running
> cluster and introduce the AI Gateway silently. Re-check gateway memory and the
> dashboard route after any RHOAI minor upgrade.

### Step 3: Seal platform credentials (Sealed Secrets) — required per cluster

All platform credentials are committed as **SealedSecrets**, encrypted with the
*target cluster's* controller public key. The committed SealedSecrets therefore
decrypt only on the specific cluster they were sealed for; a **new cluster must
generate fresh random values and re-seal them against its own controller** before
the components that consume them (MinIO, Zot, the runtime-config S3 credentials,
the UI prefill defaults, MySQL, MaaS) can sync successfully. This is a real,
required first-class bootstrap step — not an afterthought.

The Sealed Secrets controller is installed automatically by the `sealed-secrets`
Application (sync-wave -1, ahead of everything else). Verify it is up, install
the pinned `kubeseal` client, run the repository's sealing script, commit, and
then apply the root app:

```bash
# 1. Controller up (installed at wave -1 by the root app)
oc wait --for=condition=Available deployment/sealed-secrets-controller -n kube-system --timeout=300s

# 2. kubeseal client, pinned 0.39.1
KUBESEAL_VERSION=0.39.1
curl -OL "https://github.com/bitnami-labs/sealed-secrets/releases/download/v${KUBESEAL_VERSION}/kubeseal-${KUBESEAL_VERSION}-linux-amd64.tar.gz"
tar xzf "kubeseal-${KUBESEAL_VERSION}-linux-amd64.tar.gz" kubeseal
sudo install -m 755 kubeseal /usr/local/bin/kubeseal

# 3. Generate genuinely random values and re-seal everything for THIS cluster.
#    Requires htpasswd (apache2-utils) and openssl. Writes the SealedSecrets
#    back into gitops/components/ (no credential value is ever written to disk).
python3 gitops/scripts/seal-secrets.py

# 4. Commit the regenerated SealedSecrets and push, then apply the root app.
oc apply -f gitops/appproject.yaml
oc apply -f gitops/root-app.yaml
```

> **Why per cluster, and why scripted:** a SealedSecret is one-way encrypted
> with a single cluster's private key; it cannot be decrypted by another cluster
> (or even the same cluster recreated from scratch, which generates a new key).
> `seal-secrets.py` is the single place that knows the identity-group coupling
> that MUST be preserved when rotating (MinIO root user/password is one value
> shared by `minio-credentials` + the scan/result S3 + results-ui + intake-UI
> credentials; the `zotadmin` password is bcrypt-hashed in `zot-htpasswd` and
> carried plaintext in `zot-push-credentials`; the MaaS DB password appears in
> both `maas-postgres-credentials` and `maas-db-config`). Rotate each group
> together, never one half of it.

> **These committed SealedSecrets are the sandbox cluster's.** They are safe to
> commit (encrypted, unrecoverable without the sandbox controller's private key),
> but they are NOT portable to another cluster — run step 3 for every new
> deployment.

### Step 4: Platform configuration (automatic)

No manual step is needed. The **Runtime Config** Application (application #17 above)
is already synced automatically by the root Application and deploys the actual live
platform configuration from `gitops/components/runtime-config/`:

- `PlatformConfig` (model registry, S3 buckets, GPU advisor, EvalHub, benchmark defaults)
- `ModelLifecycleProfile` (declared stage sequence: capacity → sandbox → promotion)
- `IntakeProviderConfig` (Tekton pipeline names, timeout/workspace defaults)
- SealedSecrets (scan/result S3 credentials, Zot push credential) and the EvalHub endpoint Secret

The sample files under `operator/config/samples/` are for **local development and
reference only** — they are not the deployed configuration and must not be applied
directly to any cluster managed by GitOps.

### Step 5: Check status

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

## New Cluster Bootstrap Checklist

When deploying to a fresh cluster, follow this order after prerequisites
(Step 1 and 1a in Quick Start; Steps 1b/1c are now automatic) are satisfied:

1. **Export your cluster domain**:
   ```bash
   export CLUSTER_DOMAIN=$(oc get ingresses.config.openshift.io cluster -o jsonpath='{.spec.domain}')
   echo "Cluster domain: ${CLUSTER_DOMAIN}"
   ```

2. **Set the cluster MaaS hostname** — Edit the single source in
   `gitops/components/maas/cluster-config.yaml`:
   ```bash
   CLUSTER_DOMAIN=$(oc get ingresses.config.openshift.io cluster -o jsonpath='{.spec.domain}')
   echo "maas hostname should be: maas.${CLUSTER_DOMAIN}"
   # Update data.maas-hostname in gitops/components/maas/cluster-config.yaml
   # to maas.${CLUSTER_DOMAIN}, then commit and push. The kustomize
   # replacement in kustomization.yaml injects it into the Gateway
   # listeners and the passthrough Route.
   ```

3. **ArgoCD UI RBAC is already handled** — the `argocd-config` Application (wave -1)
   commits `spec.rbac` to the operator-owned `openshift-gitops` ArgoCD CR,
   mapping cluster-admin groups to `role:admin` (and everyone else to
   `role:readonly`). A cluster-admin who logs into the UI already sees every
   Application. To grant a *non*‑cluster-admin group access, do **not** `oc patch`
   the ArgoCD CR — GitOps self-heals it back — instead append the group line to
   the committed policy in `gitops/components/argocd-config/patch-argocd.yaml`:

   ```yaml
   spec:
     rbac:
       policy: |
         g, system:cluster-admins, role:admin
         g, cluster-admins, role:admin
         g, admin, role:admin
         g, <YOUR_GROUP>, role:admin
   ```

   > **Why**: ArgoCD `scopes: [groups]` only checks OpenShift group membership.
   > If your group is not in the policy, you'll see an empty application list.

4. **Deploy root app** — ArgoCD syncs all apps in wave order:
   ```bash
   oc apply -f gitops/appproject.yaml
   oc apply -f gitops/root-app.yaml
   ```

5. **Wait for wave -1** (sealed-secrets controller up), then **seal credentials**
   — the committed SealedSecrets decrypt only on the sandbox cluster they were
   generated for. On a fresh cluster you MUST regenerate random values and
   re-seal against *this* cluster's controller before any consuming app syncs:
   ```bash
   oc wait --for=jsonpath='{.status.sync.status}'=Synced application/sealed-secrets -n openshift-gitops --timeout=600s
   oc wait --for=condition=Available deployment/sealed-secrets-controller -n kube-system --timeout=300s
   # Requires kubeseal 0.39.x + htpasswd + openssl; see "Step 3: Seal platform
   # credentials" for the full procedure. Rewrites gitops/components/*-sealedsecret.yaml.
   python3 gitops/scripts/seal-secrets.py
   # commit + push the regenerated SealedSecrets, then let the wave-0+ apps sync.
   ```

6. **Wait for wave 0** to complete:
   ```bash
   oc wait --for=jsonpath='{.status.sync.status}'=Synced application/maas-prereqs -n openshift-gitops --timeout=600s
   oc wait --for=jsonpath='{.status.sync.status}'=Synced application/maas-kuadrant -n openshift-gitops --timeout=600s
   oc wait --for=condition=Ready kuadrant/kuadrant -n kuadrant-system --timeout=300s
   ```

7. **Manual: Authorino TLS** (Step 2a) — service annotation, CR patch, env vars,
   Gateway TLS bootstrap annotation. See Step 2a in Quick Start for commands.

8. **Manual: Gateway namespace label** (Step 2d):
   ```bash
   oc label namespace redhat-ods-applications maas.opendatahub.io/gateway-access=true --overwrite
   ```

9. **Manual: Restart model controllers** (Step 2b):
   ```bash
   oc delete pod -n redhat-ods-applications -l app=odh-model-controller
   oc delete pod -n redhat-ods-applications -l control-plane=kserve-controller-manager
   ```

10. **Verify** (Step 2c):
    ```bash
    # Tenant Degraded condition should have cleared "no Authorino instances found"
    oc get tenant default-tenant -n models-as-a-service -o jsonpath='{.status.conditions[?(@.type=="Degraded")].message}'
    # Gateway health
    curl -sk "https://maas.${CLUSTER_DOMAIN}/maas-api/health"
    # Expected: {"status":"healthy"}
    ```

11. **Verify all apps synced**:
    ```bash
    oc get applications -n openshift-gitops
    ```

## Customization

Edit files in `gitops/components/` and push to the repo. ArgoCD automatically detects changes and syncs (when `automated.selfHeal` is enabled).

To rotate MinIO (or any other) credentials, run `gitops/scripts/seal-secrets.py`
against the target cluster (see Step 3). Do **not** hand-edit a `secretGenerator`
or commit a plaintext `Secret` — credentials are committed exclusively as
SealedSecrets, and `operator/internal/stages/tekton/plaintext_secrets_test.go`
fails the test suite if a plaintext `Secret` or `secretGenerator` literal is ever
reintroduced. This replaced the earlier pattern where `runtime-config/secrets.yaml`
(MinIO) and the MinIO/Zot `secretGenerator` blocks committed real credentials in
the clear — see `docs/PHASE_LOG.md` for the root cause and the migration.
