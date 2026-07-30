---
name: controller-development
description: Implement and review Kubernetes reconciliation, ownership, conditions, finalizers, child resources, and controller-runtime tests.
---

# Controller Development

Use this skill for Go operators and Kubernetes reconciliation.

## Responsibilities

A controller should read desired state, resolve references, inspect child resources, create only what is needed, observe execution, update conditions, and recover safely after restarts.

Controllers should not directly perform long-running scans, GPU sizing, benchmark execution, deployment shell logic, or substantial domain processing.

## Idempotency

Use deterministic child names or stable labels. Check for existing children before creation. Use owner references where lifecycle ownership is appropriate. Use finalizers only for required external cleanup.

## Status

Prefer Kubernetes conditions containing `type`, `status`, `reason`, `message`, `observedGeneration`, and `lastTransitionTime`.

Useful conditions include `Accepted`, `ProfileResolved`, `CapacityPlanned`, `SecurityApproved`, `EvaluationPassed`, `AwaitingApproval`, `Promoted`, `Ready`, and `Failed`.

## Gotchas

- Never duplicate children after restart.
- Never start production before required earlier environments succeed.
- Never place secret values in PipelineRun parameters.
- Never overwrite newer status with stale observations.
- Distinguish terminal failures from transient dependency failures.
- Avoid long synchronous external calls in reconciliation.

## Validation

- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] envtest coverage
- [ ] duplicate reconciliation
- [ ] restart recovery
- [ ] missing references
- [ ] failed and timed-out child resources
- [ ] deletion/finalizer behavior
- [ ] status transitions
- [ ] sequential promotion
- [ ] generated CRDs and deepcopy code are current

Read `references/reconcile-checklist.md` before implementing a new reconciler.
