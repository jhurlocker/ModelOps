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

---

## Phase 2 — Split the `ModelRequirements` kitchen-sink struct

**Commit:** `bf40ade` on `feat/model-request-controller` — "Phase 2: split
ModelRequirements into GPUConfig/BenchmarkTargets/SecurityConfig/
DeploymentConfig". **Not a breaking API change** -- see below.

### What changed

`ModelRequirements` (`operator/api/v1alpha1/modelrequest_types.go`) had
~20 flat fields with no internal organization. Grouped them into four
sub-structs matching `REFACTOR_PLAN.md`'s suggested grouping:

- **`GPUConfig`**: `GPUIsolationPolicy`, `AllowTimeSlicing`, `AllowMIG`,
  `GPUCountOverride`.
- **`BenchmarkTargets`**: `ContextLength`, `ExpectedConcurrency`,
  `RequestRate`, `TargetTTFT`, `TargetThroughput`.
- **`SecurityConfig`**: `CVEThreshold`, `SecurityThreshold`,
  `CustomBenchmarkData`, `CustomBenchmarkFile`.
- **`DeploymentConfig`**: `ValuesContent`, `OpenShiftConsoleDomain`.

`TargetEnvironment`, `SandboxNamespace`, `StagingNamespace`,
`PromotionNamespaces`, and `AdvisorEndpoint` don't fit any of the four
named groups cleanly (they're environment/promotion-target selection,
not GPU/benchmark/security/deployment concerns) and were left flat
directly on `ModelRequirements`, unchanged from before this phase.

**Not moved: `MaaSOverride`.** The plan's example text lists "MaaS
override" under `DeploymentConfig`, but `MaaSOverride` (`spec.maas`) is
actually a field on `ModelRequestSpec`, not `ModelRequirements` -- it
was never one of the ~20 flattened fields in the struct being split.
Left it where it is; flagging this discrepancy between the plan's
prose and the actual code rather than silently moving a field across
structs that wasn't asked for.

### Not a breaking API change: wire format preserved via anonymous embedding

Each sub-struct is embedded **anonymously** (`GPUConfig` as a bare
embedded field, not `GPUConfig GPUConfig \`json:"gpuConfig,omitempty"\`\`)
with a `json:",inline"` tag. Go's `encoding/json` (and `sigs.k8s.io/yaml`,
which round-trips through it) promotes an anonymous field's exported
members to the parent struct's level by default; the `,inline` tag is
documentation of that intent (matching the existing
`metav1.TypeMeta \`json:",inline"\`` pattern already used elsewhere in
this API package), not what actually causes the flattening. Verified
this holds, not just assumed it:

- New tests in `operator/api/v1alpha1/modelrequest_types_test.go`
  (written first, before touching the struct) pin a golden flat
  JSON/YAML shape -- one at the `ModelRequirements` level, one at the
  full `ModelRequest` CR spec level (`requirements:` nested inside a
  complete spec) -- and assert unmarshal-then-remarshal reproduces the
  identical decoded shape, plus a explicit check that no field ends up
  nested under a wrapper key (`gpuConfig`, etc). Ran these against the
  **pre-refactor** flat struct first to confirm they pass (the TDD
  starting point), then again after the struct split with the test
  file itself unchanged -- both pass.
- `make manifests` regenerated
  `operator/config/crd/bases/modelops.example.io_modelrequests.yaml`;
  diffed field-by-field against the pre-Phase-2 version. The only diff
  is an added `description:` block (from the new Go doc comment on
  `ModelRequirements`) -- the `requirements.properties` field list
  itself is byte-identical, still 20 flat sibling properties, no
  `gpuConfig`/`benchmarkTargets`/etc. wrapper object appeared.
  Confirmed `make manifests` is idempotent (second run, no diff).
- Verified live on the sandbox cluster (see below): a real
  `ModelRequest` submitted with the exact same flat `requirements:`
  YAML shape as before this phase reconciled to `SandboxRunning`
  cleanly, and the resulting `PipelineRun`'s params (`gpu-count-override`,
  `context-length`, `allow-time-slicing`, `artifact-cve-threshold`,
  `values-content`, `openshift-console-domain`, etc. -- one field from
  each of the four new sub-structs) carried the correct submitted
  values through.

If a future field genuinely can't be inlined this way (e.g. two
sub-structs sharing a field name, which would create ambiguous
selectors), that would need to be flagged before proceeding -- it
didn't come up here since the four groups' fields are disjoint.

### Call site updates

`buildCapacityPlan`, `buildSandboxPipelineParams`, and
`buildPromotionPipelineParams` in
`operator/internal/controller/modelrequest_controller.go` were updated
to use the explicit nested path (e.g. `reqs.GPUConfig.GPUCountOverride`,
`reqs.BenchmarkTargets.ContextLength`, `reqs.SecurityConfig.CVEThreshold`,
`reqs.DeploymentConfig.ValuesContent`) rather than relying on Go's
field-promotion to keep accessing them unqualified as `reqs.GPUCountOverride`
etc. Promotion would have compiled either way (confirmed: `go build ./...`
succeeds with zero call-site changes, before this cleanup pass) -- the
explicit path was chosen only so the sub-struct grouping is visible in
the code that consumes it, per the plan's stated goal ("internal
organization should make it obvious which fields belong to which
concern"), not because it was functionally required.

### Cross-stage import check

No package under `internal/stages/*` needed a new import as a result of
this change. `ModelRequirements` stays in `api/v1alpha1`, where it
already lived -- Phase 2 doesn't relocate any logic into
`internal/stages/*` (those packages are still Phase 0's `doc.go` stubs
with no real code; that relocation is Phase 4+'s job). The instruction
to put this in `internal/stagecommon` "if that's where the type
currently lives" didn't apply since it doesn't.

### Test coverage added

- `operator/api/v1alpha1/modelrequest_types_test.go` (new file, 3
  tests): `TestModelRequirements_WireFormatUnchanged_RoundTrip`,
  `TestModelRequirements_WireFormatUnchanged_NoWrapperKeys`,
  `TestModelRequest_WireFormatUnchanged_FullCRRoundTrip`.
- `operator/internal/controller/modelrequest_controller_test.go`:
  updated the two pre-existing `ModelRequirements{...}` struct literals
  that set now-relocated fields directly (`ContextLength`/
  `ExpectedConcurrency` at line ~116, `GPUCountOverride` at line ~501)
  to use the new nested sub-struct literal form. No test *behavior*
  changed, only the literal syntax needed to compile against the new
  struct shape. All other existing `ModelRequirements{...}` literals in
  this file only set unmoved fields (`PromotionNamespaces`) or are
  empty (`&ModelRequirements{}`) and needed no change.
- Total suite: 46 tests passing (43 from Phase 1 + 3 new), all
  envtest-backed except the 3 new ones (plain `go test`, no envtest
  needed for pure marshal/unmarshal).

### Manifest regeneration

`make manifests generate` (controller-gen v0.16.5) run. CRD diff
described above (description-only). `zz_generated.deepcopy.go` gained
`DeepCopyInto`/`DeepCopy` for the four new sub-struct types;
`ModelRequirements.DeepCopyInto` now delegates
`in.GPUConfig.DeepCopyInto(&out.GPUConfig)` (needed, since `GPUConfig`
holds the two `*bool` pointer fields that need a real deep copy) and
plain-assigns the other three sub-structs (`out.BenchmarkTargets =
in.BenchmarkTargets`, etc. -- correct, since none of those three hold
pointer/slice/map fields). No other CRD (`capacityplans`,
`platformconfigs`, `lifecycleprofiles`) changed --
`CapacityPlanSpec` coincidentally shares 4 field *names*
(`ContextLength`, `AllowTimeSlicing`, `AllowMIG`, `AdvisorEndpoint`)
with `ModelRequirements` but is a distinct, unrelated struct not
touched by this phase.
`gitops/components/operator/crd-modelrequests.yaml` was synced from the
regenerated `config/crd/bases/*` output, same as the Phase 1 pattern
(confirmed the pre-Phase-2 copy was still byte-identical to the
pre-Phase-2 base before overwriting it, so no undetected drift was
carried forward).

### Sandbox cluster verification

- Rebuilt and pushed a new `quay.io/jhurlocker/modelops-operator:latest`
  image from this phase's code (same as Phase 1 -- the deployment runs
  from this pre-built tag, ArgoCD only manages the surrounding
  manifests) and rolled the `Deployment` in `modelops` to pick it up.
- Pushed this phase's commit to `feat/model-request-controller`;
  `Application/modelops-operator` (branch-tracked, auto-sync +
  self-heal, confirmed still pointed at this branch/path from Phase
  0/1) synced to `bf40ade` after a manual hard-refresh nudge (routine
  ArgoCD polling would have picked it up on its own within its normal
  interval; the refresh was only to avoid waiting in this session).
  Confirmed via `oc get crd modelrequests.modelops.example.io` that the
  live schema's `requirements.properties` is still 20 flat sibling
  properties with no wrapper object, matching the Git-committed CRD.
- Created a disposable `ModelRequest` (`phase2-verify`, `sandbox`
  namespace, referencing the pre-existing `scan-s3-credentials`/
  `result-s3-credentials` secrets and the `standard-generative-onboarding`
  profile) with a `requirements:` block touching one field from each of
  the four new sub-structs plus `gpuCountOverride`. Reconciled cleanly
  to `SandboxRunning`; the generated `PipelineRun`'s params
  (`gpu-count-override=3`, `context-length=4096`, `concurrency=2`,
  `allow-time-slicing=true`, `allow-mig=false`,
  `gpu-isolation-policy=dedicated`, `artifact-cve-threshold=high`,
  `severity-threshold=block`, `values-content=replicaCount: 1`,
  `openshift-console-domain=apps.example.com`, `request-rate=4.0`,
  `target-ttft=500ms`, `target-throughput=100`) all carried the
  submitted values correctly. Deleted the test `ModelRequest` and its
  `PipelineRun` afterward -- disposable verification, not a permanent
  cluster change.

### Known follow-up NOT done in this phase

- The plan's example grouping mentions "MaaS override" under
  `DeploymentConfig`, but no such field exists on `ModelRequirements`
  today (`MaaSOverride` lives on `ModelRequestSpec` as `spec.maas`,
  entirely separate from `spec.requirements`). Flagged above; no code
  change made since moving `spec.maas` into `requirements` isn't
  something this phase was asked to do and would itself be a breaking
  API change (a different top-level key) if done without care.
- `operator/go.mod` picked up `sigs.k8s.io/yaml` moving from an
  indirect to a direct dependency (the new test file imports it
  directly for the CR-level round-trip test). `go.sum` unchanged --
  the module was already present, just re-flagged as direct.

---

## Phase 3 — De-duplicate the pipeline param builders

**Commit:** `de3b93c` on `feat/model-request-controller` — "Phase 3:
de-duplicate buildSandboxPipelineParams and
buildPromotionPipelineParams". Not a breaking API/CRD change -- no
`_types.go` file touched.

### TDD: characterization tests written first

