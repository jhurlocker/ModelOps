package stagewalk

// TDD per REFACTOR_PLAN.md Phase 6: these tests are written before
// walk.go exists ("undefined: Walk" is the expected first failure), and
// they exercise the walker's sequencing/decision logic entirely against
// fake stagecommon.StageHandler/StageRunner implementations -- no real
// stage package, no Kubernetes client, no envtest. This is the "provable
// without any real stage implementation" proof the plan calls for.

import (
	"context"
	"fmt"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fakeHandler is a func-adapter implementing stagecommon.StageHandler,
// local to this test file -- the walker never needs a real per-stage
// package to be provable.
type fakeHandler struct {
	build func(sc stagecommon.StageContext) (stagecommon.StageSpec, error)
}

func (f fakeHandler) BuildSpec(sc stagecommon.StageContext) (stagecommon.StageSpec, error) {
	if f.build != nil {
		return f.build(sc)
	}
	return stagecommon.StageSpec{Name: sc.Stage.Name, RunName: sc.Stage.Name}, nil
}

// nameEchoHandler is the common case: a handler that just names the
// StageSpec after the stage (with namespace suffix, for PerNamespace
// stages), so a fakeRunner can script per (stage, namespace).
func nameEchoHandler() fakeHandler {
	return fakeHandler{build: func(sc stagecommon.StageContext) (stagecommon.StageSpec, error) {
		name := sc.Stage.Name
		if sc.Namespace != "" && sc.Stage.PerNamespace {
			name = fmt.Sprintf("%s-%s", sc.Stage.Name, sc.Namespace)
		}
		return stagecommon.StageSpec{Name: name, RunName: name}, nil
	}}
}

func boolPtr(b bool) *bool { return &b }

func testModelRequest() *modelopsv1alpha1.ModelRequest {
	return &modelopsv1alpha1.ModelRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-1", Namespace: "ns-1"},
	}
}

// namespacesFrom returns a NamespacesFunc that fans PerNamespace stages
// out to ns, and non-PerNamespace stages to a single "" (the walker
// treats an empty Namespaces func as mr.Namespace-only via the default
// baked into Walk itself).
func namespacesFrom(ns ...string) NamespacesFunc {
	return func(stage modelopsv1alpha1.ProfileStageSpec) []string {
		if !stage.PerNamespace {
			return nil // Walk defaults this to a single mr.Namespace-equivalent entry
		}
		return ns
	}
}

func basicContextFunc() ContextFunc {
	return func(stage modelopsv1alpha1.ProfileStageSpec, namespace string, index, count int) (stagecommon.StageContext, error) {
		return stagecommon.StageContext{
			Stage:          stage,
			Namespace:      namespace,
			NamespaceIndex: index,
			NamespaceCount: count,
		}, nil
	}
}

// --- 1. One-stage profile ---

func TestWalk_OneStage_Succeeds(t *testing.T) {
	runner := stagecommon.NewFakeStageRunner()
	runner.ScriptStage("only", stagecommon.StageStatus{Phase: stagecommon.StageSucceeded})

	in := Input{
		Stages:   []modelopsv1alpha1.ProfileStageSpec{{Name: "only", Kind: "fake"}},
		Handlers: map[string]stagecommon.StageHandler{"only": nameEchoHandler()},
		Runners:  map[string]stagecommon.StageRunner{"fake": runner},
		BuildContext: basicContextFunc(),
	}

	result, err := Walk(context.Background(), testModelRequest(), in)
	require.NoError(t, err)
	require.Equal(t, OutcomeSucceeded, result.Outcome)
	require.Len(t, runner.Calls, 1)
}

// --- 2. Three-stage profile, all succeed in order ---

