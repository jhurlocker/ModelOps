// Package stagecommon holds logic shared by more than one lifecycle stage
// package under internal/stages/.
//
// Package boundary rule (see REFACTOR_PLAN.md, "Modularity" guiding
// principle): no package under internal/stages/* may import another
// package under internal/stages/*. If two or more stages need the same
// helper (param building, secret resolution, PipelineRun construction,
// the future StageRunner/StageStatus contract), that helper belongs here
// instead, and stage packages depend downward on stagecommon rather than
// sideways on each other.
//
// This package must never import anything under internal/stages/*.
//
// Phase 3 moved the shared parameter-building helper here
// (BuildCommonModelParams in params.go): the param-building logic that
// was byte-for-byte identical between buildSandboxPipelineParams and
// buildPromotionPipelineParams in internal/controller. It intentionally
// imports only api/v1alpha1 (a shared, non-stage package) and the Tekton
// API types -- never internal/controller or any internal/stages/*
// package, so any future stage package can depend on it without
// creating a sideways dependency. Phase 4 is expected to add the
// StageRunner/StageStatus contract here as well.
package stagecommon
