# Phase Log

Handoff notes for each phase of `REFACTOR_PLAN.md`. Append new entries at
the bottom; do not overwrite or edit previous entries.

---

## Phase 0 — Test scaffolding and package boundaries

**Commit:** `21341d8` on `feat/model-request-controller` — "Phase 0: test
scaffolding, package boundaries, and manifest tooling"

**Status:** Complete. No Phase 1 work started. No behavior in
`internal/controller/*.go` was changed or moved.

### What was built

- **envtest scaffolding**: `operator/internal/controller/suite_test.go`
  boots a real `etcd`+`kube-apiserver` once per test package (not per
  test) via `sigs.k8s.io/controller-runtime/pkg/envtest`, registers the
  `modelops.example.io` and `tekton.dev/v1` schemes.
- **Vendored Tekton CRD**: `operator/config/crd/testdata/tekton-pipelinerun-crd.yaml`
  — a pinned copy (matches `go.mod`'s `tektoncd/pipeline v0.63.0`) of just
  the `PipelineRun` CRD. Needed because `ModelRequestReconciler` creates
  and owns real `PipelineRun` objects, and envtest's real API server
  requires the CRD actually installed (unlike a fake client).
- **Test fixtures**: `operator/internal/controller/testutil_test.go` —
  `newTestNamespace`, `ensureNamespace`, `newProfile`, `newPlatformConfig`,
  `newModelRequest`, etc.
- **30 characterization tests** across `capacityplan_controller_test.go`
  and `modelrequest_controller_test.go`, all passing, pinning current
  reconciler behavior end-to-end against envtest: lookup failures,
  capacity-planning → sandbox → promotion phase transitions, idempotency
  guards, RBAC provisioning, and param-builder golden values.
- **Package skeleton**: `internal/stagecommon/` and
  `internal/stages/{sandbox,capacityplanning,promotion}/`, each currently
  just a `doc.go`. Verified no package under `internal/stages/*` imports
  another.
- **`controller-gen`/`setup-envtest` wired into the Makefile** for real
  (previously `manifests` was a no-op `@echo`). Both invoked via
  `go run <module>@<pinned-version>` so neither pollutes `go.mod`.

### Naming/structural decisions not obvious from the code

- **Stage package is named `sandbox`, not `intake`.** `REFACTOR_PLAN.md`'s
  guiding-principles text says `internal/stages/intake`, but nothing in
  the actual codebase is called "intake" — the Tekton-driven stage that
  runs compliance-scan/deploy/security-scan/teardown is called
  **`sandbox`** everywhere already (`sandboxRunName`,
  `buildSandboxPipelineParams`, `sandboxPipelineNameOrDefault`,
  `Status.SandboxPipelineRunName`, pipeline name `model-intake-sandbox`).
  "Intake" in this codebase means the whole `ModelRequest` submission
  (the UI, the CR itself), not a stage. This was raised with the user
  explicitly and they chose `sandbox`. **If a future phase or doc says
  "intake stage," it means this `sandbox` package.**
- **`internal/stages/tekton` does not exist yet, deliberately.** That's
  a Phase 4 deliverable (the `TektonStageRunner`), not Phase 0 scaffolding.
  Don't be surprised it's missing.
- **Characterization tests live in `internal/controller`, not in the new
  `internal/stages/*` packages.** Since no logic has moved yet, the real
  behavior under test still lives in `internal/controller`. When Phase 2+
  relocates logic into `internal/stages/*`, the corresponding tests should
  move with it — these tests are the regression net for that move, not a
  permanent home.
- **Root cause of the previously-fake `manifests` target**:
  `api/v1alpha1/groupversion_info.go` was missing the `// +groupName=`
  marker `controller-gen` needs for CRD group inference. Without it,
  `controller-gen crd` silently emits an empty, ungrouped CRD with **no
  error at all** — which is almost certainly why someone gave up and
  stubbed the target with `@echo` instead of debugging it. Fixed by
  adding the marker. If you ever see `controller-gen` silently produce a
  `_.yaml` with `group: ""`, this is the failure mode to check for first.
- **Regenerating manifests surfaced real, pre-existing production bugs**,
  now fixed (verified via a structural before/after diff — zero fields
  removed from any CRD, only additions):
  - `ModelRequirements.PromotionNamespaces` was entirely absent from the
    `ModelRequest` CRD schema, so the API server was silently pruning it
    on every write. `getPromotionNamespaces()` could never see anything
    but the `["staging"]` fallback regardless of what was actually
    submitted, in production, until this fix.
  - `ModelRequestStatus.SandboxPipelineRunName` and
    `PromotionPipelineRunName` were likewise absent and silently pruned
    — `kubectl get modelrequest` could never actually show them.
  - `zz_generated.deepcopy.go` was missing `DeepCopyInto` for
    `ModelRequirements`, `ModelRequestSpec/Status`,
    `CapacityPlanSpec/Status`, `PlatformConfigSpec/Status`, and
    `ModelLifecycleProfileSpec/Status` — nested pointers/slices were
    shallow-copied/aliased instead of deep-copied.
  - `config/rbac/role.yaml` gained the `rolebindings`/
    `clusterrolebindings` rules the Go `+kubebuilder:rbac` markers
    already declared but the checked-in file was missing.
  - Needed `crd:allowDangerousTypes=true` in the Makefile because
    `PlatformConfigSpec.BenchmarkRate` is a `float64` (controller-gen
    warns/errors on floats by default; this is the standard, safe
    opt-in, not a design change).
- **`internal/stages/*` and `internal/stagecommon` docs are intentionally
  boundary-heavy** (each `doc.go` repeats "must never import X") — this
  is deliberate self-documentation of the litmus test from
  `REFACTOR_PLAN.md`, not filler.
- **A discovered-but-deliberately-not-fixed gap**: one characterization
  test (`TestModelRequest_MultiplePromotionNamespaces_KnownBehavior_AllCreatedInSameReconcilePass`)
  documents that promotion namespaces are **not** actually gated
  sequentially today — if namespace 2 has no `PipelineRun` yet, it gets
  created in the *same* reconcile pass as namespace 1, without waiting
  for namespace 1 to succeed. This looks like it contradicts the
  "promotion is sequential" principle in `AGENTS.md`. Captured as-is per
  Phase 0 instructions (don't fix behavior, pin it); flagged for a
  deliberate decision in a later phase, not silently corrected.
- **Go toolchain note for whoever picks this up**: there is no local
  `go` binary in this dev environment. All `go build`/`vet`/`test`/`run`
  commands this session went through a containerized wrapper
  (`registry.access.redhat.com/ubi9/go-toolset:1.22` via
  `podman run --userns=keep-id --user 1000:1000 ...`, with `GOSUMDB=off`,
  `GOTOOLCHAIN=local`, and bind-mounted `GOCACHE`/`GOMODCACHE`/envtest
  binary dirs under `/tmp/opencode/`). If the next session also lacks a
  local Go install, expect to rebuild something similar rather than
  running `make test` directly.

### Known follow-up NOT done in this phase

`gitops/components/operator/crd-*.yaml` and `clusterrole.yaml` are
**separate, manually-synced copies** that ArgoCD actually deploys to the
sandbox cluster — they were **not** touched this phase and still lack
`promotionNamespaces` and the two status fields. This is a live-cluster
bug fix, not scaffolding, and deserves its own explicit phase/decision
rather than being folded into Phase 0's commit. Recommend prioritizing
this early in Phase 1.

### Current state of the sandbox cluster's ArgoCD Applications

(Checked live at the end of this session, after pushing the Phase 0
commit.)

- `Application/modelops-operator` (namespace `openshift-gitops`, tracks
  `feat/model-request-controller` at `gitops/components/operator`):
  **Synced / Healthy**, at revision `21341d8` (this phase's commit).
  Synced trivially — none of this phase's changes touched
  `gitops/components/operator/*`, so there was nothing new to apply.
  `modelops-operator` Deployment in the `modelops` namespace is
  `1/1 Ready` on `quay.io/jhurlocker/modelops-operator:latest`.
- `Application/modelops-root` (app-of-apps, path `gitops/applications`):
  **OutOfSync / Healthy**, also at revision `21341d8`. Four child
  Applications are reported out of sync: `model-intake-ui`,
  `model-registry`, `modelops-pipelines`, `results-ui`. **This predates
  this session's Phase 0 work** — it's leftover drift from earlier
  ad-hoc `oc apply`/image-rebuild iteration in this session (per the
  GitOps guiding principle, that's expected/tolerated scratch state, not
  a regression). Not investigated or fixed here; the user explicitly
  said it's fine to leave the cluster in an awkward state for this
  handoff.
- The `sandbox` namespace has numerous leftover `ModelRequest` objects
  from earlier ad-hoc testing this session, mostly `Phase: Failed` (e.g.
  `granite-2b-onboarding`, several `model-intake-*`). These are
  disposable test artifacts, not something the next session needs to
  preserve or clean up before starting Phase 1 — but don't mistake them
  for a regression if you `oc get modelrequests -A` and see a wall of
  `Failed`.

### Verification run at the end of this phase

`go build ./...`, `go vet ./...`, `go test ./...` (30/30 passing,
envtest-backed, ~9.5s), `make manifests`/`make generate` idempotent on a
second run, confirmed no `internal/stages/*` package imports another.

---

## Phase 1 — Security and correctness fixes

**Status:** Complete, verified on the sandbox cluster via the
branch-tracked ArgoCD `Application`s from Phase 0. Fixes landed in
`internal/controller/modelrequest_controller.go` and
`api/v1alpha1/*_types.go` (Phase 1 is explicitly "fix in place," not a
relocation into `internal/stages/*` -- that's Phase 2+).

### What changed

1. **Removed `ResultS3AccessKey`/`ResultS3SecretKey`** from
   `ModelRequestSpec` (breaking API change, see callout below).
   `ResultS3Endpoint`/`ResultS3Bucket` were kept -- they're not
   credentials. `resolveSecrets` no longer has a plaintext-spec-field
   override path.
2. **Removed the hardcoded `minioadmin`/`minioadmin` fallback.**
   `resolveSecrets` now returns an error ("no scan storage credentials
   configured" / "no result storage credentials configured") if
   `scanS3SecretName`/`resultS3SecretName` isn't set or the referenced
   Secret doesn't populate `accessKeyId`/`secretAccessKey`. This routes
   through the existing `SecretLookupFailed` status path -- no new
   status mechanism needed. The endpoint URL default (a cluster-local
   service address, not a credential) was intentionally left in place.
3. **Fixed the duplicate `gpu-count-override` param** in
   `buildSandboxPipelineParams`: `reqs.GPUCountOverride`, if set, is now
   the only source of the param; the plan-derived value is only used as
   a fallback when no override is set. Exactly one param is ever added.
4. **Fixed `promotionPipelineNameOrDefault`**: added
   `WorkflowRef.PromotionPipelineRef` (additive CRD field) and wired the
   function to use it, falling back to `"model-intake-promotion"` only
   when unset. `PipelineRef` still means "sandbox stage pipeline";
   promotion now has its own, independent override.
5. **Idempotent `Create` calls**: added a shared
   `createIgnoringAlreadyExists` helper (treats `apierrors.IsAlreadyExists`
   as success, propagates everything else) and applied it at all
   `r.Create` sites for child objects -- `CapacityPlan`, both
   `PipelineRun` kinds, and all four RBAC objects in
   `ensurePromotionNamespaceRBAC`. Non-conflict errors on the three main
   reconciler call sites now requeue with a fixed backoff
   (`transientErrorRequeueDelay = 5s`) instead of a bare
   `return ctrl.Result{}, err`.

   **A live-cluster-only regression was caught and fixed here.** The
   first draft of this fix also dropped `ensurePromotionNamespaceRBAC`'s
   pre-existing Get-then-only-Create-if-NotFound guard in favor of
   calling `createIgnoringAlreadyExists` unconditionally, on the theory
   that it was simpler and closed the Get/Create race more thoroughly.
   `envtest` (43/43 tests) didn't catch anything wrong with this. On the
   real sandbox cluster, it broke: the `sandbox-pipeline-evalhub`
   `ClusterRoleBinding` already existed (created before this session,
   granting `trustyai.opendatahub.io evaluations` permissions the
   operator's own `ServiceAccount` doesn't itself hold). Kubernetes'
   RBAC privilege-escalation check runs on *every* `Create` attempt,
   before the "already exists" conflict is even evaluated -- so a
   redundant `Create` against an already-correct object was rejected
   with `Forbidden`, not `AlreadyExists`, and surfaced as
   `RBACSetupFailed`. `envtest`'s test client is admin-equivalent and
   never hits this check, so it's structurally invisible to unit/envtest
   coverage. Fixed by restoring the Get-guard around all four RBAC
   Create sites (only attempt Create when confirmed absent) and using
   `createIgnoringAlreadyExists` only for the narrow race window between
   that Get and the Create, not as a replacement for it. Added
   `TestEnsurePromotionNamespaceRBAC_ObjectsAlreadyExist_DoesNotReattemptCreate`
   to pin the "never re-Create an already-existing object" contract
   (verified via unchanged `ResourceVersion`, since the Forbidden-vs-
   AlreadyExists distinction itself isn't reproducible against envtest's
   admin client). This is the concrete reason Phase 0/this phase's
   "verify against the sandbox cluster, not just envtest" instruction
   mattered in practice, not just in principle.

### Breaking API change: `ResultS3AccessKey`/`ResultS3SecretKey` removed

Flagged to the user before making the change, per their request:

- **12 of 20** `ModelRequest` CRs in the sandbox cluster's `sandbox`
  namespace had these fields set. All are leftover ad-hoc test
  artifacts from Phase 0's session (8 `Failed`, 3 `ProfileLookupFailed`,
  1 `Succeeded`) -- confirmed disposable, left as-is per user decision
  (no migration path implemented; the field is simply pruned by the API
  server going forward).
- More importantly, the **model-intake UI wizard actively submitted
  these fields** (`wizard.html`'s "S3 Connection Override" section ->
  `intake.py`'s `s3-access-key`/`s3-secret-key` form fields ->
  `spec.resultS3AccessKey`/`resultS3SecretKey`). Per user decision, the
  UI was fixed in the same phase rather than left with dead inputs:
  `wizard.html` now has "Scan S3 Secret Name"/"Result S3 Secret Name"
  text inputs (same pattern as the existing EvalHub/HuggingFace
  secret-name inputs) defaulting to `scan-s3-credentials`/
  `result-s3-credentials` -- Secret names that **already exist** in the
  sandbox cluster's `sandbox` namespace with the right
  `accessKeyId`/`secretAccessKey`/`endpoint` shape (pre-provisioned,
  apparently for exactly this purpose, but never wired into the visible
  form). `intake.py`'s form-to-spec mapping already had dead code paths
  for `scan-s3-secret-name`/`result-s3-secret-name` -> `scanS3SecretName`/
  `resultS3SecretName` (an "expert secret references" loop) that simply
  weren't reachable because no input rendered those field names; they're
  live now. The plaintext `s3-access-key`/`s3-secret-key` inputs and
  their `resultS3AccessKey`/`resultS3SecretKey` mappings were deleted.
  A second, separate JSON-API code path in `app.py` already only used
  secret-name references and needed no change.

### GitOps mirror drift (also fixed, adjacent to this phase's scope)

Regenerating CRD manifests (`make manifests`) surfaced that
`gitops/components/operator/crd-modelrequests.yaml` and
`crd-lifecycleprofiles.yaml` -- the copies ArgoCD actually deploys to
the sandbox cluster -- are hand-maintained, not generated, and had
drifted from `operator/config/crd/bases/*`: they were missing
`promotionNamespaces`, `sandboxPipelineRunName`, and
`promotionPipelineRunName` (a real, live pruning bug flagged in Phase
0's handoff, predating this phase), and still had the now-removed
plaintext credential fields. Both were replaced verbatim with the
freshly generated `config/crd/bases/*` content, so the two copies can't
drift again as long as `make manifests` output is what gets copied.
`crd-capacityplans.yaml` and `crd-platformconfigs.yaml` were checked and
have no field-level drift (only cosmetic formatting differences from an
older controller-gen version) -- left untouched, out of scope.
`gitops/components/operator/clusterrole.yaml` has manually-added
permissions (`serving.kserve.io`, `maas.opendatahub.io`) with no
corresponding `+kubebuilder:rbac` marker -- left untouched; reconciling
that is a separate, unrelated concern from Phase 1.

### Test coverage added

All new/changed tests live in `internal/controller/modelrequest_controller_test.go`
(Phase 1 doesn't relocate logic, so tests stay where the logic is) and
`testutil_test.go`:

- `newModelRequest` now provisions a real S3-credentials `Secret` and
  points `scanS3SecretName`/`resultS3SecretName` at it by default (via
  new `newS3CredentialsSecret` helper), so every existing
  sandbox/promotion-phase test exercises the real secretRef path instead
  of the removed hardcoded fallback, with no per-test changes needed.
- `resolveSecrets`: replaced the two `KnownBehavior`/`KnownBug` tests
  that pinned the old minioadmin-fallback and plaintext-override
  behavior with tests for the new contract (secret-derived credentials,
  `ResultS3Endpoint` override still honored, missing-secret-name ->
  error containing "no scan/result storage credentials configured"),
  plus a reconciler-level test proving this surfaces as
  `SecretLookupFailed` status.
- `buildSandboxPipelineParams`: replaced the `KnownBug` duplicate-param
  test with three tests covering override-wins-and-appears-once,
  falls-back-to-plan-derived, and omitted-when-neither-is-set.
- `promotionPipelineNameOrDefault`: replaced the `KnownBug` test with
  unit tests for profile-override, no-override-default, and
  nil-profile-default, plus a reconciler-level end-to-end test
  (`TestModelRequest_PromotionUsesProfilePromotionPipelineRef_EndToEnd`)
  proving the profile's `PromotionPipelineRef` actually reaches the
  created promotion `PipelineRun`.
- `createIgnoringAlreadyExists`: new direct unit tests (absent ->
  creates; already-exists -> success, not error; other API errors ->
  still propagated), plus a race-simulation test against the exact
  `CapacityPlan` object the reconciler would build, proving the
  reconciler's own Create call site is safe against a losing race
  without needing genuine goroutine concurrency.
- `ensurePromotionNamespaceRBAC`: new test proving already-existing RBAC
  objects never see a repeat Create attempt (see the live-cluster
  regression writeup above).
- Total suite: 43 tests passing (up from Phase 0's 30), all
  envtest-backed.

### Manifest regeneration

`make manifests generate` (controller-gen v0.16.5) picked up the new
`WorkflowRef.PromotionPipelineRef` field and the removed
`resultS3AccessKey`/`resultS3SecretKey` fields. Diffed the regenerated
`config/crd/bases/*.yaml` against the previous version field-by-field to
confirm no unintended fields were added/removed.
`zz_generated.deepcopy.go` had no diff (plain string field changes don't
need generated deepcopy code -- `*out = *in` already handles them).

### Sandbox cluster verification

- Built and pushed a new `quay.io/jhurlocker/modelops-operator:latest`
  image from this phase's code (the sandbox cluster's operator
  `Deployment` runs from this pre-built image tag, not live source --
  ArgoCD only syncs the Kubernetes manifests around it, so a rebuild+push
  plus a rollout restart was necessary to actually exercise this phase's
  code on-cluster, not just its manifests).
- Pushed this phase's commit to `feat/model-request-controller`;
  `Application/modelops-operator` (auto-sync + self-heal) picked up the
  regenerated CRDs; confirmed via `oc get crd` that
  `resultS3AccessKey`/`resultS3SecretKey` are gone and
  `promotionPipelineRef` is present.
- First verification pass caught the RBAC regression described above
  (a real `ModelRequest` hit `RBACSetupFailed` against the already-existing
  `sandbox-pipeline-evalhub` ClusterRoleBinding). Fixed, re-tested
  locally (43/43), rebuilt/pushed the image again, rolled out again.
- Second verification pass, against a disposable test `ModelRequest`
  (created directly, not via the UI, then deleted after observation):
  no `*SecretName` set -> `SecretLookupFailed` with the new "no scan
  storage credentials configured" message (no more silent minioadmin
  default). A second request referencing the pre-existing
  `scan-s3-credentials`/`result-s3-credentials` Secrets in `sandbox`,
  with `requirements.gpuCountOverride: "3"`, reached `SandboxRunning`
  cleanly, its generated sandbox `PipelineRun` has exactly one
  `gpu-count-override` param with value `"3"` (the explicit override,
  not the CapacityPlan-derived value), and RBAC provisioning succeeded
  with no error.
- **Not verified on-cluster**: the `model-intake-ui` wizard change.
  `Application/model-intake-ui` is already `Unknown`/broken --
  independent of this phase, its `deployment.yaml` has a pre-existing
  (already-committed, not introduced this session) multi-document YAML
  parse error that fails `kustomize build`. Even if that were fixed, the
  UI `Deployment` also runs from a separate pre-built image
  (`quay.io/jhurlocker/model-intake-ui:latest`) that this session did not
  rebuild/push. The UI source change is committed and unit-inspected
  (Python syntax-checked) but only verified by reading, not by clicking
  through the live wizard. Flagging the broken `model-intake-ui`
  Application as a pre-existing issue worth its own fix, separate from
  Phase 1.

### Known follow-up NOT done in this phase

- `model-intake-ui`'s ArgoCD `Application` is broken for reasons
  unrelated to this phase (see above) -- worth a dedicated fix before
  relying on it for any future UI-touching phase's live verification.
- No migration tooling was written for the 12 disposable CRs that still
  reference the removed fields on the sandbox cluster; per user
  decision this wasn't needed for this environment, but a real
  multi-tenant deployment removing these fields would want a
  migration/deprecation window.
