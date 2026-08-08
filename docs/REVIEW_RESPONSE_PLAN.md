# Response Plan — External Review Findings (2026-08-03)

## Context

An external review of `feat/model-request-controller` surfaced several findings, independently verified against the actual current code before this plan was written (not all of the review's claims held up — see the "stale findings" note below). This plan organizes the real findings into phases, following the same guiding principles as `docs/REFACTOR_PLAN.md` (TDD, no cross-stage imports, GitOps as source of truth with sandbox-cluster testing). Read that file's "Three non-negotiable guiding principles" section before starting any phase here — it applies to this plan too, not just the original one.

**A note on the review itself**: two of its UI findings (the "916-line monolithic `app.py`" and the "UI only lists its own requests" claims) turned out to describe a **dead, undeployed file** left over from before this project's own UI-modularization commit — the actually-deployed `app/` package (verified via `Containerfile`/`wsgi.py`) already doesn't have those problems. Don't re-do work that's already done; Phase 10 below just deletes the stale file so this doesn't confuse the next reviewer too.

---

## Phase 8 — Secret handling hardening (do first; this is the real blocker)

Two related, verified defects:

1. **EvalHub secret bug**: `resolveSecrets` reads the `url` key from the EvalHub Secret and writes it into `s.scanS3Endpoint` (wrong field, unrelated concern). It never reads the `token` key at all — `s.evalhubToken` is unconditionally overwritten by a freshly generated service-account token, silently discarding any token an operator actually configured.

2. **Secret values leak into `PipelineRun.spec.params`**: `sandbox/handler.go` builds Tekton params directly from resolved secret values (`sc.Secrets.ScanS3AccessKey`, `sc.Secrets.ScanS3SecretKey`, etc.) via `stagecommon.AddParam`. These end up as plaintext in `PipelineRun.spec.params`, readable by anyone with permission to read `PipelineRun` objects — a materially larger RBAC surface than "can read the Secret directly." Phase 1 removed plaintext credentials from `ModelRequestSpec`; this is the same problem one layer downstream, not yet fixed by that work.

This phase changes how credentials flow through `StageContext`/`StageSpec`, which is architecturally significant — it gets a design-review-first pass, same as Phases 4–7.

---

## Phase 9 — Namespace RBAC governance (new capability; design-review-first)

Real, previously-unaddressed gap: `ensurePromotionNamespaceRBAC` will provision a `ServiceAccount` and `RoleBinding`s in *any* namespace a `ModelRequest`/profile names as a promotion target, with no check against an org-approved allowlist. A typo'd or malicious `promotionNamespaces` entry can currently cause real RBAC creation in a namespace nobody pre-approved.

---

## Phase 10 — Cleanup and honesty pass (lower risk; mostly direct implementation, no design review needed)

Several independent, smaller items. Can be done as one phase, straight to implementation - no proposal-first step needed given the low architectural risk, but still write tests first per the standing TDD principle.

1. **Delete the dead root `app.py`** in `model_onboarding_pipeline/model-intake-ui/` (979 lines, SQLite monolith). Confirm nothing references it (Containerfile/wsgi.py/deployment.yaml already confirmed to only use the `app/` package) before deleting, then remove it. This is what caused the external review to (incorrectly, but understandably) conclude the UI hadn't been modularized.

2. **Label the CapacityPlan heuristic honestly.** Per the review's "Option A": rename or clearly document the current implementation as a placeholder (e.g. `StaticCapacityEstimator` in comments/status messages, not necessarily a full Go type rename if that's disruptive - propose which is less invasive) rather than implying it does real GPU-inventory-aware capacity planning. This should be a small change; don't scope-creep into building the real provider-driven capacity advisor (that's out of scope for this phase, same reasoning as the MaxGPUsPerRequest decision in Phase 7 - log it as a tracked future item if not already logged there).

3. **Add deprecation doc-comments** to `ModelRequestStatus.PipelineRunName`/`SandboxPipelineRunName`/`PromotionPipelineRunName`, along these lines: "Retained for compatibility with the Tekton-based reference implementation. Consumers should prefer Stages[] for provider-independent lifecycle tracking." Do not remove or restructure these fields - this is documentation only, consistent with Phase 6/7's decision to keep them as reference-implementation convenience fields.

