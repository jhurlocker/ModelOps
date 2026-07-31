// Package sandbox will hold the sandbox-validation lifecycle stage: the
// Tekton PipelineRun that runs compliance/artifact scanning, GPU capacity
// application, model deployment, security scanning, and teardown in the
// sandbox namespace, before a ModelRequest is eligible for promotion.
//
// Today this logic lives inline in
// internal/controller/modelrequest_controller.go (sandboxRunName,
// buildSandboxPipelineParams, sandboxPipelineNameOrDefault, and the
// Status.SandboxPipelineRunName field). Later phases relocate that logic
// here without changing its behavior (Phase 0's characterization tests in
// internal/controller are the regression net for that move).
//
// Package boundary rule (see REFACTOR_PLAN.md, "Modularity" guiding
// principle): this package must never import internal/stages/promotion or
// internal/stages/capacityplanning. Shared helpers belong in
// internal/stagecommon.
package sandbox
