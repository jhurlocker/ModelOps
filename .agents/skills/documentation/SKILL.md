---
name: documentation
description: Maintain accurate vision, architecture, installation, samples, diagrams, limitations, and feature status.
---

# Documentation

The root README should explain the Enterprise AI Lifecycle Platform vision, implemented modules, resource architecture, execution providers, installation, limitations, and roadmap.

State that Model Intake is the first implemented lifecycle module.

Keep Go API types, generated CRDs, samples, controller behavior, UI fields, pipeline parameters, diagrams, and README instructions synchronized.

Do not describe unfinished features as complete.

Samples should avoid plaintext credentials, use Secret references, use pinned images, and demonstrate normal usage before expert overrides.

Current target flow:

```text
UI or API
  → ModelRequest
  → ModelLifecycleProfile + PlatformConfig
  → CapacityPlan
  → sandbox qualification
  → sequential promotion
  → registry, catalog, and access
```

## GitOps deployment documentation

When documenting GitOps deployment steps, document:
- Inter-Application dependencies and the sync-wave ordering that resolves them
- Retry policies and the async reconciliation they cover for
- Manual troubleshooting fallbacks for exhausted retries
- Operator resource tuning (e.g., ArgoCD controller memory) needed for cold bootstrap
- Kustomize gotchas: `patches` targeting resources not in the kustomization's `resources`
  list are silently dropped — such manifests must be listed as resources instead
- Any manual steps that cannot be expressed declaratively, with clear explanations
  of WHY they're manual (e.g., Authorino CR is auto-created by operator, Gateway
  namespace is RHOAI-managed)
- Cluster-specific values that need changing for new clusters (e.g., Route hostnames,
  domain patterns). Mark these with clear comments in the YAML files themselves.
- Operator install modes that constrain namespace placement (e.g., RHCL operator
  requires AllNamespaces → must go in openshift-operators)

## Checklist

- [ ] root README updated
- [ ] module README updated
- [ ] CR samples updated
- [ ] install steps verified
- [ ] architecture diagram updated
- [ ] limitations documented
- [ ] roadmap separated from implemented behavior
- [ ] inter-app dependencies and sync-waves documented in gitops/README.md
- [ ] manual troubleshooting fallback documented in gitops/README.md
- [ ] cluster-specific hostnames/domains documented with clear comments in YAML files
- [ ] manual-only steps (operator-managed resources) documented with rationale
- [ ] MaaS infrastructure dependency chain documented (operators, Gateway, Authorino TLS)
