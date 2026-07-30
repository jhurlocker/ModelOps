---
name: ui-development
description: Design a usable lifecycle UI with profiles, progressive disclosure, expert overrides, review, and lifecycle-focused status.
---

# UI Development

Preserve configurability while separating settings by ownership.

Normal flow:

1. Model identity
2. Workload requirements
3. Governance profile
4. Review and submit

Normal requester fields include model source, identifier, lifecycle profile, justification, context length, concurrency, SLOs, promotion targets, access groups, and deployment intent.

Show policy-derived fields as inherited, locked, or authorized overrides.

Hide pipeline overrides, endpoints, GPU-count overrides, raw Helm values, Secret names, runtime images, namespaces, and service accounts in expert or admin views protected by RBAC.

Profiles should communicate reusable policy packages such as standard generative, regulated generative, predictive, and fast sandbox.

Before submission, show a human-readable resolved plan and an optional generated Kubernetes resource preview.

Users should see lifecycle stages and useful reasons, not raw TaskRun details.

## Tests

- [ ] basic mode
- [ ] expert mode
- [ ] profile defaults
- [ ] locked fields
- [ ] override authorization
- [ ] generated CR
- [ ] validation messages
- [ ] Secret selection by reference
- [ ] understandable status
