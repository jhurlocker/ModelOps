# Enterprise AI Lifecycle Platform — Agent Instructions

This repository is evolving from governed model onboarding into an open, Kubernetes-native Enterprise AI Lifecycle Platform.

## Core principles

- Kubernetes custom resources are the stable lifecycle API.
- Controllers perform idempotent reconciliation and orchestration.
- Tekton, Kubernetes Jobs, Kubeflow, and external services are execution providers.
- Significant domain logic belongs in tested, versioned container images.
- Tekton Tasks should remain declarative and thin.
- Separate user intent, lifecycle policy, platform configuration, derived configuration, and execution details.
- Never store plaintext credentials in custom resources.
- Never pass raw secret values through PipelineRun parameters when Secret references can be mounted or injected.
- Prefer immutable image versions or digests over `latest`.
- Preserve a working Model Intake vertical slice while evolving incrementally.
- Do not claim a feature is complete unless APIs, controllers, status, tests, examples, and documentation agree.

## Skill routing

Read only the relevant skills under `.agents/skills/`:

- `lifecycle-architecture`
- `controller-development`
- `tekton-development`
- `tool-development`
- `model-intake`
- `capacity-planning`
- `promotion`
- `security-governance`
- `ui-development`
- `testing-validation`
- `documentation`

## Before editing

1. Inspect the current implementation and related tests.
2. Classify the change as user intent, lifecycle policy, platform configuration, derived configuration, or execution implementation.
3. State the intended resource flow.
4. Implement the smallest coherent vertical change.
5. Add or update tests.
6. Regenerate manifests when API types change.
7. Update samples and documentation.
8. Report limitations and follow-up work.

## Global gotchas

- Requests select lifecycle profiles, not internal Tekton pipeline names.
- Promotion is sequential unless a profile explicitly permits parallel execution.
- Controllers orchestrate; they do not perform scans, benchmarks, GPU calculations, or deployment logic.
- Capacity planning remains independently reusable.
- Secret names may appear in resources; secret values must not.
- Do not hard-code cluster-specific namespaces, endpoints, workspaces, service accounts, or credentials in controllers.
- Do not create a CRD for every technical step.
- Do not duplicate child resources after controller restarts.
- Users should not need to inspect TaskRuns to understand lifecycle status.