func TestWalk_ThreeStages_AllSucceedInSequence(t *testing.T) {
	var callOrder []string
	runner := &orderRecordingRunner{onCall: func(name string) { callOrder = append(callOrder, name) }}

	in := Input{
		Stages: []modelopsv1alpha1.ProfileStageSpec{
			{Name: "stage-1", Kind: "fake"},
			{Name: "stage-2", Kind: "fake"},
			{Name: "stage-3", Kind: "fake"},
		},
		Handlers: map[string]stagecommon.StageHandler{
			"stage-1": nameEchoHandler(), "stage-2": nameEchoHandler(), "stage-3": nameEchoHandler(),
		},
		Runners:      map[string]stagecommon.StageRunner{"fake": runner},
		BuildContext: basicContextFunc(),
	}

	result, err := Walk(context.Background(), testModelRequest(), in)
	require.NoError(t, err)
	require.Equal(t, OutcomeSucceeded, result.Outcome)
	require.Equal(t, []string{"stage-1", "stage-2", "stage-3"}, callOrder, "stages must be invoked in profile order")
	require.Len(t, result.Progress, 3)
}

// orderRecordingRunner always succeeds and records call order by
// StageSpec.Name, for tests asserting sequencing rather than scripted
// per-name outcomes.
type orderRecordingRunner struct {
	onCall func(name string)
}

func (r *orderRecordingRunner) EnsureRun(_ context.Context, _ *modelopsv1alpha1.ModelRequest, stage stagecommon.StageSpec) (stagecommon.StageStatus, error) {
	if r.onCall != nil {
		r.onCall(stage.Name)
	}
	return stagecommon.StageStatus{Phase: stagecommon.StageSucceeded}, nil
}

// --- 3. Middle stage fails (required): stops before the third ---

func TestWalk_ThreeStages_MiddleStageFails_StopsBeforeThird(t *testing.T) {
	stage3Runner := stagecommon.NewFakeStageRunner()
	runner := stagecommon.NewFakeStageRunner()
	runner.ScriptStage("stage-1", stagecommon.StageStatus{Phase: stagecommon.StageSucceeded})
	runner.ScriptStage("stage-2", stagecommon.StageStatus{Phase: stagecommon.StageFailed, Message: "boom"})

	in := Input{
		Stages: []modelopsv1alpha1.ProfileStageSpec{
			{Name: "stage-1", Kind: "fake"},
			{Name: "stage-2", Kind: "fake", Required: boolPtr(true)},
			{Name: "stage-3", Kind: "fake3"},
		},
		Handlers: map[string]stagecommon.StageHandler{
			"stage-1": nameEchoHandler(), "stage-2": nameEchoHandler(), "stage-3": nameEchoHandler(),
		},
		Runners:      map[string]stagecommon.StageRunner{"fake": runner, "fake3": stage3Runner},
		BuildContext: basicContextFunc(),
	}

	result, err := Walk(context.Background(), testModelRequest(), in)
	require.NoError(t, err)
	require.Equal(t, OutcomeFailed, result.Outcome)
	require.Equal(t, "stage-2", result.CurrentStage)
	require.Contains(t, result.Message, "boom")
	require.Empty(t, stage3Runner.Calls, "stage 3 must never be attempted once a required stage fails")
}

// --- 4. Middle stage running: stops before the third ---

func TestWalk_ThreeStages_MiddleStageRunning_StopsBeforeThird(t *testing.T) {
	stage3Runner := stagecommon.NewFakeStageRunner()
	runner := stagecommon.NewFakeStageRunner()
	runner.ScriptStage("stage-1", stagecommon.StageStatus{Phase: stagecommon.StageSucceeded})
	runner.ScriptStage("stage-2", stagecommon.StageStatus{Phase: stagecommon.StageRunning})

	in := Input{
		Stages: []modelopsv1alpha1.ProfileStageSpec{
			{Name: "stage-1", Kind: "fake"},
			{Name: "stage-2", Kind: "fake"},
			{Name: "stage-3", Kind: "fake3"},
		},
		Handlers: map[string]stagecommon.StageHandler{
			"stage-1": nameEchoHandler(), "stage-2": nameEchoHandler(), "stage-3": nameEchoHandler(),
		},
		Runners:      map[string]stagecommon.StageRunner{"fake": runner, "fake3": stage3Runner},
		BuildContext: basicContextFunc(),
	}

	result, err := Walk(context.Background(), testModelRequest(), in)
	require.NoError(t, err)
	require.Equal(t, OutcomeRunning, result.Outcome)
	require.Equal(t, "stage-2", result.CurrentStage)
	require.Empty(t, stage3Runner.Calls, "stage 3 must never be attempted while stage 2 is still running")
}

