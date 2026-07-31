// Package capacityplanning will hold the capacity-planning lifecycle
// stage: sizing GPU count/model from a CapacityPlan's requested context
// length, concurrency, and isolation policy.
//
// Today this logic lives in
// internal/controller/capacityplan_controller.go (CapacityPlanReconciler).
// It is not Tekton-driven and does not depend on the StageRunner
// abstraction introduced in later phases; it is already close to
// independently testable and is expected to be the lowest-risk stage to
// relocate here.
//
// Package boundary rule (see REFACTOR_PLAN.md, "Modularity" guiding
// principle): this package must never import internal/stages/sandbox or
// internal/stages/promotion. Shared helpers belong in
// internal/stagecommon.
package capacityplanning
