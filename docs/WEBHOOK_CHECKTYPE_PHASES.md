# Phases — WebhookProviderConfig and Check-Type Stage Decomposition

Append these two phases to `docs/REVIEW_RESPONSE_PLAN.md`, in this order. Phase B depends on Phase A — it references `WebhookProviderConfig` as one of the provider kinds a decomposed check can use, and the "swap in your own tool without forking this repo" value proposition it's really aiming for is only fully real once Phase A exists.

---

## Phase A — WebhookProviderConfig: install-time-extensible stage execution

**Why this phase exists:** every provider abstraction built so far (`StageRunner`, `IntakeProviderConfig`) requires writing Go code and recompiling this operator to add a new backend — which contradicts the project's founding premise that a user can swap in their own tooling without forking the codebase. This phase closes that gap for the most common case (an HTTP+JSON API) with one generic, built-once `StageRunner` that's infinitely extensible through CRD instances alone.

**Scope, stated precisely:**
- IN: one new `Kind: Webhook`, one Go `StageRunner` implementation, driven entirely by `WebhookProviderConfig` instances.
- OUT: a `Job`-based runner for arbitrary scripts/containers (separate future phase, already scoped in prior discussion, not this one).
- OUT: `WebhookMonitorConfig`/`ModelMonitor` (monitoring's contract is fundamentally different — non-terminal vs. terminal — and forcing it into this phase would repeat the mistake already avoided once with `PlatformConfig`).
- OUT: callback-based status delivery — polling only for v1.
- The shared HTTP-calling mechanism should be factored so a future `WebhookMonitorConfig` *can* reuse it later, but building that consumer is out of scope now.

### Kickoff prompt

```
Read docs/REFACTOR_PLAN.md, docs/PHASE_LOG.md, and docs/REVIEW_RESPONSE_PLAN.md
before starting.

New phase - WebhookProviderConfig. This is a significant architectural
addition (the project's first genuinely install-time-extensible
provider mechanism), so it gets a full design-review-first pass, at
least as rigorous as Phase 6's.

Do NOT write any implementation code yet. Propose:

1. The WebhookProviderConfig CRD schema. Must include: submitEndpoint,
   method, authSecretRef (secretKeyRef-only per the Phase 8 pattern -
   never an inline credential field), requestTemplate (Go template,
   rendered from the same StageContext already threaded through every
   other handler), statusEndpoint (templated, for polling), and a
   statusMapping block: phaseJsonPath, phaseValueMap (translates the
   provider's own vocabulary into Running/Succeeded/Failed),
   messageTemplate, and detailsUrlTemplate (a human-facing link out to
   the provider's own console/logs - not a structured step/log
   representation - native platforms always have a richer debugging
   surface than anything we'd reconstruct generically).

2. Factor the actual HTTP-calling mechanism (request templating,
   JSONPath extraction, auth header construction, polling loop) into a
   shared package (internal/webhookcore was floated as a placeholder
   name). Design its internal interfaces generically enough that a
   future, NOT-built-this-phase consumer (a monitoring-side
   WebhookMonitorConfig, mapping to a MonitorStatus shape instead of
   StageStatus) could plug into the same underlying plumbing without
   this package needing to change. Do not build that second consumer -
   just don't accidentally bake a terminal-three-phase assumption into
   code that has no business knowing about StageStatus specifically.

3. webhook.StageRunner implementing the existing StageRunner interface,
   consuming webhookcore, calling submitEndpoint once per EnsureRun
   when no execution reference exists yet, polling statusEndpoint on
   subsequent reconciles, mapping the response into StageStatus per
   the statusMapping config. Confirm how the "job ID" or execution
   reference returned by submitEndpoint gets persisted across
   reconciles (likely: written into StageStatus.RunRef the same way
   every other runner already does this).

4. Template safety: propose an explicit, minimal allowlist of Go
   template functions available in requestTemplate/messageTemplate/
   detailsUrlTemplate - no arbitrary code execution, and explicitly
   verify no path exists for a value to leak a Secret's contents into
   Message or any other field that ends up in ModelRequest.Status
   (which has a much broader read audience than the Secret itself).
   Same scrutiny for the JSONPath library choice - confirm it doesn't
   introduce its own injection/traversal risk against untrusted
   provider responses.

5. Timeout and retry policy: propose per-stage-configurable timeout
   and retry/backoff for the HTTP calls themselves (distinct from any
   retry logic the remote provider does on its own side).

6. RBAC: confirm webhook.StageRunner needs nothing beyond get on
   Secrets (already covered by existing RBAC) and no new Kubernetes
   API permissions at all, since its actual work is entirely outbound
   HTTP calls - this should be one of the narrowest RBAC footprints of
   any StageRunner in the project. Flag anything that breaks this
   expectation explicitly rather than silently widening RBAC.

7. Your test plan. Must include: golden-value tests for template
   rendering and JSONPath extraction against fixture responses; a
   phase-mapping test covering an unrecognized provider status value
   (what happens if phaseValueMap doesn't cover a value the provider
   actually returns - this needs a defined, non-silent failure mode);
   and the decisive proof test - the same kind of parity test used in
   Phase 5 (tekton-vs-noop), but this time with THREE StageRunners
   (tekton, noop, webhook) driving identical fixtures to the identical
   terminal outcome. This is the first time the project will have
   proven the StageRunner abstraction against a second REAL backend,
   not just a stub - treat this test as the most important evidence in
   this phase.

8. Real-world verification plan: propose standing up a small,
   disposable mock HTTP service (on the sandbox cluster) that mimics a
   submit/poll API with configurable Running/Succeeded/Failed
   responses, so this can be verified against real network calls and
   real JSON parsing, not just unit-test fixtures.

Stop after step 8 and wait for review before writing any code.
```

---

## Phase B — Check-type stage decomposition (combined and separated modes)

**Depends on:** Phase A merged. Some check stages in the decomposed example below use `kind: Webhook`.

**Why this phase exists:** today's sandbox validation bundles compliance scan, security scan, and benchmarking into one opaque Tekton pipeline — one `PipelineRun`, one status. An org can't independently require, skip, or swap the tool behind any single check without editing pipeline YAML. This phase makes each check independently governable *when an org wants that*, while still allowing them to be run as one combined pipeline *when an org wants that instead* — both are valid instances of the same schema, not two different mechanisms to build and maintain.

**Scope, stated precisely:**
- IN: `checkTypes` as a validated, enum-backed list field on `ProfileStageSpec`.
- IN: support for both shapes from the same schema — one stage entry claiming multiple `checkTypes` (combined), or multiple stage entries each claiming one (decomposed).
- IN: optional structured per-check evidence extraction (`checkResults`) for the combined case, so granular *evidence* isn't lost even when granular *control* is deliberately traded away.
- OUT: forcing every existing profile to migrate to decomposed stages. The live `standard-generative-onboarding` profile can keep its combined shape; decomposition is opt-in per profile, same non-breaking bar held throughout this project.
- OUT: actually splitting `sandbox-pipeline.yaml` into separate Tekton pipelines. That's real, separate pipeline-authoring work an org would do for their own profile if they want a decomposed, Tekton-backed version — this phase makes the CRD schema and walker support it, not migrate the reference implementation's own pipeline.

### Kickoff prompt

```
Read docs/REFACTOR_PLAN.md, docs/PHASE_LOG.md, docs/REVIEW_RESPONSE_PLAN.md,
and confirm Phase A (WebhookProviderConfig) is merged before starting.

Phase B - check-type stage decomposition. Design-review-first, same
rigor as Phase A.

Two small things worth a look before moving to Phase B, not blockers:

Confirm webhook.StageRunner's RBAC actually needs create on configmaps now that it's wired into main.go for real — worth a quick check that the marker from the design proposal made it in and matches what's actually used, the same spot-check that caught the dead service-ca.crt path a while back.
The io.ReadAll fix has no explicit size bound. Not a blocker for a controlled sandbox mock, but worth a one-line note in PHASE_LOG.md's follow-up list if it doesn't already have one — a malicious or misbehaving webhook provider returning an unbounded response body is a real (if low-priority) resource-exhaustion consideration for a v2 hardening pass, distinct from the correctness bug that got fixed here.

Do NOT write any implementation code yet. Propose:

1. Add checkTypes []CheckType to ProfileStageSpec, where CheckType is
   a validated CRD enum (+kubebuilder:validation:Enum=SecurityScan;
   ComplianceScan;Benchmark - confirm the exact starting set against
   what this codebase's reference pipeline actually implements today).
   Make the case explicitly for why this should be a validated enum
   (unlike Kind, deliberately left unvalidated in Phase 6) - checkTypes
   is meant to be a curated governance vocabulary that audit tooling
   will eventually query against, not an extensibility escape hatch,
   so a typo here should be a rejected write, not a silent gap in the
   evidence chain.

2. Confirm the walker requires zero changes to support both the
   combined shape (one stage entry, checkTypes: [SecurityScan,
   ComplianceScan, Benchmark]) and the decomposed shape (three stage
   entries, one checkType each) - this should already fall out of the
   existing generic stages[] iteration with no new control-flow
   branching. If it doesn't fall out for free, that's a sign checkTypes
   is being modeled wrong - flag it rather than adding a special case.

3. Design the optional checkResults evidence extraction for the
   combined case: when one stage covers multiple checkTypes, propose
   how a WebhookProviderConfig's statusMapping (or an equivalent
   addition for PipelineRun-backed combined stages) can extract a
   structured per-check breakdown (e.g. checkResults: [{type:
   SecurityScan, passed: true}, {type: Benchmark, passed: false}])
   into ModelRequest.Status.Stages[], distinct from and in addition to
   that stage's single aggregate Phase. Be explicit that this is
   evidence only, not gating - Required still applies at the whole-
   stage level for a combined stage, and this phase should not attempt
   to make individual checkTypes independently required within one
   combined stage entry (that's what decomposition is for, and
   pretending otherwise would be a false promise the design shouldn't
   make).

4. Update the live gitops/components/runtime-config/lifecycleprofile.yaml
   to add checkTypes to its existing sandbox stage entry (combined
   form: [SecurityScan, ComplianceScan, Benchmark]) as a real,
   verified example of the additive, non-breaking migration path -
   don't decompose it, just prove checkTypes can be added to an
   existing combined stage with zero behavior change.

5. Your test plan: a golden-value test proving an existing profile
   with no checkTypes set is completely unaffected (backward
   compatibility litmus test, same pattern as every additive schema
   change so far); a test proving a decomposed 3-stage profile and a
   combined 1-stage profile with equivalent checkTypes produce
   equivalent ModelRequest.Status.Stages[] governance-relevant content
   (same checks recorded as having run, different granularity of
   control); and a test for the checkResults extraction against a
   fixture combined-stage response.

Stop after step 5 and wait for review before writing any code.
```
