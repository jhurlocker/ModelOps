# Review Conventions

This captures the review discipline used throughout this project's development so far. Read this alongside `docs/REFACTOR_PLAN.md`, `docs/PHASE_LOG.md`, `docs/REVIEW_RESPONSE_PLAN.md`, and `docs/SHIM_CONTRACT.md` when picking this project back up in a new session — those files carry the *what* and *why* of every decision; this one carries the *how* of reviewing new work.

## Core principle: verify against the actual repository, never trust a summary at face value

Every phase in this project has been reviewed by pulling the real branch and checking claims directly — reading the actual diff, grepping for the actual field, and where possible actually running the actual test suite — rather than accepting a session's self-description of what it built. This has caught real, concrete problems every time it was applied, and missed real problems the couple of times it wasn't applied thoroughly enough. Treat "the summary says X" as a claim to check, not a fact to record.

**Worked example, from this project's actual history:** Phase B added `CheckResults` to the internal `stagecommon.StageStatus` type. The review caught that `CheckResults` was never threaded through to `api/v1alpha1.StageProgress` — the type actually persisted into `ModelRequest.Status` — meaning the field would be computed correctly and then silently dropped before `kubectl` could ever show it. This was only caught by reading the actual `StageProgress` struct and the actual copy site, not by reading the design proposal's prose. The identical gap was independently rediscovered later for a different field (`DetailsURL`) — this class of bug (a value computed on an internal contract type that never reaches the persisted API) is real, recurring, and worth specifically checking for on every phase that adds a field to `StageStatus`.

## When to require a design-review-first pass vs. direct implementation

Architecturally significant work gets a proposal-first pass before any code is written: new abstractions (the `StageRunner`/`StageHandler` split, `WebhookProviderConfig`, the shim contract), anything touching secret handling, anything changing a shared status contract, anything that's a breaking API change. Low-risk, mechanical work (internal refactors with no external contract change, dead-code cleanup, documentation fixes) goes straight to implementation. When in doubt, err toward requiring the proposal — it's cheap to review a design and expensive to unwind a shipped one.

## Standing checks to run on every phase, not just the first one

- **Modularity**: grep every `internal/stages/*` package to confirm none imports another. This has stayed clean through every phase so far — keep checking it every time, not just when a new package is added.
- **RBAC**: spot-check that any new Kubernetes API call has a corresponding, and only the necessary, RBAC marker. This caught a real gap once (a required `create` on `secrets` that wasn't initially requested) and confirmed a design goal once (webhook's RBAC footprint being the narrowest of any `StageRunner`).
- **Backward compatibility**: for any schema change, confirm a golden-value/wire-format test proves existing objects are unaffected. This project has held a strict non-breaking bar since very early on; every additive change has needed to prove it explicitly, not just assert it.
- **Full-path survival for new status fields**: per the worked example above, any new field added to `stagecommon.StageStatus` needs an explicit check that it's threaded through to `api/v1alpha1.StageProgress` and the copy site that builds it, with a test proving survival end to end.

## Distinguish "logic is correct" from "the real thing works"

This project has hit two separate, concrete bugs that only existed at the boundary between correct logic and real infrastructure — an SSL trust failure (correct code, wrong CA bundle mount) and an HTTP response-parsing panic on chunked transfer encoding (correct logic, wrong assumption about `Content-Length`). Both were invisible to unit tests and to `envtest`, because both test types run against fakes or in-process servers that don't reproduce the actual failure mode. A test suite passing — even a thorough one, even a three-way parity test proving an abstraction holds — is evidence the *logic* is correct. It is not evidence the *real* network path, the *real* TLS handshake, or the *real* HTTP client behavior works. When a piece of work makes real outbound calls (an HTTP client, a new execution provider), treat real-infrastructure verification as a required step, not an optional nice-to-have layered on top of unit tests.

## Tooling notes specific to this environment

- Go module dependencies generally can't be fetched in a sandboxed review environment (no `proxy.golang.org` access) — Go work has mostly been verified by static reading (`grep`, reading actual struct definitions and call sites) rather than `go build`/`go test`. Treat this as a real limitation, not a formality — recommend an actual `go build && go test ./...` run in an environment with real module access before treating Go-side work as fully proven.
- Python dependencies via `pypi.org` generally *are* fetchable — the shim catalog's test suites were actually installed and run for real (`pip install`, `pytest`), which is meaningfully stronger verification than static reading. Prefer actually running Python-side work when possible, since the tooling allows it.
- `gofmt` drift has been a low-priority, recurring, occasionally-forgotten item across several phases — worth a periodic `gofmt -w .` sweep rather than letting it accumulate.

## On session continuity itself

This project has deliberately pushed every durable decision into files in `docs/` rather than relying on conversation memory, specifically so a dropped connection or a fresh session doesn't lose anything that matters. That discipline held up under a real test: a connection was cut mid-phase, a new session picked the work back up, and because the guiding principles and phase history lived in committed files rather than chat history, nothing substantive was lost — though it did surface that a design decision not yet written to a durable file (a specific syntax convention, in that case) can silently regress across a session boundary even when the underlying capability doesn't. Keep committing decisions to `docs/` as they're made, not just at the end of a phase.