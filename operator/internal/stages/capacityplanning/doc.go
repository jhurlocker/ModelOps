// Package capacityplanning holds the capacity-planning lifecycle stage's
// StageHandler/StageRunner (Phase 6, REFACTOR_PLAN.md).
//
// The actual GPU-sizing heuristic (context length/concurrency ->
// GPU count/model) still lives in, and stays owned by,
// internal/controller/capacityplan_controller.go's CapacityPlanReconciler
// -- a separate, already-existing controller reconciling CapacityPlan
// objects directly. This package does NOT duplicate or replace that
// logic. It only adds the thin adapter the Phase 6 generic stage walker
// needs to dispatch to this stage uniformly with every other kind:
//   - Handler.BuildSpec builds a *modelopsv1alpha1.CapacityPlanSpec
//     (relocated from ModelRequestReconciler.buildCapacityPlan) and
//     attaches it via stagecommon.StageSpec.NativeSpec, since
//     CapacityPlanSpec has real typed fields, not Tekton-param-shaped
//     ones (see stagecommon.StageSpec.NativeSpec's doc comment).
//   - StageRunner.EnsureRun Get-or-Creates the CapacityPlan child object
//     and maps its Status.Phase into stagecommon.StageStatus, mirroring
//     what internal/stages/tekton.StageRunner already does for
//     PipelineRun. CapacityPlanReconciler's own watch/reconcile loop,
//     and ModelRequestReconciler's pre-existing
//     .Owns(&CapacityPlan{}) watch, are unchanged by this.
//
// Revises this package's original doc comment (Phase 0): capacity
// planning DOES now implement the StageRunner abstraction, specifically
// so the walker never needs a stage-kind-specific branch for it -- see
// docs/REFACTOR_PLAN.md Phase 6's design review.
//
// IMPORTANT: the GPU-sizing logic in CapacityPlanReconciler is a static
// heuristic (table-driven ContextLength/Concurrency -> GPU count/model
// mapping with a configurable MaxGPUsPerRequest ceiling), not a real
// GPU-inventory-aware or advisor-backed placement decision. Status
// messages produced by successful capacity plans are prefixed with
// "[static estimate]" to make this explicit to anyone reading them. A
// real gpu-advisor container image already exists and is used by the
// sandbox Tekton pipeline's own gpu-advisor Task
// (model_onboarding_pipeline/tools/gpu-advisor,
// quay.io/jhurlocker/gpu-advisor) -- wiring it into
// CapacityPlanReconciler is a tracked backlog item, not this pass. See
// docs/REFACTOR_PLAN.md Phase 7 backlog note.
//
// Package boundary rule (see REFACTOR_PLAN.md, "Modularity" guiding
// principle): this package must never import internal/stages/sandbox or
// internal/stages/promotion. Shared helpers belong in
// internal/stagecommon.
package capacityplanning
