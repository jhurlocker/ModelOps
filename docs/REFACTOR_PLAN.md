# ModelOps Refactor Instructions — CRD-Driven AI Governance Control Plane

## Context

`ModelOps` (branch `feat/model-request-controller`) is a Kubebuilder operator that governs the AI model lifecycle on Kubernetes/OpenShift: intake → capacity planning → sandbox validation (security scan, benchmark) → approval-gated promotion across namespaces → registration. The current implementation works but is tightly coupled to one execution engine (Tekton) and one platform (Red Hat OpenShift AI), with the lifecycle sequence hardcoded in Go rather than expressed as data.

**The target architecture**: a thin, portable Kubernetes control plane where CRDs define *what* must happen and *in what order*, and swappable, provider-specific adapters define *how* each stage is actually implemented (Tekton on RHOAI today; SageMaker Pipelines, Databricks Jobs, or others later — without changing the core). Think Cluster API's split between the core API and infrastructure providers, or Crossplane's split between Composite Resource Definitions and Compositions.

Work through the phases below **in order**. Each phase should be a separate, reviewable unit of work — do not start a phase until the previous one builds, passes existing tests, and has been committed. After each phase, run `go build ./...`, `go vet ./...`, and the existing test suite; regenerate CRD manifests and deepcopy code with `make manifests generate` (or the repo's equivalent) if you change any `_types.go` file. Stop and summarize what changed and why at the end of each phase before proceeding to the next.

---

## Three non-negotiable guiding principles (apply to every phase below)

### 1. Test-driven development

For every unit of behavior touched or introduced in any phase, write the test first (or alongside, never after): define the expected input/output or expected reconcile outcome as a test, watch it fail, then write the minimum code to pass it. This applies to new code (Phase 4's `StageRunner` interface, Phase 6's stage walker) just as much as to refactors of existing code (Phase 2's split structs, Phase 3's deduped param builders) — a refactor is not "done" until there's a test proving the behavior is unchanged.

Concretely:
- Every new interface (`StageRunner`, the stage-status contract) must ship with a fake/mock implementation usable in tests, not just the real Tekton implementation.
- Every stage handler (capacity planning, sandbox intake, promotion) must be testable via `envtest`/`fake client` without requiring a real Tekton install, a real cluster, or any other stage to exist or succeed first.
- Reconciler logic that branches on stage outcome (success/failure/pending) must have a test for each branch, not just the happy path.
- Do not treat tests as a cleanup task for a later phase. If a phase's diff has no corresponding test diff, the phase is incomplete.

### 2. Modularity — no lifecycle stage may depend on another

Each lifecycle stage (intake, capacity planning, sandbox validation, promotion, and every future stage — fine-tuning, guardrails, monitoring, retirement) must be independently buildable, testable, and deployable. Concretely, enforce this as a real package boundary, not just an intention:

- Structure the codebase so each stage lives in its own Go package (e.g. `internal/stages/intake`, `internal/stages/capacityplanning`, `internal/stages/promotion`), and **no stage package may import another stage package**. If intake needs something promotion also needs (e.g. a shared param-building helper from Phase 3), that shared code belongs in a common package both depend on (e.g. `internal/stagecommon`) — dependencies point down to shared code, never sideways between stages.
- The only thing that "knows about" the full sequence of stages is the generic stage walker introduced in Phase 6, and it should depend only on the shared `StageRunner`/`StageStatus` contract from Phase 4 — never on any individual stage's concrete types.
- A consequence this should satisfy as a litmus test: it must be possible to delete the promotion stage package entirely and have the intake stage still build, test, and run on its own. The reverse must also hold. This is what makes incremental adoption possible for an org that only wants intake+capacity-planning today and promotion later — treat that as the actual acceptance criterion, not just a nice-to-have.
- New stages (Phase 8's ModelCard gate, and anything beyond this document — fine-tuning, guardrails, monitoring) must follow this same package-isolation pattern from their first commit, not be retrofitted into it later.

### 3. GitOps as the source of truth — the sandbox cluster is for testing, not for deploying

There's a real sandbox cluster available for this work (isolated, no other users, disposable) — use it to actually validate behavior, not just `envtest`. But keep a clear line between *testing against a live cluster* and *deploying*, because those are different things:

- **It's fine, and encouraged, to run real workloads on the sandbox cluster to observe actual behavior** — a real `PipelineRun` executing, a real reconcile loop watching real child objects, a real approval-gated promotion across real namespaces. `envtest` (Phase 0) is good for fast, isolated unit-level reconciler tests; the sandbox cluster is for the things `envtest` can't cover — real Tekton execution, real timing, real RBAC enforcement, multi-object interactions.
- **Best setup, if it doesn't already exist: have ArgoCD track the working feature branch against the sandbox cluster**, so the test loop *is* the GitOps loop — push the branch, ArgoCD syncs it to the sandbox, observe, iterate, push again. This keeps "everything happens through Git" true even during active testing, and it's the most realistic rehearsal of how this will actually behave once merged. If this branch-tracking `Application` doesn't exist yet, set it up as part of Phase 0 rather than falling back to ad hoc `kubectl apply`.
- **If a branch-tracked Application isn't practical and direct `kubectl`/`helm` commands against the sandbox are used for quick iteration instead, treat that cluster state as disposable scratch work, never as the reference implementation.** The actual deliverable of every phase is what's committed to Git — if the sandbox cluster ends up in a state that isn't fully reproducible by applying what's in Git (via ArgoCD or `kubectl apply -k`), that's a sign the phase isn't actually done, not a shortcut to rely on. A good check at the end of each phase: could this cluster be deleted and rebuilt from nothing but a fresh ArgoCD sync of the current Git state, and end up in the same place? If not, something tested manually didn't make it back into a committed file.
- **Watch for ArgoCD self-heal/auto-sync fighting manual changes.** If the sandbox cluster's Application has auto-sync and self-heal enabled, any direct `kubectl` edit will likely get silently reverted on the next sync — which can look like a bug in the code when it's actually GitOps doing its job. Check the sync policy before debugging "why didn't my change take effect."
- **Do not include imperative deployment steps in phase summaries** for anything meant to persist (e.g. "run `kubectl apply -f ...` to deploy this permanently"). It's fine to note what was run against the sandbox for validation purposes, but the summary should be clear about what's disposable testing versus what's the actual committed change.
- If a phase requires a genuinely new ArgoCD-tracked resource (e.g. Phase 5's provider config CRD needs a new `Application`/Kustomize entry to be picked up), that addition itself must be committed as a file in this repo, following the existing pattern.

---

## Phase 0 — Establish test scaffolding and package boundaries

Do this before any behavioral change, so every subsequent phase has somewhere correct to put its tests and code.

1. Set up `envtest` (controller-runtime's test framework) if not already present, so reconciler logic can be tested against a real API server without a full cluster.
2. Create the package skeleton described in the modularity principle above (`internal/stages/intake`, `internal/stages/capacityplanning`, `internal/stages/promotion`, `internal/stagecommon`) even before logic is moved into them — this makes every later phase a "move code into the right box and add a test" exercise instead of a package-design decision made under refactor pressure.
3. Write characterization tests for the **current** behavior of `ModelRequestReconciler` and `CapacityPlanReconciler` before changing anything — capture today's actual sandbox → promotion sequence, current param output, and current status transitions as tests. These become the regression safety net for Phases 4–6 in particular, where the goal is explicitly "relocate behavior without changing it."
4. Confirm CI (or a local `make test`) runs this suite and fails loudly on regression before proceeding to Phase 1.
5. Locate and document (in a short comment or note, not necessarily a new file) where ArgoCD's tracked `Application`/`ApplicationSet` points in this repo — which paths are the actual GitOps source of truth for CRDs, RBAC, and controller deployment manifests. Every later phase that regenerates or adds manifests must write to these paths.
6. Check whether an ArgoCD `Application` already tracks the working branch against the sandbox cluster. If not, set one up now (pointing at this feature branch, sandbox cluster, with the paths identified in step 5) so that live testing throughout the rest of these phases happens via push-and-sync rather than ad hoc `kubectl` — this becomes the primary way to validate Phases 4–6 in particular, where real Tekton execution and real multi-object reconciliation matter more than `envtest` alone can prove.

---

## Phase 1 — Security and correctness fixes (do first, low risk, no architecture change)

1. **Remove plaintext credential fields from the CRD spec.** Delete `ResultS3AccessKey` and `ResultS3SecretKey` (raw string fields) from `ModelRequestSpec`. All S3 credentials must be resolved exclusively through the existing `ResultS3SecretName`-style secretRef pattern — never accepted as inline spec values. Update `resolveSecrets` accordingly and remove the code path that lets a raw spec value override the secret-derived one.
2. **Remove or loudly flag the hardcoded `minioadmin`/`minioadmin` credential fallback** in `resolveSecrets`. If no secret is configured, fail the reconcile with a clear status condition/event ("no result storage credentials configured") rather than silently defaulting to a known credential pair.
3. **Fix the duplicate `gpu-count-override` param bug** in `buildSandboxPipelineParams` — the param is currently added twice (once from `plan.Status.GPUsNeeded`, once from `reqs.GPUCountOverride`). Make the override explicit: if `reqs.GPUCountOverride` is set, use it and skip the plan-derived value; otherwise use the plan-derived value. Add exactly one `gpu-count-override` param.
4. **Fix `promotionPipelineNameOrDefault`** — it currently ignores its `profile` parameter. Either wire it to actually select a per-profile promotion pipeline name, or remove the unused parameter if that's genuinely not needed yet. Don't leave dead/misleading parameters.
5. **Add idempotency handling around `Create` calls.** Anywhere the controller calls `r.Create(...)` for a child object (CapacityPlan, PipelineRun, RBAC objects), handle `apierrors.IsAlreadyExists(err)` explicitly (treat as success / re-fetch) rather than returning a raw error. Add appropriate `ctrl.Result{RequeueAfter: ...}` backoff on transient errors instead of bare `return ctrl.Result{}, err` where it would otherwise hot-loop.

---

## Phase 2 — Break up the `ModelRequirements` kitchen-sink struct

1. Group the ~20 flattened fields in `ModelRequirements` into logical sub-structs, e.g.:
   - `GPUConfig` (GPU count override, MIG allowance, hardware profile)
   - `BenchmarkTargets` (latency, throughput, concurrency, context length, GuideLLM rate)
   - `SecurityConfig` (CVE threshold, custom benchmark file, EvalHub/garak params)
   - `DeploymentConfig` (Helm values content, console domain, MaaS override)
2. Keep `ModelRequirements` as a composed struct of these sub-structs so the top-level API doesn't break unnecessarily — but the internal organization should make it obvious which fields belong to which concern.
3. Update all call sites (`buildSandboxPipelineParams`, `buildPromotionPipelineParams`, etc.) to reference the new nested paths.
4. Update CRD manifests/deepcopy after this change.

## Phase 3 — De-duplicate the pipeline param builders

1. `buildSandboxPipelineParams` and `buildPromotionPipelineParams` share significant logic (model identity, GPU config, benchmark config blocks). Extract the shared parameter-building logic into a common helper (e.g., `buildCommonModelParams(reqs ModelRequirements) []tektonv1.Param`) and have both functions call it, layering their stage-specific params on top.
2. This phase should result in a net reduction in lines of code and a single source of truth for any param that's shared between sandbox and promotion.

---

## Phase 4 — Decouple the core reconciler from Tekton

This is the key architectural change. Goal: `ModelRequestReconciler` should never import `tektonv1` directly or read a Tekton-specific condition.

1. Define a small, generic status contract — e.g., an interface or a shared condition type (`type StageStatus struct { Phase string; Reason string; Message string }`) that any "stage run" object can expose, modeled after Kubernetes' own `metav1.Condition` pattern. Write the contract's tests first: given a set of representative inputs (Tekton conditions, a hypothetical non-Tekton status), assert the contract captures them correctly, before writing the Tekton adapter that produces them.
2. Introduce a `StageRunner` interface (name it what fits the codebase) with a method like `EnsureRun(ctx, req *ModelRequest, stage StageSpec) (StageStatus, error)`. The core reconciler calls this interface — it does not construct pipeline objects itself. Write a trivial in-memory fake `StageRunner` alongside the interface definition, purely for tests — this is what lets every stage and the core walker be tested without Tekton at all.
3. Implement a `TektonStageRunner` that satisfies this interface, in its own package (e.g. `internal/stages/tekton`) — move the existing `PipelineRun` construction, workspace bindings, and condition-reading logic (currently inline in the reconciler) into this implementation. This is a refactor, not new logic — the Tekton-specific behavior should be unchanged, just relocated. Use the Phase 0 characterization tests to confirm this.
4. Update `ModelRequestReconciler` to hold a `StageRunner` (injected at manager setup) and call it generically for both the sandbox stage and each promotion stage, rather than hand-building `PipelineRun` objects inline. Add a reconciler-level test that swaps in the fake `StageRunner` from step 2 and confirms the reconciler drives phase transitions correctly with zero Tekton involvement — this is the concrete proof that the core is now engine-agnostic.
5. Verify existing behavior is preserved: run the Phase 0 characterization tests and confirm the sandbox → promotion flow still works end to end against a real or fake cluster before proceeding.

## Phase 5 — Provider-specific configuration CRDs

Modeled on Cluster API's `infrastructureRef` pattern.

1. Add a new CRD kind, e.g. `IntakeProviderConfig` (or split further into `RHOAIIntakeConfig` if you want strong typing per backend — recommend starting with one generic kind plus a `providerType` discriminator field to keep scope small, and only split into fully separate typed kinds if/when a second real backend is implemented).
2. `ModelLifecycleProfile` should reference this object (`providerConfigRef: {name, kind}`) instead of holding a raw `PipelineRef` string directly.
3. The `TektonStageRunner` from Phase 4 should resolve the referenced provider config to get RHOAI/Tekton-specific details (pipeline name, workspace/PVC names, ConfigMap names) — these details move out of Go constants and into the new CRD's spec.
4. Do not implement a second provider (SageMaker/Databricks) in this pass — the goal is only to prove the seam exists and that the core reconciler has zero knowledge of what's behind `providerConfigRef`. A trivial no-op or logging-only second `StageRunner` implementation is a good way to smoke-test that the abstraction holds, without taking on a full second integration.

## Phase 6 — Externalize the stage sequence as data

1. Add an ordered `stages` field to `ModelLifecycleProfile`, e.g.:
   ```yaml
   stages:
     - name: capacity-planning
       kind: CapacityPlan
       required: true
     - name: sandbox-intake
       kind: PipelineRun
       providerConfigRef: {name: rhoai-sandbox}
       required: true
     - name: promotion
       kind: PipelineRun
       providerConfigRef: {name: rhoai-promotion}
       perNamespace: true
   ```
2. Refactor `ModelRequestReconciler.Reconcile` from its current hardcoded linear sequence (capacity → sandbox → promotions) into a generic walker that iterates `profile.Spec.Stages` in order, dispatches each to the appropriate handler (CapacityPlan logic from Phase 1, or `StageRunner` from Phase 4/5), and only advances to the next stage once the current one reports success. Before writing the walker, write tests for it directly against fake stage handlers: a 1-stage profile, a 3-stage profile, a profile where stage 2 fails, a profile where a stage is marked `required: false` and is skipped. The walker's correctness should be provable without any real stage implementation involved.
3. `ModelRequest.Status` should reflect which named stage it's currently on (not just a generic `Phase` enum), so `kubectl get modelrequest` shows meaningful progress against the profile's declared stage list.
4. Confirm the modularity litmus test from the guiding principles holds at the end of this phase: temporarily comment out or delete the promotion stage package and verify intake + capacity planning still build and their tests still pass unmodified. This is the concrete proof that incremental adoption (an org running only the early stages) actually works, not just an aspiration.
5. This is the highest-value, highest-risk phase — take it slowly, and preserve exact current behavior for the existing 3-stage RHOAI flow as the regression test.
6. **Also relocate `buildSandboxPipelineParams`/`buildPromotionPipelineParams`/`sandboxPipelineNameOrDefault`/`promotionPipelineNameOrDefault`/`getPromotionNamespaces` out of `internal/controller` and into `internal/stages/sandbox`/`internal/stages/promotion`.** Phase 4 deliberately left these in `internal/controller` (only changing their return type from `tektonv1.Params` to `map[string]string`, per its own scope of "decouple from Tekton," not "finish the per-stage package split") — see `docs/PHASE_LOG.md`'s Phase 4 entry for the explicit reasoning. This phase's stage-handler dispatch needs real per-stage logic behind `profile.Spec.Stages` anyway, which is the natural point to finish this move: each stage package should own building its own `stagecommon.StageSpec` (params map + `WorkflowRef` + `RunName`) from `ModelRequest`/`ModelLifecycleProfile`/`PlatformConfig`/`CapacityPlan`, calling `stagecommon.BuildCommonModelParams` for the shared portion. Verify with the existing param-builder characterization tests, relocated alongside the functions (same assertions, new package).

## Phase 7 — RBAC scoping

1. Review the full RBAC marker list on `ModelRequestReconciler` and split permissions by concern: core lifecycle (ModelRequest, CapacityPlan, status subresources) vs. Tekton-specific (PipelineRun) vs. namespace RBAC provisioning (RoleBindings, ClusterRoleBindings, ServiceAccounts).
2. If feasible within this pass, move the Tekton-specific and RBAC-provisioning permissions onto the `TektonStageRunner`'s own service account / manager setup rather than the core reconciler's, so a future non-Tekton `StageRunner` doesn't inherit permissions it doesn't need.
3. Document the resulting permission boundaries in a short `RBAC.md` or comment block — what the core controller can touch vs. what the provider adapter can touch.
4. Resolve the `WorkflowRef.Engine` vs. `IntakeProviderConfigSpec.ProviderType` redundancy introduced in Phase 5 (both now describe "which execution engine," one on `ModelLifecycleProfile` and one on the referenced provider config) — decide whether `Engine` is deprecated/removed, derived from the resolved provider config, or kept as a distinct concept, and update the `Engine` printcolumn accordingly.
5. Give `ProviderConfigRef` resolution failures (missing/malformed reference, unsupported `Kind`, unsupported `providerType` — see `internal/stages/tekton/providerconfig.go`'s `resolveProviderDetails`) a real `ModelRequest` status reason (e.g. `ProviderConfigLookupFailed`, following the existing `ProfileLookupFailed`/`PlatformConfigLookupFailed` pattern) instead of the generic silent-retry error path every other `StageRunner.EnsureRun` error currently falls into.
6. Deprecate the Phase 6 `Phase`/`Message` legacy-compatibility shim (`internal/controller/modelrequest_controller.go`'s `computeWalkStatus`, the `usingDefaultStages` branch) once profiles have migrated off the synthesized default `Stages` list — go fully generic (`"<CurrentStage>Running"`/`"Succeeded"`/`"Failed"` for every profile, no special-cased `"CapacityPlanning"`/`"SandboxRunning"`/`"PromotionRunning"` strings) rather than carrying the two-shape `Phase` contract indefinitely.
7. Give the `CapacityPlan` stage a real `Failed` path in `CapacityPlanReconciler` (today it always eventually reaches `Succeeded`, with no failure branch at all — e.g. an unreachable GPU advisor endpoint should produce `Status.Phase = "Failed"`, not spin `Running` forever) so that `ProfileStageSpec.Required: true` is actually meaningful for the `CapacityPlan` stage kind, not just structurally supported by `internal/stages/capacityplanning.StageRunner`'s already-implemented (but so far untriggered in production) `Failed` mapping.
8. **Backlog note added during this phase's implementation, deliberately not built this pass**: add `Watches()` registrations (`ModelLifecycleProfile`, `PlatformConfig`, `IntakeProviderConfig`, each with a mapping function that finds affected `ModelRequest`s) so that **all four** lookup-failure reasons — `ProfileLookupFailed`, `PlatformConfigLookupFailed`, the new `ProviderConfigLookupFailed`, and (implicitly) `NoStagesConfigured` — re-trigger reconciliation immediately when the missing/fixed dependency is created or updated, instead of relying on a bounded requeue timer (`ProviderConfigLookupFailed`'s new 30s `providerConfigLookupRequeueDelay`) or, for the other three, the `ModelRequest`'s own unrelated resync/watch events. Scope this to all four reasons together, not just `ProviderConfigLookupFailed` in isolation, since they share the exact same "waiting on a separate GitOps sync to create an object" shape.
9. **Backlog note added during this phase's implementation, deliberately not built this pass**: `MaxGPUsPerRequest` (added this phase) is a configured-ceiling stopgap, not real capacity awareness — `CapacityPlanReconciler` still has no GPU inventory/advisor integration of any kind (confirmed: no HTTP call, no Node/`ClusterPolicy` capacity query). A real implementation would wire it to an actual GPU-inventory/advisor-based feasibility check — note a real `gpu-advisor` container image already exists and is used by the sandbox Tekton pipeline's own `gpu-advisor` Task (`model_onboarding_pipeline/tools/gpu-advisor`, `quay.io/jhurlocker/gpu-advisor`), a natural candidate to eventually wire into `CapacityPlanReconciler` directly — distinguishing genuinely infeasible requests (fail) from transient advisor/pool unavailability (keep retrying), per this phase's design review.

## Phase 8 (stretch, do only if the above are stable) — ModelCard / DataCard CRD

1. Add a new, independently-owned CRD, `ModelCard` (and optionally `DataCard`), that is **not** owned/garbage-collected by any single `ModelRequest` — it should persist across a model's full lifecycle (multiple intake/promotion events).
2. Keep the CRD thin: `status.approved (bool)`, `status.riskTier`, `status.reviewedBy`, `status.contentHash`, and a `spec.documentRef` (URI to the full card content in object storage — do not store the full card body in the CRD).
3. Add a `modelCardRef` field to `ModelRequest` (reference, not ownership) and a gate check in the reconciler: promotion stages should not proceed unless the referenced `ModelCard.status.approved == true`.

---

## Working constraints for every phase

- Preserve backward compatibility with existing `ModelRequest`/`ModelLifecycleProfile` YAML wherever reasonable; call out explicitly any change that's a breaking API change.
- Do not silently change external behavior of the existing RHOAI/Tekton flow — the point of Phases 4–6 is to relocate logic, not alter it. Flag any place where preserving exact behavior is impossible without a decision.
- **A phase is not complete without tests.** Write or update tests for any new function extracted, interface introduced, or branch of behavior touched — before or alongside the implementation, not after. If asked to move to the next phase and no test diff exists for the current one, stop and write the missing tests first.
- **A phase is not complete if it introduces a cross-stage import.** Before committing, check that no package under `internal/stages/*` imports another package under `internal/stages/*`. Shared logic belongs in `internal/stagecommon` or an equivalent common package.
- **A phase is not complete if its result isn't reproducible from Git alone.** Testing against the sandbox cluster (ideally via a branch-tracked ArgoCD Application, per the GitOps principle) is expected and encouraged — but if the sandbox ends up working only because of a manual `kubectl` change that was never committed, the phase isn't done. Before marking a phase complete, confirm every behavior change is captured in a committed file, not just in the live cluster's current state.
- After each phase, produce a short summary: what changed, why, what (if anything) is now a breaking change, what test coverage was added, and what manual verification you'd recommend before merging.