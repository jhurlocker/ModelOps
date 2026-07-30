---
name: model-intake
description: Develop the governed Model Intake vertical slice from ModelRequest through qualification, approval, promotion, registration, and access.
---

# Model Intake

Expected flow:

```text
ModelRequest
  → profile and platform configuration resolution
  → CapacityPlan
  → artifact, license, provenance, and vulnerability checks
  → sandbox deployment
  → behavioral and infrastructure evaluation
  → approval
  → sequential promotion
  → registry or catalog publication
  → access assignment
```

`ModelRequest` should represent model identity, lifecycle profile, workload requirements, promotion targets, access requirements, credential references, and authorized overrides.

Do not place platform endpoints, ports, workspace names, raw credentials, or internal Task names in normal request fields.

Profiles determine required checks, approval policy, promotion sequence, providers, defaults, locked fields, and permitted overrides.

Retain evidence for source revision, license, checksums, image digests, vulnerabilities, evaluations, capacity, benchmarks, approvals, releases, and deployments.

## Completion criteria

- [ ] concise request API
- [ ] correct profile and platform resolution
- [ ] working CapacityPlan lifecycle
- [ ] secure Secret references
- [ ] sandbox qualification
- [ ] sequential promotion
- [ ] understandable status
- [ ] pinned images
- [ ] end-to-end reconciliation tests
- [ ] current samples and documentation
