// Package sandbox holds the sandbox-validation lifecycle stage's
// stagecommon.StageHandler (Phase 6, REFACTOR_PLAN.md): building the
// params/RunName/WorkflowRef for the Tekton PipelineRun that runs
// compliance/artifact scanning, GPU capacity application, model
// deployment, security scanning, and teardown in the sandbox namespace,
// before a ModelRequest is eligible for promotion.
//
// Relocated, field-for-field, from
// internal/controller/modelrequest_controller.go's pre-Phase-6
// buildSandboxPipelineParams/sandboxPipelineNameOrDefault. Execution
// (creating/tracking the actual PipelineRun) is a separate concern,
// still owned by internal/stages/tekton.StageRunner -- this package
// only builds *what* to run, not *how*.
//
// Package boundary rule (see REFACTOR_PLAN.md, "Modularity" guiding
// principle): this package must never import internal/stages/promotion or
// internal/stages/capacityplanning. Shared helpers belong in
// internal/stagecommon.
package sandbox
