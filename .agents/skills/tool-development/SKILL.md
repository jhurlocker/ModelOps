---
name: tool-development
description: Build reusable containerized CLI capabilities for scanning, evaluation, capacity planning, registry operations, and evidence handling.
---

# Tool Development

Preferred layout:

```text
tools/<capability>/
├── src/
├── tests/
├── pyproject.toml
├── Containerfile
└── README.md
```

Expose stable CLIs such as:

```text
modelops-capacity plan
modelops-security scan
modelops-evaluation run
modelops-registry publish
```

## Rules

- Keep domain logic independent of Tekton.
- Accept explicit inputs through CLI arguments, files, or environment variables.
- Emit structured JSON for machine-readable output.
- Return non-zero exit codes for failures.
- Validate required inputs.
- Avoid hidden cluster assumptions.
- Never log credentials or complete signed URLs.
- Support local testing where practical.

## Containers

Run as non-root, support arbitrary OpenShift UIDs, avoid requiring writes to the image filesystem, pin dependencies, generate an SBOM where practical, and use immutable releases.

## Testing

- [ ] unit tests
- [ ] CLI tests
- [ ] structured-output tests
- [ ] failure-exit-code tests
- [ ] malformed-input tests
- [ ] type checking and linting
- [ ] non-root execution
- [ ] no credential leakage
