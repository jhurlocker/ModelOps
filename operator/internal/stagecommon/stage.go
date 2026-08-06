package stagecommon

import (
	"context"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// StagePhase is the generic outcome of a single lifecycle stage run,
// independent of whatever execution engine (Tekton today; SageMaker,
// Databricks, etc. later, per REFACTOR_PLAN.md Phase 5) actually
// performed the work.
type StagePhase string

const (
	// StageRunning covers both "just created, no result yet" and
	// "still executing" -- ModelRequestReconciler has never
	// distinguished between these; both today drive the same
	// "<Stage>Running" ModelRequest.Status.Phase.
	StageRunning StagePhase = "Running"
	// StageSucceeded means the stage's run completed successfully and
	// the reconciler may advance to the next stage.
	StageSucceeded StagePhase = "Succeeded"
	// StageFailed means the stage's run completed unsuccessfully; the
	// reconciler stops advancing and surfaces the failure.
	StageFailed StagePhase = "Failed"
)

// CheckResult is optional per-check granular governance evidence a
// StageRunner may populate alongside the stage's aggregate Phase. Only
// meaningful when a stage's ProfileStageSpec.CheckTypes has more than
// one entry (the combined case); for a single-checkType stage, the
// aggregate Phase already captures the check's outcome.
type CheckResult struct {
	Type    string
	Passed  bool
	Reason  string
	Message string
}

// StageStatus is the generic status contract a StageRunner reports back
// to the reconciler for a single stage run. Modeled after Kubernetes'
// own metav1.Condition pattern (Reason/Message), reduced to exactly the
// three outcomes ModelRequestReconciler has ever branched on -- see
// docs/REFACTOR_PLAN.md Phase 4 for the Tekton-condition mapping
// examples this was derived from.
type StageStatus struct {
	Phase   StagePhase
	Reason  string
	Message string
	// RunRef echoes back the StageSpec.RunName the runner acted on.
	// Informational only: the reconciler already knows the RunName it
	// chose before calling EnsureRun and must not depend on this field
	// for correctness.
	RunRef string
	// DetailsURL is an optional human-facing link out to the provider's
	// own console, logs, or job page -- a different kind of thing than
	// Reason (a fixed, short status token across all StageRunners). Set
	// only by StageRunners whose execution surface is genuinely external
	// (currently: webhook.StageRunner, via its statusMapping's
	// detailsUrlTemplate); every other runner (tekton, noop,
	// capacityplanning) leaves it empty.
	DetailsURL string
	// CheckResults is optional per-check evidence, distinct from and in
	// addition to the stage's single aggregate Phase. Populated only
	// when the stage's ProfileStageSpec.CheckTypes has multiple entries
	// and the provider can produce structured per-check output.
	// +optional
	CheckResults []CheckResult
}

// StageKind classifies which named "slot" of a shared provider config a
// stage invocation corresponds to -- e.g. which of
// IntakeProviderConfigSpec's SandboxPipelineName/PromotionPipelineName
// fields a StageRunner should resolve against. This is a genuine, if
// small, widening of the StageSpec contract introduced in
// REFACTOR_PLAN.md Phase 5: StageSpec.Name stays "logging purposes
// only" (see below), so a separate, explicit field is used for anything
// a StageRunner actually branches on. StageKind itself is not
// Tekton-specific -- "this is the sandbox stage" vs "this is a
// promotion stage" is a generic lifecycle concept every provider needs
// to distinguish, not an execution-engine detail.
type StageKind string

const (
	StageKindSandbox   StageKind = "sandbox"
	StageKindPromotion StageKind = "promotion"
)

// StageSpec is everything a StageRunner needs to ensure one lifecycle
// stage's execution exists and is up to date, without needing to know
// anything about ModelRequest.Spec, ModelLifecycleProfile,
// PlatformConfig, or CapacityPlan directly. The reconciler builds one of
// these per stage invocation (sandbox, and once per promotion
// namespace).
type StageSpec struct {
	// Name identifies the stage for logging/status purposes only (e.g.
	// "sandbox", "promotion-staging"). It is never used as the child
	// object's name, and a StageRunner must not branch on it -- use
	// StageKind for that.
	Name string
	// RunName is the deterministic name of the underlying execution
	// object (a Tekton PipelineRun today). Chosen by the reconciler so
	// idempotent re-reconciles always look up the same object.
	RunName string
	// WorkflowRef identifies which workflow/pipeline to run -- the
	// Tekton pipeline name today (mr.Spec.PipelineRef /
	// profile.Spec.Workflow.PipelineRef / a fixed default). This is the
	// DEPRECATED fallback path (REFACTOR_PLAN.md Phase 5): a
	// StageRunner should prefer resolving ProviderConfigRef when set,
	// and fall back to this plain string only when it's nil.
	WorkflowRef string
	// ProviderConfigRef is a passthrough of
	// ModelLifecycleProfileSpec.ProviderConfigRef, set by the
	// reconciler without any interpretation of what it points at. Only
	// a StageRunner (e.g. internal/stages/tekton) resolves it -- the
	// reconciler never fetches or inspects the referenced object. Nil
	// means the profile hasn't opted into provider-config-based
	// resolution; a StageRunner must fall back to WorkflowRef in that
	// case.
	ProviderConfigRef *modelopsv1alpha1.ProviderConfigRef
	// StageKind says which named slot of a (potentially shared)
	// provider config this invocation corresponds to. See StageKind's
	// doc comment.
	StageKind StageKind
	// Params is a provider-agnostic bag of string parameters. Building
	// this map is the reconciler's (or, from a later phase, a
	// per-stage package's) job; converting it into whatever native
	// parameter type a specific engine needs (e.g. tektonv1.Param) is
	// solely the StageRunner implementation's job.
	Params map[string]string
	// NativeSpec is an escape hatch (Phase 6) for stage kinds whose
	// StageRunner needs a typed Go value instead of the common-case
	// string param bag -- today, exactly one: CapacityPlan, whose
	// CapacityPlanSpec has real typed fields (ContextLength int,
	// AllowMIG bool, ...), not Tekton-param-shaped ones. The Handler
	// for that kind sets NativeSpec to a *modelopsv1alpha1.CapacityPlanSpec;
	// its StageRunner type-asserts it back. Every other stage kind
	// leaves this nil and uses Params -- this field must not become
	// the common case, and the walker never inspects it (dispatch to
	// Handler/Runner stays uniform regardless of which payload shape a
	// given kind uses).
	NativeSpec any
}

// StageRunner ensures a single lifecycle stage's execution object
// exists (creating it if absent) and reports its current status. This
// is the seam that lets ModelRequestReconciler drive stage transitions
// without importing any execution-engine-specific package (tektonv1
// today; a future SageMaker/Databricks equivalent later).
type StageRunner interface {
	EnsureRun(ctx context.Context, req *modelopsv1alpha1.ModelRequest, stage StageSpec) (StageStatus, error)
}

// OwnedTypesProvider is an optional capability a StageRunner may
// implement to declare which child object types its EnsureRun
// implementation creates, so ModelRequestReconciler.SetupWithManager
// can register a generic .Owns() watch for each one without importing
// an execution-engine-specific package itself (e.g. tektonv1) purely
// for manager-wiring purposes.
//
// Not every StageRunner needs this: noop.StageRunner creates nothing
// and implements nothing here (needs "close to none" RBAC/wiring, per
// docs/REFACTOR_PLAN.md Phase 7). capacityplanning.StageRunner's owned
// type (CapacityPlan) is also NOT declared through this interface --
// CapacityPlan is a core lifecycle CRD (api/v1alpha1), not
// provider-specific, so ModelRequestReconciler.SetupWithManager already
// owns that .Owns() call explicitly and unconditionally. This interface
// exists specifically for the residual tektonv1 import Phase 4 flagged
// ("a natural candidate for Phase 5/7... a provider-agnostic 'which
// child types does this StageRunner own' hook") -- tekton.StageRunner
// is, today, this interface's only implementation.
type OwnedTypesProvider interface {
	OwnedTypes() []client.Object
}

// StageContext is everything a StageHandler needs to build the
// StageSpec for one invocation of a named stage (once per namespace,
// for a PerNamespace stage). Introduced in Phase 6 (REFACTOR_PLAN.md)
// as the seam that replaces ModelRequestReconciler calling
// buildSandboxPipelineParams/buildPromotionPipelineParams by name.
type StageContext struct {
	ModelRequest   *modelopsv1alpha1.ModelRequest
	Profile        *modelopsv1alpha1.ModelLifecycleProfile
	PlatformConfig *modelopsv1alpha1.PlatformConfig
	// CapacityPlan is the most recent CapacityPlan for this
	// ModelRequest, if one exists yet (nil otherwise). Best-effort,
	// read-only input -- a stage handler must tolerate it being nil.
	CapacityPlan *modelopsv1alpha1.CapacityPlan
	// Secrets holds resolved credentials/endpoints (Phase 3), reused
	// as-is here.
	Secrets Secrets
	// Stage is the raw declared ProfileStageSpec entry this invocation
	// is for.
	Stage modelopsv1alpha1.ProfileStageSpec
	// Namespace is the target namespace for this invocation:
	// ModelRequest.Namespace unless Stage.PerNamespace, in which case
	// it's one of the ModelRequest's own selected promotion namespaces.
	Namespace string
	// NamespaceIndex/NamespaceCount let a PerNamespace stage's handler
	// compute isFirst/isLast-style behavior (e.g. promotion's
	// approval-gate/run-register params) without the walker itself
	// knowing anything about what "first"/"last" means for a given
	// stage.
	NamespaceIndex int
	NamespaceCount int
}

// StageHandler builds *what* to run for one invocation of a named
// stage (params, WorkflowRef, RunName, and, for kinds that need it,
// NativeSpec) from a StageContext. StageRunner (Phase 4) builds/tracks
// *how* it runs. This is the seam the generic stage walker (Phase 6)
// calls instead of the reconciler calling
// buildSandboxPipelineParams/buildPromotionPipelineParams by name.
type StageHandler interface {
	BuildSpec(sc StageContext) (StageSpec, error)
}

// IsRequired reports whether stage.Required is unset (defaults to true)
// or explicitly true. The generic stage walker uses this, and only
// this, to decide whether a StageFailed outcome stops the whole walk
// (true) or is recorded and tolerated (false) -- see
// docs/REFACTOR_PLAN.md Phase 6.
func IsRequired(stage modelopsv1alpha1.ProfileStageSpec) bool {
	return stage.Required == nil || *stage.Required
}
