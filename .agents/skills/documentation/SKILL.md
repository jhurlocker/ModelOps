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

## Checklist

- [ ] root README updated
- [ ] module README updated
- [ ] CR samples updated
- [ ] install steps verified
- [ ] architecture diagram updated
- [ ] limitations documented
- [ ] roadmap separated from implemented behavior
