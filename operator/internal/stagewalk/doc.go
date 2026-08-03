// Package stagewalk implements the generic stage walker introduced in
// Phase 6 of REFACTOR_PLAN.md: it iterates a ModelLifecycleProfile's
// declared Stages (or a synthesized default list) and drives each one
// to completion via the stagecommon.StageHandler/StageRunner contracts,
// deciding advance/stop/tolerate purely from stagecommon.StagePhase and
// ProfileStageSpec.Required/PerNamespace -- never by branching on a
// stage's Name or Kind.
//
// Walk is a pure function of its Input: it performs no I/O itself
// (namespace preparation and StageContext construction are supplied as
// closures), so its own correctness is provable with in-memory fake
// StageHandlers/StageRunners and no Kubernetes client at all. See
// walk_test.go, written before walk.go existed, per the TDD guiding
// principle in REFACTOR_PLAN.md.
//
// Package boundary: this package must never import internal/controller
// or any internal/stages/* package. It depends only on
// internal/stagecommon and api/v1alpha1.
package stagewalk
