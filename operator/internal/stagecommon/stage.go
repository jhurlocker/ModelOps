package stagecommon

import (
	"context"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
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
}

// StageRunner ensures a single lifecycle stage's execution object
// exists (creating it if absent) and reports its current status. This
// is the seam that lets ModelRequestReconciler drive stage transitions
// without importing any execution-engine-specific package (tektonv1
// today; a future SageMaker/Databricks equivalent later).
type StageRunner interface {
	EnsureRun(ctx context.Context, req *modelopsv1alpha1.ModelRequest, stage StageSpec) (StageStatus, error)
}
