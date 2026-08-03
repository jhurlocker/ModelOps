// Package promotion holds the promotion lifecycle stage's
// stagecommon.StageHandler (Phase 6, REFACTOR_PLAN.md): building the
// params/RunName/WorkflowRef for the per-namespace, approval-gated
// Tekton PipelineRun sequence that promotes a validated model from
// sandbox into one or more downstream namespaces (staging,
// preproduction, production), including benchmarking, access grants,
// and registry registration.
//
// Relocated, field-for-field, from
// internal/controller/modelrequest_controller.go's pre-Phase-6
// buildPromotionPipelineParams/promotionPipelineNameOrDefault/
// getPromotionNamespaces. isFirst/isLast (approval gate, run-register)
// are now derived from stagecommon.StageContext.NamespaceIndex/Count,
// which the generic stage walker supplies for any PerNamespace-marked
// stage -- this package itself decides what "first"/"last" means, not
// the walker.
//
// ensurePromotionNamespaceRBAC/ensureMaaSNamespaceLabels stay in
// internal/controller: as of Phase 6 they're invoked generically by the
// walker for ANY stage whose declared ProfileStageSpec.NamespaceSetup
// requests them (driven by data, not by checking a stage's name), so
// they're shared walker-glue code, not promotion-specific logic. The
// Phase 0 pinned quirk (promotion namespaces not gated on each other's
// success) is preserved exactly by internal/stagewalk's per-namespace
// loop shape, not by anything in this package.
//
// Package boundary rule (see REFACTOR_PLAN.md, "Modularity" guiding
// principle): this package must never import internal/stages/sandbox or
// internal/stages/capacityplanning. Shared helpers belong in
// internal/stagecommon.
package promotion
