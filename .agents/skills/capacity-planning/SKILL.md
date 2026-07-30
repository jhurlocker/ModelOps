---
name: capacity-planning
description: Implement infrastructure-aware CapacityPlan behavior, GPU discovery, vLLM recommendations, and serving-topology advice.
---

# Capacity Planning

Capacity planning is a reusable lifecycle capability, not merely one pipeline Task.

Inputs may include model architecture, precision, context length, concurrency, request rate, latency objectives, GPU inventory, topology, isolation requirements, MIG or time-slicing policy, and provider capabilities.

Outputs may include GPU type and count, parallelism, memory feasibility, vLLM arguments, placement constraints, isolation recommendation, assumptions, confidence, and infeasibility reasons.

The parent controller creates or observes `CapacityPlan`. A dedicated controller, Job, PipelineRun, or advisor performs the analysis.

Do not embed GPU calculations in the ModelRequest controller.

Override precedence:

```text
authorized request override
  else profile policy
  else advisor recommendation
  else platform default
```

Record inventory references, advisor version, image digest, assumptions, recommendation, rejected alternatives, and SLO inputs.

## Validation

- [ ] insufficient capacity
- [ ] multi-GPU recommendation
- [ ] shared-GPU policy
- [ ] dedicated isolation
- [ ] override precedence
- [ ] stale inventory
- [ ] restart safety
- [ ] useful conditions
