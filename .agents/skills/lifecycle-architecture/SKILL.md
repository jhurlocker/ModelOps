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