// --- 5. Optional stage fails (required:false): walker advances anyway ---

func TestWalk_OptionalStageFails_RequiredFalse_AdvancesAnyway(t *testing.T) {
	runner := stagecommon.NewFakeStageRunner()
	runner.ScriptStage("optional", stagecommon.StageStatus{Phase: stagecommon.StageFailed, Message: "not applicable here"})
	runner.ScriptStage("required", stagecommon.StageStatus{Phase: stagecommon.StageSucceeded})

	in := Input{
		Stages: []modelopsv1alpha1.ProfileStageSpec{
			{Name: "optional", Kind: "fake", Required: boolPtr(false)},
			{Name: "required", Kind: "fake"},
		},
		Handlers: map[string]stagecommon.StageHandler{
			"optional": nameEchoHandler(), "required": nameEchoHandler(),
		},
		Runners:      map[string]stagecommon.StageRunner{"fake": runner},
		BuildContext: basicContextFunc(),
	}

	result, err := Walk(context.Background(), testModelRequest(), in)
	require.NoError(t, err)
	require.Equal(t, OutcomeSucceeded, result.Outcome, "an optional stage's failure must not block the walk")
	require.Len(t, runner.Calls, 2, "the required stage after an optional failure must still be attempted")

	require.Len(t, result.Progress, 2)
	require.Equal(t, "optional", result.Progress[0].Name)
	require.Equal(t, stagecommon.StageFailed, result.Progress[0].Phase, "the optional stage's failure is recorded, not hidden")
	require.Contains(t, result.Progress[0].Message, "not applicable here")
}

// --- 6. PerNamespace stage fans out; Running does not short-circuit ---

func TestWalk_PerNamespaceStage_FansOutToAll_RunningDoesNotShortCircuit(t *testing.T) {
	runner := stagecommon.NewFakeStageRunner()
	runner.ScriptStage("promo-staging", stagecommon.StageStatus{Phase: stagecommon.StageSucceeded})
	runner.ScriptStage("promo-preprod", stagecommon.StageStatus{Phase: stagecommon.StageRunning})

	in := Input{
		Stages: []modelopsv1alpha1.ProfileStageSpec{
			{Name: "promo", Kind: "fake", PerNamespace: true},
		},
		Handlers:     map[string]stagecommon.StageHandler{"promo": nameEchoHandler()},
		Runners:      map[string]stagecommon.StageRunner{"fake": runner},
		Namespaces:   namespacesFrom("staging", "preprod"),
		BuildContext: basicContextFunc(),
	}

	result, err := Walk(context.Background(), testModelRequest(), in)
	require.NoError(t, err)
	require.Equal(t, OutcomeRunning, result.Outcome)
	require.Len(t, runner.Calls, 2, "both namespaces must be attempted in the same Walk call, per the Phase 0 pinned quirk")
}

// --- 7. PerNamespace stage: a required failure short-circuits remaining namespaces ---

func TestWalk_PerNamespaceStage_FailureShortCircuitsRemainingNamespaces(t *testing.T) {
	runner := stagecommon.NewFakeStageRunner()
	runner.ScriptStage("promo-staging", stagecommon.StageStatus{Phase: stagecommon.StageFailed, Message: "boom-staging"})

	in := Input{
		Stages: []modelopsv1alpha1.ProfileStageSpec{
			{Name: "promo", Kind: "fake", PerNamespace: true},
		},
		Handlers:     map[string]stagecommon.StageHandler{"promo": nameEchoHandler()},
		Runners:      map[string]stagecommon.StageRunner{"fake": runner},
		Namespaces:   namespacesFrom("staging", "preprod"),
		BuildContext: basicContextFunc(),
	}

	result, err := Walk(context.Background(), testModelRequest(), in)
	require.NoError(t, err)
	require.Equal(t, OutcomeFailed, result.Outcome)
	require.Contains(t, result.Message, "boom-staging")
	require.Len(t, runner.Calls, 1, "preprod must never be attempted once staging fails (required)")
}

