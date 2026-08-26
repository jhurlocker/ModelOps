---
name: lifecycle-architecture
description: Design and review platform boundaries, lifecycle APIs, modules, profiles, providers, and first-class lifecycle resources.
---

# Lifecycle Architecture

Use this skill when changing platform APIs, defining module boundaries, or reviewing architectural alignment.

## Platform role

The platform is a lifecycle control plane. It orchestrates OpenShift AI, KServe, Tekton, Kubeflow, model registries, evaluation systems, observability, storage, and governance providers.

Preferred flow:

```text
User or API
  → lifecycle custom resource
  → controller
  → child lifecycle resource or execution provider
  → PipelineRun, Job, Kubeflow run, or external service
  → durable status and evidence
```

## GitOps Kustomize gotchas

When a Kustomize `patches` entry targets a resource not present in the
kustomization's `resources` list (e.g., patching a cluster-scoped
`DataScienceCluster` owned by an external operator), the patch is silently
dropped from `kustomize build` output. Such patches must be listed as
`resources` instead — they apply as strategic merge patches against the
existing cluster object when ArgoCD or `oc apply` processes them.

When modifying resources that are owned by external operators and
asynchronously reconciled (e.g., RHOAI's DataScienceCluster), pair the
change with an ArgoCD retry policy so the downstream Application can
withstand the reconciliation delay.

## Models-as-a-Service infrastructure dependency chain

MaaS requires a specific infrastructure stack that must be deployed in
order. Each layer has async readiness delays — the retry policy pattern
from the DataScienceCluster patching applies here too, multiplied across
three operators.

### Required operators (RHOAI 3.4+)

| Operator | Package | Channel | Namespace | Install Mode |
|----------|---------|---------|-----------|-------------|
| Red Hat Connectivity Link | `rhcl-operator` | `stable` | `openshift-operators` | AllNamespaces |
| Leader Worker Set | `leader-worker-set` | `stable-v1.0` | `openshift-lws-operator` | OwnNamespace |
| cert-manager for Red Hat OpenShift | `openshift-cert-manager-operator` | `stable-v1` | `cert-manager-operator` | (prerequisite, often pre-installed) |

The RHCL operator **must** go in `openshift-operators` because it only
supports `AllNamespaces` install mode. Installing it in `kuadrant-system`
or any single namespace will fail.

### Resource creation order

```text
RHCL Subscription
  → kuadrant-operator pod running
    → Kuadrant CR (kuadrant-system)
      → Authorino CR auto-created by Kuadrant operator
      → Limitador CR auto-created
        → Authorino TLS config (manual — service annotation, CR patch, env vars)
          → Gateway TLS bootstrap annotation
            → Tenant re-reconciliation
```

### ArgoCD Application split pattern

Resources that depend on a CRD registered by an operator must live in a
**separate Application** from the operator Subscription. ArgoCD validates
all resources in a single sync operation against the cluster's current CRD
set — if the CRD isn't registered yet, the validation fails and the
resource can't be created. The retry policy handles this only if the
failing resource is in its own Application.

Pattern:
```text
Application A (wave -1): operator Subscription + namespace
Application B (wave 0):  CR that depends on CRDs from wave -1
```

Both carry retry policies (20 attempts, 10s→5m exponential backoff).

### Gateway architecture (MaaS)

The MaaS Gateway routes API traffic from the dashboard to the maas-api:

```text
Dashboard maas-ui sidecar (:8243)
  → https://maas.apps.<cluster>/maas-api/v1/*
    → OpenShift Router (re-encrypt Route, service-ca TLS)
      → maas-default-gateway (HTTPS :443, TLS terminate)
        → HTTPRoute (hostname: maas.apps.<cluster>)
          → maas-api Service (:8443)
```

The Gateway requires:
1. An HTTPS listener with TLS termination using a service-ca certificate
2. An OpenShift re-encrypt Route for `maas.apps.<cluster-domain>`
3. A `maas-gw-options` ConfigMap annotating the Gateway Service for service-ca
4. The `redhat-ods-applications` namespace labeled with `maas.opendatahub.io/gateway-access=true` (RHOAI-managed namespace — manual step)

Route hostnames are cluster-specific (pattern: `maas.apps.<cluster-domain>`).
For new clusters, update `gitops/components/maas/gateway-route.yaml`.

## Lifecycle scope

Design for eventual support of Model Intake, Catalog, Application Development, Evaluation, Promotion, Production Operations, Optimization, Fine-Tuning, Governance, Lineage, AI BOM, and Retirement.

Model Intake is the first implemented module. Avoid unfinished breadth at the expense of a reliable first vertical slice.

## Configuration ownership

| Category | Examples | Owner |
|---|---|---|
| User intent | model URI, use case, context length, concurrency | Requester |
| Lifecycle policy | scans, approval rules, promotion sequence | Governance/platform admin |
| Platform configuration | namespaces, providers, stores, service accounts | Platform admin |
| Derived configuration | GPU count, vLLM flags, placement | Controller/advisor |
| Execution implementation | Task names, images, workspaces | Provider/profile |

## First-class resource test

Create a CRD only when the concept has independent lifecycle meaning, durable status, policy significance, reuse across workflows, or meaningful user queries.

Good candidates include `ModelRequest`, `ModelLifecycleProfile`, `PlatformConfig`, `CapacityPlan`, `SecurityAssessment`, `EvaluationRun`, `ApprovalRequest`, `ModelRelease`, `PromotionRequest`, `AIApplication`, `OptimizationRequest`, and `RetirementPlan`.

## API guidance

Prefer domain-oriented fields such as `lifecycleProfile`, `artifactStoreRef`, `deploymentProviderRef`, `credentialRef`, `requirements`, and `promotionTargets`.

Avoid exposing implementation fields such as `tektonPipelineName`, `minioEndpoint`, `garakTaskName`, or `registryPort`.

## Checklist

- [ ] Public API expresses lifecycle intent.
- [ ] Policy is reusable and versionable.
- [ ] Platform configuration is administrator-owned.
- [ ] Derived values are calculated.
- [ ] Provider-specific details are isolated.
- [ ] Design remains modular.
- [ ] Working flows are preserved or deliberately migrated.
- [ ] Status and evidence are durable.
- [ ] Documentation separates implemented capabilities from roadmap items.

Read `references/lifecycle-model.md` when changing the overall lifecycle or resource graph.
