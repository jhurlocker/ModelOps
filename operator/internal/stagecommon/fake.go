package stagecommon

import (
	"context"
	"sync"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
)

// FakeStageRunner is an in-memory StageRunner for tests. It records
// every StageSpec it's called with and returns pre-scripted StageStatus
// values (or errors) per stage name, without ever touching a real
// execution engine. This is what lets ModelRequestReconciler-level
// tests prove the reconciler drives a full phase transition without any
// Tekton (or other provider) involvement at all -- see
// docs/REFACTOR_PLAN.md Phase 4.
//
// This type is production code (not a _test.go file) only because Go
// cannot import another package's _test.go files across a package
// boundary, and internal/controller's tests need to construct one. It
// must never be used, or referenced, from any non-test code path.
var _ StageRunner = (*FakeStageRunner)(nil)

type FakeStageRunner struct {
	mu sync.Mutex

	// pending holds, per stage name, a queue of not-yet-served
	// StageStatus values, consumed (popped) one per EnsureRun call, in
	// the order ScriptStage appended them.
	pending map[string][]StageStatus
	// last holds the most recently served StageStatus per stage name.
	// Once pending is drained, EnsureRun keeps returning this (a copy,
	// not consumed) so a test doesn't need to re-script every reconcile
	// call while a stage stays in the same state. A later ScriptStage
	// call for the same stage always takes priority over this repeat.
	last map[string]StageStatus
	errs map[string]error

	// Calls records every StageSpec EnsureRun was invoked with, in
	// order, so tests can assert on what the reconciler built (e.g. the
	// WorkflowRef/Params it passed for a given stage).
	Calls []StageSpec
}

// NewFakeStageRunner returns an empty FakeStageRunner. Use ScriptStage/
// ScriptStageError to configure responses before reconciling; with
// nothing scripted, EnsureRun defaults to StageStatus{Phase:
// StageRunning}.
func NewFakeStageRunner() *FakeStageRunner {
	return &FakeStageRunner{
		pending: map[string][]StageStatus{},
		last:    map[string]StageStatus{},
		errs:    map[string]error{},
	}
}

// ScriptStage queues one or more StageStatus values to be returned, in
// order, for the given stage name. Queued values always take priority
// over whatever was previously served for that stage, even if the
// previous value had started "repeating" (see EnsureRun).
func (f *FakeStageRunner) ScriptStage(stageName string, statuses ...StageStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending[stageName] = append(f.pending[stageName], statuses...)
}

// ScriptStageError makes the next EnsureRun call for stageName return
// err instead of a StageStatus. Consumed exactly once.
func (f *FakeStageRunner) ScriptStageError(stageName string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[stageName] = err
}

func (f *FakeStageRunner) EnsureRun(_ context.Context, _ *modelopsv1alpha1.ModelRequest, stage StageSpec) (StageStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.Calls = append(f.Calls, stage)

	if err, ok := f.errs[stage.Name]; ok {
		delete(f.errs, stage.Name)
		return StageStatus{}, err
	}

	var next StageStatus
	if queue := f.pending[stage.Name]; len(queue) > 0 {
		next = queue[0]
		f.pending[stage.Name] = queue[1:]
		f.last[stage.Name] = next
	} else if prev, ok := f.last[stage.Name]; ok {
		next = prev
	} else {
		next = StageStatus{Phase: StageRunning}
	}

	if next.RunRef == "" {
		next.RunRef = stage.RunName
	}
	return next, nil
}
