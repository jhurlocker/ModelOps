# ModelOps Agent Skills Pack

This package contains a concise root `AGENTS.md` and focused skills under `.agents/skills/`.

## Install

Copy both items into the root of the ModelOps repository:

```bash
cp -R AGENTS.md .agents /path/to/ModelOps/
```

## Example session prompt

```text
Read AGENTS.md and the applicable skills under .agents/skills before editing.

Inspect the current implementation and tests. Explain the intended resource flow,
implement the smallest coherent change, run the required validation, and update
samples and documentation.
```

For promotion work:

```text
Read AGENTS.md, controller-development, promotion, testing-validation, and documentation.

Make promotion environments strictly sequential. Preserve idempotency and restart
safety, add envtest coverage, and do not pass secret values through PipelineRun parameters.
```

Some agents expect a different skills directory. Keep `AGENTS.md` at the repository root and move or copy the skill folders to the supported path if necessary.
