// Package promotion will hold the promotion lifecycle stage: the
// per-namespace, approval-gated Tekton PipelineRun sequence that promotes
// a validated model from sandbox into one or more downstream namespaces
// (staging, preproduction, production), including benchmarking, access
// grants, and registry registration.
//
// Today this logic lives inline in
// internal/controller/modelrequest_controller.go
// (buildPromotionPipelineParams, promotionPipelineNameOrDefault,
// getPromotionNamespaces, ensurePromotionNamespaceRBAC, and the
// Status.PromotionPipelineRunName field). Later phases relocate that
// logic here without changing its behavior (Phase 0's characterization
// tests in internal/controller are the regression net for that move,
// including known-current-behavior quirks slated for a Phase 1 fix, such
// as promotion namespaces not being strictly gated on the previous
// namespace's success).
//
// Package boundary rule (see REFACTOR_PLAN.md, "Modularity" guiding
// principle): this package must never import internal/stages/sandbox or
// internal/stages/capacityplanning. Shared helpers belong in
// internal/stagecommon.
package promotion
