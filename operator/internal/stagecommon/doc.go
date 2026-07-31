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
// As of Phase 0 this package is an empty placeholder. Phase 3 is expected
// to move the shared parameter-building helpers here, and Phase 4 the
// StageRunner/StageStatus contract.
package stagecommon
