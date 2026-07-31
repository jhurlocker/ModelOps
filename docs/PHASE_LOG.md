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
