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

// StageSpec is everything a StageRunner needs to ensure one lifecycle
// stage's execution exists and is up to date, without needing to know
// anything about ModelRequest.Spec, ModelLifecycleProfile,
// PlatformConfig, or CapacityPlan directly. The reconciler builds one of
// these per stage invocation (sandbox, and once per promotion
// namespace).
type StageSpec struct {
	// Name identifies the stage for logging/status purposes only (e.g.
	// "sandbox", "promotion-staging"). It is never used as the child
	// object's name.
	Name string
	// RunName is the deterministic name of the underlying execution
	// object (a Tekton PipelineRun today). Chosen by the reconciler so
	// idempotent re-reconciles always look up the same object.
	RunName string
	// WorkflowRef identifies which workflow/pipeline to run -- the
	// Tekton pipeline name today (mr.Spec.PipelineRef /
	// profile.Spec.Workflow.PipelineRef / a fixed default).
	// REFACTOR_PLAN.md Phase 5 is expected to replace this with a
	// provider-config reference; kept as a plain string for now to
	// avoid getting ahead of that phase.
	WorkflowRef string
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
