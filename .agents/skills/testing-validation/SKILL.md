---
name: testing-validation
description: Validate Go controllers, tools, Tekton, UI, generated manifests, security boundaries, and documentation consistency.
---

# Testing and Validation

## Go

Run as applicable:

```bash
go test ./...
go vet ./...
make generate
make manifests
```

Use envtest for state transitions, duplicate reconciliation, restart recovery, missing references, failures, timeouts, deletion, finalizers, sequential promotion, and generation changes.

## Tools

Run unit, CLI, malformed-input, structured-output, failure-exit-code, lint, type, non-root, and credential-leakage tests.

## Tekton

Verify parameter declarations, workspace bindings, success and failure gates, missing dependencies, pinned images, result format, and absence of secret values in PipelineRun specs.

## UI

Verify profile defaults, locked fields, standard and expert modes, generated resources, validation errors, Secret references, and status rendering.

## Completion report

Report changed files, resource flow, commands run, security implications, limitations, and follow-up work.

Never claim tests passed unless they were actually run successfully.
