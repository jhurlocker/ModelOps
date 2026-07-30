---
name: promotion
description: Implement safe sequential promotion across sandbox, staging, preproduction, and production.
---

# Promotion

Default sequence:

```text
sandbox qualification
  → staging
  → preproduction
  → production
```

Unless a profile permits parallel execution, create only the next eligible promotion resource or PipelineRun.

For each environment:

```text
not found → create and return
running   → wait and return
failed    → mark failed and return
succeeded → inspect the next environment
```

Each environment may have separate approvals, capacity requirements, providers, evidence, benchmarks, access policy, and rollback behavior.

Promotion status should identify the current environment, completed environments, pending environment, approval state, execution reference, failure reason, and release version.

## Tests

- [ ] staging starts first
- [ ] production is not created early
- [ ] next stage starts after success
- [ ] failure blocks later stages
- [ ] restart does not duplicate runs
- [ ] approval is enforced per profile
- [ ] status shows the current stage