4. **Verify (don't blindly redo) items 10 and 11 from the review** - the README/module-documentation staleness complaints. Check whether the README rewrite already completed (the one with the "Current Scope: One Stage of a Larger Lifecycle" section) already disambiguates "model release promotion" from "AI application promotion." If it does, no action needed. If the module-level docs under model_onboarding_pipeline/ (e.g. README.adoc) still describe a stale fixed two-phase Tekton workflow, update those specifically - don't touch the root README again.

5. **Add one tracked backlog note to docs/REFACTOR_PLAN.md**, not implemented this phase: separating a stage's lifecycle semantic type (e.g. "this is a security assessment") from its execution engine (Kind, which currently conflates the two). Explicitly note that Phase 6 deliberately rejected making Kind a validated CRD enum, and that decision stands - this note is about adding a separate semantic-type concept alongside Kind/ProviderConfigRef, not about validating Kind itself.

6. Explicitly do NOT implement, this phase or as a new phase without a
   separate design review: stage dependency DAGs, per-stage retry/
   timeout policies, or cancellation behavior. These are reasonable
   long-term ideas from the review but are real scope, not cleanup -
   add them as backlog notes in docs/REFACTOR_PLAN.md if not already
   captured, and revisit only when there's an actual need driving them.

## Phase A — WebhookProviderConfig: install-time-extensible stage execution

**Why this phase exists:** every provider abstraction built so far (`StageRunner`, `IntakeProviderConfig`) requires writing Go code and recompiling this operator to add a new backend — which contradicts the project's founding premise that a user can swap in their own tooling without forking the codebase. This phase closes that gap for the most common case (an HTTP+JSON API) with one generic, built-once `StageRunner` that's infinitely extensible through CRD instances alone.

**Scope, stated precisely:**
- IN: one new `Kind: Webhook`, one Go `StageRunner` implementation, driven entirely by `WebhookProviderConfig` instances.
- OUT: a `Job`-based runner for arbitrary scripts/containers (separate future phase, already scoped in prior discussion, not this one).
- OUT: `WebhookMonitorConfig`/`ModelMonitor` (monitoring's contract is fundamentally different — non-terminal vs. terminal — and forcing it into this phase would repeat the mistake already avoided once with `PlatformConfig`).
- OUT: callback-based status delivery — polling only for v1.
- The shared HTTP-calling mechanism should be factored so a future `WebhookMonitorConfig` *can* reuse it later, but building that consumer is out of scope now.

## Phase B — Check-type stage decomposition (combined and separated modes)

**Depends on:** Phase A merged. Some check stages in the decomposed example below use `kind: Webhook`.

**Why this phase exists:** today's sandbox validation bundles compliance scan, security scan, and benchmarking into one opaque Tekton pipeline — one `PipelineRun`, one status. An org can't independently require, skip, or swap the tool behind any single check without editing pipeline YAML. This phase makes each check independently governable *when an org wants that*, while still allowing them to be run as one combined pipeline *when an org wants that instead* — both are valid instances of the same schema, not two different mechanisms to build and maintain.

**Scope, stated precisely:**
- IN: `checkTypes` as a validated, enum-backed list field on `ProfileStageSpec`.
- IN: support for both shapes from the same schema — one stage entry claiming multiple `checkTypes` (combined), or multiple stage entries each claiming one (decomposed).
- IN: optional structured per-check evidence extraction (`checkResults`) for the combined case, so granular *evidence* isn't lost even when granular *control* is deliberately traded away.
- OUT: forcing every existing profile to migrate to decomposed stages. The live `standard-generative-onboarding` profile can keep its combined shape; decomposition is opt-in per profile, same non-breaking bar held throughout this project.
- OUT: actually splitting `sandbox-pipeline.yaml` into separate Tekton pipelines. That's real, separate pipeline-authoring work an org would do for their own profile if they want a decomposed, Tekton-backed version — this phase makes the CRD schema and walker support it, not migrate the reference implementation's own pipeline.