Per the guiding principle ("a refactor is not done until there's a test
proving the behavior is unchanged"), before touching either function:

- Added `TestBuildSandboxPipelineParams_FullFixture_CharacterizesCurrentOutput`
  and `TestBuildPromotionPipelineParams_FirstAndLastNamespace_FullFixture_CharacterizesCurrentOutput`
  / `..._MiddleNamespace_OmitsApprovalURL_AndRunRegisterFalse` in
  `modelrequest_controller_test.go`, each against a single shared fixture
  (`fullCharacterizationFixture`) with **every** field the two functions
  read from populated with a distinct, non-empty/non-zero value, so no
  `addParam` call is silently skipped for being empty. `paramsToMap`
  fails the test outright if any param name appears more than once --
  the exact shape of the Phase 1 `gpu-count-override` duplicate-param
  bug -- so a regression there would be caught immediately, not just a
  wrong value.
- Ran these against the **pre-refactor** code first and confirmed they
  passed (the actual TDD starting point: capture today's real behavior,
  don't hand-derive an "expected" one and hope it matches). Only then
  extracted `internal/stagecommon.BuildCommonModelParams` and rewired
  both functions to call it. All three tests, and the two direct
  `internal/stagecommon` unit tests added alongside the new function,
  pass unmodified after the extraction -- proving it strictly
  output-preserving: same param sets in, same params out.
- Param comparison is by `map[string]string` (name -> value), not by
  slice order -- confirmed order doesn't matter to Tekton (`PipelineRun`
  binds params by name) and the pre-existing test helpers
  (`findParam`/`findAllParams`) already searched by name, not position.

### What changed

`buildSandboxPipelineParams` and `buildPromotionPipelineParams`
(`operator/internal/controller/modelrequest_controller.go`) had 41
params that were byte-for-byte identical between them: model identity
(8), modelcar reference (2, though `modelcar-image` is always `""` and
so never actually emitted -- `addParam` skips empty values), the
GPU/benchmark config block excluding `gpu-count-override` (15),
deployment/chart config (6), EvalHub config (2), `openshift-console-domain`
(1), `huggingface-token` (1), result-S3 config (3), and model-registry
config (3). Extracted into `internal/stagecommon.BuildCommonModelParams(spec
modelopsv1alpha1.ModelRequestSpec, reqs *modelopsv1alpha1.ModelRequirements,
cfg *modelopsv1alpha1.PlatformConfig, secrets stagecommon.Secrets)
tektonv1.Params`. Both functions now call it first, then layer their own
stage-specific params on top (sandbox: `target-namespace`,
`gpu-count-override`, the artifact-scan/severity-threshold/tenant-ns
block, scan-S3 credentials and compliance/security buckets; promotion:
`target-namespace`/`plan-id`, `gpu-count-override`, the approval-gate
block, the GuideLLM benchmark block, access/MaaS config, `run-register`).

`stagecommon.Secrets` is a small struct (`EvalHubToken`,
`HuggingFaceToken`, `ResultS3Endpoint`, `ResultS3AccessKey`,
`ResultS3SecretKey`) holding only the fields `BuildCommonModelParams`
needs -- deliberately not the scan-specific S3 credentials, which stay
sandbox-only. Each caller builds one from its own `resolvedSecrets`
(an internal/controller-private type that stays private; `stagecommon`
never needs to know it exists).

### The Phase 1 `gpu-count-override` fix: confirmed correctly scoped, not reintroduced as a duplicate

This was called out explicitly as something to verify, and it surfaced a
real pre-existing behavioral divergence that's easy to miss:

- **`buildSandboxPipelineParams`** (Phase 1's fix): an explicit
  `reqs.GPUConfig.GPUCountOverride` wins over the `CapacityPlan`-derived
  value; the plan-derived value is only used as a fallback. Exactly one
  `gpu-count-override` param is ever added.
- **`buildPromotionPipelineParams`**: always uses only the
  `CapacityPlan`-derived value (`plan.Status.GPUsNeeded`) and never looks
  at `reqs.GPUConfig.GPUCountOverride` at all. This was true **before**
  this phase too (Phase 1's fix, per `REFACTOR_PLAN.md`, was explicitly
  scoped to `buildSandboxPipelineParams` only) -- this phase did not
  introduce this divergence, it only had to decide what to do with it
  during extraction.
- **Decision: `gpu-count-override` stays out of `BuildCommonModelParams`
  entirely**, and each function keeps its own single `AddParam` call for
  it, now with an explicit comment cross-referencing the other
  function's different behavior. Folding it into the shared helper would
  have forced a choice between two bad outcomes: (a) silently giving
  promotion the override-aware behavior too, changing real behavior
  outside this phase's stated scope ("relocate, don't change"), or (b)
  re-implementing the same sandbox-vs-promotion if/else split *inside*
  the shared helper, which duplicates the exact decision logic this
  refactor exists to remove -- just relocated, not eliminated.
- Verified both on `envtest` (new characterization tests, and the
  pre-existing Phase 1 golden tests for sandbox's override precedence)
  and live on the sandbox cluster: a disposable `ModelRequest` with
  `gpuCountOverride: "3"` produced `gpu-count-override=3` (exactly one
  instance) on the sandbox `PipelineRun`, and `gpu-count-override=1`
  (the `CapacityPlan`-derived value for that request's small
  context-length/concurrency, ignoring the override) on the promotion
  `PipelineRun` -- confirming the documented divergence is real,
  unchanged, and not a duplicate-param bug of any kind.

### Single source of truth for the tiny param-building helpers too

`addParam`, `strOrDefault`, `intOrDefault`, and `boolOrDefault` (used by
`BuildCommonModelParams` and, before this phase, duplicated as private
functions in `internal/controller`) were moved to `internal/stagecommon`
and exported (`AddParam`/`StrOrDefault`/`IntOrDefault`/`BoolOrDefault`).
`buildCapacityPlan`'s six call sites and the remaining stage-specific
calls in both builder functions were updated to the `stagecommon.`-
qualified versions; the private copies in `internal/controller` were
deleted. `floatOrDefault` stays private in `internal/controller` --
it's only ever used by `buildPromotionPipelineParams`'s
`guidellm-rate` formatting, never part of the sandbox/promotion-shared
param set, so exporting it would have been scope creep with no
de-duplication benefit.

### Net lines of code: an honest accounting, not just a headline number

`REFACTOR_PLAN.md` asks for "a net reduction in lines of code" from this
phase. The two functions this phase specifically targets did shrink
substantially: `buildSandboxPipelineParams` went from 92 to 55 lines,
`buildPromotionPipelineParams` from 116 to 83 -- a combined 208 -> 138
lines, **-70 lines (-34%)** in the exact functions named in the task.
`operator/internal/controller/modelrequest_controller.go` as a whole
shrank by 95 lines (969 -> 874).

That said, **total production code across the repo grew by roughly +44
lines**, because the duplicated logic didn't disappear, it moved to a
new shared home that needed its own package/import boilerplate,
`Secrets` struct, and -- per this phase's own explicit instruction to
confirm the Phase 1 fix's scoping -- a fairly detailed rationale comment
explaining exactly why `gpu-count-override` is deliberately excluded
from the shared helper (see above). `internal/stagecommon/params.go` is
133 lines; `doc.go`'s update added 6. Test code grew substantially more
(+322 lines in `modelrequest_controller_test.go`, +186 in the new
`internal/stagecommon/params_test.go`) -- expected and welcomed under
the TDD guiding principle, not counted against the "net reduction"
goal, which reads as being about production/duplicated logic, not test
coverage.

Flagging this discrepancy explicitly rather than letting a stat like
"742 insertions, 192 deletions" stand unexplained: the qualitative goal
Phase 3 actually cares about -- "a single source of truth for any param
that's shared between sandbox and promotion" -- was met without
exception (0 params still hand-duplicated between the two functions,
confirmed by the characterization tests' exact-map comparison), and the
two named functions did get smaller. The repo-wide total line count
went up slightly because eliminating duplication of substantial logic
(41 params' worth) still costs a small, fixed amount of shared-package
scaffolding that doesn't scale with how much duplication it removes --
that scaffolding cost is what tipped the total slightly positive here.

### Cross-stage import check

`internal/stagecommon` imports only `api/v1alpha1` and the Tekton API
types (`go list -deps` confirmed) -- never `internal/controller` or any
`internal/stages/*` package. No package under `internal/stages/*`
imports another; all three (`sandbox`, `promotion`, `capacityplanning`)
are still Phase 0's empty `doc.go` stubs, since this phase (per
`REFACTOR_PLAN.md`'s own phrasing, "extract...into a common helper...
have both functions call it") only extracts the shared logic out of the
two `internal/controller` functions -- it does not relocate
`buildSandboxPipelineParams`/`buildPromotionPipelineParams` themselves
into `internal/stages/sandbox`/`internal/stages/promotion`. That
relocation, per the Phase 0/2 `doc.go` comments, is later phases' job
(Phase 4's `StageRunner` work is the natural point to do it, since it's
already moving stage-specific logic into these packages).

### Manifest regeneration

No `_types.go` file was touched. `make manifests generate` run anyway
per this phase's instructions to confirm; `git status` showed zero diff
under `operator/config/` or `operator/api/` -- confirmed a no-op, as
expected.

### Test coverage added

- `operator/internal/controller/modelrequest_controller_test.go`: three
  new characterization tests (see "TDD" section above), plus a local
  `boolPtr` helper and the shared `fullCharacterizationFixture`/
  `paramsToMap` fixtures reused by all three.
- `operator/internal/stagecommon/params_test.go` (new file): two direct
  unit tests for `BuildCommonModelParams` --
  `TestBuildCommonModelParams_FullFixture_ProducesExpectedSharedParams`
  (full fixture, plus an explicit assertion that `gpu-count-override`
  never appears in this function's output) and
  `TestBuildCommonModelParams_DefaultsAppliedWhenFieldsEmpty` (every
  default value, plus confirming fields with no default -- tokenizer,
  advisor-endpoint, evalhub-url -- are omitted rather than emitted
  empty). These test `internal/stagecommon` in complete isolation from
  `internal/controller`, proving the extraction is independently usable
  by a future stage package without needing the controller's test
  scaffolding.
- Total suite: 52 tests passing (49 from Phase 2 + 3 new
  characterization tests in `internal/controller`, none removed) plus 2
  new in `internal/stagecommon` (previously `[no test files]`). All
  `internal/controller` tests remain `envtest`-backed;
  `internal/stagecommon`'s are plain `go test` (no `envtest` needed --
  pure function, no Kubernetes client involved).

### Sandbox cluster verification

- Rebuilt and pushed a new `quay.io/jhurlocker/modelops-operator:latest`
  image from this phase's code (same pattern as Phases 1-2 -- the
  `Deployment` runs from this pre-built tag with `imagePullPolicy:
  Always`, so a `kubectl rollout restart` after the push is what
  actually picks up new code; `Application/modelops-operator` itself
  had nothing new to sync since no manifest changed).
- Confirmed `Application/modelops-operator` stayed `Synced`/`Healthy`
  throughout (expected: this phase touched no file under
  `gitops/components/operator/*`).
- Created a disposable `ModelRequest` (`phase3-verify`, `sandbox`
  namespace, referencing the pre-existing `scan-s3-credentials`/
  `result-s3-credentials` secrets and the `standard-generative-onboarding`
  profile, `requirements.gpuCountOverride: "3"`,
  `contextLength: 4096`, `expectedConcurrency: 2`). Reconciled cleanly to
  `SandboxRunning`; the sandbox `PipelineRun`'s params matched the
  characterization tests exactly, including `gpu-count-override=3`
  (the explicit override, exactly once) and every `BuildCommonModelParams`
  param (`model-id`, `evalhub-url`/`evalhub-token`, `s3-api-endpoint`,
  `mr-server`, `chart-url`, `hardware-profile-name`, etc.) present with
  the expected values.
- Manually flipped the sandbox `PipelineRun`'s `Succeeded` condition to
  `True` (no real Tekton pipeline execution needed for this check) to
  drive the request into `PromotionRunning`, then confirmed the
  promotion `PipelineRun`'s params: `gpu-count-override=1` (the
  `CapacityPlan`-derived value for this request's small context-length/
  concurrency, correctly **ignoring** the `"3"` override -- confirming
  the documented sandbox/promotion divergence live, not just in tests),
  plus every `BuildCommonModelParams` param present with matching
  values, and promotion-only params (`approval-api-url`,
  `guidellm-rate=4.0`, `run-register=true`, etc.) all correct for a
  single-namespace, first-and-last promotion.
- Deleted the test `ModelRequest` and its two `PipelineRun`s afterward --
  disposable verification, not a permanent cluster change.

### Known follow-up NOT done in this phase

- The net-LOC discrepancy above (net +44 production lines despite the
  two target functions shrinking by 70) is fully explained, not a loose
  end needing more work -- flagging it as documentation, not a TODO.
- `buildSandboxPipelineParams`/`buildPromotionPipelineParams` still live
  in `internal/controller`, not `internal/stages/sandbox`/
  `internal/stages/promotion`. Confirmed intentional (see "Cross-stage
  import check" above) and expected to happen as part of Phase 4's
  `StageRunner` relocation work, not silently deferred without
  explanation.

---

## Phase 4 — Decouple the core reconciler from Tekton

**Commit:** `3d336bb` on `feat/model-request-controller` — "Phase 4:
decouple ModelRequestReconciler from Tekton via StageRunner". Not a
breaking API/CRD change -- no `_types.go` file touched, `make manifests
generate` confirmed a no-op.

This phase went through an explicit design review first (per the user's
request, the same as Phase 0) before any code was written; the approved
design is what's summarized below. See the design-review conversation
for the full rationale on each shape decision.

### What changed

- **`internal/stagecommon/stage.go`** (new): `StagePhase`
  (`StageRunning`/`StageSucceeded`/`StageFailed`), `StageStatus`
  (`Phase`/`Reason`/`Message`/`RunRef`), `StageSpec`
  (`Name`/`RunName`/`WorkflowRef`/`Params map[string]string`), and the
  `StageRunner` interface (`EnsureRun(ctx, *ModelRequest, StageSpec)
  (StageStatus, error)`) -- the generic status/execution contract
  `ModelRequestReconciler` now depends on instead of `tektonv1` directly.
- **`internal/stagecommon/fake.go`** (new): `FakeStageRunner`, an
  in-memory `StageRunner` for tests. Deliberately a real (non-`_test.go`)
  file, since Go can't import another package's `_test.go` files across
  a package boundary and `internal/controller`'s tests need to construct
  one. Records every `StageSpec` it's called with (`Calls`) and serves
  scripted `StageStatus` values per stage name (`ScriptStage`), with the
  last scripted value repeating once its queue drains -- fixed one bug
  in this repeat logic during development (see "TDD" section below).
- **`internal/stagecommon/params.go`**: `BuildCommonModelParams` and
  `AddParam` now build/take `map[string]string` instead of
  `tektonv1.Params`. This is what actually lets
  `ModelRequestReconciler` stop importing `tektonv1` to build a stage's
  inputs -- previously `stagecommon` (and therefore anything calling
  it) was Tekton-typed even though none of the param *values* are
  Tekton-specific. `AddParam`'s doc comment now explains a deliberate
  side effect: building directly into a map means a second `AddParam`
  call for an already-set name silently overwrites rather than
  producing a duplicate slice entry -- a *stronger* structural fix for
  the Phase 1/3 duplicate-param bug class (a duplicate literally cannot
  reach a real `PipelineRun`'s `Spec.Params` now) at the cost of losing
  the old tests' loud "duplicate detected" failure for a *different*
  mistake (two unrelated params accidentally sharing a name overwriting
  silently instead of erroring). Flagged, not silently traded away.
- **`internal/stages/tekton`** (new package): `StageRunner`, the
  Tekton-backed `stagecommon.StageRunner` implementation. Holds,
  verbatim-relocated from `modelrequest_controller.go`: `buildPipelineRun`
  (workspace bindings -- `shared-workspace`/`guidellm-output-pvc` PVC,
  `manifests`/`mmlu-manifest` and `custom-mmlu` ConfigMaps,
  `ServiceAccountName: "pipeline"`, unbounded pipeline timeout),
  condition-reading (`mapCondition`: no condition or `Unknown` ->
  `StageRunning`; `False` -> `StageFailed`; `True` -> `StageSucceeded`,
  `Reason`/`Message` passed through verbatim), and the new
  `toTektonParams` conversion (`map[string]string` -> `tektonv1.Params`,
  same empty-value-omitted guard as `AddParam`).
- **`internal/controller/modelrequest_controller.go`**:
  `ModelRequestReconciler` gains a `StageRunner stagecommon.StageRunner`
  field. The sandbox phase and the per-namespace promotion loop now call
  `r.StageRunner.EnsureRun(ctx, &modelRequest, stagecommon.StageSpec{...})`
  and switch on the returned `StageStatus.Phase`, instead of
  `r.Get`/`buildPipelineRun`/`createIgnoringAlreadyExists`/
  `GetCondition("Succeeded")` inline. `buildSandboxPipelineParams`/
  `buildPromotionPipelineParams` stay in `internal/controller` for this
  phase -- only their return type changed
  (`tektonv1.Params` -> `map[string]string`) -- see "Deliberately not
  done this phase" below. The old private `buildPipelineRun` function
  was deleted from this file (moved to `internal/stages/tekton`).
  `tektonv1` is still imported here for exactly one thing:
  `SetupWithManager`'s `.Owns(&tektonv1.PipelineRun{})` watch
  registration -- manager-wiring code, not `Reconcile`'s domain logic,
  and out of this phase's stated goal ("`ModelRequestReconciler` should
  never import `tektonv1` directly **or read a Tekton condition**" --
  `Reconcile` itself now does neither). Flagged as a known residual, a
  natural candidate for Phase 5/7 once a provider-agnostic "which types
  does this `StageRunner` own" hook exists.
- **`main.go`**: wires the real `tektonstage.StageRunner{Client:
  mgr.GetClient(), Scheme: mgr.GetScheme()}` into
  `ModelRequestReconciler.StageRunner` at manager setup.

### TDD: what was genuinely new vs. relocated

Per the guiding principle ("every new interface must ship with a
fake... every stage handler must be testable... without any real stage
implementation"):

- **Written first, before the implementation existed**:
  `internal/stages/tekton/stagerunner_test.go`'s
  `TestToTektonParams_*` (the `map[string]string` -> `tektonv1.Params`
  conversion didn't exist anywhere before this phase) and
  `TestBuildPipelineRun_WorkspaceBindings_MatchTodaysHardcodedShape`
  (nothing in the pre-Phase-4 suite asserted on workspace bindings
  directly -- only `PipelineRef`/`OwnerReferences`/`Params` were ever
  checked -- so this closes a real, previously-uncovered gap while
  relocating `buildPipelineRun`, not just re-testing what Phase 0-3
  already covered). Also new:
  `TestEnsureRun_*` directly isolating the condition-mapping table
  (freshly-created/no-condition, `Unknown`, `True`, `False`) that used
  to only be exercised indirectly, inline in `Reconcile`.
- **A real bug caught by this TDD process, not just a hypothetical
  benefit of it**: the first draft of `FakeStageRunner`'s "last
  scripted value repeats" behavior kept the already-served single-item
  queue in place after serving it (so it could keep "repeating"); a
  later `ScriptStage` call for the same stage appended *after* that
  stale item instead of replacing it, so the next `EnsureRun` call
  served the *old* value again instead of the newly-scripted one. Caught
  immediately by
  `TestModelRequest_FullLifecycle_DrivenEntirelyByFakeStageRunner_NoTektonInvolved`
  failing its second assertion (expected `PromotionRunning`, got
  `SandboxRunning` again). Fixed by splitting `FakeStageRunner`'s state
  into a `pending` queue (always consumed front-to-back) and a separate
  `last`-served value used only once `pending` is empty, so a fresh
  `ScriptStage` call always takes priority over a stale repeat.
- **Characterization-verified relocation** (not new logic): the
  `buildPipelineRun` body itself, and the four-way condition branch,
  are unchanged line-for-line from `modelrequest_controller.go`'s
  pre-Phase-4 version -- proven by every pre-existing
  `internal/controller` characterization test still passing unmodified
  once `newModelRequestReconciler()` was wired to construct the real
  `tekton.StageRunner` instead of leaving `StageRunner` nil.

### The two proof tests (plus a failure-path variant)

`internal/controller/modelrequest_stagerunner_test.go` (new file):

1. `TestModelRequest_FullLifecycle_DrivenEntirelyByFakeStageRunner_NoTektonInvolved`
   -- runs against this package's shared `envtest` apiserver (which
   does have the `PipelineRun` CRD installed, since other tests in this
   package need it), reconciles through
   `SandboxRunning -> PromotionRunning -> Succeeded` using only a
   `stagecommon.FakeStageRunner`, and after **every** reconcile asserts
   a `tektonv1.PipelineRunList` for the test's namespace comes back
   empty. Also asserts the `StageSpec` the reconciler actually built for
   the promotion stage (`WorkflowRef`, `Params["model-id"]`,
   `Params["target-namespace"]`, `Params["run-register"]`) matches the
   real `ModelRequest`/profile/`PlatformConfig`/`CapacityPlan` inputs --
   proving only execution/status-reading is faked, not the domain logic
   that decides what to run.
2. `TestModelRequest_SandboxFails_UsingFakeStageRunner_ReportsFailedPhase_NoTektonInvolved`
   -- the failure-path companion: a scripted `StageFailed` status
   produces `ModelRequest.Status.Phase == "Failed"` with the fake's
   message surfaced, still with zero `PipelineRun`s ever created.
3. `TestModelRequest_FullLifecycle_FakeClientWithoutTektonScheme` -- the
   strongest form of the proof, exactly as proposed in the design
   review: builds its own `controller-runtime/pkg/client/fake` client
   whose `runtime.Scheme` registers `client-go`'s scheme and
   `api/v1alpha1`, but **never** `tektonv1.AddToScheme`. Seeds the
   `Namespace`/`Secret`/`PlatformConfig`/`ModelLifecycleProfile`/
   `ModelRequest`/`CapacityPlan` (pre-set to `Succeeded`) objects
   directly, scripts both stages to `StageSucceeded`, reconciles once,
   and asserts the overall phase reaches `Succeeded` with exactly 2
   `FakeStageRunner.Calls` (`sandbox`, `promotion-staging`). Since the
   scheme never learned what a `tektonv1.PipelineRun` is,
   this is stronger than "zero were created" -- the reconciler
   literally could not have constructed one even if some code path had
   tried.

### Reconciler-level regression net: mechanical harness change, same assertions

`newModelRequestReconciler()` (`modelrequest_controller_test.go`) now
wires `StageRunner: &tektonstage.StageRunner{Client: k8sClient, Scheme:
scheme}` instead of leaving the field nil. Every pre-existing
characterization test in this package (sandbox creation, pending/no-op,
failure, promotion creation/multi-namespace/failure/success, the
profile-override end-to-end test) passed **unmodified** once this one
helper function changed -- confirming the Tekton-specific behavior
really was relocated, not altered. This is the concrete Phase 0
regression net doing its job for Phases 4-6, per `REFACTOR_PLAN.md`'s
own description of what it's for.

The param-builder-focused tests needed **mechanical, signature-only**
updates (no assertion values changed), since
`buildSandboxPipelineParams`/`buildPromotionPipelineParams` now return
`map[string]string` directly instead of `tektonv1.Params`:
`TestBuildSandboxPipelineParams_ExplicitOverride_TakesPrecedenceAndAppearsExactlyOnce`/
`_NoOverride_FallsBackToPlanDerivedGPUCount`/`_NoOverrideAndNoPlan_OmitsParam`
switched from `findAllParams(params, ...)` to direct map lookups; the
two full-fixture characterization tests
(`TestBuildSandboxPipelineParams_FullFixture_CharacterizesCurrentOutput`,
`TestBuildPromotionPipelineParams_FirstAndLastNamespace_FullFixture_CharacterizesCurrentOutput`,
`TestBuildPromotionPipelineParams_MiddleNamespace_OmitsApprovalURL_AndRunRegisterFalse`)
dropped their `paramsToMap(t, params)` conversion step since the
function's own return value already is that map now -- the `want`
maps and every asserted value are byte-identical to before this phase.
Same treatment for `internal/stagecommon/params_test.go`'s two direct
`BuildCommonModelParams` tests.

### Deliberately NOT done this phase (see REFACTOR_PLAN.md Phase 6 note)

`buildSandboxPipelineParams`/`buildPromotionPipelineParams`/
`sandboxPipelineNameOrDefault`/`promotionPipelineNameOrDefault`/
`getPromotionNamespaces` still live in `internal/controller`, not
`internal/stages/sandbox`/`internal/stages/promotion` -- only their
Tekton-typed return values changed. `REFACTOR_PLAN.md`'s own Phase 4
text only names "PipelineRun construction, workspace bindings, and
condition-reading" as what moves into `TektonStageRunner`; the param
*values* were never Tekton-specific, only the type they were expressed
in, so changing that type was sufficient to meet this phase's stated
goal. Physically relocating these functions into their per-stage
packages is now an explicit numbered step in `REFACTOR_PLAN.md`'s
Phase 6 (added by this phase's edit to that file), since Phase 6's
stage-handler dispatch needs real per-stage logic behind
`profile.Spec.Stages` anyway -- the natural point to finish the move,
rather than doing it disconnected from that work now.

### Cross-stage import check

`go list -deps` confirmed for all four packages under
`internal/stages/*` (`sandbox`, `promotion`, `capacityplanning`,
`tekton`): none imports another. `internal/stages/tekton` imports only
`internal/stagecommon` (for the `StageRunner`/`StageSpec`/`StageStatus`
contract) and `api/v1alpha1` -- never `internal/controller` or a
sibling stage package.

### Manifest regeneration

No `_types.go` file was touched. `make manifests generate`
(controller-gen v0.16.5) run anyway per this phase's instructions;
`git status` showed zero diff under `operator/config/` or
`operator/api/` -- confirmed a no-op, as expected.

### `go.mod` note

`gopkg.in/evanphx/json-patch.v4` moved from an indirect-only mention to
an explicit `// indirect` require line -- `internal/stages/tekton`'s
test file is the first place in this module to import
`sigs.k8s.io/controller-runtime/pkg/client/fake` directly, which pulls
this transitively. `go.sum` unchanged; the module was already present
in the graph.

### Test coverage added

- `internal/stagecommon`: no new test file for `stage.go` itself (a
  pure type/interface declaration has nothing to unit test in
  isolation); `fake.go`'s behavior is exercised indirectly by every
  `internal/controller` test that uses `FakeStageRunner`, per the
  "genuinely new" TDD section above.
- `internal/stages/tekton/stagerunner_test.go` (new, 8 tests): 3 for
  `toTektonParams`, 1 for `buildPipelineRun`'s workspace bindings, 4 for
  `EnsureRun`'s condition-mapping (freshly-created, existing+`Unknown`,
  existing+`True`, existing+`False`). Plain `go test` against a
  `controller-runtime/pkg/client/fake` client -- no `envtest` needed for
  this package.
- `internal/controller/modelrequest_stagerunner_test.go` (new, 3
  tests): the two `envtest`-backed proof tests and the
  no-tekton-scheme fake-client variant, described above.
- Total suite: 49 tests passing in `internal/controller` (46 from Phase
  3 + 3 new), all still passing unmodified except the mechanical
  signature-only updates described above; 2 in `internal/stagecommon`
  (unchanged from Phase 3, mechanically updated); 8 new in
  `internal/stages/tekton`. `go build ./...`/`go vet ./...` clean.

### Sandbox cluster verification

- Pushed this phase's commit (`3d336bb`) to
  `feat/model-request-controller`; `Application/modelops-operator`
  (branch-tracked, auto-sync + self-heal) synced automatically --
  confirmed `Synced`/`Healthy` at `3d336bb` (no manifest changes this
  phase, so nothing new to actually apply beyond the revision marker).
- Rebuilt and pushed a new `quay.io/jhurlocker/modelops-operator:latest`
  image from this phase's code (same pattern as Phases 1-3 -- the
  `Deployment` runs from this pre-built tag with `imagePullPolicy:
  Always`; `kubectl rollout restart` picked it up). Manager started
  cleanly, all three `EventSource`s (`ModelRequest`, `PipelineRun`,
  `CapacityPlan`) registered without error.
- Created a disposable `ModelRequest` (`phase4-verify`, `sandbox`
  namespace, referencing the pre-existing `scan-s3-credentials`/
  `result-s3-credentials` secrets and the
  `standard-generative-onboarding` profile,
  `requirements.gpuCountOverride: "3"`). Reconciled cleanly to
  `SandboxRunning` via the new `tekton.StageRunner`; the sandbox
  `PipelineRun`'s `pipelineRef.name` (`model-intake-sandbox`) and
  params (`gpu-count-override=3`, `model-id`, `context-length=4096`)
  matched expectations, and `Status.Message` carried the real Tekton
  condition's message through (`"Tasks Completed: 0 ... Incomplete: 6
  ..."`) exactly as the pre-Phase-4 inline logic did.
- Manually flipped the sandbox `PipelineRun`'s `Succeeded` condition to
  `True` (`status` subresource patch) -- request moved to
  `PromotionRunning`; the promotion `PipelineRun` was created with
  `pipelineRef.name=model-intake-promotion`,
  `gpu-count-override=1` (the `CapacityPlan`-derived value, correctly
  **ignoring** the `"3"` override -- confirming the documented
  sandbox/promotion divergence still holds through the new code path),
  `target-namespace=staging`, `run-register=true`, the same three
  workspace bindings (`shared-workspace`/`manifests`/`custom-mmlu`),
  and a correct owner reference back to the `ModelRequest`.
  Flipped that condition to `True` as well -- request reached
  `Succeeded` with `Status.Message == "Model onboarding completed
  successfully"`.
- Deleted the test `ModelRequest`; both `PipelineRun`s were
  garbage-collected automatically via owner references (confirmed
  gone on a follow-up `get`) -- disposable verification, not a
  permanent cluster change. `Application/modelops-operator` remained
  `Synced`/`Healthy` throughout.

### Known follow-up NOT done in this phase

- `SetupWithManager`'s `.Owns(&tektonv1.PipelineRun{})` is the one
  remaining `tektonv1` import in `internal/controller` -- manager
  wiring, not `Reconcile`'s domain logic, and out of this phase's
  stated scope. A natural candidate for Phase 5 (once a provider config
  exists) or Phase 7 (RBAC/permission scoping) to address with a
  provider-agnostic "which child types does this `StageRunner` own"
  hook.
- `buildSandboxPipelineParams`/`buildPromotionPipelineParams` and their
  neighboring helpers still live in `internal/controller`, not their
  per-stage packages -- confirmed intentional, now an explicit Phase 6
  step (see "Deliberately NOT done this phase" above), not silently
  deferred.
- The Phase 0 known-behavior quirk (promotion namespaces not gated
  sequentially on each other's success) is untouched by this phase --
  still exactly the same loop structure, just calling `StageRunner`
  instead of building `PipelineRun`s inline. Unchanged on purpose; not
  this phase's concern.

---

## Phase 5 — Provider-specific configuration CRDs

**Commit:** `8a24641` on `feat/model-request-controller` — "Phase 5:
provider-specific configuration CRDs". Additive CRD/API change (see
below for exactly what's additive vs. new).

This phase went through an explicit design review first (per the
user's request, same as Phase 0/4) before any code was written; the
approved design is what's summarized below.

### What changed

- **`api/v1alpha1/intakeproviderconfig_types.go`** (new): `IntakeProviderConfig`,
  one generic CRD kind with a `providerType` discriminator
  (`+kubebuilder:validation:Enum=tekton` -- restricted to `"tekton"` for
  now, per the user's explicit choice) rather than a separate typed kind
  per backend. `IntakeProviderConfigSpec` holds exactly what used to be
  hardcoded Go constants in `internal/stages/tekton.StageRunner`
  (`ServiceAccountName`, workspace bindings, pipeline timeout) or
  embedded directly in `ModelLifecycleProfile`
  (`WorkflowRef.PipelineRef`/`PromotionPipelineRef`):
  `SandboxPipelineName`, `PromotionPipelineName`, `ServiceAccountName`,
  `PipelineTimeout`, `Workspaces` (`[]IntakeProviderWorkspace`, reduced
  to the two binding kinds `buildPipelineRun` has ever produced --
  PVC-by-name or ConfigMap-by-name). `IntakeProviderConfigStatus` is a
  `Conditions`-only field, matching the same (currently unwritten)
  precedent already set by `ModelLifecycleProfileStatus`/
  `PlatformConfigStatus`.
- **`api/v1alpha1/modellifecycleprofile_types.go`**:
  `ModelLifecycleProfileSpec` gains `ProviderConfigRef *ProviderConfigRef`
  (a new `{Name, Kind}` reference type, `Kind` defaulting to
  `"IntakeProviderConfig"`). `WorkflowRef.PipelineRef` loses its
  `required` marker (now `omitempty`) and, along with
  `PromotionPipelineRef`, is documented as DEPRECATED: honored only as a
  fallback when `ProviderConfigRef` is nil. **Additive, not breaking**:
  every existing `ModelLifecycleProfile` (including every Phase 0-4
  characterization-test fixture and the live
  `gitops/components/runtime-config/lifecycleprofile.yaml`) needs zero
  changes to keep working -- confirmed by running the full pre-Phase-5
  suite unmodified (see "TDD" section below) and, live, by the
  sandbox-cluster distinct-value test described further down.
- **`internal/stagecommon/stage.go`**: `StageSpec` gains
  `ProviderConfigRef *modelopsv1alpha1.ProviderConfigRef` (a straight
  passthrough of `profile.Spec.ProviderConfigRef`, set by the
  reconciler without any interpretation -- see its doc comment) and
  `StageKind` (`StageKindSandbox`/`StageKindPromotion`), a new, small,
  deliberate widening of the contract: `StageSpec.Name` stays
  "logging/status purposes only" per its existing doc comment, so a
  separate field is used for anything a `StageRunner` actually branches
  on (which of `IntakeProviderConfigSpec`'s two pipeline-name fields to
  resolve).
- **`internal/stages/tekton/providerconfig.go`** (new):
  `resolveProviderDetails` -- the one place in the whole codebase that
  interprets `stage.ProviderConfigRef`. `internal/controller` never
  fetches or inspects an `IntakeProviderConfig` object itself. Behavior:
  nil ref -> `defaultProviderDetails(stage.WorkflowRef)` (the DEPRECATED
  fallback, reproducing the exact pre-Phase-5 hardcoded shape --
  service account `"pipeline"`, unbounded timeout, the 3 workspace
  bindings -- now the single source of truth for those defaults,
  referenced by both the fallback path and `buildPipelineRun`'s own test);
  `Kind` other than `"IntakeProviderConfig"` (or empty) -> explicit error
  without attempting a `Get` at all; missing/otherwise-failing `Get` ->
  error propagated to `EnsureRun`'s caller (see "Known follow-up," this
  still hits the generic transient-retry path, not a dedicated status
  reason); `Spec.ProviderType != "tekton"` -> explicit error (a Go-level
  guard, since the CRD's own enum only enforces this through a real API
  server, not the fake client used in unit tests); any individual field
  left unset on an otherwise-resolved CR falls back to
  `defaultProviderDetails`'s value for that field specifically, not as
  an all-or-nothing choice.
- **`internal/stages/tekton/stagerunner.go`**: `EnsureRun`'s
  not-found branch now calls `resolveProviderDetails` before
  `buildPipelineRun`. `buildPipelineRun`'s signature changed from
  `(name, namespace, pipelineName string, ...)` to
  `(name, namespace string, details providerDetails, ...)` -- the
  function's own body is otherwise unchanged, just reading from
  `details` instead of 3 hardcoded literals. New RBAC marker:
  `+kubebuilder:rbac:groups=modelops.example.io,resources=intakeproviderconfigs,verbs=get;list;watch`,
  placed on `StageRunner` itself (not the core reconciler), per Phase
  4's own flagged direction ("a natural candidate for Phase 5/7").
- **`internal/controller/modelrequest_controller.go`**: both
  `StageSpec` construction sites (sandbox, promotion) gain
  `ProviderConfigRef: providerConfigRef(profile)` and the appropriate
  `StageKind`. The new `providerConfigRef` helper is a one-line nil-safe
  passthrough of `profile.Spec.ProviderConfigRef` -- the reconciler
  still never interprets what it points at.
- **`internal/stages/noop`** (new package): `StageRunner`, the trivial
  second `StageRunner` the plan asked for -- logs the stage it was
  asked to run and unconditionally returns `StageSucceeded`
  immediately, creating no child object of any kind. Deliberately not a
  real second execution-engine integration.
- **`docs/REFACTOR_PLAN.md`**: two bullets added to Phase 7, per the
  user's explicit request: (4) resolve the `WorkflowRef.Engine` vs.
  `IntakeProviderConfigSpec.ProviderType` redundancy this phase
  introduced; (5) give `ProviderConfigRef` resolution failures a real
  `ModelRequest` status reason instead of the generic silent-retry
  error path every other `EnsureRun` error currently falls into.

### TDD: resolveProviderDetails and the parity test written first

Per the guiding principle, `internal/stages/tekton/providerconfig_test.go`
(9 tests) was written and confirmed failing (`undefined:
resolveProviderDetails`) *before* `providerconfig.go` existed: nil-ref
defaults, sandbox/promotion CR resolution, full-CR override of
service-account/timeout/workspaces, partial-CR per-field fallback,
missing-CR error, unsupported-`Kind` error (asserted to short-circuit
*without* attempting a `Get`, by using a fake client with no object
seeded at all), empty-`Kind` defaulting, and the Go-level
unsupported-`providerType` guard (deliberately seeding a value the
CRD's own enum would reject at a real API server, to prove this
package's own defensive check independent of that enum).
`internal/stages/noop`'s 2 tests and the reconciler-level parity test
were written the same way, against the not-yet-existing `noop.StageRunner`.

### The parity test: the actual evidence the provider abstraction is real

`TestModelRequest_FullLifecycle_TektonAndNoopStageRunners_ReachSameTerminalPhase`
(`internal/controller/providerconfig_test.go`) runs the identical
fixture (profile, `PlatformConfig`, `ModelRequest`, a pre-succeeded
`CapacityPlan`) through `Reconcile` twice, as subtests: once wired to
the real `tekton.StageRunner` (requiring the usual condition-flip
dance between reconciles, same as every other test in this file), once
wired to `noop.StageRunner` (completes in one `Reconcile` call, since
it reports every stage `StageSucceeded` immediately). Both reach
`Status.Phase == "Succeeded"`, and both provision the exact same RBAC
side effects (a `"pipeline"` `ServiceAccount` in both the request's own
namespace and `"staging"`) -- proving the reconciler's actual decision
logic (RBAC provisioning, phase transitions) ran identically regardless
of which concrete `StageRunner` is injected, not just that the
interface type-checks against two implementations. The differing
reconcile-call-count between the two subtests is explicitly documented
in the test's own comment as expected, not a discrepancy the test is
papering over.

### Existing suite: verified, not assumed, to need zero modification

Ran the full pre-Phase-5 suite (59 tests) before writing any Phase 5
code to establish a clean baseline, then again after each major step.
Confirmed via `git status` that no file under `internal/controller`
needed *any* change until the two new Phase-5 test files were added
(`providerconfig_test.go` in both `internal/controller` and
`internal/stages/tekton`) -- every pre-existing characterization and
proof test in this package passed completely unmodified, including all
of Phase 4's `FakeStageRunner`/no-Tekton-scheme tests and every
param-builder golden-value test.

**One exception, exactly as anticipated in the design review and
flagged rather than silently absorbed**: `internal/stages/tekton`'s
pre-existing `TestBuildPipelineRun_WorkspaceBindings_MatchTodaysHardcodedShape`
needed a mechanical, signature-only update (`buildPipelineRun`'s new
`providerDetails` parameter instead of a bare pipeline-name string,
constructed via the new `defaultProviderDetails` helper) -- no
assertion value changed, same category of update Phase 4's own log
called out for the param-builder tests when their return type changed
from `tektonv1.Params` to `map[string]string`. All 5 of
`internal/stages/tekton`'s other pre-existing `EnsureRun`/`toTektonParams`
tests needed zero changes at all, since a nil `ProviderConfigRef`
(their default `StageSpec{}` zero value) makes `resolveProviderDetails`
skip any CR lookup and reproduce the identical hardcoded shape those
tests already asserted on.

### A real RBAC-marker gotcha caught during manifest regeneration

The first draft of the new `+kubebuilder:rbac` marker on
`tekton.StageRunner` was placed as a doc comment directly attached to
the `type StageRunner struct` declaration (no blank line separating the
marker from the declaration) -- and `controller-gen rbac` silently
produced *zero* diff to `config/rbac/role.yaml`, no error at all.
Comparing against every existing `+kubebuilder:rbac` marker in this
codebase (`capacityplan_controller.go`, `modelrequest_controller.go`)
showed they all have a **blank line** between the marker comment block
and the following declaration. Moving the new marker to match (blank
line before `type StageRunner struct`) fixed it immediately --
confirmed via `controller-gen rbac ... output:stdout` in isolation
before regenerating the real manifests. Flagging this as a real,
previously-undocumented `controller-gen` gotcha for whoever adds the
next RBAC marker to this codebase: a marker directly attached as a
Go doc comment (no blank line) to a declaration is apparently not
picked up by the `rbac` generator the same way a free-floating comment
is, and it fails **silently** (no error, just an empty diff) -- the
same failure-mode shape as Phase 0's `+groupName=` marker gotcha, just
for a different generator.

### Cross-stage import check

`go list -deps` confirmed for all five packages under
`internal/stages/*` (`sandbox`, `promotion`, `capacityplanning`,
`tekton`, `noop`): none imports another. `internal/stages/noop` imports
only `internal/stagecommon` and `api/v1alpha1` (plus
`sigs.k8s.io/controller-runtime/pkg/log`) -- never `internal/stages/tekton`
or `internal/controller`.

### Manifest regeneration

`make manifests generate` (controller-gen v0.16.5) picked up
`IntakeProviderConfig` (new CRD:
`config/crd/bases/modelops.example.io_intakeproviderconfigs.yaml`) and
`ModelLifecycleProfileSpec.ProviderConfigRef`/the now-optional
`WorkflowRef.PipelineRef` (diffed
`config/crd/bases/modelops.example.io_modellifecycleprofiles.yaml`:
`pipelineRef` moved out of `required`, `providerConfigRef` added --
no field removed). `config/rbac/role.yaml` gained
`intakeproviderconfigs` in the existing `get;list;watch` rule
(alongside `modellifecycleprofiles`/`platformconfigs`, since they share
identical apiGroup+verbs). `zz_generated.deepcopy.go` gained
`DeepCopyInto`/`DeepCopy` for the 4 new types
(`IntakeProviderConfig(List/Spec/Status)`, `IntakeProviderWorkspace`,
`ProviderConfigRef`) and `ModelLifecycleProfileSpec.DeepCopyInto` now
deep-copies the new `*ProviderConfigRef` pointer field.

### GitOps manifests (all committed, following the Phase 0/1 pattern)

- `gitops/components/operator/crd-intakeproviderconfigs.yaml` (new,
  verbatim copy of the generated base CRD, added to
  `gitops/components/operator/kustomization.yaml`) and
  `crd-lifecycleprofiles.yaml` re-synced (was already an exact copy per
  Phase 1's precedent; re-verified byte-identical after copying).
- `gitops/components/operator/clusterrole.yaml`: added the
  hand-maintained `intakeproviderconfigs` `get;list;watch` rule
  (matching this file's existing per-resource-group style, distinct
  from `config/rbac/role.yaml`'s generated aggregated style -- same
  hand-sync debt Phase 1 flagged and left alone, not addressed here).
- `gitops/components/runtime-config/intakeproviderconfig.yaml` (new):
  the live sample `IntakeProviderConfig`
  (`standard-generative-onboarding-provider`), setting only the two
  pipeline names (`sandboxPipelineName`/`promotionPipelineName`) and
  deliberately leaving `serviceAccountName`/`pipelineTimeout`/`workspaces`
  unset, to also prove the partial-CR-falls-back-per-field path live,
  not just in `envtest`. Added to
  `gitops/components/runtime-config/kustomization.yaml`.
- `gitops/components/runtime-config/lifecycleprofile.yaml`: migrated to
  set `providerConfigRef: {name: standard-generative-onboarding-provider}`,
  with `workflow.pipelineRef`/`engine` left in place but commented as
  inert -- exactly as the user requested, so sandbox-cluster
  verification exercises the real `ProviderConfigRef` resolution path,
  not the deprecated fallback.
- `operator/config/samples/intakeproviderconfig-sample.yaml` (new,
  kubebuilder convention, not ArgoCD-tracked): a fully-populated sample
  showing every field, unlike the deliberately-partial live runtime-config
  copy.
- `kustomize build` run locally against both
  `gitops/components/operator` and `gitops/components/runtime-config`
  before pushing, confirming no rendering errors and the expected new
  resources appear in the output.

### Sandbox cluster verification

- Pushed this phase's commit (`8a24641`) to
  `feat/model-request-controller`; rebuilt and pushed a new
  `quay.io/jhurlocker/modelops-operator:latest` image (same pattern as
  every prior phase -- the `Deployment` runs from this pre-built tag).
- `Application/modelops-operator`: needed an explicit hard-refresh
  annotation to pick up the new commit promptly (routine polling would
  have caught it; this just avoided waiting) -- reached `Synced`/`Healthy`
  at `8a24641`, confirmed the new `intakeproviderconfigs.modelops.example.io`
  CRD is `Established` on-cluster.
- `Application/modelops-runtime-config`: also reached
  `Synced`/`Healthy` at `8a24641`, confirming the new live
  `IntakeProviderConfig` and the migrated `ModelLifecycleProfile` (now
  showing `providerConfigRef: {name: ..., kind: IntakeProviderConfig}`,
  with `kind` defaulted by the API server from the CRD's own
  `+kubebuilder:default`, confirming that default actually works
  against a real API server, not just `envtest`) applied cleanly.
- `kubectl rollout restart` on the operator `Deployment`; manager
  started cleanly, all `EventSource`s registered without error.
- Created a disposable `ModelRequest` (`phase5-verify`, `sandbox`
  namespace, referencing the existing `scan-s3-credentials`/
  `result-s3-credentials` secrets and the now-migrated
  `standard-generative-onboarding` profile). Reconciled to
  `SandboxRunning`; the sandbox `PipelineRun`'s `pipelineRef.name`
  (`model-intake-sandbox`), `serviceAccountName` (`pipeline`, the
  fallback default since the live CR leaves it unset), and workspace
  bindings (the 3 hardcoded-default bindings, same reason) all matched
  expectations for a CR that only sets the two pipeline-name fields.
- **A real ArgoCD self-heal race was hit and worked around, exactly the
  gotcha `REFACTOR_PLAN.md`'s guiding principles warn about**: the
  first attempt at a decisive "prove this is genuinely resolved from
  the CR, not coincidentally identical to the inert fallback" test
  (patch the live `IntakeProviderConfig`'s `sandboxPipelineName` to a
  distinct value, delete+recreate the `PipelineRun`) silently failed --
  the resulting `PipelineRun` still showed the old pipeline name.
  Investigation showed `Application/modelops-runtime-config`'s
  auto-sync+self-heal reverted the manual patch back to the
  Git-committed value before the reconciler ever read it. Worked around
  by temporarily clearing that Application's `syncPolicy.automated`,
  re-running the exact same patch+delete+recreate sequence (this time
  the resulting `PipelineRun`'s `pipelineRef.name` was
  `phase5-distinct-check-pipeline`, the patched value -- decisive proof
  the live reconciler genuinely resolves `ProviderConfigRef`, not a
  coincidence of both paths agreeing), then reverting the CR patch,
  deleting the disposable `ModelRequest`/`PipelineRun`/`CapacityPlan`,
  and restoring `syncPolicy.automated` (confirmed `Synced`/`Healthy`
  again afterward, matching Git with no drift left behind).

### Known follow-up NOT done in this phase

- Both items added to `REFACTOR_PLAN.md`'s Phase 7 (see above) are
  deliberately deferred, not silently fixed: the `WorkflowRef.Engine`
  vs. `providerType` redundancy, and giving `ProviderConfigRef`
  resolution failures their own status reason instead of the generic
  transient-retry error path every other `EnsureRun` error already
  falls into.
- `gitops/components/operator/clusterrole.yaml`'s hand-maintained
  drift risk relative to the generated `config/rbac/role.yaml` (flagged
  in Phase 1) is unchanged by this phase -- the new rule was added to
  both by hand, not fixed at the tooling level.
- No second real provider (SageMaker/Databricks) was implemented, per
  the plan's explicit instruction; `internal/stages/noop` is the
  trivial stand-in that exists solely to prove the seam.

## Phase 6 — Externalize the stage sequence as data via a generic stage walker

**Commits:** `e34dfa5` ("Phase 6: externalize the stage sequence as data
via a generic stage walker") and `54fe06e` ("Fix Phase 6
PromotionPipelineRunName regression caught by sandbox cluster testing")
on `feat/model-request-controller`. Additive CRD/API change (see below
for exactly what's additive).

This phase went through an explicit design review first (per the
user's request, same as Phase 0/4/5) before any code was written; the
approved design — the `stages` field shape, the walker's decision
table and per-namespace loop, the `StageHandlers`/`StageRunners` dual
registry, the `NativeSpec` escape hatch for `CapacityPlan`, and the
`Phase`/`Message` legacy-compatibility shim — is what's implemented
below. Two decisions and two backlog notes were agreed explicitly in
review; see "What changed" and the `REFACTOR_PLAN.md` diff.

### What changed

- **`api/v1alpha1/modellifecycleprofile_types.go`**: `ModelLifecycleProfileSpec`
  gains `Stages []ProfileStageSpec` (new `ProfileStageSpec` type: `Name`,
  `Kind`, `ProviderConfigRef`, `Required *bool` (default `true`),
  `PerNamespace bool`, `NamespaceSetup *StageNamespaceSetup`) and the new
  `StageNamespaceSetup` type (`EnsureRBAC bool`, `Labels map[string]string`).
  **Additive, not breaking**: when `Stages` is empty (every existing
  `ModelLifecycleProfile`), the reconciler synthesizes the exact
  pre-Phase-6 3-stage sequence via `defaultStages()`
  (`internal/controller/stages_default.go`) — confirmed by the full
  pre-Phase-6 characterization suite passing unmodified against it (see
  "Test coverage," below).
- **`api/v1alpha1/modelrequest_types.go`**: `ModelRequestStatus` gains
  `CurrentStage string` and `Stages []StageProgress` (new `StageProgress`
  type: `Name`, `Namespace`, `Phase` (a plain string mirroring
  `stagecommon.StagePhase`'s values, so `api/v1alpha1` doesn't depend on
  `internal/stagecommon`), `RunRef`, `Message`). Additive; `Phase`/
  `Message`/`PipelineRunName`/`SandboxPipelineRunName`/
  `PromotionPipelineRunName` are unchanged fields, with unchanged
  *values* for the default stage list (see the compatibility shim below).
- **`internal/stagecommon/stage.go`**: `StageSpec` gains `NativeSpec any`
  (the agreed escape hatch — used only by the `CapacityPlan` kind, which
  needs a typed `*modelopsv1alpha1.CapacityPlanSpec` rather than a string
  param bag; every other kind leaves it `nil` and uses `Params`, and the
  walker/`Handler`/`Runner` dispatch stays uniform across all kinds, no
  bypass). New `StageContext` (what a `StageHandler` needs: `ModelRequest`/
  `Profile`/`PlatformConfig`/`CapacityPlan`/`Secrets`/`Stage`/`Namespace`/
  `NamespaceIndex`/`NamespaceCount`), new `StageHandler` interface
  (`BuildSpec(StageContext) (StageSpec, error)`), and `IsRequired(stage)
  bool` (the walker's sole `Required`-interpretation point).
- **`internal/stagecommon/params.go`**: `Secrets` gains `ScanS3Endpoint`/
  `ScanS3AccessKey`/`ScanS3SecretKey` (sandbox-stage-only; `BuildCommonModelParams`
  itself still never reads them) — needed so `sandbox.Handler` can build
  its scan-S3 params without `internal/stages/sandbox` importing
  `internal/controller`'s private `resolvedSecrets` type.
- **`internal/stagewalk`** (new package): `Walk(ctx, mr, Input) (Result, error)` —
  the generic stage walker. Pure/client-free: `Handlers`/`Runners` are
  plain maps, `Namespaces`/`SetupNamespace`/`BuildContext` are
  caller-supplied closures, so the walker's own sequencing logic is
  provable with in-memory fakes and zero Kubernetes client (see "Test
  coverage"). Its decision table, exactly as approved in review:
  `StageSucceeded` → advance; `StageRunning` → stop this stage's fan-out
  loop is *not* short-circuited for still-unattempted namespaces, but the
  whole `Walk` call stops advancing past the stage; `StageFailed` with
  `stagecommon.IsRequired(stage)` → stop the whole walk immediately
  (skips remaining namespaces too); `StageFailed` with `Required: false` →
  recorded in `Result.Progress`, walk advances anyway (the entire
  "optional/skippable" mechanism — no 4th `StagePhase` value was added,
  per the review's preference to reuse the Phase 4 contract rather than
  widen it before a real producer needs to). Depends only on
  `api/v1alpha1` and `internal/stagecommon` (verified, see "Cross-stage
  import check").
- **`internal/stages/capacityplanning`** (new `Handler`+`StageRunner`,
  package doc.go revised): `Handler.BuildSpec` relocates
  `buildCapacityPlan`'s field mapping into a `*CapacityPlanSpec`,
  attached via `StageSpec.NativeSpec`. `StageRunner.EnsureRun` is
  genuinely new (TDD: tests written first, confirmed failing on
  `undefined: StageRunner` before `stagerunner.go` existed) — a thin
  Get-or-Create-then-map-status adapter over the `CapacityPlan` object,
  type-asserting `NativeSpec` back to `*CapacityPlanSpec`. Crucially,
  **`CapacityPlanReconciler` itself (the actual GPU-sizing heuristic) is
  completely unchanged and untouched** — this package only adds the
  translation layer the walker needs to dispatch to this stage kind
  uniformly with `PipelineRun`. `mapStatus` already handles a
  `Status.Phase == "Failed"` case (`CapacityPlanReconciler` never
  produces one today — see the `REFACTOR_PLAN.md` Phase 7 backlog note
  below — but the mapping is ready for when it does).
- **`internal/stages/sandbox`** (new `Handler`, doc.go revised):
  `Handler.BuildSpec` relocates `buildSandboxPipelineParams`/
  `sandboxPipelineNameOrDefault` (renamed `PipelineNameOrDefault`)
  field-for-field. The full-fixture characterization test relocated
  alongside produced **byte-identical output on the first implementation
  attempt** — no param, default, or precedence rule needed adjustment
  during the move.
- **`internal/stages/promotion`** (new `Handler`, doc.go revised):
  `Handler.BuildSpec` relocates `buildPromotionPipelineParams`/
  `promotionPipelineNameOrDefault` (renamed `PipelineNameOrDefault`)/
  `getPromotionNamespaces` (renamed `GetNamespaces`) field-for-field.
  `isFirst`/`isLast` are now derived from `StageContext.NamespaceIndex`/
  `NamespaceCount` (supplied generically by the walker for any
  `PerNamespace` stage) instead of being passed in as explicit bools —
  this package decides what "first"/"last" means, the walker doesn't.
  `ensurePromotionNamespaceRBAC`/`ensureMaaSNamespaceLabels` deliberately
  **did not** move into this package (see "What deliberately didn't
  move," below) — this is a considered deviation from this package's
  original Phase 0 doc comment, explained in its revised doc.go.
- **`internal/controller/stages_default.go`** (new): `defaultStages(profile)`
  synthesizes the 3-entry default list. **Naming deviation, deliberate,
  same judgment call as Phase 0's `sandbox`-not-`intake`**: stage names
  are `"capacity"`/`"sandbox"`/`"promotion"`, not the plan's illustrative
  `"capacity-planning"`/`"sandbox-intake"` — because `RunName` is derived
  mechanically as `"<ModelRequest.Name>-<stage.Name>"`, and the more
  descriptive names would silently rename the `CapacityPlan`/`PipelineRun`
  child objects on upgrade (a real migration break, not a cosmetic one).
- **`internal/controller/modelrequest_controller.go`**: `ModelRequestReconciler`
  replaces its single `StageRunner stagecommon.StageRunner` field with
  `StageHandlers map[string]stagecommon.StageHandler` (keyed by
  `ProfileStageSpec.Name`) and `StageRunners map[string]stagecommon.StageRunner`
  (keyed by `.Kind`). `Reconcile` now: looks up profile/platform config
  (unchanged); resolves `stages := profile.Spec.Stages` or
  `defaultStages(profile)`; best-effort-fetches the declared
  `CapacityPlan`-kind stage's deterministic child object (via
  `capacityPlanRunName`, the one place `Reconcile` inspects a `Kind`
  string outside the walker's own dispatch — reconciler-level glue for a
  genuine cross-stage *data* dependency, not part of the walker's
  advance/stop/skip *decision* logic); builds `BuildContext`/`Namespaces`/
  `SetupNamespace` closures (secrets resolved lazily, at most once,
  skipped entirely for the `CapacityPlan` kind, preserving the exact
  pre-Phase-6 ordering guarantee that `resolveSecrets` is never attempted
  before capacity planning succeeds); calls `stagewalk.Walk`; and
  persists the result via `persistWalkResult`/`computeWalkStatus`
  (`ProfileStageSpec.NamespaceSetup`-driven RBAC/label provisioning
  replaces the old hardcoded "sandbox's own namespace, then every
  promotion namespace" / "only promotion gets MaaS labels" special
  cases — see `defaultStages()`'s `NamespaceSetup` values for exactly
  where the old `ensurePromotionNamespaceRBAC(modelRequest.Namespace,
  modelRequest.Namespace)` call and `ensureEvalHubTenantLabel`/
  `ensureMaaSNamespaceLabels` behavior moved to, as data).
- **`ensureNamespaceLabels`** (new, generalizes the old
  `ensureEvalHubTenantLabel`+`ensureMaaSNamespaceLabels` into one
  data-driven helper: "set if absent or different," applied to whatever
  `ProfileStageSpec.NamespaceSetup.Labels` says, for whatever namespace a
  stage targets — not a Go function hardcoded to "sandbox gets evalhub,
  promotion gets MaaS").
- **The `Phase`/`Message` compatibility shim** (`computeWalkStatus`,
  approved in review): for the synthesized default stage list, reproduces
  the exact pre-Phase-6 `Phase` strings (`"CapacityPlanning"`/
  `"SandboxRunning"`/`"PromotionRunning"`/`"Succeeded"`/`"Failed"`) and
  message conventions (e.g. promotion's fixed `"Promotion pipeline(s)
  running"` regardless of which namespace is running, matching the
  original hardcoded string) via a small, isolated `switch` on
  `result.CurrentStage` compared against `defaultCapacityStageName`/
  `defaultSandboxStageName`/`defaultPromotionStageName`. A profile with
  explicit `Spec.Stages` gets fully generic values instead
  (`"<CurrentStage>Running"`/`"Succeeded"`/`"Failed"`), since there's no
  pre-existing behavior to preserve for it.
- **`main.go`**: wires the real `StageHandlers`/`StageRunners` maps
  (`"capacity"→capacityplanning.Handler{}`, `"sandbox"→sandbox.Handler{}`,
  `"promotion"→promotion.Handler{}`; `"CapacityPlan"→&capacityplanning.StageRunner{...}`,
  `"PipelineRun"→&tektonstage.StageRunner{...}`) at manager setup.
- **`docs/REFACTOR_PLAN.md`**: two bullets added to Phase 7, per the
  user's explicit request: (6) deprecate the `Phase`/`Message` legacy
  shim once profiles migrate off the default `Stages` list; (7) give
  `CapacityPlan` a real `Failed` path in `CapacityPlanReconciler` so
  `Required: true` is actually meaningful for that stage kind (today it
  always eventually reaches `Succeeded`; `capacityplanning.StageRunner`'s
  `Failed` mapping is implemented and tested but has no real producer
  yet).

### A real bug caught only by live-cluster verification, not envtest

Exactly the kind of gap Phase 1's own RBAC-escalation incident already
demonstrated this repo's regression net has: `Status.PromotionPipelineRunName`
silently stayed empty forever. `lastPromotionProgress`'s first draft
prefix-matched `stagewalk.Progress.Name` against `"promotion-"`, on the
(wrong) assumption it held the per-invocation `StageSpec.Name` built by
`promotion.Handler` (e.g. `"promotion-staging"`). It actually holds the
*`ProfileStageSpec`'s own* `Name` (`"promotion"`), with the target
namespace recorded separately in `Progress.Namespace` — so the prefix
check never matched anything, and `PromotionPipelineRunName` was
computed as an empty string on every reconcile, no error, no test
failure. **No pre-existing Phase 0-5 characterization test ever asserted
on this field's value at all** (only its presence was implied by other
assertions), so nothing in the 106-test `envtest` suite caught it. It
was caught by literally running `kubectl get modelrequest phase6-verify2
-n sandbox -o yaml` against the rebuilt image on the sandbox cluster and
noticing the field was missing after a real sandbox→promotion→`Succeeded`
run — a direct instance of `REFACTOR_PLAN.md`'s own stated reason for
requiring cluster verification, not just `envtest`, for Phases 4-6.
Fixed by delegating to the same `lastProgressNamed` helper `sandbox`
already uses (exact-match on `Name`, not a namespace-suffixed prefix).
Added `TestModelRequest_AllPromotionsSucceeded_SetsPromotionPipelineRunName`
to pin it, then rebuilt/redeployed/re-verified live (see below) before
considering the phase done.

### TDD: what was genuinely new vs. relocated

Per the guiding principle:

- **`internal/stagewalk`'s 11 tests were written first**, against
  `walk.go` not existing at all (`go vet` failure: `undefined:
  NamespacesFunc`), entirely against in-memory fakes (a local
  `fakeHandler` func-adapter and `stagecommon.NewFakeStageRunner`) — no
  real stage package, no Kubernetes client, no `envtest`. All 9
  originally-scoped scenarios (1-stage, 3-stage-all-succeed,
  middle-stage-fails-stops-before-third, middle-stage-running-stops-
  before-third, optional-stage-fails-advances-anyway,
  per-namespace-fans-out-running-doesn't-short-circuit,
  per-namespace-failure-short-circuits-remaining-namespaces,
  unknown-Kind/unknown-Name config errors, handler-`BuildSpec`-error-
  stops-walk) plus 2 more added during implementation (a
  `BuildContext`-error variant, and splitting the two config-error cases
  into their own tests) — 11 total, all passing on the first
  implementation attempt with zero test-side changes needed afterward.
- **`internal/stages/capacityplanning/stagerunner_test.go`'s 6 tests
  were written before `stagerunner.go` existed** (`vet` failure:
  `undefined: StageRunner`) — this `EnsureRun` adapter has no pre-Phase-6
  equivalent to characterize (capacity-planning creation/status-reading
  was hardcoded inline in `Reconcile`, never behind any interface).
- **Characterization-verified relocation** (not new logic):
  `sandbox.Handler`'s and `promotion.Handler`'s full-fixture golden-value
  tests (moved verbatim from `internal/controller`, same fixture data,
  same expected maps) passed on the first attempt against the relocated
  implementation — proof the move didn't change any param, default, or
  precedence rule. `capacityplanning.Handler`'s 3 field-mapping tests are
  new (no pre-existing test isolated `buildCapacityPlan`'s field mapping
  from `Reconcile` before this phase), but assert the same defaults
  `buildCapacityPlan` always used.

### Cross-stage import check

`go list -deps` confirmed for all five packages under `internal/stages/*`
(`sandbox`, `promotion`, `capacityplanning`, `tekton`, `noop`): none
imports another (each command below produced no output, i.e. no match):

```
$ go list -deps ./internal/stages/sandbox/...        | grep 'internal/stages' | grep -v 'internal/stages/sandbox'
$ go list -deps ./internal/stages/promotion/...       | grep 'internal/stages' | grep -v 'internal/stages/promotion'
$ go list -deps ./internal/stages/capacityplanning/... | grep 'internal/stages' | grep -v 'internal/stages/capacityplanning'
$ go list -deps ./internal/stages/tekton/...          | grep 'internal/stages' | grep -v 'internal/stages/tekton'
$ go list -deps ./internal/stages/noop/...            | grep 'internal/stages' | grep -v 'internal/stages/noop'
```

`internal/stagewalk` (the walker itself) and `internal/stagecommon` were
also checked and depend only on `api/v1alpha1` and each other:

```
$ go list -deps ./internal/stagewalk/...
github.com/jhurlocker/modelops-operator/api/v1alpha1
github.com/jhurlocker/modelops-operator/internal/stagecommon
github.com/jhurlocker/modelops-operator/internal/stagewalk

$ go list -deps ./internal/stagecommon/...
github.com/jhurlocker/modelops-operator/api/v1alpha1
github.com/jhurlocker/modelops-operator/internal/stagecommon
```

### The modularity litmus test: an actual drill, not just an assertion

Run twice, on disposable scratch branches created from the Phase 6
commit (`e34dfa5`), each deleted afterward without merging. Exact
commands and real output below (not a description of having done it).

**Direction 1: delete `internal/stages/promotion`, confirm `sandbox` +
`capacityplanning` still build and their tests still pass unmodified.**

```
$ git checkout -b phase6-litmus-scratch-delete-promotion
$ git rm -r operator/internal/stages/promotion
rm 'operator/internal/stages/promotion/doc.go'
rm 'operator/internal/stages/promotion/handler.go'
rm 'operator/internal/stages/promotion/handler_test.go'

$ go build ./...
go: finding module for package github.com/jhurlocker/modelops-operator/internal/stages/promotion
internal/controller/modelrequest_controller.go:11:2: cannot find module providing package github.com/jhurlocker/modelops-operator/internal/stages/promotion: module github.com/jhurlocker/modelops-operator/internal/stages/promotion: git ls-remote -q origin in ...: exit status 128:
	fatal: could not read Username for 'https://github.com': terminal prompts disabled
```

Exactly one compile failure, exactly where predicted in the design
review: `internal/controller`'s import of the deleted package. `main.go`
and `internal/controller/testutil_test.go` also reference it (registry
wiring), as expected.

```
$ go build ./internal/stages/sandbox/... ./internal/stages/capacityplanning/... ./internal/stagecommon/...
(exit 0, no output)

$ go test ./internal/stages/sandbox/... ./internal/stages/capacityplanning/... ./internal/stagecommon/... -v
=== RUN   TestBuildSpec_ExplicitOverride_TakesPrecedenceAndAppearsExactlyOnce
--- PASS: TestBuildSpec_ExplicitOverride_TakesPrecedenceAndAppearsExactlyOnce (0.00s)
... (8 sandbox tests, all PASS) ...
ok  	github.com/jhurlocker/modelops-operator/internal/stages/sandbox	0.004s
=== RUN   TestHandler_BuildSpec_UsesRequirementsAndPlatformConfigWithDefaults
--- PASS: TestHandler_BuildSpec_UsesRequirementsAndPlatformConfigWithDefaults (0.00s)
... (9 capacityplanning tests, all PASS) ...
ok  	github.com/jhurlocker/modelops-operator/internal/stages/capacityplanning	0.037s
=== RUN   TestBuildCommonModelParams_FullFixture_ProducesExpectedSharedParams
--- PASS: TestBuildCommonModelParams_FullFixture_ProducesExpectedSharedParams (0.00s)
=== RUN   TestBuildCommonModelParams_DefaultsAppliedWhenFieldsEmpty
--- PASS: TestBuildCommonModelParams_DefaultsAppliedWhenFieldsEmpty (0.00s)
PASS
ok  	github.com/jhurlocker/modelops-operator/internal/stagecommon	0.004s
```

**8/8 sandbox, 9/9 capacityplanning, 2/2 stagecommon tests pass,
unmodified, with `promotion` physically absent from the repo.** This is
the core litmus assertion.

As a further, optional demonstration of the actual "org that only wants
intake + capacity-planning today" story the modularity principle
describes, the small (3-line-import, 2-map-entry) wiring fix predicted
above was also made (`main.go`, `modelrequest_controller.go`'s
`namespaces` closure, `stages_default.go`'s `defaultStages` dropped to a
2-stage list) to get the *whole repo* building with `promotion` deleted:

```
$ go build ./...
(exit 0, no output)
$ go vet ./...
(exit 0, no output)
$ go test ./...
... 10 failures (11 counting one subtest), all named or clearly about
    promotion (TestModelRequest_SandboxSucceeded_CreatesPromotionPipelineRun_*,
    TestModelRequest_*Promotion*, TestModelRequest_FullLifecycle_*
    proof tests that script a "promotion-staging" stage,
    TestModelRequest_FullLifecycle_TektonAndNoopStageRunners_ReachSameTerminalPhase) ...
ok  	.../internal/stages/capacityplanning
ok  	.../internal/stages/noop
ok  	.../internal/stages/sandbox
ok  	.../internal/stages/tekton
ok  	.../internal/stagewalk
ok  	.../internal/stagecommon
FAIL	.../internal/controller (10 failing, all promotion-specific)
```

Expected and not a concern: those 10 `internal/controller` tests were
written for the 3-stage default sequence and explicitly assert on
promotion behavior that, in this disposable hypothetical, no longer
exists in the profile at all — not a regression in `sandbox`/
`capacityplanning`, which is what the litmus test actually checks.

Cleanup (both scratch changes fully discarded, not merged):

```
$ git checkout feat/model-request-controller
$ git reset --hard e34dfa5
$ git branch -D phase6-litmus-scratch-delete-promotion
```

**Direction 2 (the reverse, per the modularity principle's "the reverse
must also hold"): delete `internal/stages/sandbox`, confirm `promotion`
+ `capacityplanning` still build and their tests still pass unmodified.**

```
$ git checkout -b phase6-litmus-scratch-delete-sandbox
$ git rm -r operator/internal/stages/sandbox
rm 'operator/internal/stages/sandbox/doc.go'
rm 'operator/internal/stages/sandbox/handler.go'
rm 'operator/internal/stages/sandbox/handler_test.go'

$ go build ./...
go: finding module for package github.com/jhurlocker/modelops-operator/internal/stages/sandbox
main.go:12:2: cannot find module providing package github.com/jhurlocker/modelops-operator/internal/stages/sandbox: ...
```

Same shape of failure, this time in `main.go` (the first file
importing it alphabetically) rather than `internal/controller` — both
reference it, exactly as expected.

```
$ go build ./internal/stages/promotion/... ./internal/stages/capacityplanning/... ./internal/stagecommon/...
(exit 0, no output)

$ go test ./internal/stages/promotion/... ./internal/stages/capacityplanning/... ./internal/stagecommon/... -v
... (9 promotion tests, all PASS) ...
ok  	github.com/jhurlocker/modelops-operator/internal/stages/promotion	0.004s
... (9 capacityplanning tests, all PASS) ...
ok  	github.com/jhurlocker/modelops-operator/internal/stages/capacityplanning	(cached)
... (2 stagecommon tests, all PASS) ...
ok  	github.com/jhurlocker/modelops-operator/internal/stagecommon	(cached)
```

**9/9 promotion, 9/9 capacityplanning, 2/2 stagecommon tests pass,
unmodified, with `sandbox` physically absent.** Cleanup:

```
$ git checkout feat/model-request-controller
$ git reset --hard e34dfa5
$ git branch -D phase6-litmus-scratch-delete-sandbox
```

Confirmed via `git status`/`go test ./...` afterward that the working
tree and full test suite were back to exactly the pre-drill state (106
passing, 0 failing) before proceeding.

### Test coverage added

- `internal/stagewalk/walk_test.go` (new, 11 tests, TDD): see above.
- `internal/stages/capacityplanning/handler_test.go` (new, 3 tests) and
  `stagerunner_test.go` (new, 6 tests, TDD): see above.
- `internal/stages/sandbox/handler_test.go` (new, 8 tests): 3 relocated
  `gpu-count-override` precedence tests, 1 relocated full-fixture
  characterization test, 3 relocated `PipelineNameOrDefault` precedence
  tests, 1 new `RunName` test.
- `internal/stages/promotion/handler_test.go` (new, 9 tests): 2 relocated
  full-fixture/middle-namespace characterization tests, 3 relocated
  `PipelineNameOrDefault` tests, 3 relocated `GetNamespaces` tests, 1 new
  `RunName`/`Name` test.
- `internal/controller/modelrequest_controller_test.go`: `newModelRequestReconciler()`
  and the `FakeStageRunner`/`noop.StageRunner` test constructions updated
  mechanically (registry maps instead of one field) via two new shared
  helpers in `testutil_test.go` (`newStageHandlers`/`newStageRunners`), so
  the change is isolated to a handful of call sites, not scattered across
  every test body. The ~13 relocated golden-value/`PipelineNameOrDefault`
  tests were removed from this file (now living in `sandbox`/`promotion`,
  same assertions). `TestModelRequest_CapacityPlanCreateRace_AlreadyExists_DoesNotFailReconcile`
  adapted to build its fixture via `capacityplanning.Handler.BuildSpec`
  instead of the removed `buildCapacityPlan`. One new regression test
  added: `TestModelRequest_AllPromotionsSucceeded_SetsPromotionPipelineRunName`
  (see "A real bug caught only by live-cluster verification," above).
- **Total suite: 106 passing test cases (`go test -v -count=1 ./...`,
  counting subtests), 0 failing, up from 77 at the end of Phase 5**
  (measured against a clean `git worktree` checkout of `58d16da` for an
  apples-to-apples comparison, not the working tree, since new untracked
  Phase 6 files would otherwise inflate a naive count). Every pre-existing
  Phase 0-5 characterization and proof test that didn't get physically
  relocated passed **completely unmodified** — no assertion value
  changed anywhere in this phase, only mechanical harness wiring at the
  handful of `ModelRequestReconciler{...}` construction sites.

### Manifest regeneration

`make manifests generate` (controller-gen v0.16.5) picked up
`ModelLifecycleProfileSpec.Stages`/`ProfileStageSpec`/`StageNamespaceSetup`
and `ModelRequestStatus.CurrentStage`/`Stages`/`StageProgress`. Diffed
both regenerated CRD bases field-by-field against the pre-Phase-6
version: purely additive (new `stages`/`currentStage`/`stages` status
properties and description-only changes to existing fields; zero fields
removed or renamed). `zz_generated.deepcopy.go` gained `DeepCopyInto`/
`DeepCopy` for `ProfileStageSpec`, `StageNamespaceSetup`, `StageProgress`,
and updated `ModelLifecycleProfileSpec.DeepCopyInto`/
`ModelRequestStatus.DeepCopyInto` to deep-copy the new slice fields.
Confirmed `make manifests generate` idempotent on a second run (no
further diff).

### GitOps manifests (all committed, following the Phase 1/5 pattern)

`gitops/components/operator/crd-lifecycleprofiles.yaml` and
`crd-modelrequests.yaml` had drifted from the freshly regenerated
`config/crd/bases/*` (same hand-sync debt Phase 1 first flagged) —
re-synced verbatim, confirmed byte-identical afterward via `diff`.
`crd-capacityplans.yaml`/`crd-platformconfigs.yaml`/`crd-intakeproviderconfigs.yaml`
checked and confirmed to have no field-level drift from this phase
(only the same pre-existing cosmetic formatting difference Phase 1
already flagged and left alone). `kubectl kustomize gitops/components/operator`
and `gitops/components/runtime-config` both run locally before pushing,
confirming no rendering errors.

### Sandbox cluster verification

- Pushed `e34dfa5` to `feat/model-request-controller`; hard-refreshed
  `Application/modelops-operator` (branch-tracked, auto-sync +
  self-heal) — reached `Synced`/`Healthy` at `e34dfa5`, confirmed the new
  `stages`/`currentStage`/`status.stages` fields are `Established` on the
  live `modellifecycleprofiles`/`modelrequests` CRDs via `oc get crd ...
  -o jsonpath`.
- Built and pushed a new `quay.io/jhurlocker/modelops-operator:latest`
  image from `e34dfa5`'s code (same pattern as every prior phase — the
  `Deployment` runs from this pre-built tag, `imagePullPolicy: Always`, a
  `kubectl rollout restart` picks it up). Manager started cleanly, all
  three `EventSource`s (`ModelRequest`, `PipelineRun`, `CapacityPlan`)
  registered without error.
- Created a disposable `ModelRequest` (`phase6-verify`, `sandbox`
  namespace, referencing the live `standard-generative-onboarding`
  profile — which has **no** `Spec.Stages` set, so this exercises the
  synthesized-default-list path exactly as every existing profile in
  production does — with `requirements.gpuCountOverride: "3"`).
  Reconciled to `SandboxRunning` with `Status.CurrentStage: sandbox` and
  a fully populated `Status.Stages[]` (capacity `Succeeded` with its real
  GPU-sizing message, sandbox `Running`); the sandbox `PipelineRun`'s
  `pipelineRef.name` (`model-intake-sandbox`), `gpu-count-override=3`
  (the explicit override, matching sandbox's precedence rule), and
  `model-id` all matched expectations.
- Manually flipped the sandbox `PipelineRun`'s `Succeeded` condition to
  `True` (via `kubectl replace --raw .../status`, since this cluster's
  `kubectl`/`oc` client versions don't support `--subresource=status`) —
  request advanced to `PromotionRunning`, `CurrentStage: promotion`,
  `Status.Stages[]` now showing capacity+sandbox `Succeeded` and
  promotion `Running` for the `staging` namespace. The promotion
  `PipelineRun` (created in the `sandbox` namespace, same as
  pre-Phase-6 — `target-namespace` is a param, not the object's actual
  namespace) had `gpu-count-override=4` (the `CapacityPlan`-derived
  value, correctly **ignoring** the `"3"` override — the documented
  sandbox/promotion divergence, confirmed still holding through the
  walker), `run-register=true`, `target-namespace=staging`; the
  `pipeline` `ServiceAccount` and the three MaaS labels
  (`opendatahub.io/generated-namespace`, `maas.opendatahub.io/gateway-access`,
  `opendatahub.io/dashboard`) were provisioned on the `staging` namespace
  via `ProfileStageSpec.NamespaceSetup` — the data-driven RBAC/label
  mechanism, confirmed working live, not just in `envtest`.
- **This first run is exactly what surfaced the `PromotionPipelineRunName`
  bug described above** (`Status.PromotionPipelineRunName` was silently
  missing from `kubectl get modelrequest -o yaml` after flipping
  promotion's condition to `True` and reaching `Succeeded`). Fixed,
  re-tested locally (106/106), committed as `54fe06e`, pushed, hard-
  refreshed `Application/modelops-operator` (`Synced`/`Healthy` at
  `54fe06e`), rebuilt/pushed the image again, rolled out again.
- **Second verification pass**, against a fresh disposable `ModelRequest`
  (`phase6-verify2`, no `gpuCountOverride` this time) against the fixed
  image: reconciled sandbox→promotion→`Succeeded` exactly as above, and
  this time `Status.PromotionPipelineRunName`/`PipelineRunName` correctly
  showed `phase6-verify2-promotion-staging`, with `Status.Stages[2]`
  showing `{name: promotion, namespace: staging, phase: Succeeded, runRef:
  phase6-verify2-promotion-staging}` — confirming the fix live.
- Deleted both disposable `ModelRequest`s; their `CapacityPlan`s and
  `PipelineRun`s were garbage-collected automatically via owner
  references (confirmed gone on a follow-up `get`) — disposable
  verification, not a permanent cluster change. `Application/modelops-operator`
  and `Application/modelops-runtime-config` both remained
  `Synced`/`Healthy` throughout.

### Known follow-up NOT done in this phase

Both items added to `REFACTOR_PLAN.md`'s Phase 7 (see above) are
deliberately deferred, not silently fixed: deprecating the `Phase`/
`Message` legacy-compatibility shim once profiles migrate off the
default `Stages` list, and giving `CapacityPlan` a real `Failed` path so
`Required: true` is meaningful for that stage kind (today it's
structurally supported and tested in `capacityplanning.StageRunner` but
has no real producer — `CapacityPlanReconciler` never sets
`Phase="Failed"`).

`ensurePromotionNamespaceRBAC`/`ensureNamespaceLabels` stay in
`internal/controller`, not relocated into `internal/stages/promotion` —
a deliberate deviation from that package's original Phase 0 doc comment,
explained in its revised doc.go: as of Phase 6 these are invoked
generically by the walker for *any* stage whose declared
`ProfileStageSpec.NamespaceSetup` requests them, driven by data rather
than by checking a stage's name, so they're shared walker-glue code, not
promotion-specific logic.

---

## Phase 7 — RBAC scoping, plus four Phase 5/6 backlog items

**Commits:** `f978549` ("Phase 7: RBAC scoping, ProviderConfigLookupFailed,
Phase/Message shim removal, CapacityPlan Failed path") and `cdf4643`
("Fix Phase 7 modelrequests/finalizers RBAC regression caught by sandbox
cluster testing") on `feat/model-request-controller`. This phase went
through an explicit design-review pass first (per the user's request,
same as Phases 4/5/6), covering all six numbered items below; the
approved design is what's implemented. Additive CRD change for
`MaxGPUsPerRequest`; a real, deliberate breaking behavior change for the
`Phase`/`Message` shim removal (see item 5 below) and RBAC tightening.

### What changed

**1. RBAC split by package.** The combined marker list was attributed to
the package that actually needs each permission, grounded in what Phase
4-6 actually built (`StageHandlers`/`StageRunners` registries):

- `internal/stages/tekton/stagerunner.go` gained the `tekton.dev/pipelineruns`
  marker (moved from `ModelRequestReconciler`, joining the
  `intakeproviderconfigs` marker already there from Phase 5) -- the only
  place in the codebase that Gets/Creates a `PipelineRun`.
- `internal/stages/capacityplanning/stagerunner.go` gained its own
  `capacityplans` marker, narrowed to `get;list;watch;create` (its
  `EnsureRun` only ever Get-or-Creates, never mutates an existing one).
- `internal/controller/capacityplan_controller.go`'s (`CapacityPlanReconciler`)
  own `capacityplans` marker was tightened from full CRUD to
  `get;list;watch` (+ `capacityplans/status: get;update;patch`) -- it
  never creates/updates/deletes a `CapacityPlan`'s spec, only reads it and
  later writes its status.
- `capacityplans/finalizers` marker removed (confirmed dead: nothing in
  this codebase ever creates a child object owned by a `CapacityPlan`).
- **`modelrequests/finalizers` was ALSO removed in the first draft, and
  this was wrong** -- see "A real regression caught only by live-cluster
  verification," below. Restored in the follow-up commit.
- New `stagecommon.OwnedTypesProvider` interface (`OwnedTypes()
  []client.Object`), implemented only by `tekton.StageRunner` (returning
  `[]client.Object{&tektonv1.PipelineRun{}}`). `ModelRequestReconciler.SetupWithManager`
  now iterates `r.StageRunners` (via a new, deterministically-sorted
  `sortedStageRunnerKeys` helper) and calls `.Owns(t)` generically for any
  registered runner implementing this interface, instead of a hardcoded
  `.Owns(&tektonv1.PipelineRun{})` -- this is what lets
  `internal/controller/modelrequest_controller.go` drop its last
  `tektonv1` import entirely, closing the exact residual Phase 4's log
  flagged ("a natural candidate for Phase 5/7... a provider-agnostic
  'which child types does this StageRunner own' hook").
  `capacityplanning.StageRunner`/`noop.StageRunner` deliberately do NOT
  implement this interface: `CapacityPlan` ownership stays an explicit,
  unconditional `.Owns(&modelopsv1alpha1.CapacityPlan{})` call in
  `SetupWithManager` (a core lifecycle CRD, not provider-specific), and
  `noop.StageRunner` creates nothing.
- New `docs/RBAC.md` documents the resulting split package-by-package,
  including an explicit caveat that this is currently RBAC-marker
  *attribution* (correct `make manifests` shrinkage if a stage package is
  deleted), not runtime privilege *isolation* -- one `ClusterRole`/one
  `ServiceAccount` still backs the whole manager process; genuinely
  separate least-privilege service accounts per `StageRunner` would need
  separate manager processes, out of scope for this pass.
- Namespace-provisioning RBAC (`ensurePromotionNamespaceRBAC`/
  `ensureNamespaceLabels`, invoked generically via
  `ProfileStageSpec.NamespaceSetup`) stays on the core reconciler, per
  the design review: it's driven by stage *data*, not a specific
  execution engine -- a future non-Tekton `StageRunner` would need the
  identical RBAC bootstrap.

**A real regression caught only by live-cluster verification, not
envtest.** The first draft removed `modelrequests/finalizers` as a
believed-dead marker (no finalizer is registered on `ModelRequest`
anywhere in Go code). This broke every `CapacityPlan`/`PipelineRun`
creation on the sandbox cluster:

```
error: stage "capacity": capacityplans.modelops.example.io "phase7-verify-capacity"
is forbidden: cannot set blockOwnerDeletion if an ownerReference refers to
a resource you can't set finalizers on: , <nil>
```

Root cause: both `tekton.StageRunner.buildPipelineRun` and
`capacityplanning.StageRunner.EnsureRun` call
`controllerutil.SetControllerReference(modelRequest, child, scheme)`,
which sets `OwnerReference.BlockOwnerDeletion = true` by default. The API
server's admission control requires `update` permission on
`modelrequests/finalizers` to set `blockOwnerDeletion: true` on *any*
owner reference pointing at a `ModelRequest` object -- a generic
Kubernetes owner-reference safety check, completely independent of
whether the owning controller (`ModelRequestReconciler`) itself ever
registers a finalizer. `envtest`'s admin-equivalent client bypasses this
admission check entirely (structurally invisible to the 124-test unit
suite), so nothing caught it until a real `ModelRequest` was created on
the sandbox cluster and its `CapacityPlan` creation failed. **This is the
same shape of gap Phase 1's RBAC-escalation incident already
demonstrated** -- flagging again, as a pattern: real RBAC-enforcement
behavior in this codebase has now twice been invisible to `envtest` and
only caught by live-cluster testing, exactly why `REFACTOR_PLAN.md`'s
guiding principles require it. Fixed by restoring the marker (kept
`capacityplans/finalizers` removed -- confirmed genuinely unused, since
nothing ever creates a child object owned by a `CapacityPlan`); rebuilt,
redeployed, re-verified (see "Sandbox cluster verification," below).

**2. Namespace-provisioning RBAC**: confirmed staying on the core
reconciler, no code change (see design review above).

**3. `WorkflowRef.Engine` vs. `IntakeProviderConfigSpec.ProviderType`.**
`Engine` gets a field-level deprecation doc comment (previously only the
*type*-level comment implied it) confirming it's non-functional --
verified by grep that no Go code outside its own declaration and the
printcolumn marker ever reads it; routing has gone through
`ProfileStageSpec.Kind` + the `StageRunners` registry since Phase 6.
`ProviderType` is the field that actually carries functional weight (the
Go-level guard in `resolveProviderDetails`). `Engine` is left in place,
non-functional -- removing a field outright is a breaking CRD change not
needed for this pass. `ModelLifecycleProfile`'s printcolumn repointed
from `.spec.workflow.engine` to `.spec.providerConfigRef.name`, renamed
`"Engine"` -> `"Provider"`.

**A wrinkle discovered while implementing item 5 (below), not
anticipated in the design review**: once `Spec.Stages` became mandatory
and each `ProfileStageSpec` carries its own `ProviderConfigRef`, the
top-level `ModelLifecycleProfileSpec.ProviderConfigRef` field the new
printcolumn reads is now **also** non-functional (nothing reads it once
`defaultStages()` -- its only consumer -- was removed). Its doc comment
was updated with the same DEPRECATED treatment as `Engine`. The
printcolumn still shows a generally-useful value in practice (profiles
are expected to keep the top-level field in sync with what their stages
actually reference, as the live profile does), but it's worth being
explicit that it's now reading an inert field, same as `Engine` was.

**4. `ProviderConfigLookupFailed` status reason.** New
`stagecommon.ProviderConfigError{Err error}` (in `stagecommon`, not
`internal/stages/tekton`, since the error must be recognizable from
`internal/controller`, which never imports `internal/stages/tekton`
directly). `tekton.StageRunner.EnsureRun` now wraps every
`resolveProviderDetails` failure in this type. `ModelRequestReconciler.Reconcile`
recognizes it via `errors.As` (alongside the existing
`namespaceSetupError`/`secretLookupError` checks) and calls a new
`failRequestWithRequeue` helper (`failRequest`'s counterpart that always
returns a bounded `RequeueAfter`, even on the "nothing changed" no-op
branch) with a new, dedicated `providerConfigLookupRequeueDelay = 30 *
time.Second` -- distinct from the existing 5s `transientErrorRequeueDelay`,
long enough to tolerate the referenced `IntakeProviderConfig` being
created moments later by a separate GitOps sync without masking the
failure as permanent. Per the design review's explicit scope: the
`Watches()`-based immediate-re-trigger mechanism (for this and the three
older `*LookupFailed` reasons) was deliberately NOT built this pass --
added as a new backlog bullet under `REFACTOR_PLAN.md`'s Phase 7 section
instead, scoped to all four reasons together.

**5. Deprecating the Phase 6 `Phase`/`Message` compatibility shim.**
`internal/controller/stages_default.go` (`defaultStages()`,
`defaultCapacityStageName`/`defaultSandboxStageName`/`defaultPromotionStageName`)
deleted outright. `Reconcile` now returns a new `"NoStagesConfigured"`
status reason (via the existing `failRequest`) if
`profile.Spec.Stages` is empty, instead of silently synthesizing the old
3-stage default -- **an addition beyond exactly what was asked, made
deliberately to avoid a silent no-op walk-of-zero-stages footgun once the
implicit fallback was removed; flagged here rather than silently
expanding scope.** `computeWalkStatus` lost its `usingDefaultStages`
parameter and the `switch result.CurrentStage { case
defaultCapacityStageName: ... }` branch entirely -- every profile now
gets the fully generic `Phase` values
(`"<CurrentStage>Running"`/`"Succeeded"`/`"Failed"`, `result.Message`
passed through verbatim) the `!usingDefaultStages` branch already
produced for custom-`Stages` profiles since Phase 6.
`sandboxStageName`/`promotionStageName` (renamed from
`defaultSandboxStageName`/`defaultPromotionStageName`, `defaultCapacityStageName`
dropped as fully dead) survive in `modelrequest_controller.go` itself,
now serving only `lastProgressNamed`/`lastPromotionProgress`'s
population of the three legacy singular RunName fields
(`PipelineRunName`/`SandboxPipelineRunName`/`PromotionPipelineRunName`,
which predate Phase 6 entirely) -- a known, narrower limitation flagged
in code comments: these three fields only populate for a stage literally
named `"sandbox"`/`"promotion"`, unlike `Status.Stages[]`/`CurrentStage`,
which are fully generic regardless of naming. Out of scope for this
phase (only the `Phase`/`Message` shim was asked to be deprecated).

**The live migration, per the design review's explicit staging
decision** (gate by per-profile `Spec.Stages` opt-in, not a global
flag-day cutover): `gitops/components/runtime-config/lifecycleprofile.yaml`
(the only `ModelLifecycleProfile` in this repo) now declares its 3 stages
explicitly, a mechanical field-for-field copy of what `defaultStages()`
used to synthesize in Go (same stage names, same `NamespaceSetup`
blocks, each stage's own `providerConfigRef` pointing at
`standard-generative-onboarding-provider`). `operator/config/samples/lifecycleprofile-sample.yaml`
(the kubebuilder convention sample, not GitOps-tracked) was updated the
same way, but relying on the deprecated `workflow.pipelineRef` fallback
(no `providerConfigRef` set on its stages), to keep exercising that path
too.

**13 test assertions updated** (`"CapacityPlanning"` ->
`"capacityRunning"`, `"SandboxRunning"` -> `"sandboxRunning"`,
`"PromotionRunning"` -> `"promotionRunning"`, across
`modelrequest_controller_test.go`, `modelrequest_stagerunner_test.go`,
`providerconfig_test.go` -- exactly the count predicted in the design
review). **A materially larger test-fixture migration beyond those 13**,
not fully anticipated in the design review's scope estimate: every
characterization test that previously relied on `defaultStages()`'s
implicit synthesis needed its fixture profile to declare `Stages`
explicitly too, since `Reconcile` now hard-fails with
`"NoStagesConfigured"` otherwise. Handled with one new
test-only helper, `testDefaultStages(providerConfigRef
*modelopsv1alpha1.ProviderConfigRef) []modelopsv1alpha1.ProfileStageSpec`
(`testutil_test.go`) -- a test-side mirror of the deleted production
`defaultStages()`, wired into `defaultProfileSpec()` (fixing the ~20 call
sites that use it in one place) plus 4 standalone
`ModelLifecycleProfileSpec{...}` literals updated individually
(`TestModelRequest_SandboxPipelineNameOrDefault_PrecedenceOrder`,
`TestModelRequest_PromotionUsesProfilePromotionPipelineRef_EndToEnd`,
`newProfileWithProviderConfigRef` in `providerconfig_test.go`, and the
new `TestModelRequest_ProviderConfigRef_UnsupportedKind_SetsProviderConfigLookupFailed`).

**Verified live on the sandbox cluster specifically via the
branch-tracked `Application`s**, per the user's explicit instruction that
this be checked live, not just against `envtest`, given the real,
visible behavior change:

- `Application/modelops-runtime-config` synced the migrated
  `lifecycleprofile.yaml` (`kubectl get modellifecycleprofile` showed the
  new `Provider` printcolumn correctly resolving
  `standard-generative-onboarding-provider`, confirming item 3's
  printcolumn repoint live).
- A disposable `ModelRequest` (`phase7-verify`, `sandbox` namespace,
  referencing the live, now-migrated `standard-generative-onboarding`
  profile) reconciled through a REAL sandbox Tekton pipeline execution
  (not immediately flipped) to `Status.Phase: sandboxRunning`,
  `Status.CurrentStage: sandbox` -- confirmed fully generic, lowercase,
  no `"SandboxRunning"` special-casing. Manually flipped the sandbox
  `PipelineRun`'s condition via a `--subresource=status` merge patch to
  advance to `Status.Phase: promotionRunning`, then flipped the
  promotion `PipelineRun`'s condition to reach `Status.Phase: Succeeded`,
  `Status.Message: "Model onboarding completed successfully"`, with
  `Status.Stages[]` showing all three stages `Succeeded` with correct
  `RunRef`s and namespaces. `pipeline` `ServiceAccount`s confirmed
  present in both `sandbox` and `staging`. Deleted the disposable
  `ModelRequest` afterward; its `CapacityPlan` and both `PipelineRun`s
  were garbage-collected automatically via owner references (confirmed
  gone on a follow-up `get`) -- disposable verification, not a permanent
  cluster change.

**6. `CapacityPlan` real `Failed` path.** New
`PlatformConfigSpec.MaxGPUsPerRequest`/`CapacityPlanSpec.MaxGPUsPerRequest`
(`int`, populated from `PlatformConfig` into each `CapacityPlan` by
`capacityplanning.Handler.BuildSpec`, the same pattern as
`GPUOperatorNamespace`/`ClusterPolicyName`). `CapacityPlanReconciler`
computes an unclamped `rawGPUs` value (mathematically equivalent to the
old per-branch-clamped `baseGPUs` computation for every case where no
ceiling is configured -- proven both by a dedicated characterization
test and by re-running the pre-existing golden-value tests unmodified);
if `Spec.MaxGPUsPerRequest > 0 && rawGPUs > Spec.MaxGPUsPerRequest`, sets
`Status.Phase = "Failed"` with message `"requested capacity (%d GPUs)
exceeds configured maximum (%d)"` instead of silently clamping to 8.
Zero/unset `MaxGPUsPerRequest` preserves the exact pre-Phase-7 behavior
byte-for-byte. Also added a `Status.Phase == "Failed"` no-op guard
(alongside the existing `"Succeeded"` one) so an already-`Failed` plan
isn't re-processed/re-written on every reconcile. Per the design
review's explicit scope: real GPU-inventory/advisor-based feasibility
checking (the actual, harder problem -- confirmed via code inspection
that `CapacityPlanReconciler` has no HTTP call, no `Node`/`ClusterPolicy`
capacity query of any kind, despite unused `AdvisorEndpoint`/
`AdvisorSecretName`/`AdvisorTimeoutSeconds` fields already existing on
both `CapacityPlanSpec` and `PlatformConfigSpec`) remains explicitly out
of scope -- flagged as a new backlog bullet under `REFACTOR_PLAN.md`'s
Phase 7 section, noting a real `gpu-advisor` container image already
exists and is used by the sandbox Tekton pipeline's own `gpu-advisor`
Task (`model_onboarding_pipeline/tools/gpu-advisor`,
`quay.io/jhurlocker/gpu-advisor`) -- a natural future integration point,
discovered during this phase's research but not wired up.

**Verified live**: a disposable `CapacityPlan`
(`ContextLength: 32768, Concurrency: 16, MaxGPUsPerRequest: 4` -- raw
GPU recommendation 8, exceeding the configured ceiling of 4) reached
`Status.Phase: Failed`, `Status.Message: "requested capacity (8 GPUs)
exceeds configured maximum (4)"` on the sandbox cluster. Deleted
afterward.

### TDD: what was genuinely new vs. relocated

Per the guiding principle, tests were written before/alongside each new
piece of behavior:

- `internal/stagecommon/errors_test.go` (new, 3 tests): `ProviderConfigError`'s
  `Error()`/`Unwrap()` behavior, including a test proving it survives
  `fmt.Errorf("...: %w", ...)` wrapping and is still found by
  `errors.As` -- mirroring exactly how `internal/stagewalk.Walk` wraps a
  `StageRunner.EnsureRun` error before it reaches `Reconcile`.
- `internal/stages/tekton/stagerunner_test.go` (5 new tests):
  `TestEnsureRun_ProviderConfigResolutionFails_ReturnsProviderConfigError`
  (written before `EnsureRun` wrapped this error), plus
  `TestStageRunner_ImplementsOwnedTypesProvider`/
  `TestStageRunner_OwnedTypes_ReturnsExactlyPipelineRun` (written before
  `OwnedTypes()` existed).
- `internal/stages/capacityplanning/stagerunner_test.go` /
  `internal/stages/noop/stagerunner_test.go` (1 new test each):
  `TestStageRunner_DoesNotImplementOwnedTypesProvider` -- the structural
  proof (a type-assertion returning `false`) that CapacityPlan ownership
  stays explicit on the core reconciler and that `noop.StageRunner`
  needs "close to none" wiring, exactly as the design review claimed.
- `internal/controller/modelrequest_controller_setup_test.go` (new, 2
  tests): `sortedStageRunnerKeys`, written first (TDD: didn't exist
  before this phase). `SetupWithManager` itself is not exercised
  end-to-end (consistent with this package's pre-existing convention --
  no prior phase tested `SetupWithManager` against a real `ctrl.Manager`
  either, only `Reconcile` directly via `envtest`).
- `internal/controller/capacityplan_controller_test.go` (5 new tests):
  `TestCapacityPlan_MaxGPUsPerRequestUnset_PreservesExactPreviousClampingBehavior`,
  `TestCapacityPlan_RequestedGPUsExceedMaxGPUsPerRequest_SetsFailedPhase`,
  `TestCapacityPlan_RequestedGPUsWithinMaxGPUsPerRequest_Succeeds`,
  `TestCapacityPlan_RequestedGPUsExactlyAtMaxGPUsPerRequest_DoesNotFail`
  (boundary: exceeds means strictly-greater-than, not `>=`),
  `TestCapacityPlan_AlreadyFailed_IsANoOp`. All written before the
  `Reconcile` change landed.
- `internal/controller/providerconfig_test.go`: the pre-existing
  `TestModelRequest_ProviderConfigRef_MissingCR_SurfacesResolveErrorNotSilentDefault`
  was rewritten (renamed
  `..._SetsProviderConfigLookupFailed_WithBoundedRequeue`) to assert the
  new behavior instead of the old raw-reconcile-error one; 3 new tests
  added (`..._UnsupportedKind_SetsProviderConfigLookupFailed`,
  `..._KeepsRequeueingUntilFixed`).
- `internal/controller/modelrequest_controller_test.go`:
  `TestModelRequest_ProfileWithNoStages_SetsNoStagesConfigured` (new,
  for the `NoStagesConfigured` guard -- added slightly after the
  production code, not strictly before; flagged rather than silently
  presented as pure TDD).
- `internal/stages/capacityplanning/handler_test.go`: `MaxGPUsPerRequest`
  assertions added to the existing full-fixture and defaults tests
  (confirms it's threaded through with no default applied, unlike
  `MaxTimeSlices`).

### Manifest regeneration

`make manifests generate` (controller-gen v0.16.5), run twice (once for
the main phase commit, once more after the `modelrequests/finalizers`
fix). `config/crd/bases/*` diffs are purely additive
(`maxGPUsPerRequest` on both `capacityplans`/`platformconfigs`,
description-only changes elsewhere for the updated doc comments, the
`Provider` printcolumn rename on `modellifecycleprofiles`).
`config/rbac/role.yaml` diff matches the split described above exactly.
`zz_generated.deepcopy.go` unchanged (plain `int` fields need no special
deep-copy logic). `gitops/components/operator/crd-*.yaml` (4 of 5) and
`clusterrole.yaml` re-synced from the regenerated bases, confirmed
byte-identical afterward (CRDs) / matching verb-for-verb (the
hand-maintained, differently-formatted `clusterrole.yaml`).
`gitops/components/operator/crd-intakeproviderconfigs.yaml` confirmed
unchanged (untouched by this phase). The pre-existing
`serving.kserve.io`/`maas.opendatahub.io` hand-added rules in
`clusterrole.yaml` (flagged first in Phase 1, no corresponding Go
marker) are untouched.

### Cross-stage import check

`go list -deps` confirmed for all five packages under `internal/stages/*`
(`sandbox`, `promotion`, `capacityplanning`, `tekton`, `noop`): none
imports another (all five commands produced no matching output).
`internal/stagecommon` gained a new import
(`sigs.k8s.io/controller-runtime/pkg/client`, for `OwnedTypesProvider`'s
`client.Object` return type) but still depends on nothing beyond
`api/v1alpha1` + stdlib + controller-runtime/apimachinery (already a
transitive dependency of everything in this module) -- confirmed via
`go list -deps` showing no `internal/controller`/`internal/stages/*`
entries.

### Test coverage added

- New: `internal/stagecommon/errors.go`+`errors_test.go` (3 tests).
- New: `internal/controller/modelrequest_controller_setup_test.go` (2
  tests).
- Modified/added across `internal/stages/tekton` (+5),
  `internal/stages/capacityplanning` (+1 stagerunner, handler tests
  extended), `internal/stages/noop` (+1),
  `internal/controller/capacityplan_controller_test.go` (+5),
  `internal/controller/providerconfig_test.go` (+3, 1 rewritten),
  `internal/controller/modelrequest_controller_test.go` (+1, 13
  assertions updated), `internal/controller/modelrequest_stagerunner_test.go`
  (2 assertions updated).
- **Total suite: 124 passing test cases (`go test -count=1 ./...`,
  counting subtests), 0 failing** -- up from 106 at the end of Phase 6.

### Sandbox cluster verification

- Built and pushed `quay.io/jhurlocker/modelops-operator:latest` from
  `f978549`'s code; pushed the commit; hard-refreshed
  `Application/modelops-operator` (auto-sync + self-heal) and
  `Application/modelops-runtime-config`, both reached `Synced`/`Healthy`.
  `kubectl rollout restart` on the operator `Deployment` picked up the
  new image; manager started cleanly, all `EventSource`s (`ModelRequest`,
  `CapacityPlan`, `PipelineRun` -- the last now registered via the
  generalized `OwnedTypesProvider`-driven `.Owns()`, not a hardcoded
  call) registered without error.
- **First verification pass caught the `modelrequests/finalizers`
  regression** described above (a real `ModelRequest` hit a reconcile
  error on `CapacityPlan` creation: `"cannot set blockOwnerDeletion..."`).
  Fixed, re-tested locally (124/124), committed as `cdf4643`, pushed,
  hard-refreshed `Application/modelops-operator` (`Synced`/`Healthy` at
  `cdf4643`, confirmed `modelrequests/finalizers` present on the live
  `ClusterRole`), rebuilt/pushed the image again, rolled out again.
- **Second verification pass**, against 4 disposable objects (all
  created directly, not via the UI, then deleted after observation):
  1. `phase7-verify` (`ModelRequest`, live migrated profile): full
     sandbox -> promotion -> `Succeeded` lifecycle as described above,
     confirming both the RBAC fix and the generic `Phase` strings live.
  2. `phase7-bad-providerconfig` (`ModelLifecycleProfile` with a
     deliberately-unresolvable stage-level `providerConfigRef`) +
     `phase7-verify-pcref` (`ModelRequest`): reached
     `Status.Phase: ProviderConfigLookupFailed`,
     `Status.Message` containing `"does-not-exist-provider-config"` and
     `"not found"`.
  3. `phase7-no-stages` (`ModelLifecycleProfile` with no `Spec.Stages`) +
     `phase7-verify-nostages` (`ModelRequest`): reached
     `Status.Phase: NoStagesConfigured`,
     `Status.Message: 'ModelLifecycleProfile "phase7-no-stages" has no
     Spec.Stages configured'`.
  4. `phase7-verify-maxgpu` (`CapacityPlan`, direct,
     `ContextLength: 32768`/`Concurrency: 16`/`MaxGPUsPerRequest: 4`):
     reached `Status.Phase: Failed`,
     `Status.Message: "requested capacity (8 GPUs) exceeds configured
     maximum (4)"`.
  All four deleted afterward; owner-reference-based child objects (for
  #1) confirmed garbage-collected on a follow-up `get`.
  `Application/modelops-operator`/`Application/modelops-runtime-config`
  both remained `Synced`/`Healthy` throughout.

### Known follow-up NOT done in this phase

Both items added to `REFACTOR_PLAN.md`'s Phase 7 section (bullets 8-9,
appended by this phase) are deliberately deferred, not silently fixed:

- **`Watches()`-based immediate re-trigger for all four lookup-failure
  reasons** (`ProfileLookupFailed`, `PlatformConfigLookupFailed`, the new
  `ProviderConfigLookupFailed`, and implicitly `NoStagesConfigured`) --
  today only `ProviderConfigLookupFailed` gets an active bounded requeue
  (30s, via `failRequestWithRequeue`); the other three still rely
  entirely on `failRequest`'s no-requeue-at-all pattern (the
  `ModelRequest`'s own resync or an unrelated watch event). Scoped
  together since they share the identical "waiting on a separate GitOps
  sync" shape.
- **Real GPU-inventory/advisor-based capacity feasibility checking** for
  `CapacityPlanReconciler` -- `MaxGPUsPerRequest` is a configured-ceiling
  stopgap, not genuine capacity awareness. A real `gpu-advisor` container
  image already exists (used by the sandbox Tekton pipeline's own
  `gpu-advisor` Task) as a natural future integration point, discovered
  but not wired up this phase.
- `ModelLifecycleProfileSpec.Stages` is now functionally required but not
  yet enforced at the CRD schema level (no `+kubebuilder:validation:MinItems=1`
  marker) -- an empty/missing `Stages` is still a syntactically valid
  object that fails only at reconcile time (`NoStagesConfigured`), not at
  `kubectl apply` time. Flagged as a reasonable, low-risk follow-up in
  the field's own doc comment, not implemented this pass.
- `gitops/components/operator/clusterrole.yaml`'s pre-existing
  `serving.kserve.io`/`maas.opendatahub.io` hand-added rules (flagged in
  Phase 1, no corresponding Go marker) remain untouched -- unrelated to
  this phase's RBAC split.

### Phase 8 (ModelCard/DataCard CRD) explicitly NOT started

Per the user's explicit instruction: Phase 7 was the last plan-doc phase
besides Phase 8 (stretch). Phase 8 was not started automatically --
flagged here as a separate decision to be made once this Phase 7 entry
is reviewed, exactly as instructed.

---

## Out-of-band task — model-intake-ui fixes and README.md rewrite

**Commits:** `8522c27` ("model-intake-ui: fix Phase display regression,
add stage progress/provider display, fix gpuCountOverride type bug") and
`b486be7` ("docs: rewrite README.md as a grounded, public-facing
explanation") on `feat/model-request-controller`. **Explicitly not a
`REFACTOR_PLAN.md` phase** -- a separate, scoped task with two
independent parts, done as two commits per the user's request. No Go
code, CRD, or manifest changed; `make manifests generate` was not run
(nothing under `operator/api` or `operator/config` touched).

### Part 1 — model-intake-ui fixes

The UI is a demo/visualization tool, not core to the solution -- kept
deliberately small and scoped, no new abstractions beyond what each fix
needed.

1. **Status.Phase display regression, fixed.** Since Phase 7's live
   profile migration (see that phase's entry above),
   `ModelRequest.Status.Phase` is fully generic and lowercase-prefixed
   (`"sandboxRunning"`/`"promotionRunning"`) instead of the old bespoke
   strings the templates were written against
   (`"SandboxRunning"`/`"PromotionRunning"`). `requests/list.html` and
   `requests/detail.html` rendered this raw string directly.

   New `app/status_display.py` (+3 Jinja filters registered in
   `app/__init__.py`): `status_label(status)` prefers a humanized
   `Status.CurrentStage` (e.g. `"sandbox"` -> `"Sandbox"`) over the raw
   `Phase` string, falling back to `Phase` verbatim in two cases --
   empty `CurrentStage` (a pre-Phase-6 status shape, or a lookup/setup
   failure that never reached the stage walker), **and** the two
   genuine terminal outcomes (`"Succeeded"`/`"Failed"`).

   The terminal-outcome exception is a deliberate, reviewed deviation
   from a fully literal reading of "always prefer CurrentStage when
   non-empty": `internal/stagewalk.Walk` does NOT clear
   `Status.CurrentStage` on a `Failed` outcome -- it stays set to
   whichever stage actually failed (confirmed against a live `Failed`
   `ModelRequest` on the sandbox cluster: `currentStage: sandbox`,
   `phase: Failed`). A fully literal implementation would show
   "Sandbox" (colored red) for a failed request instead of "Failed",
   silently losing the terminal outcome from the primary label. Raised
   with the user before implementing (via the question tool); the
   user chose showing Phase verbatim for both terminal values,
   confirmed as the recommended option. (On success, `Walk` *does*
   clear `CurrentStage` to `""`, so this asymmetry only bites the
   `Failed` case in practice -- also confirmed live.) `status_badge_class`
   is keyed off the raw `Phase` (not `CurrentStage`, which carries no
   success/failure information): `Succeeded`/`Failed` get their own
   classes, anything ending in `"Running"` gets `badge-info`, and any
   other value (the `*LookupFailed`/`*SetupFailed`/`NoStagesConfigured`
   reasons) gets `badge-warning` (blocked/needs-attention, not
   necessarily permanent -- several of these retry on a bounded
   requeue per Phase 7).

2. **Per-stage progress display, added.** `requests/detail.html` gains
   a "Stage Progress" card rendering `Status.Stages[]` (Phase 6) as an
   ordered table: stage name (humanized), namespace, phase (its own
   small badge), message. This is the first UI surface for this data
   since it was introduced three phases ago.

3. **Read-only "Provider" field, added.** `requests/detail.html` gains
   a "Provider" card showing, per stage declared on the request's
   resolved `ModelLifecycleProfile`, which `IntakeProviderConfig` it
   points at (or the legacy inline `pipelineRef`/`promotionPipelineRef`
   fallback, labeled "deprecated", when a stage has no
   `providerConfigRef` of its own) -- mirroring
   `internal/stages/tekton/providerconfig.go`'s `resolveProviderDetails`
   at the display layer. Deliberately does NOT fetch the referenced
   `IntakeProviderConfig` object itself: the UI's `ServiceAccount`
   (`gitops/components/model-intake-ui/deployment.yaml`) has RBAC for
   `modellifecycleprofiles` but not `intakeproviderconfigs`, and adding
   that would have been scope creep for a read-only display field --
   this only reports what the profile's own spec says it resolves to.
   `app/routes/requests.py`'s `request_detail` fetches the profile via
   a new `get_lifecycle_profile` (added to
   `app/kubernetes/model_requests.py`, alongside the existing
   `list_lifecycle_profiles`) and degrades gracefully (empty
   `provider_rows`, rest of the page still renders) if the profile was
   deleted/renamed -- confirmed live against a real `ProfileLookupFailed`
   `ModelRequest` on the sandbox cluster referencing a profile that
   doesn't exist.

4. **`gpuCountOverride` type bug, fixed and confirmed pre-existing.**
   `ModelRequirements.GPUConfig.GPUCountOverride` has always been a
   `string` field on the CRD (Phase 2 of `REFACTOR_PLAN.md`), but
   `app/routes/intake.py`'s `submit()` sent it as a Python `int`.
   Confirmed this was a hard, pre-existing failure, not silent
   coercion -- both via a direct `kubectl`/`oc apply` with a bare YAML
   integer and via the exact `kubernetes` Python client call path
   `create_model_request` uses, against the real sandbox-cluster API
   server: `422 Unprocessable Entity`, `"must be of type string"`.
   Every wizard submission that set a GPU override was failing
   `create_model_request` outright (an unhandled exception in the
   Flask request, not a silently-wrong value). Fixed by sending the
   raw string straight through.

**Sandbox cluster verification (all of Part 1).** Confirmed
`Application/model-intake-ui` (namespace `openshift-gitops`) exists,
tracks `feat/model-request-controller` at
`gitops/components/model-intake-ui`, deploys into the `sandbox`
namespace via Kustomize, auto-sync + self-heal enabled -- and found it
stuck at sync status `Unknown` (not just `OutOfSync`), because
`gitops/components/model-intake-ui/deployment.yaml` has had a malformed
`stringData` value (`DEFAULT_S3_ACCESS_KEY: " "minioadmin"` -- an extra
leading `" "` before the real value) since Phase 1, which fails
`kustomize build` outright and has silently blocked ArgoCD from
computing a diff at all since Phase 1 first flagged (but didn't fix)
this Application being broken. Fixed as part of this task, since it
directly blocked doing the live verification this task's instructions
asked for:

- Fixed the malformed YAML; confirmed `kubectl kustomize
  gitops/components/model-intake-ui` succeeds.
- Pushed both commits; hard-refreshed `Application/model-intake-ui` --
  reached `Synced`/`Healthy` for the first time since Phase 1 (previous
  state: `Unknown`/`Healthy`).
- Ran this session's exact changed application code directly against
  the live sandbox-cluster API server (Flask test client, real
  kubeconfig, no mocking) against real existing `ModelRequest` objects
  spanning every interesting status shape: `Failed` (confirmed primary
  label reads "Failed", not "Sandbox"), `Succeeded` (label reads
  "Succeeded"), `SecretLookupFailed` (label falls back to the raw
  reason verbatim, no Stage Progress card since `Status.Stages` is
  empty), `ProfileLookupFailed` referencing a since-deleted profile
  (Provider card omitted, rest of the page still renders -- the
  graceful-degradation path). Confirmed the Stage Progress and Provider
  tables render with real data (stage names, namespaces, phases,
  messages; resolved `standard-generative-onboarding-provider` for the
  `sandbox`/`promotion` stages).
- Built and pushed a new `quay.io/jhurlocker/model-intake-ui:latest`
  image from this session's code (the `Deployment` runs from this
  pre-built tag with `imagePullPolicy: Always`, same convention as
  every operator-image rebuild in earlier phases); `kubectl rollout
  restart` picked it up. Hit the real `Route` (not the local test
  client) directly with `curl` against the live pod: the "Failed"
  request's detail page shows the "Failed" primary badge and both new
  cards render.
- Submitted a real `ModelRequest` with a GPU override two ways against
  the live pod: once via a direct Python `kubernetes`-client call
  reproducing `create_model_request`'s exact call shape (confirmed the
  pre-fix `int` value gets a `422` from the real API server, then that
  the post-fix `str` value is accepted), and once via a real HTTP `POST`
  to the live Route's `/intake/submit` (the actual wizard's code path,
  end to end) -- both created a real `ModelRequest` with
  `spec.requirements.gpuCountOverride: "3"` (string). Both disposable
  objects were deleted afterward.

### Part 2 — README.md rewrite

Full rewrite (not an edit): the previous README described a
pre-refactor "Enterprise AI Lifecycle Platform" module tree
(catalog/application-development/optimization/governance entries that
don't correspond to anything in this repo), a hardcoded 3-phase pipeline
table, and setup steps referencing CRD fields removed in Phase 1
(`resultS3AccessKey`/`resultS3SecretKey`). Per the task's own
instructions, a section outline (including a description of what each
diagram would show) was proposed and reviewed before writing full
prose; the user approved it with one optional addition (a horizontal
7-stage Mermaid diagram in the scope section, in addition to the
already-planned table), which is included.

Final structure: (1) what this is -- a governance/orchestration control
plane, not a platform replacement, stating plainly the CRDs+operator are
the product and the tools underneath are swappable; (2) an explicit
"this repo's Tekton/RHOAI implementation is a reference architecture,
not the solution" callout, pointing at `internal/stages/tekton` as the
concrete adapter example and `internal/stages/noop` as the
seam-is-real proof; (3) a Mermaid architecture diagram (CRDs -> core
reconciler/stage walker depending only on the `StageHandler`/
`StageRunner` contract -> one real Tekton provider box, one
illustrative/not-implemented box); (4) an explicit "only model intake
is implemented" scope statement, a horizontal Mermaid lifecycle diagram
(intake solid/filled, six future stages dashed/grayed) plus a table
confirming no CRD/controller/code exists yet for any of the six; (5) a
CRD reference for all five CRDs that exist today, each grounded in the
actual current `api/v1alpha1` types (not a simplified/outdated shape)
with a short example YAML -- `ModelRequest`'s and
`IntakeProviderConfig`'s trimmed from real samples already in the repo,
`PlatformConfig`'s trimmed from its real sample, `CapacityPlan`'s drawn
from a real object observed on the sandbox cluster
(`granite-2b-onboarding-capacity`); (6) operator architecture --
package layout table, the `StageHandler`-builds-*what*-vs.-`StageRunner`
-executes-*how* distinction, how the generic walker drives
`profile.Spec.Stages`, and a "how to add a new provider" walkthrough
referencing `noop` (minimal) and `tekton` (real) plus `docs/RBAC.md`
for the permission-attribution side; (7) getting started, pointing at
`gitops/applications` (the app-of-apps) and `gitops/components` (what
each `Application` actually syncs), stating plainly this is
ArgoCD-deployed, not `kubectl apply`-deployed; (8) a pointer to
`docs/REFACTOR_PLAN.md`/`docs/PHASE_LOG.md` for implementation history,
deliberately not duplicated in the README itself.

No sandbox-cluster verification needed for this part -- it's a
documentation file with no runtime behavior; every factual claim in it
(CRD field names/shapes, package names, file paths) was grounded by
reading the actual current `operator/api/v1alpha1/*_types.go` source
and, for the CapacityPlan example specifically, a real object on the
live sandbox cluster, rather than by re-deriving it from memory or from
the old README's (stale) descriptions.

### Known follow-up NOT done in this task

- The model-intake-ui's list-page phase filter dropdown
  (`requests/list.html`'s `<select>` offering
  `Pending`/`Evaluating`/`Deploying`/`Completed`/`Failed`) has been
  inert since before this task -- `app/routes/requests.py`'s
  `list_page()` never reads a `phase` query parameter at all, and none
  of those five values match any `Phase`/reason string the operator
  actually produces post-Phase-7. Noticed while fixing the badge
  logic in the same file; left alone as a pre-existing, unrelated gap
  outside this task's explicit scope (the task named `list.html`'s
  Phase *display*, not its filtering).
- `docs/RBAC.md`, `docs/REFACTOR_PLAN.md`, and `docs/PHASE_LOG.md`
  itself were not rewritten or trimmed -- the task was explicit that
  these remain the detailed internal record and the README should
  point to them, not absorb or duplicate their content.
- No second real provider adapter (SageMaker/Databricks) exists;
  the README's second provider box and the `noop` walkthrough are
  both explicitly labeled illustrative/minimal, not implied to be
  more than they are.

---

## Out-of-band follow-up — model-intake-ui: real scan/result S3 secret-ref bug + missing-field validation

**Commit:** `aa25f0b` on `feat/model-request-controller` --
"model-intake-ui: fix silently-dropped scan/result S3 secret refs,
block submission when missing". Triggered by the user hitting a live
`SecretLookupFailed` immediately after using the wizard following the
task above -- not a REFACTOR_PLAN.md phase, and not part of that
task's original scope; a same-day follow-up once the report came in.

### What was initially suspected, and why that was wrong

The first (incorrect) diagnosis, based only on the affected
`ModelRequest`'s persisted spec: `evalhubSecretName` was set but
`scanS3SecretName`/`resultS3SecretName` were empty, which looked like
the user had cleared two pre-filled form fields by hand. That
diagnosis was given to the user, then disproven within the same
session: resubmitting through the exact same wizard code path with
both fields explicitly filled in (`scan-s3-credentials`/
`result-s3-credentials`) -- via `curl`, via Python `requests` against
the live Route, and via the Flask test client locally -- still
produced a `ModelRequest` with both keys absent from `spec`, every
time, regardless of what was sent. This is why the earlier UI-fixes
task's own live verification (previous log entry) didn't catch it: the
gpuCountOverride/status-display checks it ran didn't happen to inspect
`scanS3SecretName`/`resultS3SecretName` specifically on the objects it
created.

### Root cause

`app/routes/intake.py`'s "Expert secret references" loop mapped a form
field name to its CRD spec key with a generic string replace:

```python
spec[key.replace("-secret-name", "SecretName")] = val
```

This produces the right key for `evalhub-secret-name`/
`huggingface-secret-name` (`-> evalhubSecretName`/`huggingfaceSecretName`,
matching `ModelRequestSpec`'s real JSON tags) but the WRONG key for the
two S3 fields: `"scan-s3-secret-name".replace("-secret-name",
"SecretName")` yields `"scan-s3SecretName"` -- the embedded `-s3`
hyphen isn't part of the literal `-secret-name` suffix being replaced,
so it survives untouched, leaving a stray hyphen where the real field
(`scanS3SecretName`) has none. Same for `result-s3-secret-name` ->
`result-s3SecretName` instead of `resultS3SecretName`. Neither
computed key matches any real `ModelRequestSpec` field, so the API
server's structural-schema pruning silently drops it on every
`Create` -- no error, no warning, just a missing field. This has been
broken since these two inputs were added to the wizard (Phase 1 of
`REFACTOR_PLAN.md`); Phase 1's own handoff log explicitly flagged that
the wizard change was "only verified by reading, not by clicking
through the live wizard," which is exactly the gap this bug lived in
for two sessions.

Fixed with an explicit `dict` mapping field name -> JSON key instead of
a derived string transform, for all four secret-reference fields (not
just the two broken ones, so nothing else depends on the fragile
pattern going forward).

### Defense in depth: block submission when either field is blank

Independent of (1): even with the mapping fixed, nothing stopped a
user from submitting with `scan-s3-secret-name`/`result-s3-secret-name`
genuinely empty. There is no server-side credential fallback for
either -- `internal/controller.resolveSecrets` removed the old
hardcoded `minioadmin` default in Phase 1 of `REFACTOR_PLAN.md` -- so a
`ModelRequest` submitted without them is guaranteed to reach
`SecretLookupFailed` once it reaches the sandbox stage's secret
resolution. This is exactly the gap the user's own question identified
("shouldn't I be blocked from submitting?").

Added server-side validation to `intake.py`'s `submit()`: if either
field is blank, no `ModelRequest` is created at all (`HTTP 400`), and
the wizard re-renders with:

- An error banner (new markup in `wizard.html`, outside the step
  panels so it's visible regardless of which step is active).
- The submitted values preserved as-is (including the blank
  field(s)) rather than silently repopulated with the sensible
  defaults -- so the user can actually see what's empty, not just be
  told something is wrong.
- The "Show expert overrides" section auto-expanded and the two
  offending inputs given a red border, via plain Jinja conditionals
  (`{{ ' visible' if errors }}` / a checked checkbox) -- no new JS
  needed for this part, since the section's CSS `visible` class
  already existed.
- The wizard landing directly on the Review step (where these fields
  live) instead of always resetting to step 1: `app.js`'s
  `initWizard()` now reads a `data-start-step` attribute on the form
  instead of hardcoding `showStep(0)`.

**Deliberately not implemented**: HTML5 `required` on these two
inputs. They live inside a step panel that's `display:none` by
default (multi-step wizard, only the active step is shown), and this
codebase already avoids putting `required` on any field outside step
1 for exactly this reason -- a `required` field whose ancestor is
`display:none` makes some browsers (confirmed behavior in Chromium)
silently refuse to submit with no visible validation message at all,
since the browser can't focus/scroll to an unrendered element. Server-
side validation with an explicit, always-visible error banner avoids
that failure mode entirely.

### Sandbox cluster verification

All against the live Route, not just envtest-equivalent local checks
(there is no Go/envtest layer involved in this fix -- it's Python
template/route code only):

- **Reproduced the drop bug pre-fix**: submitted a real `ModelRequest`
  through the then-deployed pod with both S3 secret-name fields
  correctly filled in (`curl`, then Python `requests`, then the Flask
  test client locally against the same code) -- `spec.scanS3SecretName`/
  `resultS3SecretName` came back unset every time, confirming the bug
  independent of transport.
- Rebuilt/pushed `quay.io/jhurlocker/model-intake-ui:latest` from the
  fixed code; `kubectl rollout restart` picked it up.
- **Confirmed fixed, end to end, not just at the spec level**:
  resubmitted the identical request against the new pod --
  `spec.scanS3SecretName`/`resultS3SecretName` now populated correctly,
  and critically, the resulting `ModelRequest` progressed all the way
  past capacity planning into a real, executing sandbox `PipelineRun`
  (`status.phase: sandboxRunning`, a genuine Tekton task-completion
  message), not just a status field that happened to look right.
- Confirmed the new validation guard live: an empty-fields submission
  against the live pod returns `HTTP 400` with the error banner and
  creates no `ModelRequest`; a valid submission still returns `302` to
  the new request's detail page, unchanged.
- All disposable `ModelRequest`s created during this investigation
  (on both the pre-fix and post-fix pod, including two the user's own
  browser session created mid-investigation while the fix was being
  written) were deleted afterward; their `CapacityPlan`/`PipelineRun`
  children were garbage-collected via owner references.

### Known follow-up NOT done here

- Only `scan-s3-secret-name`/`result-s3-secret-name` were given
  submission-blocking validation. `evalhub-secret-name`/
  `huggingface-secret-name` remain best-effort/optional in
  `internal/controller.resolveSecrets` (no error if unset), so they
  correctly don't need the same treatment -- not an oversight.
- No equivalent audit was done of every other wizard field for a
  similarly silent CRD-field-name mismatch; this fix addressed the one
  reported and found, not a general sweep. The explicit-map pattern
  now used for secret-name fields is a reasonable model for auditing
  the rest, if a similar report comes in.

---

## Out-of-band follow-up #2 — URL-shaped model-id crashing the compliance-inspect artifact resolution

**Commit:** `45100b7` on `feat/model-request-controller` -- "model-intake-ui,
compliance-artifact-scan-task, deploy_model: reject and harden against
URL-shaped model-id". Reported minutes after the previous follow-up:
the user hit a scary-looking `level=fatal msg="Error parsing image
name ... invalid reference format"` from `skopeo` in a fresh sandbox
`PipelineRun`.

### Diagnosis

Found the exact `ModelRequest` (`model-intake-5652d230f1`,
created `2026-08-03T20:05:23Z`, matching the log timestamps):
`spec.model.sourceType` was `"huggingface"` but `spec.model.uri` was a
full URL
(`https://quay.io/redhat-ai-services/modelcar-catalog:granite-3.3-2b-instruct`)
-- pasted into the wizard's single shared "Model ID" input while
"Model Source" was left at its Hugging Face default. There is no
validation anywhere that the two are consistent.

`compliance-artifact-scan-task.yaml`'s `compliance-inspect` step
derives two candidate modelcar tags from `MODEL_ID`
(`SHORT_TAG`/`ORG_TAG`) assuming it is always a bare HF `org/name`; it
only replaces `/` with `--` and never strips a URL scheme or handles
an embedded `:`. Fed the URL above, both derived candidates still
contained a stray scheme/colon, producing a *second*, more mangled
attempt appended onto `quay.io/<modelcar-repo>:`, which is what
`skopeo` correctly rejected as an invalid reference. Independently
confirmed via the `TaskRun`'s per-step statuses that this was **not**
a crash: `compliance-inspect` itself finished with `exitCode: 0` (by
design -- an unresolvable artifact is recorded as an empty inspect,
per the code's own comment, and left for `evaluate-and-upload` to mark
compliance FAILED); the actual `TaskRun` failure was the `gate` step
(`exitCode: 1`), which is the intentional policy gate correctly halting
the pipeline before deploy because the artifact could not be verified.
So the pipeline behaved *correctly* given the bad input -- the alarming
part was the confusing, mangled log line, not an actual malfunction --
but the bad input should never have been accepted in the first place.

### Fixes (three files, matching the three points in the chain)

1. **`model_onboarding_pipeline/model-intake-ui/app/routes/intake.py`
   (the actual root cause)**: added `_validate_model_source_uri()` --
   if `model-source` is `"huggingface"` and `model-id` contains `"://"`
   or `":"` (a bare HF repo id never does), the request is rejected
   (`HTTP 400`) with an explanation and a pointer to switch to "OCI
   Container Registry" instead. Generalized the error-rendering
   mechanism added in follow-up #1 (which assumed all errors belonged
   on the Review step) into a `FIELD_STEP` map + `error_fields` set, so
   a re-render now lands on whichever step actually contains the
   invalid field -- Model step for `model-id`, Review step for the
   secret fields -- instead of always jumping to Review.
2. **`compliance-artifact-scan-task.yaml`'s `compliance-inspect`
   step**: strip any `http(s)://`/`docker://`/`oci://` scheme prefix
   from `MODEL_ID` before deriving `SHORT_TAG`/`ORG_TAG`, mirroring the
   scheme-strip already present for the explicit `modelcar-image`
   override path a few lines above. Defense in depth for any
   `ModelRequest` that reaches this stage with a URL-shaped `model-id`
   regardless of how it got there (e.g. created directly via the API,
   bypassing the UI). This does **not** make every URL resolvable --
   a URL that already embeds its own `registry-path:tag` (as in the
   reported case) still isn't a bare HF id and will still correctly
   fail to resolve (and correctly fail the gate); it narrowly fixes
   the case of a *bare* scheme-prefixed id (e.g.
   `"https://ibm-granite/granite-3.3-2b-instruct"`), which now
   resolves cleanly on the first (`SHORT_TAG`) attempt instead of
   producing a mangled candidate.
3. **`model_onboarding_pipeline/tools/deploy-model-task/deploy_model.py`'s
   `_resolve_modelcar_uri()`**: identical scheme-strip fix applied to
   the same duplicated derivation pattern used by the later
   deploy-model stage.

### Verification

- Reproduced the reported `TaskRun`'s exact step-level statuses live
  (`compliance-inspect` exitCode 0, `gate` exitCode 1), confirming the
  diagnosis above before writing any fix.
- Rebuilt/pushed the UI image, rolled it out; confirmed live via a
  real POST to the Route that the identical huggingface+URL
  `model-id` combination that produced the original report now
  returns `HTTP 400`, with the error banner, `model-id` field
  flagged, and `data-start-step="0"` (Model step) -- and that a normal
  huggingface submission (bare `org/name`) and a normal oci submission
  (URL sanitized via the pre-existing `model-source=="oci"` path) both
  still succeed unchanged.
- The `compliance-artifact-scan-task.yaml` change is GitOps-managed
  (`gitops/components/pipelines`, `Application/modelops-pipelines`);
  forced an ArgoCD refresh/sync after pushing and confirmed the live
  `Task` object's script contains the new `sed` scheme-strip line
  before testing it.
- Verified the task-level fix directly (bypassing the UI entirely) by
  applying a raw `ModelRequest` with
  `spec.model.uri: "https://ibm-granite/granite-3.3-2b-instruct"` --
  confirmed via the resulting pod's `step-compliance-inspect` logs
  that it now resolves on the first (`SHORT_TAG`) attempt with no
  scheme/colon mangling.
- Verified the `deploy_model.py` fix directly via a standalone Python
  invocation of `_resolve_modelcar_uri()` with a scheme-prefixed
  `MODEL_ID` and a mocked `_tag_exists()`, confirming it resolves to
  the correct `oci://` URI.
- All disposable `ModelRequest` objects created during this
  verification were deleted afterward.

### Known follow-up NOT done here

- An OCI-sourced `ModelRequest` whose sanitized URI is already a
  fully-qualified `org/repo:tag` reference (not a bare HF id) still
  hits the same `SHORT_TAG`/`ORG_TAG` derivation, since there is no
  source-type branching anywhere in this call chain: confirmed
  `modelcar-image` is unconditionally hardcoded to `""` in
  `stagecommon.BuildCommonModelParams` (so the explicit-override path
  is never taken by controller-driven runs), and `sandbox-pipeline.yaml`
  declares a `model-source-type` param that is never actually
  forwarded to `compliance-artifact-scan`. A legitimate oci-sourced
  request with a colon-tag in its URI would still produce a malformed,
  double-tagged candidate. Correctly handling that requires
  recognizing an already-tagged reference and inspecting it directly
  instead of combining it with `modelcar-repo` -- a larger change than
  the scheme-strip fixed here, deliberately left out of this fix's
  scope.
- No equivalent audit was done of `deploy-model-task.yaml`'s own
  `modelcar-repo`/`modelcar-image` param wiring (it has no
  `modelcar-repo` param at all; `deploy_model.py` hardcodes the repo
  string, ignoring any `PlatformConfig.Spec.ModelCarRepo` override that
  `compliance-artifact-scan` does respect) -- a separate, pre-existing
  inconsistency noticed during this investigation but out of scope for
  this fix.

---

## Out-of-band follow-up #3 — gpu-advisor's OCI/modelcar model-id producing wildly inflated GPU memory estimates

**Commit:** `e019a0a` on `feat/model-request-controller` -- "gpu-advisor:
use model-tokenizer for architecture sizing, flag low-confidence
estimates". Reported immediately after follow-up #2: an oci-sourced
sandbox `PipelineRun` (`model-intake-ba9d47665f`,
`spec.model.uri="granite-3.3-2b-instruct"`) was `BLOCKED` by
`gpu-advisor` with an estimated 73.32 GB per-replica requirement
against a single 20.2 GB L4 GPU -- for what is actually a 2B model.

### Diagnosis

`advisor.py`'s `local_recommendation()` calls
`transformers.AutoConfig.from_pretrained(model_id)` to resolve real
architecture shape (layers/hidden size/heads/KV-heads) for KV-cache
sizing. For an oci-sourced `ModelRequest`, `model-id` (the Tekton
param, sourced from `spec.model.uri`) is the deployment artifact
reference -- here a bare modelcar tag `"granite-3.3-2b-instruct"`,
missing its `"ibm-granite/"` org prefix (the modelcar catalog's naming
convention drops it) -- never a resolvable Hugging Face id. The lookup
therefore always fails for oci sources, falling back to a hardcoded
generic ~7B-class shape (32 layers, 4096 hidden, 32 heads) for the
KV-cache term specifically. Meanwhile a separate, independent
name-parsing heuristic (matching size hints like `"2b"` directly out
of the tag string) still correctly estimated the *weight* size at 2B.
The mismatch -- 2B weights combined with a 7B-shaped KV cache -- is
what produced the inflated 73.32 GB estimate and the resulting
`BLOCKED` verdict. Confirmed by reproducing the exact numbers with a
standalone call to the unmodified function before touching any code.

Found that the CRD already has a `spec.model.tokenizer` field, and the
controller already builds a `model-tokenizer` Tekton param from it
(`stagecommon.BuildCommonModelParams`) -- but nothing downstream ever
consumed it: `gpu-advisor-task.yaml` had no `model-tokenizer` param at
all, and `sandbox-pipeline.yaml`/`promotion-pipeline.yaml` declared
`model-tokenizer` as a *Pipeline* param (with a description literally
saying "Hugging Face tokenizer ID for GuideLLM benchmarks") but never
forwarded it into their `gpu-advisor`/`gpu-advisor-sandbox` Task
invocation. The param existed at every layer except the one that
actually needed it.

### Fixes

1. `gpu-advisor-task.yaml`: added a `model-tokenizer` Task param
   (default `""`, non-breaking) wired to a new `MODEL_TOKENIZER` env
   var.
2. `sandbox-pipeline.yaml`, `promotion-pipeline.yaml`: forward the
   already-declared `model-tokenizer` Pipeline param into their
   `gpu-advisor`/`gpu-advisor-sandbox` Task invocation.
3. `advisor.py`'s `local_recommendation()` (and
   `remote_recommendation()`'s payload) now take an optional
   `hf_config_id` (falling back to `model_id`, matching prior behavior
   when unset) used specifically for the `AutoConfig.from_pretrained()`
   call -- the param-count name-heuristic keeps using the original
   `model_id` string, since that heuristic was already working
   correctly and needs no real HF id.
4. Per the capacity-planning skill's guidance to record assumptions and
   confidence: when the config lookup still fails, the result now
   carries an explicit `confidence: "low"` and a human-readable
   `assumptions` list, surfaced in the step's console log (a `WARN`
   block immediately after the existing blocked debug line), the
   human-readable `gpu-advisor-summary.txt`, and the machine-readable
   `deployment-options.json`. This deliberately does **not** change the
   `BLOCKED` decision itself when the fallback numbers still say the
   model doesn't fit -- it only makes a low-confidence block visibly
   distinguishable from a high-confidence one; getting an accurate
   *estimate* (and thus a correct decision) depends on the
   `model-tokenizer` value actually being a real HF id, which is a data
   quality/UI concern, not something this fix can force.
5. `model-intake-ui/wizard.html`: corrected the Tokenizer field's hint
   text (previously "for GuideLLM", which -- confirmed by reading
   `guidellm-benchmark-task.yaml`'s underlying `benchmark.py` in full --
   was never accurate; see below) to describe its actual purpose:
   GPU-sizing accuracy, particularly for S3/OCI sources whose `model-id`
   isn't a real HF id.
6. Bumped `quay.io/jhurlocker/gpu-advisor` `v0.1.3` -> `v0.1.4`
   (immutable version, not `latest`) and updated the Task's image
   reference.

**Note:** the exact previously-`BLOCKED` live request
(`model-intake-ba9d47665f`) already had `spec.model.tokenizer` set to
`"ibm-granite/granite-3.3-2b-instruct"` (the UI's static form default,
which the user happened not to have cleared) -- so this fix alone,
with **no UI data-entry change required**, resolves that specific case
merely by actually consuming a field that was already being correctly
populated and already present on the CRD.

### Investigated but explicitly NOT done: wiring `model-tokenizer` into `guidellm-benchmark-task.yaml`

This was one of the approved fix-scope items going in, but investigating
`guidellm-benchmark-task.yaml` and its underlying `benchmark.py` in
full found it has **no consumer for a tokenizer id at all**: GuideLLM
benchmarks are submitted to EvalHub using only `MODEL_NAME` (the
registered model name); EvalHub resolves the model/tokenizer itself
since the model is already deployed and known to it by that name.
There is no `AutoTokenizer` call, tokenizer param, or tokenizer field
anywhere in that Task's script or its EvalHub payload to plug a
tokenizer id into. Adding the param there regardless would just create
a second dead param -- the exact class of problem this fix addresses
elsewhere -- so it was deliberately not added. Both Pipelines'
`model-tokenizer` param descriptions were corrected to state this
plainly instead of repeating the previous inaccurate "for GuideLLM
benchmarks" claim.

### Verification

- Reproduced the exact reported numbers (73.32 GB, `Could not load
  config` message) via a standalone call to the unmodified
  `local_recommendation()` before writing any fix, confirming the
  diagnosis.
- Unit-level, both before/after: called the fixed
  `local_recommendation("granite-3.3-2b-instruct", ...)` once with no
  `hf_config_id` (old behavior: 73.32 GB, `confidence: "low"`, the new
  assumption message) and once with
  `hf_config_id="ibm-granite/granite-3.3-2b-instruct"` (new behavior:
  real config loaded -- 2048 hidden, 40 layers, 8 KV heads -- 15.34 GB,
  `confidence: "high"`).
- Rebuilt/pushed `gpu-advisor:v0.1.4` and the UI image; forced an
  ArgoCD refresh/sync for the GitOps-managed Pipeline/Task changes and
  confirmed the live `Task`/`Pipeline` objects contain the new
  `model-tokenizer` param and `v0.1.4` image reference before testing.
- **Full live end-to-end reproduction of the original bug's exact
  scenario**: applied a raw `ModelRequest` with
  `sourceType: oci`, `uri: "granite-3.3-2b-instruct"`,
  `tokenizer: "ibm-granite/granite-3.3-2b-instruct"` (the same shape as
  the original failing request) directly to the cluster. The
  `gpu-advisor-sandbox` `TaskRun` -- previously the one that failed --
  now `Succeeded`; its logs show `Loaded config
  (ibm-granite/granite-3.3-2b-instruct): 2048 hidden, 40 layers, 32
  heads, 8 KV heads`, `Total per replica: 15.34 GB`, `Confidence:
  HIGH`, `Status: RECOMMENDED` (was `BLOCKED`); the `ModelRequest`
  progressed to `sandboxRunning` with 3 tasks completed and 0 failed
  (was: 2 completed, 1 failed at this exact step). Deleted the test
  object afterward.

### Known follow-up NOT done here

- The Tokenizer field is still a free-text input with no validation
  against `model-source`/`model-id` (unlike the URL/OCI-ref validation
  added in follow-up #2 for `model-id` itself) -- a user can still
  leave it blank or put in a non-HF value for an OCI/S3 submission,
  which will silently fall back to the low-confidence path (now at
  least visibly flagged as such, per this fix, rather than presented
  as precise).
- `remote_recommendation()`'s new `model_hf_config_id` payload field is
  unvalidated against the external advisor endpoint's actual schema --
  no live remote-advisor-endpoint test was performed (this cluster's
  `advisor-endpoint` param is empty, so only the local heuristic path
  was exercised end-to-end).

---

## Phase 8 (docs/REVIEW_RESPONSE_PLAN.md) — Secret handling hardening

**Commits:** `ef6aa6d` ("Phase 8: secret handling hardening - fix
EvalHub secret bug, eliminate credential values from Tekton params")
and `bd1a228` ("Phase 8 fix: guidellm-benchmark-task.yaml was missed in
the initial pass") on `feat/model-request-controller`. Not a breaking
API/CRD change -- no `_types.go` file touched, `make manifests
generate` regenerated only `config/rbac/role.yaml` (the new
`secrets: create;update;patch` grant).

This phase went through an explicit design review first (per the
user's request, same as Phase 0/4/5/6/7), covering two related,
independently-verified defects named in `docs/REVIEW_RESPONSE_PLAN.md`:
the EvalHub secret bug, and secret VALUES leaking into
`PipelineRun.spec.params`. The approved design is what's implemented
below; see the design-review conversation for the full rationale on
each shape decision (why `resolveSecrets` still resolves values for
validation but only propagates names, why the generated EvalHub token
is wrapped in an owned ephemeral Secret rather than passed as a param,
why `secretKeyRef` was chosen over Secret-typed workspaces).

### What changed

**The EvalHub secret bug** (`internal/controller/modelrequest_controller.go`):
`resolveSecrets` previously read the EvalHub Secret's `"url"` key into
`s.scanS3Endpoint` -- the wrong field entirely, unrelated to EvalHub --
and never read `"token"` at all, unconditionally overwriting
`s.evalhubToken` with a freshly generated `ServiceAccount` token
regardless of whether the operator had configured one. Fixed: `url`
now lands in a new `evalhubURL` field; an explicit `token` key, when
present and non-empty, is honored by reusing the operator's own
Secret's name; the generated-token fallback only runs when no explicit
token was found.

**Eliminating credential values from Tekton params** -- the design
approved in review, implemented exactly as proposed:

- **`internal/stagecommon/params.go`**: `Secrets` no longer carries any
  credential VALUE field. It now carries only non-secret
  endpoints (`EvalHubURL`, `ResultS3Endpoint`, `ScanS3Endpoint`) and
  Secret NAME references (`EvalHubSecretName`, `HuggingFaceSecretName`,
  `ResultS3SecretName`, `ScanS3SecretName`). `BuildCommonModelParams`
  emits `evalhub-secret-name`/`huggingface-secret-name` (always
  present, defaulting to the conventional names
  `evalhub-credentials`/`huggingface-credentials` when unset -- see
  "The empty-secretKeyRef-name gotcha" below) and
  `result-s3-secret-name` (omitted when unset, since `resolveSecrets`'
  fail-loud validation guarantees a real `ModelRequest` never reaches
  this function without one) instead of `evalhub-token`/
  `huggingface-token`/`s3-access-key-id`/`s3-secret-access-key`.
- **`internal/stages/sandbox/handler.go`**: emits `scan-s3-secret-name`
  instead of `scan-s3-access-key-id`/`scan-s3-secret-access-key`.
  `internal/stages/promotion/handler.go` needed **no logic change** --
  it never read `sc.Secrets.*` directly, only through
  `BuildCommonModelParams`.
- **`internal/stages/tekton/stagerunner.go` needed no change at all** --
  `toTektonParams`/`buildPipelineRun` just copy whatever's in
  `map[string]string` into `tektonv1.Params`; they have no opinion
  about whether a value is a name or a credential. Confirms the
  proposal's claim that this is a smaller-blast-radius Go change than
  it might look, concentrated entirely in `resolveSecrets`,
  `stagecommon.Secrets`/`BuildCommonModelParams`, and
  `sandbox.Handler`.
- **`resolveSecrets`** (`modelrequest_controller.go`): kept its exact
  pre-Phase-8 shape of `Get`-then-validate for every `*SecretName`
  field (still fails loudly on a missing Secret or missing
  `accessKeyId`/`secretAccessKey` keys for scan/result S3, matching
  Phase 1's contract byte-for-byte) -- the only change is that the
  *return value* carries the Secret's own name instead of the values
  read out of it. `resolvedSecrets` (the private struct) dropped
  `evalhubToken`/`huggingfaceToken`/`scanS3AccessKey`/`scanS3SecretKey`/
  `resultS3AccessKey`/`resultS3SecretKey` entirely; nothing in this
  file ever holds a credential value past the local scope of the `if`
  block that read it out of a `corev1.Secret.Data` map.
- **`ensureEvalHubTokenSecret`** (new): when no operator-configured
  EvalHub token exists, the freshly generated `TokenRequest` token is
  upserted into an owned, ephemeral Secret named
  `"<ModelRequest.Name>-evalhub-token"` (create-if-absent, refresh
  `.Data["token"]` in place if it already exists -- mirroring the
  `createIgnoringAlreadyExists` idempotency pattern already used
  elsewhere in this file for other owned child objects, but as an
  upsert rather than a create-or-ignore, since the token has a 24h TTL
  and must be refreshed every reconcile). Owner-referenced to the
  `ModelRequest` via `controllerutil.SetControllerReference`, same GC
  story as every other child object this reconciler creates --
  confirmed live (see "Sandbox cluster verification" below): deleting
  the `ModelRequest` deleted this Secret automatically.
- **RBAC**: `+kubebuilder:rbac:groups="",resources=secrets` gained
  `create;update;patch` (previously `get;list;watch` only), narrowly
  scoped per the user's explicit request -- the marker comment
  explains this is for `ensureEvalHubTokenSecret`'s single, owned,
  ephemeral, per-`ModelRequest` Secret specifically, not a general
  secret-writing capability; every other `*SecretName` field stays
  read-only. `gitops/components/operator/clusterrole.yaml` (the
  hand-maintained copy ArgoCD actually deploys, per the Phase 1/5
  precedent) updated in lockstep with the same explanatory comment.
  `controller-gen` aggregated the new `secrets` verb set with the
  pre-existing `serviceaccounts` rule (identical verbs) into one
  combined rule in the generated `config/rbac/role.yaml` -- confirmed
  this is normal `controller-gen` aggregation behavior, not a manual
  edit gone wrong.

### Task/Pipeline YAML changes (same commit, not a follow-up)

Per the approved design's own reasoning (a value-carrying param with a
hardcoded default silently wins the moment Go stops supplying a value
-- exactly Phase 1's `minioadmin` bug, one layer downstream), the
underlying Tekton definitions changed in the same commit as the Go
code, across **9 files** (originally scoped as 8; see "A real bug
caught only by live-cluster verification" below for the 9th):

- **`sandbox-pipeline.yaml`, `promotion-pipeline.yaml`**: replaced
  `scan-s3-access-key-id`/`scan-s3-secret-access-key`/
  `s3-access-key-id`/`s3-secret-access-key`/`evalhub-token`/
  `huggingface-token` Pipeline-level params (several with hardcoded
  `minioadmin`/`minio` defaults -- see below) with
  `scan-s3-secret-name`/`result-s3-secret-name`/`evalhub-secret-name`/
  `huggingface-secret-name`, and stopped forwarding the removed params
  into their Task invocations.
- **`compliance-artifact-scan-task.yaml`, `security-scan-task.yaml`,
  `deploy-model-task.yaml`, `guidellm-benchmark-task.yaml`,
  `promote-and-benchmark-task.yaml`, `upload-guidellm-results-task.yaml`,
  `upload-lm-eval-results-task.yaml`**: replaced their own
  `s3-access-key-id`/`s3-secret-access-key`/`evalhub-token`/
  `huggingface-token` params (several also with hardcoded
  `minioadmin`/`minio`/`minio`/`test` defaults) with a single
  `s3-secret-name` (S3-consuming Tasks) or `evalhub-secret-name`/
  `huggingface-secret-name` (token-consuming Tasks) param, and changed
  the corresponding step `env` entries from a literal `value:
  $(params.xxx)` to `valueFrom.secretKeyRef.name: $(params.xxx-secret-name)`
  -- exactly mirroring the pre-existing `advisor-secret-name`/
  `ADVISOR_API_KEY` pattern already live in `gpu-advisor-task.yaml`
  (the concrete precedent the design review's proposal was built on).
  S3 credentials (always required, per `resolveSecrets`' fail-loud
  validation) use a plain `secretKeyRef` with no `optional: true`; the
  two genuinely-optional credentials (EvalHub token, HuggingFace token)
  set `optional: true`.
- **`promote-and-benchmark-task.yaml`, `upload-lm-eval-results-task.yaml`**
  are not on the live path (their referencing Pipeline,
  `model-intake-pipeline.yaml`, is dead/orphaned -- confirmed unreferenced
  by any `WorkflowRef`/`ProviderConfigRef` anywhere in this repo) but
  were fixed anyway, per explicit user instruction to fold this into
  the same commit rather than leave latent hardcoded-credential-shaped
  defaults sitting in GitOps-deployed-but-unused `Task` CRs.
  `model-intake-pipeline.yaml` and `model-intake-pipelinerun.yaml`
  themselves (the dead Pipeline and a static sample manifest) still
  contain the old pattern -- deliberately out of scope, same reasoning
  as the already-identified dead `app.py` in
  `docs/REVIEW_RESPONSE_PLAN.md` Phase 10 -- flagged as a known
  follow-up below, not silently ignored.

### The empty-`secretKeyRef`-name gotcha

A real Kubernetes API-validation constraint shaped this design, caught
during the design-review discussion before any code was written:
`env[].valueFrom.secretKeyRef.name` must be a non-empty string even
when `optional: true` -- `optional` only tolerates the *referenced
Secret* not existing, not an empty *name* field, which the API server
rejects outright at Pod admission. Since HuggingFace/EvalHub tokens are
genuinely optional (unlike S3 credentials, which `resolveSecrets`
already guarantees non-empty), `BuildCommonModelParams` always emits
*some* name for these two -- falling back to the conventional
`evalhub-credentials`/`huggingface-credentials` placeholder when
unconfigured -- rather than omitting the param. A pod referencing a
nonexistent Secret by a valid name, with `optional: true`, is
completely harmless (confirmed live -- see below); an empty name is
not tolerated at all. This is exactly the existing, working shape of
`advisor-secret-name`'s default (`gpu-advisor-credentials`), just
applied deliberately here instead of being an accident of `AddParam`'s
ordinary empty-value guard.

### TDD: tests written first, confirmed failing, then implemented

Per the standing principle, every change below started as a failing
test against the *old* code:

- `internal/stagecommon/params_test.go`: rewrote the full-fixture and
  defaults-applied tests to the new `Secrets` shape and new expected
  param names *before* touching `params.go` -- confirmed `go vet`
  failure (`unknown field EvalHubSecretName`) first. Added explicit
  `require.NotContains` assertions that `evalhub-token`/
  `huggingface-token`/`s3-access-key-id`/`s3-secret-access-key` never
  appear in `BuildCommonModelParams`' output at all -- the decisive
  per-function assertion, not just a renamed golden value.
- `internal/stages/sandbox/handler_test.go`,
  `internal/stages/promotion/handler_test.go`: same treatment,
  mechanical rename of the fixture's `Secrets` literal plus the same
  `NotContains` assertions added to each package's full-fixture test.
- `internal/controller/modelrequest_controller_test.go`: 7 new tests
  for the EvalHub bug fix and the HuggingFace name-only contract,
  written against the not-yet-existing `evalhubURL`/`evalhubSecretName`/
  `huggingfaceSecretName` fields and the not-yet-existing
  `ensureEvalHubTokenSecret` (confirmed failing:
  `unknown field EvalHubSecretName in struct literal` at `go vet`
  time). New `newSecret`/`newPipelineServiceAccount` test helpers in
  `testutil_test.go` (the latter needed because
  `generateServiceAccountToken`'s `TokenRequest` requires the
  `"pipeline"` `ServiceAccount` to actually exist server-side, normally
  provisioned by `ensurePromotionNamespaceRBAC` during a full
  `Reconcile` -- tests calling `resolveSecrets` directly bypass that,
  so they provision it themselves).
- `internal/controller/modelrequest_credentials_test.go` (new file):
  the canary-value test, written and confirmed failing (`undefined:
  resolveSecrets` fields, then real assertion failures against the
  unfixed code) before the fix landed.
- `internal/stages/tekton/pipeline_yaml_credentials_test.go` (new
  file): the static YAML safety net, written and confirmed failing
  against the *original* Task/Pipeline YAML (real failure output
  showing `sandbox-pipeline.yaml` containing `default: "minioadmin"`
  and `name: s3-access-key-id`) before any YAML file was edited.

### The canary-value test: the decisive evidence, per the design review

`TestModelRequest_SandboxAndPromotionPipelineRuns_NeverContainRawCredentialValues`
(`internal/controller/modelrequest_credentials_test.go`) seeds four
Secrets (`canary-scan-s3`, `canary-result-s3`, `canary-evalhub`,
`canary-hf`) with long, distinctive, random-looking values that could
never collide with a legitimate default, drives a `ModelRequest`
through `Reconcile` to create both the sandbox and promotion
`PipelineRun`s, and asserts for every param in both objects: the value
is never *equal to* any canary value (the direct leak) and never
*contains* one as a substring (a defensive check against
concatenation). Also asserts the `*-secret-name` params **are**
present with the correct Secret names, and that `evalhub-url`'s value
(the Secret's own URL) wins over `PlatformConfig`'s default -- so the
test fails loudly if the fix regresses to "just delete the param"
instead of "replace it with a reference." This is the test envtest can
actually run that most directly answers "is the leak closed," per the
design review's own framing.

### A real bug caught only by live-cluster verification, not envtest

Exactly the category of gap this repo's own guiding principles warn
about (Phase 1's RBAC-escalation incident, Phase 6's
`PromotionPipelineRunName` bug): **`guidellm-benchmark-task.yaml` was
missed entirely in the first implementation pass.** The original design
proposal's grep for `sc.Secrets.*` usage in Go correctly found the six
Tasks Go code's *param names* pointed at, but `guidellm-benchmark-task.yaml`
is invoked by `promotion-pipeline.yaml`'s `benchmark` step using the
*same* `evalhub-url`/`evalhub-token` forwarding pattern as
`security-scan` -- and nothing about the Go-side investigation would
surface a Task file that was never directly named in `internal/stages/*`.
`envtest` could not have caught this either: the fake client has no
real Tekton reconciler and never validates a `PipelineRun`'s params
against its referenced `Pipeline`/`Task`'s actual declared param
schema -- that validation is a genuine Tekton-controller-side check
this repo's test suite has no way to exercise short of a real cluster.
A real `ModelRequest` (`phase8-verify`, `sandbox` namespace) reached
`promotionRunning` cleanly through the *entire* sandbox pipeline (proving
every other file's fix correct) and then failed outright with `[User
error] Validation failed for pipelinerun ... invalid input params for
task guidellm-benchmark: missing values for these params which have no
default values: [evalhub-token]` -- because `promotion-pipeline.yaml`'s
`benchmark` step had already stopped supplying `evalhub-token` (renamed
to `evalhub-secret-name`), but `guidellm-benchmark-task.yaml` itself
still declared `evalhub-token` as a required, no-default Task param.
Fixed identically to its six siblings (`bd1a228`); added it to
`pipeline_yaml_credentials_test.go`'s `affectedPipelineAndTaskFiles`
list with a doc comment explaining exactly how it was missed, so the
static safety net now covers all 9 files (2 Pipelines + 7 Tasks) and a
10th missed file would fail the same two tests immediately.

### Sandbox cluster verification

This phase needed real cluster verification beyond `envtest` for
exactly the reasons anticipated in the design review: `envtest` cannot
exercise Tekton's own `secretKeyRef` substitution/resolution, and
cannot confirm a Task actually *authenticates successfully* using a
`secretKeyRef`-sourced credential at runtime (only that the Go code
built the right map, or that a fake client accepted an object).

- Pushed both commits (`ef6aa6d`, then the `guidellm-benchmark-task.yaml`
  fix `bd1a228`) to `feat/model-request-controller`; hard-refreshed
  `Application/modelops-operator` and `Application/modelops-pipelines`
  (branch-tracked, auto-sync + self-heal) after each push, confirmed
  `Synced`/`Healthy` at the respective revision, and confirmed the live
  `ClusterRole`/`Task`/`Pipeline` objects matched the committed YAML
  (`secrets` verb list, `s3-secret-name`/`evalhub-secret-name` params,
  `secretKeyRef` step definitions) before testing against them.
- Rebuilt and pushed a new `quay.io/jhurlocker/modelops-operator:latest`
  image (same pattern as every prior phase); `kubectl rollout restart`
  on the `modelops-operator` `Deployment`; manager started cleanly, all
  three `EventSource`s registered without error.
- **Passive confirmation before any dedicated test**: the moment the
  new image started reconciling the sandbox namespace's pre-existing
  leftover `ModelRequest`s (disposable artifacts from earlier sessions,
  per Phase 0/1's handoff notes), it immediately began creating real
  `<name>-evalhub-token` Secrets for every one of them (confirmed via
  `oc get secrets -n sandbox` showing ~20 freshly-created Secrets,
  each holding a real JWT-shaped token under `.data.token`) -- since
  none of those requests had an explicit `EvalHubSecretName` configured,
  this is the generated-token fallback path firing for real, unprompted,
  proving the ephemeral-Secret upsert works against genuinely
  pre-existing production-shaped state, not just a purpose-built test
  fixture.
- **The decisive test**: created `phase8-verify` (`sandbox` namespace,
  `standard-generative-onboarding` profile), pointing
  `scanS3SecretName`/`resultS3SecretName` at two *canary-named* Secrets
  (`phase8-canary-scan-s3`/`phase8-canary-result-s3`) holding the
  **real, working** `accessKeyId`/`secretAccessKey` values copied from
  the cluster's existing `scan-s3-credentials`/`result-s3-credentials`
  Secrets under a deliberately distinct name -- so the test proves both
  "the Secret is resolved by the *name* the reconciler actually chose"
  (not a coincidence of two paths agreeing on a shared default name)
  and "real S3/EvalHub authentication genuinely succeeds using a
  `secretKeyRef`-sourced credential," not just that a plausible-looking
  string reached a param.
  - Confirmed via `oc get pipelinerun ... -o json`: the sandbox
    `PipelineRun`'s params included `scan-s3-secret-name:
    phase8-canary-scan-s3`, `result-s3-secret-name:
    phase8-canary-result-s3`, `evalhub-secret-name:
    phase8-verify-evalhub-token` (the generated ephemeral Secret,
    confirming the fallback path), `huggingface-secret-name:
    huggingface-credentials` (the unconfigured-default placeholder) --
    and a direct string search of the full `PipelineRun` JSON for the
    real, known `scan-s3-credentials` Secret's actual
    `accessKeyId`/`secretAccessKey` values (base64-decoded from the
    live cluster) found **zero matches**, live, on the real object
    Tekton executed against -- the same assertion the envtest canary
    test makes, now confirmed against genuine cluster state rather
    than a fixture.
  - `compliance-artifact-scan`'s `s3-upload` step **genuinely
    authenticated and uploaded** 5 real objects to the live MinIO
    (`Uploaded compliance-artifact-sandbox/trivy-report.json ->
    s3://compliance-artifact-results/...`, etc.) using credentials
    resolved via `secretKeyRef.name: $(params.scan-s3-secret-name)` =
    `phase8-canary-scan-s3` -- real authentication success, not a
    structural check.
  - `security-scan`'s garak job **genuinely submitted to and completed
    against the real EvalHub API** (`Submitting garak job ... to
    EvalHub`, a real job ID, successful polling to completion) using
    `EVALHUB_TOKEN` resolved via `secretKeyRef.name:
    $(params.evalhub-secret-name)` = the generated ephemeral Secret --
    an unrelated, pre-existing SSL-cert-trust issue in that
    container's *own* S3-report-upload code path (`certificate verify
    failed`) surfaced separately and is flagged as out-of-scope, not a
    regression from this phase (the actual credential-resolution
    concern this phase touches -- the EvalHub bearer token -- worked
    correctly; a 401/403 would have failed job *submission*, not a
    downstream unrelated TLS handshake).
  - After the `guidellm-benchmark-task.yaml` fix and a fresh
    `phase8-verify`, the **entire end-to-end flow reached
    `Status.Phase == "Succeeded"` legitimately** (not manually flipped,
    unlike every prior phase's verification pattern -- the whole
    sandbox→promotion sequence ran for real): `benchmark` (promotion's
    GuideLLM-via-EvalHub step) again showed a real job submitted and
    completed; `upload-guide-llm-results` genuinely uploaded 3 objects
    to S3 using `result-s3-secret-name: phase8-canary-result-s3`;
    `deploy-model` (both sandbox and promotion invocations) succeeded
    with `HF_TOKEN` sourced from a `secretKeyRef` pointing at
    `huggingface-credentials`, a Secret confirmed **not to exist** in
    this namespace (`oc get secret huggingface-credentials` ->
    `NotFound`) -- decisive proof the `optional: true` +
    non-empty-placeholder-name design for genuinely-unconfigured
    credentials works exactly as intended against real Kubernetes API
    validation, not just in theory.
  - Deleted `phase8-verify`; confirmed both its `PipelineRun`s, its
    `CapacityPlan`, **and** `phase8-verify-evalhub-token` (the
    ephemeral Secret) were all garbage-collected via owner references
    -- the new Secret type follows the exact same cleanup story as
    every other child object this reconciler creates. Deleted the two
    canary Secrets afterward. `Application/modelops-operator`/
    `Application/modelops-pipelines` remained `Synced`/`Healthy`
    throughout.

### Cross-stage import check

`go list -deps` confirmed for all five packages under
`internal/stages/*` (`sandbox`, `promotion`, `capacityplanning`,
`tekton`, `noop`): none imports another (all five commands produced no
matching output). This phase touched `internal/stages/sandbox` (params
only) and `internal/stages/tekton` (new test file only, no production
code) -- neither gained a new import of any kind.

### Manifest regeneration

No `_types.go` file was touched. `make manifests generate`
(controller-gen v0.16.5) run; `git diff` confirmed the only change is
`config/rbac/role.yaml`'s `secrets` rule gaining `create;update;patch`
(aggregated with the pre-existing `serviceaccounts` rule, since both
now share an identical verb set -- normal `controller-gen` behavior,
not a manual edit). `zz_generated.deepcopy.go`: no diff, as expected.

### Test coverage added

- `internal/stagecommon/params_test.go`: both existing tests rewritten
  to the new `Secrets` shape/param names, plus new `NotContains`
  assertions for the four removed credential-value param names.
- `internal/stages/sandbox/handler_test.go`,
  `internal/stages/promotion/handler_test.go`: fixture literal renamed;
  same `NotContains` assertions added to each package's full-fixture
  test.
- `internal/controller/modelrequest_controller_test.go`: 7 new tests
  (`TestResolveSecrets_EvalHubSecretHasURLAndToken_UsesTokenVerbatim_NeverGeneratesOrOverwrites`,
  `TestResolveSecrets_EvalHubSecretHasURLButNoToken_GeneratesAndPersistsEphemeralSecret`,
  `TestResolveSecrets_NoEvalHubSecretConfigured_GeneratesAndPersistsEphemeralSecret`,
  `TestResolveSecrets_EvalHubEphemeralSecret_UpsertIsIdempotentAcrossReconciles`,
  `TestResolveSecrets_HuggingFaceSecretName_ReturnsNameNotValue`,
  `TestResolveSecrets_NoHuggingFaceSecretNameConfigured_LeavesItEmpty`,
  `TestResolveSecrets_MissingHuggingFaceSecret_ReturnsError`), plus the
  two pre-existing `resolveSecrets` tests mechanically updated to the
  new name-only contract. New `newSecret`/`newPipelineServiceAccount`
  helpers in `testutil_test.go`.
- `internal/controller/modelrequest_credentials_test.go` (new file, 1
  test): the canary-value decisive-evidence test described above.
- `internal/stages/tekton/pipeline_yaml_credentials_test.go` (new file,
  4 tests): the static safety net against reintroducing a
  value-carrying credential param or a hardcoded credential default in
  any of the 9 affected Task/Pipeline files, plus a check that the
  replacement `*-secret-name` params actually exist and that credential-
  consuming Tasks reference `secretKeyRef` at all.
- Total suite: **136 tests passing** (124 at the end of Phase 7 + 12
  new: 7 in `internal/controller`, 1 canary test, 4 in
  `internal/stages/tekton`), `go build ./...`/`go vet ./...` clean.

### Known follow-up NOT done in this phase

- `model-intake-pipeline.yaml` and `model-intake-pipelinerun.yaml` (the
  dead/orphaned Pipeline and a static sample manifest -- confirmed
  unreferenced by any live `WorkflowRef`/`ProviderConfigRef`) still
  contain the pre-Phase-8 value-carrying credential params and
  `minioadmin`/`minio` defaults. Deliberately out of scope, same
  reasoning as the already-identified dead `app.py` in
  `docs/REVIEW_RESPONSE_PLAN.md` Phase 10 -- worth folding into that
  same cleanup pass rather than a dedicated fix here.
- The SA-projected-token alternative for the EvalHub credential (have
  `security-scan` read its own `pipeline` ServiceAccount's
  automatically-projected token file directly, eliminating the
  `TokenRequest`/ephemeral-Secret/RBAC-grant machinery entirely) was
  added as backlog item 10 under `docs/REFACTOR_PLAN.md` Phase 7, not
  built this phase -- it requires a Task-*script* change (source the
  token from a file instead of an env var), a larger blast radius than
  this phase's plumbing-focused scope.
- The unrelated SSL-certificate-trust issue in `security-scanner`'s own
  S3-report-upload code path (surfaced during live verification, see
  above) was not investigated or fixed -- it predates this phase and
  is orthogonal to the credential-reference design this phase
   implements (the actual EvalHub bearer-token authentication this
   phase touches worked correctly).

---

## Phase 9 — Namespace RBAC governance (AllowedNamespaceSelector)

**Status:** Complete. This phase went through an explicit design-review
pass first (per the user's request, same as Phases 4/5/6/7/8) before any
code was written; the approved design is what was implemented with two
refinements from the review (see below).

### What changed

- **`StageNamespaceSetup.AllowedNamespaceSelector`**
  (`operator/api/v1alpha1/modellifecycleprofile_types.go`): new optional
  field of type `*metav1.LabelSelector`. When set, the walker evaluates
  it against each candidate namespace's labels before any RBAC is
  provisioned or labels are applied. A namespace that doesn't exist or
  doesn't match causes the walk to fail with a `"NamespaceNotApproved"`
  status reason — the stage never executes there and no RBAC is
  provisioned. When nil or empty (the default), all namespaces are
  permitted — backward-compatible with every existing profile. Uses the
  standard `metav1.LabelSelector` type, so both `matchLabels` (simple
  equality) and `matchExpressions` (set-based In/NotIn/Exists/
  DoesNotExist) selectors are supported.

- **`stagecommon.NamespaceApprovalError`** (`operator/internal/stagecommon/errors.go`):
  new error type, mirroring the existing `ProviderConfigError` pattern
  (lives in `stagecommon` so both `internal/controller` and any future
  namespace-setup consumer can recognize it via `errors.As` without
  importing each other). Wraps the underlying failure (namespace not
  found, labels don't match, or invalid selector syntax) with a
  human-readable message distinguishing "does not exist" from "labels
  do not match."

- **`checkNamespaceApproved`**
  (`operator/internal/controller/modelrequest_controller.go`): new
  method on `ModelRequestReconciler`. GETs the target namespace,
  evaluates the `metav1.LabelSelectorAsSelector` against its labels, and
  returns a `NamespaceApprovalError` wrapping the specific failure.
  NotFound and labels-mismatch are distinguished via distinct error
  messages, both surfacing as `"NamespaceNotApproved"` — the status
  reason is the same, but the message tells the user whether they need
  to create the namespace or relabel it.

- **Wired into `setupNamespace` closure**: the approval check fires
  first, before `EnsureRBAC` and `Labels` — if it fails, nothing else
  happens for that namespace and the walk stops. The existing
  `namespaceSetupError`→`SecretLookupFailed`/`RBACSetupFailed`/
  `ProviderConfigLookupFailed` chain in `Reconcile`'s error handler
  gained `NamespaceApprovalError`→`"NamespaceNotApproved"` recognized
  via `errors.As`, returning `failRequest` (no requeue).

### Requeue decision: no-requeue (design review item 1)

`ProviderConfigLookupFailed` (Phase 7) used a 30s bounded requeue
because a missing `IntakeProviderConfig` is a real GitOps race —
created by the same sync process as the profile. A namespace that
doesn't match a selector is different: namespaces are long-lived cluster
infrastructure, not dynamically-provisioned CRDs. If `staging` exists
but lacks `modelops.io/approved: "true"`, polling every 30s won't fix
it — a human needs to label the namespace or adjust the selector. Even
the NotFound sub-case (namespace doesn't exist yet) needs not just the
namespace to be created but also the right labels to be applied. The
operator has no reason to expect this to happen on its own within any
particular window, so `failRequest` (no requeue, the request sits idle
until something changes) is the correct posture.

This is documented in `stagecommon.NamespaceApprovalError`'s doc comment
and in the `Reconcile` error-handler code comment, with an explicit
cross-reference to `ProviderConfigLookupFailed`'s different reasoning.

### NotFound vs. label-mismatch (design review item 2)

Both surface as `"NamespaceNotApproved"` but with distinct messages:

- NotFound: `namespace "nonexistent-ns" does not exist`
- Labels mismatch: `namespace "staging" labels do not match allowedNamespaceSelector`

This lets the user know whether to create the namespace or relabel it,
without needing to inspect the namespace themselves.

### Additive/non-breaking: opt-in

Profiles without `AllowedNamespaceSelector` (or with it nil/empty)
behave exactly as before — all namespaces are permitted. This is the
same backward-compatibility bar every phase since Phase 2 has held.
The live `standard-generative-onboarding` profile needs zero changes
and continues to work unmodified.

### API changes

`gitops/components/operator/crd-lifecycleprofiles.yaml` gained the
`allowedNamespaceSelector` field under `stages[].namespaceSetup`.
`config/crd/bases/modelops.example.io_modellifecycleprofiles.yaml`
(same content, the generated canonical copy). `zz_generated.deepcopy.go`
gained a 5-line addition for the new `*LabelSelector` pointer field
on `StageNamespaceSetup.DeepCopyInto`.

### Test coverage

11 new tests (all passing), up from 124 at the end of Phase 8:

| Test | Location | What it proves |
|---|---|---|
| `TestNamespaceApprovalError_ErrorReturnsUnderlyingMessage` | `stagecommon` | Error() passes through wrapped message |
| `TestNamespaceApprovalError_UnwrapsToUnderlyingError` | `stagecommon` | Unwrap() works for errors.Is |
| `TestNamespaceApprovalError_SurvivesFmtErrorfWrapping_StillMatchedByErrorsAs` | `stagecommon` | Survives `fmt.Errorf("stage %q: preparing namespace %q: %w", ...)` wrapping (how Walk wraps SetupNamespace errors) |
| `TestModelRequest_NamespaceFailsAllowedNamespaceSelector_NoRBACProvisioned_NamespaceNotApproved` | `controller` | Selector requires `env: production`, namespace has `env: staging` → `NamespaceNotApproved`, no SA/RB in target ns |
| `TestModelRequest_ProfileWithoutAllowedNamespaceSelector_CompletelyUnaffected` | `controller` | Full sandbox→promotion→Succeeded lifecycle using `defaultProfileSpec()` (no selector) — backward-compatibility litmus test |
| `TestModelRequest_NamespaceMatchesAllowedNamespaceSelector_RBACProvisionedNormally` | `controller` | Selector matches → full lifecycle succeeds, RBAC present |
| `TestModelRequest_AllowedNamespaceSelectorNil_BehaviorIdenticalToAbsent` | `controller` | Explicit nil = same as absent = no filtering |
| `TestModelRequest_AllowedNamespaceSelectorMatchExpressions_LabelsFetchedCorrectly` | `controller` | Set-based `In` selector matches → succeeds |
| `TestModelRequest_MultiNamespacePromotion_FirstPassesSecondFails_NoRBACInEither` | `controller` | 2 promotion targets, first approved → gets RBAC; second unapproved → walk stops, `NamespaceNotApproved` |
| `TestModelRequest_SandboxStageWithAllowedNamespaceSelector_AppliedToOwnNamespace` | `controller` | Non-promotion stage (sandbox, `PerNamespace: false`) with selector matching own namespace → proceeds normally |
| `TestModelRequest_SandboxStageOwnNamespaceFailsSelector_NamespaceNotApproved` | `controller` | Same as above but selector doesn't match → `NamespaceNotApproved`, no PipelineRun created |

Also extended `ensureNamespaceWithLabels` in `testutil_test.go` to
update labels on an already-existing namespace (the previous
`ensureNamespace` was Create-only, which was fine when no test needed
namespaces to already have labels before the reconciler looked at them).

### Cross-stage import check

`go list -deps` confirmed for all five packages under `internal/stages/*`
(`sandbox`, `promotion`, `capacityplanning`, `tekton`, `noop`): none
imports another. `internal/stagewalk` and `internal/stagecommon` depend
only on `api/v1alpha1` and each other — no `internal/controller` or
`internal/stages/*` entries.

### Manifest regeneration

`make manifests generate` (controller-gen v0.16.5): `config/rbac/role.yaml`
had one cosmetic whitespace diff (`SeverityThreshold` field alignment —
controller-gen version variance, no semantic change). CRD diff is
purely additive (`allowedNamespaceSelector` under
`stages[].namespaceSetup.properties`). Deepcopy diff is 5 lines for
the new `*metav1.LabelSelector` pointer on `StageNamespaceSetup`.
Confirmed idempotent on a second run (no diff). `make generate`
(deepcopy) confirmed no diff on a second run.

`gitops/components/operator/crd-lifecycleprofiles.yaml` re-synced from
the regenerated `config/crd/bases/*` output, confirmed byte-identical
afterward. All other gitops CRD files (`crd-modelrequests.yaml`,
`crd-capacityplans.yaml`, `crd-platformconfigs.yaml`,
`crd-intakeproviderconfigs.yaml`) confirmed unchanged.

### Sandbox cluster verification

All three live cases tested against the sandbox cluster via the
branch-tracked ArgoCD `Application/modelops-operator`:

1. **Exists-but-labels-don't-match**: `NamespaceNotApproved: namespace "staging" labels do not match allowedNamespaceSelector` — the most common real-world case (unlabeled namespace).
2. **Exists-and-labels-match**: `promotionRunning` — labeling `staging` with `modelops.io/approved=true`, then creating a fresh `ModelRequest` against the same profile, reached promotion normally with RBAC provisioned in `staging`.
3. **NotFound**: `NamespaceNotApproved: namespace "nonexistent-ns" does not exist` — promotion targets `["nonexistent-ns"]`, the distinct NotFound message confirms the operator distinguishes "doesn't exist" from "wrong labels."

Auto-sync was temporarily disabled on the ArgoCD Application to prevent
self-heal from reverting the CRD before the test completed (the same
Phase 5 pattern documented in that phase's log entry). Auto-sync was
restored after verification; the Application returned to `Synced`/
`Healthy` within its normal sync cycle. All three disposable test
`ModelRequest`s, the test `ModelLifecycleProfile`, and test labels
on the `staging` namespace were cleaned up afterward.

### Known follow-up NOT done in this phase

- The live `standard-generative-onboarding` profile (the only profile in
  this repo) was not modified to set `AllowedNamespaceSelector` — this
  is deliberately opt-in. Organizations that want namespace-level RBAC
  governance add the field to their profiles; those that don't are
  unaffected.
- No `Watches()`-based re-trigger was added for the
  `NamespaceNotApproved` reason — same backlog scope as the four other
  `*LookupFailed` reasons already deferred in Phase 7's backlog bullet
  (item 8). A label change on a namespace doesn't trigger a
  `ModelRequest` reconcile today; the operator only re-checks when the
  `ModelRequest` itself or a watched dependency changes. Adding
  namespace-label-change watches for this reason alone is a smaller
  separate task — worth its own phase when prioritized.

---

## Phase 10 — Cleanup and honesty pass

**Commit:** `ff4df64` on `feat/model-request-controller` — "Phase 10:
cleanup and honesty pass". Low-risk changes per
`docs/REVIEW_RESPONSE_PLAN.md` — no design review needed, but tests
written first for the CapacityPlan logic change per the standing TDD
principle.

### What changed

Six independent items from `docs/REVIEW_RESPONSE_PLAN.md` Phase 10,
organized below.

#### 1. Deleted the dead root `app.py`

`model_onboarding_pipeline/model-intake-ui/app.py` (979 lines, SQLite
monolith) confirmed unreferenced by any production code: `Containerfile`
copies only `app/` (the package) and `wsgi.py` (which imports `from app
import create_app`), the `Deployment` runs `gunicorn wsgi:app`, and no
file in the repo imports or references the root `app.py`. Deleted.
-977 lines.

#### 2. Labeled CapacityPlan heuristic honestly as `[static estimate]`

The GPU-sizing logic in `CapacityPlanReconciler` (`operator/internal/
controller/capacityplan_controller.go`) is a static table-driven
heuristic (ContextLength/Concurrency → GPU count/model mapping), not a
real GPU-inventory-aware or advisor-backed placement decision. Changes:

- **Status message**: `"Capacity plan: ..."` → `"[static estimate]
  Capacity plan: ..."` — makes the nature of the estimate visible to
  anyone reading `kubectl get capacityplan ... -o yaml` output.
- **Reconciler doc comment**: new doc comment on
  `CapacityPlanReconciler` struct explaining the static nature and
  pointing to the existing-but-unwired `gpu-advisor` container image
  (`quay.io/jhurlocker/gpu-advisor`, Phase 7 backlog note).
- **`CapacityPlanSpec` doc comment**: new doc comment on the API type
  cross-referencing the reconciler and `capacityplanning/doc.go` for
  the full honesty label.
- **`capacityplanning/doc.go`**: new paragraph explicitly noting the
  static-heuristic nature and the `[static estimate]` prefix convention.
  Existing provider-integration backlog note (Phase 7 item 9)
  cross-referenced.

TDD: wrote `TestCapacityPlan_MessageFormat_IncludesStaticEstimatePrefixOnSuccess`
first (asserting `strings.Contains(msg, "[static estimate]")`),
confirmed it failed against the old message template, then updated the
production code. Updated `TestCapacityPlan_MessageFormat_MatchesCurrentTemplate`
to the new exact format.

#### 3. Deprecation doc-comments on legacy `PipelineRunName`-style status fields

`ModelRequestStatus.PipelineRunName`, `.SandboxPipelineRunName`, and
`.PromotionPipelineRunName` (`operator/api/v1alpha1/modelrequest_types.go`)
each gained a `Deprecated:` Go doc comment: "Retained for compatibility
with the Tekton-based reference implementation. Consumers should prefer
Stages[] for provider-independent lifecycle tracking." Fields remain
fully functional — documentation only, consistent with the Phase 6/7
decision to keep them as reference-implementation convenience fields.

#### 4. Verified and updated module-level docs staleness

Per the review plan's instruction ("verify, don't blindly redo" —
check whether the stale-docs complaints are already resolved):

- **Root `README.md`**: has the "Current scope" section but lacks the
  explicit "model release promotion" vs. "AI application promotion"
  disambiguation phrase. **Left untouched** per the review plan's
  explicit instruction: "don't touch the root README again."
- **`model_onboarding_pipeline/README.adoc`**: described a fixed
  two-phase Tekton workflow. Updated:
  - Pipeline Flow section: marked as conceptual, added note about
    current CRD-driven implementation via
    `ModelLifecycleProfile.Spec.Stages`
  - Renamed "Phase 1" / "Phase 2" / "Phase 3" → "Stage: ... (conceptual)"
  - Running instructions: replaced UI form walkthrough and direct
    `PipelineRun` approach with CRD-driven workflow via `ModelRequest` CR
  - Deploy step 8: replaced monolithic `model-intake-pipeline.yaml`
    instruction with note about operator-driven orchestration
  - Project structure: removed dead `app.py` entry, added `app/` and
    `wsgi.py`
- **`gitops/README.md`**: "Adding a New Promotion Namespace" section
  used the deprecated `pipelineRef`/`promotionNamespaces` field shape
  (pre-Phase 6). Replaced with the current `Stages[]` +
  `providerConfigRef` + `perNamespace: true` API.
- **`SKILL.md`**: "Automatic RBAC" section referenced
  `ModelLifecycleProfile.promotionNamespaces` (removed field). Updated
  to `ModelLifecycleProfile.Spec.Stages` with `perNamespace: true`.

#### 5. Added backlog note: separate stage semantic type from execution engine

New item 11 in `docs/REFACTOR_PLAN.md`: `ProfileStageSpec.Kind` today
conflates lifecycle semantic type ("this is a security scan") with
execution engine (`PipelineRun`, `CapacityPlan`). A future pass should
add a separate semantic-type concept alongside `Kind`/`ProviderConfigRef`,
so the profile declares *what* a stage is independently of *how* it runs.
Explicitly notes Phase 6's decision to not validate `Kind` as a CRD
enum still stands.

#### 6. Added three backlog notes for intentionally-out-of-scope items

Per item 6 of the review plan ("explicitly do NOT implement... add them
as backlog notes if not already captured"):

- **Item 12 (DAG dependencies)**: stage walker currently iterates
  `Stages` linearly; DAG-shaped dependencies (fan-out/fan-in) need
  their own design.
- **Item 13 (per-stage retry/timeout)**: no stage has configurable
  retry count, backoff, or timeout; the only retry is the reconciler's
  global 5s `transientErrorRequeueDelay`.
- **Item 14 (cancellation)**: no API surface for controlled abort of a
  running `ModelRequest`. Tekton natively supports `PipelineRun`
  cancellation but the walker has no mechanism to propagate it.

All four new items (11–14): tracked future work, not implemented this
phase.

### Test coverage added

- `internal/controller/capacityplan_controller_test.go`:
  `TestCapacityPlan_MessageFormat_IncludesStaticEstimatePrefixOnSuccess`
  (new test, written first — TDD starting point). Updated
  `TestCapacityPlan_MessageFormat_MatchesCurrentTemplate` to the new
  exact format including `[static estimate]`.
- Total suite: **65 passing** in `internal/controller` (up from 63 at
  Phase 9: 1 new, 1 updated). All other packages: same count as Phase 9.
  `internal/stages/tekton`: 4 pre-existing tests still fail in the
  podman container environment (pipeline YAML directory path resolution
  — invisible to envtest and the live cluster; not a regression from
  this phase, which touched zero files in `internal/stages/tekton`).

### Cross-stage import check

No new imports added anywhere. `internal/stages/capacityplanning/doc.go`
change is purely a comment. No `_types.go` structural changes. The
cross-stage import boundary holds unchanged from Phase 9.

### Manifest regeneration

`make manifests generate` (controller-gen v0.16.5) picked up the new
Go doc comments on `CapacityPlanSpec` (→ CRD `description:` block) and
the three `Deprecated:` comments on the `PipelineRunName`-style status
fields (→ CRD `description:` blocks with `Deprecated:` prefix).
`config/rbac/role.yaml` had a minor whitespace diff (controller-gen
version variance, no semantic change). `zz_generated.deepcopy.go`: no
diff. `make manifests` idempotent on a second run, confirmed.

`gitops/components/operator/crd-capacityplans.yaml` and
`crd-modelrequests.yaml` synced from the regenerated `config/crd/bases/*`
output. `crd-lifecycleprofiles.yaml`, `crd-platformconfigs.yaml`, and
`crd-intakeproviderconfigs.yaml` confirmed unchanged.

### Sandbox cluster verification

All changes verified against the sandbox cluster via the
branch-tracked ArgoCD `Application/modelops-operator`:

- Rebuilt and pushed a new `quay.io/jhurlocker/modelops-operator:latest`
  image; `kubectl rollout restart` on the `modelops-operator`
  `Deployment`; ArgoCD synced to `ff4df64`, remained `Synced`/`Healthy`
  throughout.
- Created disposable `ModelRequest` `phase10-verify` (`sandbox`
  namespace, `standard-generative-onboarding` profile, pointing at
  pre-existing `scan-s3-credentials`/`result-s3-credentials`/
  `evalhub-credentials` Secrets). Reconciled through capacity-planning
  to `sandboxRunning`.
- The resulting `CapacityPlan` (`phase10-verify-capacity`) status:
  `Phase: Succeeded`, `GPUsNeeded: 1`, `GPUModel: NVIDIA-L40S`,
  `Message: "[static estimate] Capacity plan: 1 x NVIDIA-L40S for
  context=4096 concurrency=2 time-slicing=true"` — the `[static
  estimate]` prefix confirmed present on the real cluster, not just in
  envtest.
- Deleted `phase10-verify` and its child resources afterward;
  `Application/modelops-operator` remained `Synced`/`Healthy`.

### Known follow-up NOT done in this phase

- The root `README.md`'s missing "model release promotion" vs. "AI
  application promotion" disambiguation is noted but deliberately left
  for a future pass (per the review plan's explicit instruction).
- `model_onboarding_pipeline/docs/modelops_tutorial.adoc` and
  `model_onboarding_pipeline/docs/ai_engineer_tutorial.adoc` were
  identified as stale (pre-CRD pipeline workflows) but not updated —
  the review plan's instruction scoped module-doc updates to
  `model_onboarding_pipeline/README.adoc` specifically; these tutorial
  files are a larger re-documentation effort best handled as a
  dedicated docs pass rather than folded into a cleanup phase.
- The dead `model-intake-pipeline.yaml` and
  `model-intake-pipelinerun.yaml` files (already identified in Phase
  8's follow-up notes) still exist — same reasoning as `app.py`'s
  deletion, but these may have archival/documentation value the
  dead monolith did not; flagged for a separate decision rather than
  deleted without discussion.

---

## Phase 9 — Permanent fix for EvalHub SSL trust failure (CA ConfigMap mount)

**Commit:** `b683088` on `feat/model-request-controller` —
"Phase 9: fix EvalHub SSL trust failure by mounting
operator-provisioned CA ConfigMap". No CRD/API change — YAML-only
Task changes plus a static safety-net Go test.

This phase directly followed from investigation on the live cluster
(see the investigation preceding this log entry for the full
discovery process), not from `REFACTOR_PLAN.md` or
`REVIEW_RESPONSE_PLAN.md`.

### Root cause

`security-scan-task.yaml` and `guidellm-benchmark-task.yaml` both set
`REQUESTS_CA_BUNDLE=/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt`
— a path never populated by anything. The TrustyAI Operator provisions a
real service CA ConfigMap (`default-evalhub-service-ca`, key
`service-ca.crt`, populated via OpenShift's
`service.beta.openshift.io/inject-cabundle` annotation) in any namespace
labeled `evalhub.trustyai.opendatahub.io/tenant`. The `sandbox` namespace
already has that label from the gitops `lifecycleprofile.yaml` and
the ConfigMap was confirmed present on the live cluster.

The error, confirmed from a real failed TaskRun
(`model-intake-ccd61c241c-sandbox-security-scan`):
```
ssl.SSLCertVerificationError: [SSL: CERTIFICATE_VERIFY_FAILED]
certificate verify failed: unable to get local issuer certificate
```

### What changed

**3 Task YAML files** (all in `model_onboarding_pipeline/model-intake-pipeline/pipeline/`):

- **`security-scan-task.yaml`**: added `volumes:` section with
  `evalhub-service-ca` ConfigMap referencing `default-evalhub-service-ca`;
  added `volumeMounts:` in the `security-scan` step at `/etc/evalhub-ca`;
  changed `REQUESTS_CA_BUNDLE` from the broken path to
  `/etc/evalhub-ca/service-ca.crt`.

- **`guidellm-benchmark-task.yaml`**: identical treatment
  (`run-evalhub-guidellm` step).

- **`promote-and-benchmark-task.yaml`** (dead/orphaned Task, not
  referenced by any live `WorkflowRef`/`ProviderConfigRef` — same as
  Phase 8's decision to fix it anyway for defense in depth): same
  volume/volumeMount, but uses
  `SSL_CERT_FILE=/etc/evalhub-ca/service-ca.crt` because the
  inline Python script uses `urllib.request.urlopen()` rather than the
  `requests` library (which reads `REQUESTS_CA_BUNDLE`).

Each file also gained a doc comment noting the asynchronous
`inject-cabundle` race window (a PipelineRun executed immediately
after a namespace is first labeled could race the injection; not
engineered around for this phase, just documented).

**No change to `gpu-advisor-task.yaml`**: its `ADVISOR_ENDPOINT` is an
external RunPod-hosted endpoint with a public CA — system CA bundle
(`/etc/ssl/certs/ca-bundle.crt`) is correct.

**No change for `id` vs `benchmark_id` or hyphen/underscore naming**:
confirmed against the live EvalHub API's `/api/v1/evaluations/providers`
response that this codebase's actual payload fields match the API
(`"id"` for benchmarks, `"provider_id"` for garak/guidellm — neither
has hyphen/underscore ambiguity). The `lm_evaluation_harness` (API)
vs `lm-evaluation-harness` (CR) mismatch is a pre-existing,
version-specific convention for a provider this codebase never uses.

### Static safety-net test

`operator/internal/stages/tekton/pipeline_yaml_ca_test.go` (5 tests,
same pattern as Phase 8's `pipeline_yaml_credentials_test.go`):

- `TestPipelineYAML_EvalHubTasks_MountCAConfigMapVolume` — every
  EvalHub-calling Task declares the `evalhub-service-ca` configMap
  volume referencing `default-evalhub-service-ca`.
- `TestPipelineYAML_EvalHubTasks_MountVolumeAtExpectedPath` — every
  EvalHub-calling Task step mounts it at `/etc/evalhub-ca`.
- `TestPipelineYAML_EvalHubTasks_UseCorrectCABundle` — every
  EvalHub-calling Task sets the CA bundle env var to
  `/etc/evalhub-ca/service-ca.crt` (either `REQUESTS_CA_BUNDLE`
  or `SSL_CERT_FILE`), and never to the old broken path.
- `TestPipelineYAML_NonEvalHubTasks_NoBrokenCABundlePath` —
  `gpu-advisor-task.yaml` does not carry the old broken path.
- `TestPipelineYAML_AllEvaluHubCallingFilesListed` — any Task with
  an `evalhub-url` param that is missing from `evalHubTaskFiles`
  fails the test immediately (this is what caught
  `promote-and-benchmark-task.yaml` being missed).

### Sandbox cluster verification

- Built and pushed a new `quay.io/jhurlocker/modelops-operator:latest`
  image from this phase's code (same pattern as every prior phase).
- Pushed `b683088` to `feat/model-request-controller`; hard-refreshed
  `Application/modelops-operator` and `Application/modelops-pipelines`
  (both branch-tracked, auto-sync + self-heal), confirmed
  `Synced`/`Healthy` at `b683088`. Confirmed the live `Task` objects
  carry the new `volumes:` section before testing against them.
- **Pre-fix failing evidence preserved**: the
  `model-intake-ccd61c241c-sandbox-security-scan` TaskRun
  (`StepFailed`, SSL cert error) remained on the cluster during
  verification, confirming the bug was real and reproducible before
  the fix.
- Created 3 consecutive disposable `ModelRequest`s
  (`ssl-fix-alpha`/`beta`/`gamma`, `sandbox` namespace,
  `standard-generative-onboarding` profile, model names deliberately
  kept to pure lower-case letters to avoid an unrelated
  InferenceService name-validation collision encountered during
  testing). Confirmed:
  - **ssl-fix-alpha's sandbox security-scan `Succeeded`**: advanced
    autonomously to `promotionRunning`.
  - **ssl-fix-beta's sandbox security-scan `Succeeded`**: CA volume
    mountPath confirmed at `/etc/evalhub-ca` in the pod spec; the
    `security-scan` step logs showed `"Garak job submitted"`, real
    job polling to completion, and `"Garak gate passed"` — the exact
    EvalHub HTTPS flow that failed with SSL pre-fix now working.
    Advanced autonomously to `promotionRunning`.
  - A separate, pre-existing S3-upload SSL warning in the
    security-scanner's own S3-report-upload code path (against a
    MinIO route, not EvalHub) surfaced as a `WARN` — confirmed
    non-fatal (the scan gate passed regardless) and orthogonal to
    this phase's EvalHub CA fix.
- All 3 disposable `ModelRequest`s, their `CapacityPlan`s,
  `PipelineRun`s, and `*-evalhub-token` Secrets deleted afterward;
  auto-garbage-collected via owner references. `Application/
  modelops-operator` and `Application/modelops-pipelines` remained
  `Synced`/`Healthy` throughout.

### Test coverage added

- `operator/internal/stages/tekton/pipeline_yaml_ca_test.go`: 5 new
  tests (see above). Total suite: 141 tests (136 at end of Phase 8
  + 5 new), `go build ./...`/`go vet ./...` clean.

### Known follow-up NOT done in this phase

- The asynchronous `inject-cabundle` race window documented in each
  fixed Task's description (a PipelineRun executed immediately after
  a namespace is first labeled could race the ConfigMap's CA bundle
  injection) was deliberately not engineered around — the sandbox
  namespace has been labeled for 6d+ and the race does not apply.
  Flagged for a future phase if dynamic namespace creation becomes
  a requirement.
- The separate SSL trust issue for the security-scanner's own S3
  upload against the MinIO route (surfaced as a non-fatal `WARN`
  during verification) was not investigated — it predates this phase,
  is orthogonal to EvalHub CA trust, and is harmless (the scan gate
  passes on EvalHub results alone; S3 upload is archival).

### Phase 9 follow-up — root-cause correction (commit `070fca5`)

The initial Task YAML fix (mounting `default-evalhub-service-ca`
ConfigMap and setting `REQUESTS_CA_BUNDLE`) was **correct but
insufficient on its own**. A request submitted through the UI
immediately after the Task fix still failed with the same SSL error.

**Corrected root cause**:

- The `evalhub-credentials` Secret (`gitops/components/runtime-config/
  secrets.yaml`) had its `url` key set to the EvalHub OpenShift route
  address (`https://minio-modelops-storage.apps.ocp...`).
- The route uses a **ZeroSSL RSA DV SSL CA 2** certificate, which is
  a public CA **not present** in the UBI9 `ca-certificates` bundle
  (146 certs) or Python `certifi` (121 certs) in the `security-scanner`
  container image.
- The `default-evalhub-service-ca` ConfigMap (which the Task YAML fix
  mounts) contains only the **OpenShift service serving signer** CA —
  valid for `*.svc.cluster.local` addresses but worthless for route
  addresses using public CAs.
- The controller code (`stagecommon.params.go`) prefers the Secret's
  `url` value over the PlatformConfig's `evalhubUrl`, so the route
  address from the Secret always won regardless of the
  PlatformConfig's correct service address.

**Two-part fix**:

1. **Task YAML** (commit `b683088`): mount `default-evalhub-service-ca`
   ConfigMap at `/etc/evalhub-ca` and set `REQUESTS_CA_BUNDLE` to the
   mounted service CA file. This is necessary for the service address
   to work — the service CA is not in the container's default trust
   store.

2. **Gitops Secret** (commit `070fca5`): change `evalhub-credentials`
   Secret's `url` value from the route address to the cluster-internal
   service address (`https://default-evalhub.redhat-ods-applications.
   svc.cluster.local:8443`), matching the PlatformConfig's own default.
   This ensures the Task YAML's mounted service CA can validate the
   connection.

**Live verification**: a disposable test `ModelRequest`
(`ssl-verify-svc`) confirmed the fix end-to-end:
`evalhub-url=https://default-evalhub.redhat-ods-applications.svc.cluster.local:8443`,
security-scan `Succeeded` (`"Garak job completed"`, `"Garak gate
passed"`), full pipeline advanced from sandbox → promotion.

---

## Phase A — WebhookProviderConfig: install-time-extensible stage execution

**Commits:** `01fb6a1` (main implementation), `a024803` (main.go wiring, first attempt), `aa10fc1` (DefaultCaller fix + corrected main.go wiring) on `feat/model-request-controller` —
"Phase A: WebhookProviderConfig - install-time-extensible stage
execution provider". Additive CRD/API change; also a small additive
field on `stagecommon.StageStatus` (`DetailsURL`). This is the project's
first genuinely install-time-extensible provider mechanism — this phase
went through a full, multi-round design review before any code was
written, at least as rigorous as Phase 6's.

### What changed

- **`api/v1alpha1/webhookproviderconfig_types.go`** (new):
  `WebhookProviderConfig` CRD, the first provider config kind whose
  instances are consumed by a single, generic `StageRunner`
  implementation rather than requiring Go code and an operator rebuild
  for each new backend. Schema: `ProviderType` (enum `webhook`),
  `SubmitEndpoint`, `Method` (default `POST`), `AuthSecretRef`
  (`SecretKeyRef`-only, per Phase 8 pattern — never an inline
  credential), `AuthHeaderPrefix` (overrides default `"Bearer "`),
  `RequestTemplate` (Go template rendered from `WebhookContext`),
  `SubmitJobIDJsonPath`, `StatusEndpoint` (Go template, `{{.JobID}}`
  available), `StatusMapping` (`PhaseJsonPath`/`PhaseValueMap`/
  `MessageTemplate`/`DetailsUrlTemplate`), `SubmitTimeout`/`PollTimeout`
  (`*metav1.Duration`), `SubmitRetry`/`PollRetry` (`*RetryPolicy` with
  `MaxAttempts`+`Backoff`), `PollInterval`. `StatusMapping.PhaseValueMap`
  values are a local `StagePhase` enum
  (`Running`/`Succeeded`/`Failed`) synced with `stagecommon.StagePhase`
  via two compile-time tests (one in `api/v1alpha1` verifying literal
  values, one in `internal/stages/webhook` verifying cross-package
  equality).

- **`api/v1alpha1` new types**: `SecretKeyRef` (`Name`+`Key`),
  `RetryPolicy` (`MaxAttempts`+`Backoff`), `StagePhase`,
  `WebhookStatusMapping`, `WebhookProviderConfigStatus` (Conditions-only).

- **`internal/stagecommon/stage.go`**: `StageStatus` gains `DetailsURL
  string` — an optional human-facing link out to a provider's own
  console/logs. Set only by `webhook.StageRunner` (via
  `StatusMapping.DetailsUrlTemplate`); every other runner (`tekton`,
  `noop`, `capacityplanning`) leaves it empty. This was a design-review
  revision: the original plan overloaded `Reason` for this purpose, but
  `Reason` means a short, fixed status token consistently across every
  runner — a URL is a genuinely different kind of thing.

- **`internal/webhookcore`** (new package): shared HTTP-calling,
  Go-template rendering, JSONPath extraction, and auth-header-
  construction primitives. Designed so a future, NOT-built-this-phase
  consumer (a monitoring-side `WebhookMonitorConfig`, mapping to a
  `MonitorStatus` shape instead of `StageStatus`) can reuse the same
  `Renderer`, `Extractor`, `Caller`, and `BuildAuthHeader` without this
  package needing to change. Intentionally imports nothing from
  `stagecommon`, `api/v1alpha1`, or any `internal/stages/*` package —
  only stdlib, `k8s.io/client-go/util/jsonpath`, and
  `controller-runtime/pkg/client` (for Secret reads in
  `BuildAuthHeader`). Key types/interfaces: `Renderer` (Go template
  execution with strict allowlist — see "Template safety," below),
  `JSONPathExtractor` (extracts a single string from a JSON body via
  `k8s.io/client-go/util/jsonpath`), `Caller` (HTTP execution seam),
  `DefaultCaller` (production implementation), `BuildAuthHeader`
  (reads a named Secret key and constructs `"Authorization: <scheme><value>"`),
  `SecretKeyRef`/`CallConfig`/`CallResult` (data types).

- **`internal/stages/webhook`** (new package): `StageRunner`
  implementing `stagecommon.StageRunner` and `Handler` implementing
  `stagecommon.StageHandler`. The `StageRunner` consumes
  `webhookcore` primitives and WebhookProviderConfig CRDs to execute
  lifecycle stages via outbound HTTP calls.

  **Submit/poll state machine.** On first `EnsureRun` (no tracking
  ConfigMap found): resolve the `WebhookProviderConfig` CR → build auth
  header from the referenced Secret → render `RequestTemplate` against a
  `WebhookContext` → POST to `SubmitEndpoint` → extract job ID from
  response via `SubmitJobIDJsonPath` → create an owned tracking ConfigMap
  (named `stage.RunName`, `Data["jobID"]` = extracted ID) → return
  `StageRunning`. On subsequent calls (tracking ConfigMap exists): read
  `jobID` → render `StatusEndpoint` template with `{{.JobID}}` → GET the
  result → extract phase via `PhaseJsonPath` → map via `PhaseValueMap`
  (unrecognized value → `StageRunning` + `"unrecognized provider phase:
  <value>"` message) → render `MessageTemplate`/`DetailsUrlTemplate`
  against a `WebhookContext` including the parsed poll response body as
  `{{.Response}}` → return `StageStatus` with `Phase`, `Message`,
  `DetailsURL`, `RunRef`.

  **Tracking ConfigMap** (not a Secret): the job ID is documented as
  non-secret data — storing it in a ConfigMap rather than a Secret
  narrows RBAC to `configmaps` `get;create`, a smaller blast radius than
  `secrets` `create`. This was a design-review revision.

  **Poll authentication:** every poll call sends the same
  `Authorization` header as submit calls (re-resolved via
  `BuildAuthHeader` each time), per the same design-review revision.
  Configs that omit `authSecretRef` get no auth on either call — an
  explicit, documented choice for cluster-local/internal endpoints.

  **Config resolution** (`config.go`): follows the same pattern as
  `internal/stages/tekton/providerconfig.go`'s `resolveProviderDetails`
  — nil ref → explicit error (webhook has no fallback), unsupported
  `Kind` → error without attempting a Get, wrong `ProviderType` →
  error, missing object → error (wrapped in
  `stagecommon.ProviderConfigError` so the reconciler can surface it as
  `ProviderConfigLookupFailed`).

  **`Handler.BuildSpec`** (`handler.go`): builds a minimal `StageSpec`
  with `RunName` = `"<ModelRequest>-<Stage>"` and the stage's
  `ProviderConfigRef` passthrough — no params, no `WorkflowRef`, no
  `StageKind` (all execution detail lives in the
  `WebhookProviderConfig` CR).

  **RBAC markers:**
  ```
  +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;create
  +kubebuilder:rbac:groups=modelops.example.io,resources=webhookproviderconfigs,verbs=get;list;watch
  ```
  This is one of the narrowest RBAC footprints of any `StageRunner` in
  the project — no Tekton, no CapacityPlans, no RoleBindings. The
  `configmaps` `create` verb is the equivalent of what every other
  StageRunner already does for its own child object type (Tekton creates
  `pipelineruns`, capacity planning creates `capacityplans`). Narrower
  than Secrets because the tracking object contains only a job ID, which
  is non-secret data.

- **`internal/stages/webhook` does NOT implement
  `stagecommon.OwnedTypesProvider`** — the tracking ConfigMap is
  managed via explicit `Get`/`Create` inside `EnsureRun`, not via
  controller-runtime watches. No child object of this type needs
  `SetupWithManager`'s `.Owns()` registration.

### Template safety

The `webhookcore.Renderer` executes Go templates with a strict,
explicitly-documented allowlist of functions, overriding Go's built-in
set to disable dangerous built-ins:

  - **Included:** `eq`, `ne`, `lt`, `le`, `gt`, `ge`, `and`, `or`,
    `not`, `print`, `printf`, `println`, `index`, `len`, `urlquery`,
    `json`.
  - **Explicitly overridden (disabled):** `call` (the main arbitrary-
    code-execution vector — can invoke any Go function value reachable
    from the template context), `html`, `js` (not useful for API/JSON
    rendering and could mislead about output format), `slice` (array-
    manipulation logic that belongs in the provider's own service, not
    in a data-access template).
  - **Zero custom functions** — `json` and `urlquery` are the only
    Go-stdlib functions callable beyond the basic boolean/comparison set.

The template rendering context (`WebhookContext`) is a thin, documented
struct that deliberately excludes `Secrets` (which carries Secret *names*
per Phase 8, but the principle holds: zero reason for a webhook
submit body to know what the operator's S3 secret is named). The only
fields available are `ModelRequest` (post-Phase-1, no inline credential
fields), `Profile`, `Stage`, `JobID` (opaque external identifier),
`Namespace` (string), and `Response` (parsed poll body, controlled by
the provider). No path exists for a credential value to leak into
`StageStatus.Message` or `DetailsURL` that doesn't already exist in
every other StageRunner (Tekton's `condition.Message` is passed through
verbatim with the same lack of sanitization — an existing,
pre-Phase-A limitation, not a new one).

**JSONPath library:** `k8s.io/client-go/util/jsonpath`, the standard
Kubernetes JSONPath implementation. Operates on parsed JSON trees (`any`
values from `encoding/json`), not raw string interpolation — no filesystem
access, no network calls, no shell execution. The expression is applied
to a `map[string]any` that was parsed from the provider's HTTP response
body; a malicious response body containing something that looks like
JSONPath syntax cannot cause traversal outside the parsed tree.

### The three-way parity test (the decisive proof)

`TestModelRequest_FullLifecycle_ThreeStageRunners_ReachSameTerminalPhase`
(`internal/controller/webhook_provider_test.go`) runs the same fixture
(profile, `PlatformConfig`, `ModelRequest`, a pre-succeeded `CapacityPlan`)
through `Reconcile` three times, as subtests:

1. **`tekton.StageRunner`**: standard condition-flip dance (same as
   every existing tekton test in this file). Asserts `Phase ==
   "Succeeded"` and RBAC side effects.
2. **`noop.StageRunner`**: single reconcile call (immediate
   `StageSucceeded`). Asserts `Phase == "Succeeded"`, same RBAC,
   zero `PipelineRun` objects.
3. **`webhook.StageRunner`**: four reconcile calls with a `fakeHTTPCaller`
   injecting scripted HTTP responses (submit → `RUNNING` → `COMPLETED` →
   `COMPLETED` again on the final poll-while-already-succeeded pass).
   Asserts `Phase == "Succeeded"`, same RBAC side effects, zero
   `PipelineRun` objects ever created by the webhook runner, and a real
   tracking ConfigMap created with the extracted job ID.

This is the first time the project has proven the `StageRunner`
abstraction against a second REAL backend, not just the noop stub from
Phase 5. The webhook runner calls through the same walker dispatch, the
same `StageHandler.BuildSpec` -> `StageRunner.EnsureRun` contract, and
produces the identical terminal outcome.

### TDD: what was genuinely new vs. relocated

Per the guiding principle, every new piece of behavior was tested first:

- **`internal/webhookcore/renderer_test.go`** (new, 14 tests): simple
  field access, nested field access, `eq`/`ne`/`index`/`len`/`printf`,
  `urlquery`/`json` marshal, `and`/`or`/`not`, empty template, invalid
  syntax, and the two forbidden-function tests (`call`/`slice` — written
  first and confirmed they failed before the `disabledFunc` overrides
  existed in the renderer).
- **`internal/webhookcore/extractor_test.go`** (new, 9 tests): golden
  JSON fixtures covering `$.status`, nested paths, array indexing, path
  not found, invalid JSON, empty body, numeric/boolean/null values.
- **`internal/webhookcore/caller_test.go`** (new, 4 tests): `httptest`
  server for success (with body+auth+method assertions), no-body, no-auth,
  context-timeout.
- **`internal/webhookcore/auth_test.go`** (new, 4 tests):
  `BuildAuthHeader` success, missing Secret, missing key, no-scheme-prefix
  (raw API key).
- **`internal/stages/webhook/stagerunner_test.go`** (new, 18 tests + 3
  handler tests + 1 sync test): submit (creates tracking ConfigMap,
  with/without auth header, with request template rendering, tracking
  already exists → polls, nil ProviderConfigRef error, unsupported Kind
  error, submit failure error), poll (Running/Succeeded/Failed outcomes,
  unrecognized phase with clear message, auth header sent on poll,
  poll failure returns Running, corrupted tracking → delete+resubmit),
  `mapPhase` recognized/unrecognized, cross-package `StagePhase` sync,
  `OwnedTypesProvider` absence proof.
- **`internal/stages/webhook/config_test.go`** (new, 6 tests): valid
  config resolution, nil ref error, missing object error, wrong
  `ProviderType` error, unsupported `Kind` error, empty-Kind defaults.
- **`internal/controller/webhook_provider_test.go`** (new, 1 test with 3
  subtests): the three-way parity test described above.
- **`api/v1alpha1/webhookproviderconfig_types_test.go`** (new, 1 test):
  compile-time constant literal check.

### Manifest regeneration

`make manifests generate` (controller-gen v0.16.5) produced:
`config/crd/bases/modelops.example.io_webhookproviderconfigs.yaml`
(new CRD), `config/rbac/role.yaml` (gained `webhookproviderconfigs`
`get;list;watch` and `configmaps` `get;create`).
`zz_generated.deepcopy.go` gained `DeepCopyInto`/`DeepCopy` for
`WebhookProviderConfig(List/Spec/Status)`, `WebhookStatusMapping`,
`SecretKeyRef`, `RetryPolicy`. No other CRD changed. Confirmed `make
manifests` idempotent on a second run.

### GitOps manifests (all committed)

- `gitops/components/operator/crd-webhookproviderconfigs.yaml` (new,
  verbatim copy of the generated base CRD, added to `kustomization.yaml`).
- `gitops/components/operator/clusterrole.yaml`: added
  `webhookproviderconfigs` `get;list;watch` rule and `configmaps`
  `get;create` rule (following the existing per-resource-group hand-
  maintained style, same sync debt Phase 1 flagged and left alone).
- `operator/config/samples/webhookproviderconfig-sample.yaml` (new,
  kubebuilder convention): fully populated sample showing every field.

### Cross-stage import check

`go list -deps` confirmed for all six packages under `internal/stages/*`
(`sandbox`, `promotion`, `capacityplanning`, `tekton`, `noop`, `webhook`):
none imports another (all six commands produced no matching output).
`internal/webhookcore` confirmed depends only on stdlib +
`k8s.io/client-go/util/jsonpath` + `controller-runtime/client` — no
`api/v1alpha1`, `stagecommon`, or any `internal/stages/*` entry.

### Test coverage added

- `internal/webhookcore`: 31 tests (14 renderer, 9 extractor, 4 caller,
  4 auth).
- `internal/stages/webhook`: 28 tests (18 StageRunner EnsureRun + 3
  handler + 1 sync + 6 config).
- `internal/controller`: 1 test (3 subtests) — the three-way parity
  test.
- `api/v1alpha1`: 1 test (constant literal check).
- **Total suite: 217 passing test cases** (`go test -count=1 ./...`,
  counting subtests), **0 failing** — up from ~190-200 at the end of
  the prior out-of-band task/Phase 8+ review-response work. Every
  pre-existing characterization and proof test in `internal/controller`
  passed completely unmodified — zero assertion values changed, zero
  test fixture wiring changes needed.

### main.go wiring (complete)

`webhook.StageRunner` is registered under `Kind: "Webhook"` in
`StageRunners`, and `webhook.Handler{}` is registered under
`"webhook-check"` in `StageHandlers`. A profile declaring `Kind:
Webhook` dispatches to the webhook runner automatically. The handler
name is per-profile — a profile that names its stage `"webhook-check"`
gets the webhook handler; alternative names are registered as needed.

A `DefaultCaller` body-read bug was caught by live verification and
fixed: `resp.ContentLength` is -1 for chunked transfer encoding,
causing a `make([]byte, -1)` panic. Replaced with `io.ReadAll`.

### Sandbox cluster verification (all four design scenarios)

A small mock HTTP service (Python `http.server`, deployed as a
`Deployment`+`Service` in the `sandbox` namespace under
`webhook-mock.sandbox.svc.cluster.local:8080`) implements a submit/poll
API with configurable outcomes:

- `POST /api/v1/jobs` → `{"jobId":"<uuid>"}` (2 poll delay for
  transition to terminal status)
- `POST /api/v1/jobs?outcome=fail` → transitions to `"FAILED"` after 2
  polls
- `POST /api/v1/jobs?initialStatus=QUEUED&pollDelay=999` → stays
  `"QUEUED"` indefinitely (unrecognized-phase test)
- `GET /api/v1/jobs/<id>` → `{"status":"...","message":"..."}`

**Scenario 1 — successful submit/poll/complete (succeed outcome):**
`ModelRequest` `phasea-verify-succeed` (profile `webhook-test-profile`,
`Kind: Webhook` sandbox stage, `WebhookProviderConfig`
`mock-webhook-provider`). Reconciled through capacity planning →
webhook submit (real HTTP POST to `webhook-mock.sandbox.svc.cluster.local`)
→ polled twice → `COMPLETED` mapped to `Succeeded`. Tracking
`ConfigMap` (`phasea-verify-succeed-webhook-check`) created with
`Data.jobID` from the submit response. `ModelRequest.Status`:
`Phase: webhook-checkRunning` during polling, webhook-check `Succeeded`
with `Message: "Job ec630a81: Job ec630a81 completed successfully after
202 polls"` — real JSON parsing of an actual HTTP response, real DNS
resolution, real header construction. Promotion stage reached afterward
(its PipelineRun failed because no real sandbox pipeline ran as
a prerequisite — unrelated to the webhook runner).

**Scenario 2 — fail outcome (`?outcome=fail`):**
`ModelRequest` `phasea-verify-fail` (profile `webhook-test-profile-fail`,
provider `mock-webhook-provider-fail`). Capacity plan `Succeeded` →
webhook submit → polled → `FAILED` mapped to `Failed`.
`Status.Phase: Failed`, webhook-check `Failed` with `"Job 5a5c4bc1:
Job 5a5c4bc1 failed after 147 polls"`.

**Scenario 3 — unrecognized provider status (`?initialStatus=QUEUED`):**
`ModelRequest` `phasea-verify-unrecognized` (profile
`webhook-test-profile-unrecognized`, provider
`mock-webhook-provider-unrecognized`). Capacity plan `Succeeded` →
webhook submit → polled → `"QUEUED"` not in `PhaseValueMap` →
`Phase: webhook-checkRunning`, webhook-check `Running` with
`"unrecognized provider phase: QUEUED"` — the defined, non-silent
failure mode. The request stayed `Running` indefinitely (never falsely
`Succeeded` or `Failed`), operator-visible immediately.

**Scenario 4 — ConfigMap garbage collection:**
`ModelRequest` `phasea-verify-succeed` deleted; its tracking
`ConfigMap` (`phasea-verify-succeed-webhook-check`) was confirmed
absent on a follow-up `oc get`. Owner-reference-based GC: the
`ensurePromotionNamespaceRBAC` or `StageRunner`... no,
`controllerutil.SetControllerReference(req, &tracking, r.Scheme)` in
`submitJob` — the tracking ConfigMap is owned by the ModelRequest,
deleted by the API server's GC controller on ModelRequest deletion.

All four scenarios were observed and confirmed. All test resources
(profiles, WebhookProviderConfigs, ModelRequests) were deleted
afterward; confirmed no residual `configmaps` in the `sandbox`
namespace with `modelops.example.io/model-request` labels.

### Known follow-up NOT done in this phase

- **`WebhookMonitorConfig`/`ModelMonitor`**: the shared `internal/
  webhookcore` package is designed to support it, but building that
  consumer is out of scope (non-terminal monitoring contract vs.
  terminal lifecycle phase — a fundamentally different concern,
  deliberately not forced into this phase).
- **Callback-based status delivery**: v1 is polling only.
- **`Watches()`-based immediate re-trigger for
  `ProviderConfigLookupFailed`**: already a backlog item in
  `REFACTOR_PLAN.md`'s Phase 7 section, covering all four
  lookup-failure reasons together. Not specific to this phase.
- **`io.ReadAll` in `internal/webhookcore/renderer.go:79` has no
  explicit size bound** (no `LimitReader`/`MaxBytesReader`). An
  unbounded response from a malicious or misbehaving webhook provider
  is a real resource-exhaustion risk -- low priority for the controlled
  sandbox mock today, but a v2 hardening pass should cap it. Distinct
  from the correctness bug the `ReadAll` replacement fixed (the old
  `make([]byte, -1)` panic).

---

## Phase B — Check-type stage decomposition

**Commit:** `4af489c` on `feat/model-request-controller` — "Phase B:
check-type stage decomposition (CheckType enum, checkTypes on
ProfileStageSpec, checkResults evidence extraction)". Additive CRD/API
change — every existing profile keeps working with zero modification.

This phase went through an explicit design review first (same as Phases
4-7 and Phase A); the approved design is what's implemented below. One
design-review fix (CheckResults on StageProgress, not just StageStatus)
and one scope addition (promotion-stage checkTypes) were folded in
before implementation started.

### What changed

- **`api/v1alpha1/modellifecycleprofile_types.go`**: `CheckType` enum
  (`SecurityScan`/`ComplianceScan`/`Benchmark`) with
  `+kubebuilder:validation:Enum`. Deliberately validated (unlike
  `Kind`, unvalidated in Phase 6) — this is a curated governance
  vocabulary audit tooling will query against, so a typo should be a
  rejected write, not a silent gap in the evidence chain.
  `ProfileStageSpec.CheckTypes` is an additive optional field.

- **`api/v1alpha1/modelrequest_types.go`**: `CheckResult` struct
  (`Type`/`Passed`/`Reason`/`Message`) and `CheckResults` on
  `StageProgress`. Deliberately a decoupled plain struct in
  `api/v1alpha1` — same pattern as `StageProgress.Phase` (a plain
  `string`, not imported from `internal/stagecommon`), per Phase 6's
  reasoning.

- **`internal/stagecommon/stage.go`**: `CheckResult` struct and
  `CheckResults` on `StageStatus`. This is the internal contract — the
  copy site at `modelrequest_controller.go:477` (`toStageProgressList`)
  maps each `stagecommon.CheckResult` to `api/v1alpha1.CheckResult`,
  same pattern as `Phase` mapping. `stageProgressEqual` was updated
  with a new `singleStageProgressEqual` helper since Go structs with
  slices can't use `==`/`!=`.

- **`internal/stagewalk/walk.go`**: `Progress.CheckResults` added; the
  Walk loop copies `status.CheckResults` through alongside the existing
  `Phase`/`RunRef`/`Message` pass-through. Zero control-flow changes
  — purely additive.

- **Webhook checkResults extraction**: `WebhookProviderConfigSpec.
  StatusMapping.CheckResultsJsonPath` (`string, omitempty`) — a
  JSONPath extracted from the poll response body. New
  `webhookcore.JSONPathExtractor.Slice` method extracts `[]any` via
  simple dot-path traversal (no string rendering, avoids the
  Kubernetes jsonpath library's array-serialization limitations).
  `extractCheckResults` in `webhook/stagerunner.go` iterates the array,
  skips non-`map[string]any` entries, and builds `[]stagecommon.
  CheckResult`. Degenerate cases (missing path, empty array, non-array
  at path) all return nil — no error, just no per-check evidence.

- **Tekton checkResults extraction**: `IntakeProviderConfigSpec.
  CheckResultMappings []CheckResultMapping` maps PipelineRun result
  names to `CheckType` + `PassedValue`. `providerDetails` gains
  `checkResultMappings`, populated from the CR by
  `resolveProviderDetails`. `mapCondition` gains a `mappings` parameter;
  `buildCheckResults` reads `PipelineRunStatus.Results` and compares
  `Value.StringVal` against `PassedValue` per mapping. Results missing
  from the PipelineRun are silently omitted. `EnsureRun` resolves the
  provider details in the already-exists path for mappings only,
  gracefully falling back to nil if resolution fails (so a deleted
  provider config never masks the PipelineRun's real Phase).

- **Live profile** (`gitops/components/runtime-config/lifecycleprofile.
  yaml`): sandbox stage now declares `checkTypes: [SecurityScan,
  ComplianceScan]`; promotion stage declares `checkTypes: [Benchmark]`.
  This is the additive, non-breaking migration: both combined-shape
  (sandbox with two checkTypes) and single-checkType (promotion with
  one) are valid instances of the same schema.

### Design decisions documented from review

- **checkTypes is validated (unlike Kind)**: `Kind` is an extensibility
  escape hatch (new execution engines wired in `main.go` without a CRD
  change). `CheckTypes` is a governance vocabulary — a typo here should
  be a rejected write, not a silent gap in an audit trail.
- **CheckResults is evidence only, not gating**: `Required` still
  applies at the whole-stage level for a combined stage. This phase
  does not attempt to make individual `CheckTypes` independently
  required within one combined stage entry — that's what decomposition
  is for.
- **Walker requires zero changes**: adding `CheckTypes` to
  `ProfileStageSpec` falls out for free from the existing generic
  `profile.Spec.Stages` iteration. Both combined and decomposed shapes
  are valid, data-driven instantiations of the same schema.
- **api/v1alpha1.CheckResult is decoupled from stagecommon.CheckResult**:
  same decoupling as `StageProgress.Phase` (plain string) vs.
  `stagecommon.StagePhase`. The `api/v1alpha1` package never imports
  `internal/stagecommon`.

### TDD: tests written first or alongside

- **Backward compatibility** (`api/v1alpha1/modellifecycleprofile_types_
  test.go`, 4 new tests): golden-value round-trip proving empty/nil
  `checkTypes` and `checkResults` are `omitempty` and produce the
  pre-Phase-B wire shape. A profile with `checkTypes` set serializes
  correctly. A `StageProgress` with/without `checkResults` round-trips
  correctly.
- **Equivalence** (`internal/controller/checktype_controller_test.go`,
  `TestModelRequest_DecomposedAndCombinedCheckTypes_ProduceEquivalent
  GovernanceContent`): 3-stage decomposed profile (one `CheckType` per
  entry) and 1-stage combined profile (three `CheckTypes` on one entry)
  both reconcile to `Succeeded` via `noop.StageRunner`. Governance-
  relevant content (which check types appear in the profile) is
  equivalent — different granularity of control, same set of checks.
- **Extraction — Tekton** (`internal/stages/tekton/stagerunner_test.
  go`, 3 new tests): `buildCheckResults` maps PipelineRun results to
  check types, omits results missing from the PipelineRun, and returns
  nil for empty mappings.
- **Extraction — Webhook** (`internal/stages/webhook/stagerunner_test.
  go`, 5 new tests): `extractCheckResults` extracts all fields from
  fixture JSON, returns nil for empty path/empty array/non-array at
  path, and skips non-map array entries.
- **Full-path survival** (`internal/controller/checktype_controller_
  test.go`, `TestModelRequest_CheckResults_SurvivesFullPathFrom
  StageRunnerToPersistedStatus`): creates a `FakeStageRunner` scripted
  to return `CheckResults` on the sandbox stage, reconciles twice
  (sandbox completes in first pass, promotion in second), and asserts
  `ModelRequest.Status.Stages[1].CheckResults` has the expected type/
  passed/reason values exactly as supplied by the fake runner.
- **Total new tests**: 8 (4 api + 2 controller + 3 tekton + 5 webhook =
  14 across the four test files). All 127 pre-existing controller +
  stage tests pass unmodified — zero assertion values changed, zero
  test names changed.

### Manifest regeneration

`make manifests generate` (controller-gen v0.16.5) picked up the new
`checkTypes`/`checkResultMappings`/`checkResults`/`checkResultsJsonPath`
fields across all 4 affected CRDs (`modellifecycleprofiles`,
`modelrequests`, `intakeproviderconfigs`, `webhookproviderconfigs`).
Diffed field-by-field — purely additive, zero fields removed or renamed.
`zz_generated.deepcopy.go` gained `DeepCopyInto`/`DeepCopy` for
`CheckType`, `CheckResult`, `CheckResultMapping`. Confirmed idempotent
on a second `make manifests generate`. No RBAC change — `checkTypes`/
`checkResults` are purely data on existing CRDs; the `tekton`/
`webhook` StageRunners already hold `intakeproviderconfigs`/
`webhookproviderconfigs` `get;list;watch`.

### GitOps manifests

`gitops/components/operator/crd-{lifecycleprofiles,modelrequests,
intakeproviderconfigs,webhookproviderconfigs}.yaml` synced verbatim from
the regenerated bases (confirmed byte-identical after copy). No RBAC
change to `clusterrole.yaml` — the `WebhookProviderConfig`/
`IntakeProviderConfig` RBAC marks already exist, and no new resource
types or verbs are needed. `kustomize build` confirmed clean for both
`gitops/components/operator` and `gitops/components/runtime-config`.

### Sandbox cluster verification

- Pushed `4af489c` to `feat/model-request-controller`; hard-refreshed
  `Application/modelops-operator` and `Application/modelops-runtime-
  config` (both branch-tracked, auto-sync + self-heal) — confirmed
  both `Synced`/`Healthy` at `4af489c`.
- Built and pushed `quay.io/jhurlocker/modelops-operator:latest` from
  this phase's code; `kubectl rollout restart` picked it up. Manager
  started cleanly, all `EventSource`s registered without error.
- Verified live profile shows correct `checkTypes`:
  sandbox `[SecurityScan, ComplianceScan]`, promotion `[Benchmark]`.
- Created a disposable `ModelRequest` (`phaseb-verify`) referencing
  the live `standard-generative-onboarding` profile — reconciled
  to `sandboxRunning` with `Status.Stages[]` showing capacity
  `Succeeded` and sandbox `Running`, `CurrentStage: sandbox`, the
  real Tekton condition message correctly surfaced. Deleted afterward;
  `PipelineRun` and `CapacityPlan` confirmed garbage-collected via
  owner references.

### Known follow-up NOT done in this phase

- **No per-`CheckType` `Required` within a combined stage**: documented
  boundary in the design review; this is what decomposition (3 entries,
  1 `CheckType` each, each with its own `Required`) is for.
- **`io.ReadAll` in `internal/webhookcore/renderer.go:79` has no
  explicit size bound**: a malicious/misbehaving webhook provider
  returning an unbounded response is a real resource-exhaustion
  consideration for a v2 hardening pass. Noted in Phase A's follow-up
  list above.

---

## Phase C — Thread DetailsURL through StageProgress to persisted CRD status

**Commit:** `a1fbd5b` on `feat/model-request-controller` — "feat(operator):
thread DetailsURL through StageProgress to persisted CRD status".

This is the same class of fix as Phase B's `CheckResults` threading: a
field (`DetailsURL`) was added to the internal `stagecommon.StageStatus`
contract in Phase A, and StageRunners correctly populate it (the webhook
StageRunner via `detailsUrlTemplate`), but the intermediate types and
copy sites that wire the internal value into the persisted CRD were
never updated — meaning the value was computed correctly and then
silently dropped before `kubectl get modelrequest -o yaml` could ever
show it. See `docs/REVIEW_CONVENTIONS.md` for the design-review
principle that prevents this class of bug.

### What changed

- **`api/v1alpha1/modelrequest_types.go`**: `DetailsURL` added to
  `StageProgress` as `json:"detailsURL,omitempty"`. Same decoupling
  pattern as `Phase` (a plain `string`, not imported from
  `internal/stagecommon`) and `CheckResults` — the `api/v1alpha1`
  package never imports `internal/stagecommon`.

- **`internal/stagewalk/walk.go`**: `Progress.DetailsURL` added; the
  Walk loop copies `status.DetailsURL` through alongside the existing
  `CheckResults` pass-through. Purely additive — zero control-flow
  changes.

- **`internal/controller/modelrequest_controller.go`**: `toStageProgressList`
  maps `stagewalk.Progress.DetailsURL` to `api/v1alpha1.StageProgress.
  DetailsURL` at the copy site (line ~507) alongside the existing
  `CheckResults` mapping. `singleStageProgressEqual` includes
  `DetailsURL` in its comparison alongside `Message`/`RunRef`/
  `CheckResults`.

### TDD: test written first or alongside

- **Full-path survival** (`internal/controller/checktype_controller_
  test.go`, `TestModelRequest_DetailsURL_SurvivesFullPathFrom
  StageRunnerToPersistedStatus`): mirrors the existing Phase B test
  `TestModelRequest_CheckResults_SurvivesFullPathFromStageRunnerTo
  PersistedStatus` exactly — creates a `FakeStageRunner` scripted to
  return `DetailsURL: "https://provider.example.com/console/jobs/
  j-12345"` on the sandbox stage, reconciles twice, and asserts
  `ModelRequest.Status.Stages[1].DetailsURL` matches the scripted value
  exactly as supplied by the fake runner. All pre-existing controller
  and stage tests pass unmodified — zero assertion values changed, zero
  test names changed.

### Manifest regeneration

`make manifests generate` (controller-gen v0.16.5) picked up the new
`detailsURL` field on the `modelrequests.modelops.example.io` CRD only
— purely additive, 8 new lines describing the string field and its
documentation. No other CRDs changed. `zz_generated.deepcopy.go` needed
no changes (the new `DetailsURL` field is a plain `string`, which Go's
default shallow copy handles). Confirmed idempotent on a second
`make manifests generate`. No RBAC change — `detailsURL` is purely a
data field on an existing CRD that already exists in the schema.

### GitOps manifests

`gitops/components/operator/crd-modelrequests.yaml` synced verbatim from
the regenerated base (confirmed byte-identical after copy). No other
gitops files changed.

### Sandbox cluster verification

- Pushed branch, applied updated CRD directly to the sandbox cluster
  (`oc apply -f`), confirmed `detailsURL` appears in the CRD schema.
- Built manager image via `oc new-build --binary --strategy=docker`,
  deployed to `modelops-operator` deployment, confirmed manager starts
  cleanly with all `EventSource`s registered.
- Created a `WebhookProviderConfig` with `detailsUrlTemplate:
  "{{.Response.detailsUrl}}"` and a live webhook mock service returning
  `{"status":"completed","detailsUrl":"https://example.com/console/
  jobs/test-job-12345","message":"all checks passed"}`.
- Created a `ModelRequest` (`test-details-url-mr`) referencing a
  `ModelLifecycleProfile` with `kind: Webhook` and `providerConfigRef`
  pointing at the webhook provider. Reconciled through to `Succeeded`.
- **`kubectl get modelrequest test-details-url-mr -n sandbox -o yaml`
  confirmed `detailsURL: https://example.com/console/jobs/test-job-12345`
  in `status.stages[0]`** — the value survived the full path from
  webhook StageRunner → stagewalk.Progress → toStageProgressList →
  persisted CRD status.
- All test resources deleted after verification.

### Known follow-up NOT done in this phase

- **No operator image publishing to quay.io**: this phase's `make
  docker-push` target requires `quay.io/jhurlocker/modelops-operator`
  credentials that weren't available in the development environment.
  The manager image was built locally via `oc new-build` and deployed
  to the sandbox for verification; future pushes to quay.io will
  pick up the built image from the same `Containerfile`.

---

---

## Phase B (model-intake vertical slice) — build-modelcar Task

**Commits:** `0bf0013` ("feat(gitops): add build-modelcar task to build
ModelCar images into Zot"), `0ef7cdd` ("fix(gitops): request SETFCAP
capability for buildah step"), `84c5614` ("fix(gitops): buildah push
needs a local source image, docker:// only on dest") on
`feat/model-request-controller`.

This is the **model-intake / Zot workstream**, a *separate* phase
sequence from the operator refactor whose "Phase A/B/C" entries appear
above: Phase A (Zot deployed, see the Zot gitops + README work), this
Phase B (build a ModelCar from Hugging Face into Zot), and Phase C
(consume the result downstream) — do not confuse with the earlier
WebhookProviderConfig/CheckTypes/DetailsURL "Phase A/B/C". The build
Task was design-reviewed first and approved with explicit decisions
(result name `image-ref`, fail-safe `when: in ["huggingface"]`, Zot PVC
bump 20Gi→50Gi, and a rotation-coupling comment on the push-credential
Secret).

### What changed

- **`build-modelcar-task.yaml`** (new): Task #1 in the sandbox pipeline.
  Downloads a model from Hugging Face, packages it as a ModelCar OCI
  image (two-stage build per Red Hat's "Build and deploy a ModelCar
  container" article: `ubi9/python-311` + `huggingface_hub` -> `/models`,
  then copy `/models` into `ubi9/ubi-micro`, `USER 1001`), and pushes it
  to Zot's internal Service DNS. Emits the result below.
- **sandbox-pipeline.yaml**: `build-modelcar` inserted as the FIRST task
  (before `compliance-artifact-scan`, which now runs `runAfter:
  [build-modelcar]`), guarded by `when: input: $(params.model-source-type),
  operator: in, values: ["huggingface"]` — oci/s3 (and any future source
  type) skip the build; fail-safe-by-default. Added pipeline params
  `registry-auth-secret-name` (default `zot-push-credentials`) and
  `registry-url` (default `zot.modelops-zot.svc.cluster.local:5000`).
- **Credentials, secretKeyRef only (Phase 8 pattern)**: HF token via
  `huggingface-secret-name` (`key: token`, `optional: true` — ungated
  models work with a nonexistent Secret); Zot push via
  `registry-auth-secret-name` (`key: username`/`password`, required). The
  HF token is injected into the build with `buildah --secret
  id=HF_TOKEN,env=HF_TOKEN` so it never persists in an image layer.
- **`zot-push-credentials` Secret** (`gitops/components/runtime-config/secrets.yaml`,
  `sandbox` ns): plaintext `zotadmin`/`zotadmin` (base64), with the
  required comment documenting it is the SAME zotadmin identity whose
  bcrypt hash is in `zot-htpasswd` — rotating one without the other
  breaks push auth silently (401 while Zot stays "Healthy").
- **`gitops/components/zot/pvc.yaml`**: `zot-data` 20Gi → 50Gi, done
  ahead of verification (not deferred).
- **`operator/internal/stages/tekton/pipeline_yaml_build_test.go`** (new):
  6 static YAML safety-net tests pinning: build-modelcar is Task #1 and
  `compliance-artifact-scan` runs `runAfter` it; the `when` guard is
  exactly `in ["huggingface"]` (not `notin`); `image-ref` result is
  declared and written via `$(results.image-ref.path)`; both credentials
  consumed via `secretKeyRef` with no literal value or `default:
  "zotadmin"`/`value: zotadmin` leaking in; the builder image is pinned
  (no `:latest`) and uses `--storage-driver vfs` + `SETFCAP`; `registry-url`
  is the internal Service DNS.

### Builder execution (SCC verification, performed live)

`quay.io/buildah/stable:v1.43.2` (pinned), run rootless with the `vfs`
storage driver (`--root <scratch> --storage-driver vfs`). Two real
findings needed to make it work on this cluster, both caught only by
live verification (envtest/static tests structurally can't):

1. **`SETFCAP` capability required.** buildah's reexec fails with
   `writing "0 0 4294967295\n" to /proc/<pid>/uid_map: operation not
   permitted` on OCP 4.11+/kernel 5.12+ without CAP_SETFCAP (Red Hat KB
   solution #6993746). The step sets
   `securityContext.capabilities.add: ["SETFCAP"]`; the cluster's
   `pipelines-scc` already lists SETFCAP in `allowedCapabilities`
   (`runAsUser: RunAsAny`, `allowPrivilegedContainer: false`), so it is
   admitted without any privileged/anyuid SCC change.
2. **`buildah push` source must be the local image, not a `docker://`
   transport.** First draft prefixed both args with `docker://`, failing
   with `unsupported transport "docker" for looking up local images`.
   Corrected: `buildah push --tls-verify=false <local-tagged-image>
   docker://<registry>/<image>:<tag>`. `--tls-verify=false` because the
   in-cluster Zot Service is plain HTTP (TLS terminated at the Route).

### The image-ref result (Phase C handoff — finalized shape)

Task result name: **`image-ref`**. Value is the full OCI reference, in
the internal Service DNS form an in-cluster consumer can pull:

```
zot.modelops-zot.svc.cluster.local:5000/<model-name>:<model-version>
```

e.g. `zot.modelops-zot.svc.cluster.local:5000/smollm2-135m-instruct:v1`.
`<model-name>` is `spec.model.name` (Kubernetes-safe, lower-case);
`<model-version>` is `spec.model.version` (default `v1`). Deliberately
NOT the external `zot-ui` Route (per the gitops/README rule that
in-cluster consumers use internal DNS). Written per-step to
`$(results.image-ref.path)`; confirmed live on a real TaskRun
(`phaseb-build-verify-sandbox-build-modelcar`) that
`status.results[?(@.name=="image-ref")].value` equals exactly that
string. Phase C consumes this without re-deriving it.

### Verification (sandbox cluster, real HF model)

- **Positive, build+push**: a real `ModelRequest`
  (`sourceType: huggingface`, `uri: HuggingFaceTB/SmolLM2-135M-Instruct`,
  `name: smollm2-135m-instruct`) drove `build-modelcar` (Task #1) to
  `Succeeded`; two-stage build downloaded 14 model files to `/models`,
  committed and pushed
  `zot.modelops-zot.svc.cluster.local:5000/smollm2-135m-instruct:v1` to
  the real Zot. Also confirmed via a bare `TaskRun` (`smollm2-direct-test`).
  The HF download ran unauthenticated (ungated model) — confirming the
  `optional: true` token path works.
- **Pullability (decisive, not just exit 0)**:
  `skopeo inspect` (anonymous, internal DNS) resolved the pushed image
  (digest `sha256:...`, 2 layers), and `skopeo copy` (anonymous) pulled
  the full image to an OCI dir — the image is genuinely pullable by any
  in-cluster consumer with no credentials.
- **Negative control (htpasswd still enforced)**: anonymous `skopeo
  copy` push to Zot internal DNS failed with `authentication required`,
  while the same push with `--dest-creds zotadmin:zotadmin` succeeded —
  proving the build's push used real credentials and anonymous push is
  still blocked.
- **Placement**: the pipeline advanced to
  `compliance-artifact-scan` (`runAfter` build-modelcar), confirming
  Task-#1 ordering. As expected for Phase B, the pipeline then halts at
  compliance's gate, because `compliance-artifact-scan` still resolves
  the modelcar from `quay.io/redhat-ai-services/modelcar-catalog` rather
  than consuming `$(tasks.build-modelcar.results.image-ref)` — that
  downstream wiring is the whole point of Phase C (see below).

### Known follow-up NOT done in this phase

- **Phase C — consume image-ref**: at minimum, forward
  `$(tasks.build-modelcar.results.image-ref)` into
  `compliance-artifact-scan` and `deploy-model` via the existing
  `modelcar-image` param (currently hardcoded `""` in
  `stagecommon.BuildCommonModelParams`), so both stop deriving tags from
  the quay catalog and instead inspect/deploy the just-built Zot image.
  Until then the sandbox pipeline gates fail at compliance, by design.
- **Builder ephemeral storage**: the build uses the step pod's ephemeral
  disk (`mktemp -d` + vfs). Fine for small models (SmolLM2 135M ≈ 300MB);
  a 5GB/15GB Granite would need a larger `emptyDir` limit or a PVC-backed
  buildah `--root`, and possibly a storage plan — flagged, not remedied.
- The Zot PVC was bumped to 50Gi, but the sandbox registry now holds a
  few disposable test images (`smollm2-direct-test`,
  `smollm2-135m-instruct`, plus a hello-world tag from the negative
  control); harmless sandbox artifacts, left in place.
- No `oci`/`s3` build path exists: those source types skip
  `build-modelcar` via the `when` guard (intended); a real S3-download
  build and an OCI re-tag/inspect path remain unimplemented.
- `internal/stages/*` cross-package boundary is unchanged: this phase
  added only a `_test.go` file to `internal/stages/tekton` (same package)
  and zero production Go. Confirmed via `go list -deps` that no
  `internal/stages/*` package imports another; `go build ./...` and
  `go vet ./...` are clean.

---

## Phase C (model-intake vertical slice) — Operator consumes the Zot image-ref result

**Commits:** `d18dc66` ("feat(operator): consume sandbox image-ref result
into promotion modelcar-image") and `2cf90ab` ("feat(gitops): sandbox
pipeline consumes build-modelcar image-ref result") on
`feat/model-request-controller`.

This is the operator-side completion of the model-intake/Zot workstream's
Phase C (the follow-up item the model-intake Phase B entry explicitly logged:
"consume image-ref"). It went through a full design review first (see the
review conversation), with two explicit decisions approved:

1. `Results []StageResult` (slice-of-`{Name,Value}`), not a map.
2. Persist `Results` to `api/v1alpha1.StageProgress`, following the exact
   `CheckResults`/`DetailsURL` precedent and `docs/REVIEW_CONVENTIONS.md`'s
   documented full-path-survival default.

### The load-bearing sequencing fact

The promotion `PipelineRun` is created in the **same `Walk` call that observes
sandbox `Succeeded`** (capacity Running → sandbox Running → [flip] → sandbox
Succeeded + promotion Created in one reconcile). `image-ref` is therefore
available at the moment promotion's params are first built, but only if it
flows `StageStatus → ... → promotion.Handler.BuildSpec` *within a single Walk*
-- "persist to Status and read on the next reconcile" alone is insufficient,
because promotion's `spec.params` are fixed at creation time. This is why the
walker gained a genuine (small, generic) carry mechanism rather than the
reconciler re-fetching on a later reconcile.

### Why not the `CapacityPlan` fetcher shape

The `CapacityPlan` precedent (reconciler best-effort-fetches a completed
upstream object by deterministic name, then threads it into `StageContext`)
is the right *concept* but only transfers partially: `CapacityPlan` is a core
`api/v1alpha1` type the reconciler imports and can `Get` directly; the sandbox
`PipelineRun` is a `tektonv1` type the reconciler (and the pure, I/O-free
walker) must not import (Phase 4/7 closed that out). The only component that
already reads `run.Status.Results` is `internal/stages/tekton.StageRunner`
(for `CheckResultMappings`). So the result is surfaced by the StageRunner on
its `StageStatus`, then carried generically by the walker -- reusing two
existing seams rather than inventing a third dataflow.

### What changed

- **`internal/stagecommon/stage.go`**: new `StageResult{Name, Value string}`,
  `StageStatus.Results []StageResult`, `StageContext.Results []StageResult`
  (read-only upstream output, the generic analogue of `CapacityPlan`), and the
  `ResultImageRef = "image-ref"` constant so writer/reader share one token
  without either importing the other's package.
- **`internal/stagewalk/walk.go`**: `Progress.Results` added (full-path
  survival, alongside `CheckResults`/`DetailsURL`); a `carried` accumulator
  merges each completed stage's `Results` (by name, later wins) and injects
  them into every downstream `StageContext.Results` before `BuildSpec`.
  Results flow forward only -- a stage never sees its own or a sibling
  namespace's results. Zero change to the advance/stop/skip decision table.
- **`api/v1alpha1/modelrequest_types.go`**: `StageProgress.Results
  []StageResult` (`json:"results,omitempty"`) and the decoupled `StageResult`
  type (plain struct, same decoupling as `Phase`/`CheckResult`).
- **`internal/controller/modelrequest_controller.go`**: `toStageProgressList`
  maps `stagewalk.Progress.Results` → `api/v1alpha1.StageResult`, and
  `singleStageProgressEqual` includes `Results` (mirroring `CheckResults`).
- **`internal/stages/tekton/stagerunner.go`**: new `buildResults` forwards a
  `PipelineRun`'s string-typed `Status.Results` into `StageStatus.Results`
  (skips empty/non-string); `mapCondition` attaches them on the `Succeeded`
  branch. The runner stays generic -- it never knows the string `image-ref`.
- **`internal/stages/promotion/handler.go`**: after `BuildCommonModelParams`,
  a single guarded `AddParam` binds `modelcar-image` from
  `sc.Results[ResultImageRef]` when non-empty; otherwise `modelcar-image`
  stays omitted exactly as before (so `model-id` remains the sole source).

### Sandbox-YAML companion (immediate follow-on commit, same review cycle)

- **`model_onboarding_pipeline/model-intake-pipeline/pipeline/sandbox-pipeline.yaml`**:
  `compliance-artifact-scan` and `deploy-model` now bind `modelcar-image` to
  `$(tasks.build-modelcar.results.image-ref)` instead of `$(params.modelcar-image)`.
  For oci/s3, `build-modelcar` is skipped (its `when: in ["huggingface"]`
  guard), so the result reference resolves empty and both tasks fall back to
  their existing `model-id` → `modelcar-repo` derivation -- byte-identical to
  today's behavior for that path.

### Backward compatibility

`Results` is `omitempty`; the two new `api/v1alpha1` golden-value tests
(`TestStageProgress_NoResults_SerializesIdenticallyToPrePhaseC`,
`TestStageProgress_WithResults_SerializesCorrectly`) pin that an empty/nil
`results` serializes to no key at all, and that the full
`Status.Phase`/`Stages[]` structure is otherwise unchanged.

### Test coverage added

- `internal/stages/promotion/handler_test.go` (2): the two required tests --
  `TestBuildSpec_ImageRefResult_SetsModelcarImage` (image-ref → modelcar-image
  bound, model-id unchanged) and
  `TestBuildSpec_NoImageRefResult_ProducesIdenticalParams` (oci/s3 → no
  modelcar-image key, byte-identical to today).
- `internal/stages/tekton/stagerunner_test.go` (2): `buildResults` forwards
  string results and skips empty/nil.
- `internal/stagewalk/walk_test.go` (2): `TestWalk_CarriesUpstreamResultsIntoDownstreamContext`,
  `TestWalk_ResultsFlowForwardOnly_NotToSelfOrSiblings`.
- `internal/controller/results_controller_test.go` (2): full-path tests --
  `TestModelRequest_ImageRefResult_SetsPromotionModelcarImage_AndPersists`
  (persists to `Status.Stages[sandbox].Results` AND promotion's recorded
  `StageSpec.Params["modelcar-image"]` equals the reference) and
  `TestModelRequest_NoImageRefResult_PromotionParamsUnchanged` (oci path →
  no modelcar-image in promotion params, sandbox persists no results).
- `internal/stages/tekton/pipeline_yaml_build_test.go` (1):
  `TestPipelineYAML_SandboxConsumesImageRef_ComplianceAndDeploy` pins exactly
  two `$(tasks.build-modelcar.results.image-ref)` consumers and that the old
  `value: $(params.modelcar-image)` forwarding is gone from sandbox-pipeline.yaml.
- `api/v1alpha1/modellifecycleprofile_types_test.go` (2): wire-format
  round-trip for the new `results` field.
- **Total suite: 244 passing test cases, 0 failing** (up from 233 before this
  phase's 11 new tests).

### Manifest regeneration

`make generate manifests` (controller-gen v0.16.5): `zz_generated.deepcopy.go`
gained `DeepCopyInto`/`DeepCopy` for `StageResult` and `StageProgress`
deep-copies the new `Results` slice. The `modelrequests` CRD gained the
additive `stages[].results` array (name/value, both required); no other CRD and
no RBAC changed (the tekton runner already holds `pipelineruns get`; the
promotion handler consumes no new API). `gitops/components/operator/crd-modelrequests.yaml`
re-synced verbatim from the regenerated base (diff confirmed byte-identical
after copy). Idempotent on a second run.

### Cross-stage import check

`go list -deps` confirmed all six `internal/stages/*` packages
(`sandbox`, `promotion`, `capacityplanning`, `tekton`, `noop`, `webhook`)
import no sibling stage package; `stagewalk`/`stagecommon` still depend only
on `api/v1alpha1` + stdlib + controller-runtime/apimachinery.

### Cluster verification (partial)

Committed and pushed (`0a75f1a`); `Application/modelops-operator` and
`Application/modelops-pipelines` hard-refreshed and confirmed `Synced`/
`Healthy` at `0a75f1a`. Verified live:

1. **`results` field is `Established`** on the live `modelrequests` CRD (the
   `stages[].results` array with name/value properties is present on-cluster).
2. **Sandbox pipeline YAML wiring is live**: the synced `Pipeline`
   `model-intake-sandbox` binds `modelcar-image` to
   `$(tasks.build-modelcar.results.image-ref)` in BOTH `compliance-artifact-scan`
   and `deploy-model` (confirmed in the live object at lines ~240 and ~337).

NOT verified live (blocked, see below): the promotion `PipelineRun`'s
`modelcar-image` param and the real HuggingFace end-to-end, because deploying
the rebuilt operator image to this cluster was blocked -- there are no quay.io
push credentials this session, and the internal image registry
(`image-registry.openshift-image-registry.svc:5000`, S3-backed) rejected the
kubelet pull of the operator image with `500`/`unauthorized` (a
cluster-environment auth issue, not a code issue) despite a clean `podman`-side
push/pull via the exposed default route. The cluster was reverted to its prior
state (deployment image restored to `quay.io/...:latest`, ArgoCD auto-sync
re-enabled, registry `defaultRoute` reverted). The promotion `modelcar-image`
behavior is nonetheless covered end-to-end by
`TestModelRequest_ImageRefResult_SetsPromotionModelcarImage_AndPersists` against
a real `envtest` apiserver.

### TLS investigation (input to the Zot TLS decision, not yet applied)

Empirically confirmed on this cluster (all throwaway, cleaned up afterward):

- `service.beta.openshift.io/serving-cert-secret-name` on a scratch `Service`
  auto-generated a `kubernetes.io/tls` Secret with a cert issued by
  `openshift-service-serving-signer` (SAN `probe.default.svc`).
- The serviceaccount CA bundle present in every pod
  (`/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt` -- confirmed
  to EXIST on this cluster, contradicting Phase 9's earlier cluster-specific
  "never populated" observation) verifies that serving cert
  (`openssl verify -CAfile ... : OK`).
- The service CA is NOT in the default trust store of the tools
  (`buildah`/`skopeo`/`curl`/Python), so it must be explicitly mounted/pointed
  to -- the exact class of problem the EvalHub CA fix solved at the Task level.
  This is the evidence base for the pending Zot-TLS design decision (real TLS
  vs. insecure-registry sprawl); see the follow-up review.

### Known follow-up NOT done in this phase

- **`compliance-artifact-scan`'s `skopeo inspect` TLS flag**: the Zot internal
  Service is plain HTTP (`build-modelcar` already pushes/login with
  `--tls-verify=false`), but `compliance-artifact-scan`'s `compliance-inspect`
  step runs `skopeo inspect "docker://$CANDIDATE"` with no `--tls-verify=false`,
  and so will likely fail a TLS handshake when it first tries to inspect the
  internal Zot reference. This is predicted to surface in the cluster
  verification above; the fix (add `--tls-verify=false` to that step, scoped to
  the Zot path rather than weakening the quay HTTPS path, or add an insecure
  `registries.conf`) and the `deploy-model` container's pull-from-HTTP-Zot
  behavior are not resolved this session and are a required part of the
  verification pass. Flagged here rather than silently papered over.
- No `oci`/`s3` build path exists (unchanged from model-intake Phase B): those
  source types skip the build and keep the catalog-derivation fallback, which
  is exactly the negative-control behavior this phase pins.
