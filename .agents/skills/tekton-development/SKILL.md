---
name: tekton-development
description: Develop and review Tekton Tasks, Pipelines, PipelineRuns, workspaces, results, and secure execution boundaries.
---

# Tekton Development

Tekton should sequence execution, declare parameters, bind workspaces, mount Secrets and ConfigMaps, invoke versioned tools, publish concise results, and enforce gates.

Tekton should not contain substantial Python, GPU sizing algorithms, policy engines, registry clients, report generation, or complex state machines.

## Task design

Prefer small reusable Tasks such as resolving artifacts, scanning images, evaluating policy, publishing evidence, and registering releases. Avoid combining unrelated capabilities in one oversized Task.

## Secrets

Pass Secret names or mount Secrets. Never pass secret contents as PipelineRun parameters.

## Images

Pin semantic versions or image digests. Do not use `latest` in governed workflows. Ensure images run as non-root under OpenShift restricted security constraints.

## Results

Use results for small values such as evidence URIs, digests, recommendation IDs, pass/fail state, and release identifiers. Store large reports in an artifact store.

## Checklist

- [ ] Every parameter is declared once.
- [ ] Workspaces are consistently bound.
- [ ] Secret values do not appear in PipelineRun specs.
- [ ] Tasks are reusable and capability-focused.
- [ ] Domain logic lives in a containerized tool.
- [ ] Images are pinned.
- [ ] Results are small and structured.
- [ ] Sequential gates are enforced.
- [ ] Success and failure paths are tested.
