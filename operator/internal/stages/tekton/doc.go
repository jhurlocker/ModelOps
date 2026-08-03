// Package tekton holds StageRunner (internal/stagecommon.StageRunner),
// the Tekton-backed implementation of the generic stage-execution
// contract introduced in REFACTOR_PLAN.md Phase 4.
//
// Everything Tekton-specific that used to live inline in
// internal/controller/modelrequest_controller.go lives here instead:
// PipelineRun construction (workspace bindings, PVC/ConfigMap names,
// ServiceAccountName, timeouts), the map[string]string ->
// tektonv1.Params conversion, and condition-reading (mapping a
// PipelineRun's "Succeeded" condition into a stagecommon.StageStatus).
// This is a verbatim relocation, not a reimplementation -- see
// docs/PHASE_LOG.md's Phase 4 entry for the characterization tests that
// prove behavior is unchanged.
//
// Package boundary rule (see REFACTOR_PLAN.md, "Modularity" guiding
// principle): this package must never import internal/stages/sandbox,
// internal/stages/promotion, internal/stages/capacityplanning, or
// internal/controller. It depends only on internal/stagecommon (for the
// StageRunner/StageSpec/StageStatus contract) and api/v1alpha1.
package tekton
