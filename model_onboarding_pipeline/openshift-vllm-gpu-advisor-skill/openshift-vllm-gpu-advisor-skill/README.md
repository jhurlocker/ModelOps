# OpenShift vLLM GPU Advisor Skill Package

This package contains an all-in-one `SKILL.md` plus optional modular subskills for an LLM onboarding pipeline.

## Files

```text
SKILL.md
subskills/gpu-discovery/SKILL.md
subskills/model-sizing/SKILL.md
subskills/vllm-recommendation/SKILL.md
examples/input.example.yaml
```

## Recommended use

Use the root `SKILL.md` when your agentic skill runtime expects one skill file.

Use the modular subskills when your runtime supports delegation or composition:

1. `gpu-discovery` scans OpenShift GPU resources.
2. `model-sizing` estimates model and KV cache memory needs.
3. `vllm-recommendation` generates ranked vLLM deployment options.

The skill is intentionally read-only by default and is designed to produce recommendation artifacts for a ModelOps approval gate.
