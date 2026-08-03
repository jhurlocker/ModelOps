// Package noop holds StageRunner, a trivial, logging-only
// stagecommon.StageRunner implementation that performs no execution
// engine work at all and immediately reports every stage as succeeded.
//
// This exists purely to prove the seam introduced in Phase 4
// (stagecommon.StageRunner) and extended in Phase 5
// (stagecommon.StageSpec.ProviderConfigRef/StageKind) is real: that
// internal/controller.ModelRequestReconciler drives a ModelRequest
// through the exact same phase transitions regardless of which
// concrete StageRunner is injected. It is deliberately not a second
// real execution-engine integration (no SageMaker/Databricks Jobs
// support is implied or planned here) -- see
// docs/REFACTOR_PLAN.md Phase 5 and docs/PHASE_LOG.md for the
// tekton-vs-noop parity test this package exists to support.
//
// Package boundary rule (see REFACTOR_PLAN.md, "Modularity" guiding
// principle): this package must never import internal/stages/sandbox,
// internal/stages/promotion, internal/stages/capacityplanning,
// internal/stages/tekton, or internal/controller. It depends only on
// internal/stagecommon (for the StageRunner/StageSpec/StageStatus
// contract) and api/v1alpha1.
package noop
