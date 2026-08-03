# RBAC permission boundaries

Written as part of Phase 7 of `REFACTOR_PLAN.md` ("Review the full RBAC
marker list... and split permissions by concern"). This documents what
each Go package's `+kubebuilder:rbac` markers grant, not the mechanics of
`controller-gen` itself.

## Important caveat: one ClusterRole, one ServiceAccount

Today the operator runs as a single `Deployment`/`ServiceAccount`
(`modelops-operator`), and `make manifests` aggregates every
`+kubebuilder:rbac` marker found anywhere under `./...` into one
`ClusterRole` (`config/rbac/role.yaml`), regardless of which Go file (or
package) declares the marker. **Attributing a marker to the package that
actually needs it is therefore primarily about correct RBAC-by-package
bookkeeping, not runtime privilege isolation** — deleting, say,
`internal/stages/tekton` today would make `make manifests` correctly stop
granting `tekton.dev/pipelineruns` and `intakeproviderconfigs` (proving the
marker attribution is real), but as long as one manager process holds one
`ServiceAccount`, every `StageRunner`'s permissions are still available to
every other line of code in that same process. Splitting into genuinely
separate least-privilege service accounts per `StageRunner` would require
separate manager processes/`Deployment`s (or client impersonation) — a
materially larger change, out of scope for this phase, and not attempted
here.

## The split, by package

### Core reconciler (`internal/controller`)

- `modelrequests` (full CRUD + `/status`): owns this CR outright.
- `modellifecycleprofiles`, `platformconfigs` (`get;list;watch`):
  `lookupProfile`/`lookupPlatformConfig` fetch these directly; no
  `StageHandler`/`StageRunner` ever fetches them itself (they're passed in
  via `stagecommon.StageContext`).
- `capacityplans` (`get;list;watch` only): `capacityPlanRunName`'s
  best-effort lookup reads the declared `CapacityPlan`-kind stage's child
  object directly, read-only. (Creation is `capacityplanning.StageRunner`'s
  job; status writes are `CapacityPlanReconciler`'s — see below.)
- `secrets` (`get;list;watch`), `events` (`create;patch`): `resolveSecrets`
  and event emission live here.
- `namespaces` (`get;list;watch;update;patch`), `serviceaccounts` (+
  `serviceaccounts/token`), `rolebindings`/`clusterrolebindings`: namespace
  provisioning (`ensurePromotionNamespaceRBAC`/`ensureNamespaceLabels`),
  invoked generically by the stage walker via `ProfileStageSpec.NamespaceSetup`
  data. **Deliberately stays on the core reconciler, not any `StageRunner`**:
  this is driven by stage *data* (`NamespaceSetup`), not by a specific
  execution engine — a future non-Tekton `StageRunner` would need the exact
  same RBAC bootstrapping in its target namespace before it could run
  anything there.

### `CapacityPlanReconciler` (`internal/controller/capacityplan_controller.go`)

A separate, pre-existing core controller (not a `StageRunner`) that owns the
actual GPU-sizing heuristic and the `CapacityPlan` object's status:

- `capacityplans` (`get;list;watch` — **not** `create`/`update`/`patch`/
  `delete`: it never creates a `CapacityPlan`, only reads and later writes
  its status).
- `capacityplans/status` (`get;update;patch`).
- (The `capacityplans/finalizers` marker was removed this phase and
  confirmed safe against the sandbox cluster: nothing in this codebase
  ever creates a child object with a `CapacityPlan` as its owner, so no
  `blockOwnerDeletion` admission check ever needs it — see the important
  caveat about `modelrequests/finalizers` below, which is the opposite
  case.)

### A real, live-cluster-only regression: `modelrequests/finalizers` is NOT dead

Despite no finalizer being literally registered on `ModelRequest`
anywhere in this codebase, the `modelrequests/finalizers` marker is
**required**, and removing it (the first draft of this phase's RBAC
tightening did) broke every `CapacityPlan`/`PipelineRun` creation on the
sandbox cluster — caught only by live verification, not `envtest` (whose
admin-equivalent client bypasses this check entirely, the same shape of
gap Phase 1's RBAC-escalation incident already demonstrated for a
different resource). Both `tekton.StageRunner.buildPipelineRun` and
`capacityplanning.StageRunner.EnsureRun` call
`controllerutil.SetControllerReference(modelRequest, child, scheme)`,
which sets `OwnerReference.BlockOwnerDeletion = true` by default. The API
server's admission control requires `update` permission on
`modelrequests/finalizers` to set `blockOwnerDeletion: true` on *any*
owner reference pointing at a `ModelRequest` — a generic Kubernetes
owner-reference safety check, unrelated to whether the `ModelRequest`
controller itself ever uses a finalizer. Restored on the core
reconciler's marker block after being caught live; see
`docs/PHASE_LOG.md`'s Phase 7 entry for the exact error and fix.

### `internal/stages/capacityplanning.StageRunner`

The Phase 6 `EnsureRun` adapter the stage walker calls on behalf of the
core reconciler (distinct from `CapacityPlanReconciler` above):

- `capacityplans` (`get;list;watch;create` — no `update`/`patch`/`delete`:
  it only Get-or-Creates, never mutates an existing one).

### `internal/stages/tekton.StageRunner`

- `tekton.dev/pipelineruns` (full CRUD): the only place in the codebase
  that Gets/Creates a `PipelineRun`. Moved here from the core reconciler
  in Phase 7 (previously declared on `ModelRequestReconciler` even though
  only this package's `EnsureRun` ever used it).
- `intakeproviderconfigs` (`get;list;watch`): `resolveProviderDetails`'s
  resolution of `StageSpec.ProviderConfigRef` (Phase 5).
- Also implements `stagecommon.OwnedTypesProvider` (Phase 7), declaring
  `tektonv1.PipelineRun` as an owned child type — this is what lets
  `ModelRequestReconciler.SetupWithManager` register a generic `.Owns()`
  watch without `internal/controller` importing `tektonv1` at all (closing
  the residual Phase 4 flagged: "a natural candidate for Phase 5/7... a
  provider-agnostic 'which child types does this StageRunner own' hook").

### `internal/stages/noop.StageRunner`

No markers. Creates no child object of any kind — the concrete proof that
a trivial second execution-engine integration needs "close to none" RBAC.

### `internal/stages/sandbox.Handler` / `internal/stages/promotion.Handler` / `internal/stages/capacityplanning.Handler`

No markers. These only build `stagecommon.StageSpec` values from an
in-memory `stagecommon.StageContext` — no Kubernetes client is threaded
into any `StageHandler`.

## Known pre-existing drift (not introduced or fixed this phase)

`gitops/components/operator/clusterrole.yaml` (the hand-maintained copy
ArgoCD actually deploys) carries two extra rule blocks — `serving.kserve.io`
(`llminferenceservices`/`llminferenceserviceconfigs`) and
`maas.opendatahub.io` (`maasauthpolicies`/`maasmodelrefs`/
`maassubscriptions`/`externalmodels`/`tenants`), both with wildcard `"*"`
verbs — with **no corresponding `+kubebuilder:rbac` marker anywhere in Go
source**, flagged first in Phase 1. `make manifests` will never regenerate
these; they're purely hand-maintained. Left untouched by this phase.
