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