// --- 8. Unregistered Kind/Name: clear config errors, not panics ---

func TestWalk_UnknownKind_ReturnsConfigError(t *testing.T) {
	in := Input{
		Stages:       []modelopsv1alpha1.ProfileStageSpec{{Name: "only", Kind: "does-not-exist"}},
		Handlers:     map[string]stagecommon.StageHandler{"only": nameEchoHandler()},
		Runners:      map[string]stagecommon.StageRunner{},
		BuildContext: basicContextFunc(),
	}

	_, err := Walk(context.Background(), testModelRequest(), in)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does-not-exist")
}

func TestWalk_UnknownHandlerName_ReturnsConfigError(t *testing.T) {
	in := Input{
		Stages:       []modelopsv1alpha1.ProfileStageSpec{{Name: "no-handler", Kind: "fake"}},
		Handlers:     map[string]stagecommon.StageHandler{},
		Runners:      map[string]stagecommon.StageRunner{"fake": stagecommon.NewFakeStageRunner()},
		BuildContext: basicContextFunc(),
	}

	_, err := Walk(context.Background(), testModelRequest(), in)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no-handler")
}

// --- 9. Handler.BuildSpec error stops the walk; runner never called ---

func TestWalk_HandlerBuildSpecError_StopsWalk_RunnerNeverCalled(t *testing.T) {
	runner := stagecommon.NewFakeStageRunner()
	failingHandler := fakeHandler{build: func(sc stagecommon.StageContext) (stagecommon.StageSpec, error) {
		return stagecommon.StageSpec{}, fmt.Errorf("cannot build spec")
	}}

	in := Input{
		Stages:       []modelopsv1alpha1.ProfileStageSpec{{Name: "broken", Kind: "fake"}},
		Handlers:     map[string]stagecommon.StageHandler{"broken": failingHandler},
		Runners:      map[string]stagecommon.StageRunner{"fake": runner},
		BuildContext: basicContextFunc(),
	}

	_, err := Walk(context.Background(), testModelRequest(), in)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot build spec")
	require.Empty(t, runner.Calls, "the runner must never be invoked if BuildSpec fails")
}

// --- 10. BuildContext error stops the walk; handler/runner never called ---

func TestWalk_BuildContextError_StopsWalk_HandlerAndRunnerNeverCalled(t *testing.T) {
	runner := stagecommon.NewFakeStageRunner()
	handlerCalled := false
	handler := fakeHandler{build: func(sc stagecommon.StageContext) (stagecommon.StageSpec, error) {
		handlerCalled = true
		return stagecommon.StageSpec{}, nil
	}}

	in := Input{
		Stages:   []modelopsv1alpha1.ProfileStageSpec{{Name: "only", Kind: "fake"}},
		Handlers: map[string]stagecommon.StageHandler{"only": handler},
		Runners:  map[string]stagecommon.StageRunner{"fake": runner},
		BuildContext: func(stage modelopsv1alpha1.ProfileStageSpec, namespace string, index, count int) (stagecommon.StageContext, error) {
			return stagecommon.StageContext{}, fmt.Errorf("secrets unavailable")
		},
	}

	_, err := Walk(context.Background(), testModelRequest(), in)
	require.Error(t, err)
	require.Contains(t, err.Error(), "secrets unavailable")
	require.False(t, handlerCalled, "BuildSpec must never be invoked if BuildContext fails")
	require.Empty(t, runner.Calls)
}
