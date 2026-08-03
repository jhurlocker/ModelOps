package controller

// Phase 7 of REFACTOR_PLAN.md: ModelRequestReconciler.SetupWithManager's
// generalized .Owns() wiring (see modelrequest_controller.go). Written
// first (TDD): sortedStageRunnerKeys doesn't exist yet at this point.
//
// SetupWithManager itself is not exercised end-to-end here (consistent
// with this package's existing convention -- no prior phase tested
// SetupWithManager against a real ctrl.Manager either, only Reconcile
// directly via envtest). sortedStageRunnerKeys is the one genuinely new,
// pure piece of logic this phase adds to the wiring, so it gets a
// direct unit test; stagecommon.OwnedTypesProvider itself is exercised
// by internal/stages/tekton's/capacityplanning's/noop's own tests (see
// docs/PHASE_LOG.md Phase 7).

import (
	"testing"

	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	"github.com/stretchr/testify/require"
)

func TestSortedStageRunnerKeys_ReturnsKeysInDeterministicSortedOrder(t *testing.T) {
	m := map[string]stagecommon.StageRunner{
		"PipelineRun":  nil,
		"CapacityPlan": nil,
		"Zeta":         nil,
	}

	require.Equal(t, []string{"CapacityPlan", "PipelineRun", "Zeta"}, sortedStageRunnerKeys(m))
}

func TestSortedStageRunnerKeys_EmptyMap_ReturnsEmptySlice(t *testing.T) {
	require.Empty(t, sortedStageRunnerKeys(map[string]stagecommon.StageRunner{}))
}
