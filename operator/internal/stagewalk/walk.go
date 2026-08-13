package stagewalk

import (
	"context"
	"fmt"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"
)

// Outcome is the walker's own summary of one Walk call -- distinct from
// stagecommon.StagePhase (a single stage/namespace invocation's
// outcome): Walk aggregates however many stages/namespaces it attempted
// into exactly one of these three values.
type Outcome string

const (
	OutcomeRunning   Outcome = "Running"
	OutcomeSucceeded Outcome = "Succeeded"
	OutcomeFailed    Outcome = "Failed"
)

// NamespacesFunc resolves the set of target namespaces a stage fans out
// to. Returning nil/empty (or the func being nil) means "just the
// ModelRequest's own namespace" -- Walk supplies that single default
// itself, so callers only need to implement this for PerNamespace
// stages.
type NamespacesFunc func(stage modelopsv1alpha1.ProfileStageSpec) []string

// NamespaceSetupFunc prepares a target namespace (RBAC, labels) before a
// stage runs against it. A nil func means no preparation is ever
// performed. Returning an error stops the walk.
type NamespaceSetupFunc func(ctx context.Context, namespace string, stage modelopsv1alpha1.ProfileStageSpec) error

// ContextFunc builds the stagecommon.StageContext for one (stage,
// namespace) invocation. Returning an error stops the walk -- this is
// how caller-side, cross-stage plumbing that can fail (e.g. lazily
// resolving Secrets, only once a prior stage has actually succeeded)
// surfaces without the walker itself needing to know anything about
// what that plumbing is.
type ContextFunc func(stage modelopsv1alpha1.ProfileStageSpec, namespace string, index, count int) (stagecommon.StageContext, error)

// Input is everything Walk needs to drive one ModelRequest through a
// declared stage sequence. Handlers/Runners/Namespaces/SetupNamespace/
// BuildContext are all supplied by the caller (internal/controller in
// production; a test constructs them directly with in-memory fakes) --
// Walk itself performs no I/O and imports nothing beyond api/v1alpha1
// and stagecommon.
type Input struct {
	Stages         []modelopsv1alpha1.ProfileStageSpec
	Handlers       map[string]stagecommon.StageHandler // keyed by ProfileStageSpec.Name
	Runners        map[string]stagecommon.StageRunner  // keyed by ProfileStageSpec.Kind
	Namespaces     NamespacesFunc
	SetupNamespace NamespaceSetupFunc
	BuildContext   ContextFunc
}

// Progress is one (stage, namespace) outcome recorded during a Walk
// call, in the order attempted.
type Progress struct {
	Name         string
	Namespace    string
	Phase        stagecommon.StagePhase
	RunRef       string
	Message      string
	DetailsURL   string
	CheckResults []stagecommon.CheckResult
}

// Result summarizes one Walk call.
type Result struct {
	Outcome      Outcome
	CurrentStage string
	Message      string
	Progress     []Progress
}

// Walk iterates in.Stages in order, dispatching each to the
// StageHandler/StageRunner registered (by Name/Kind respectively) for
// it, and decides advance/stop/tolerate purely from the returned
// stagecommon.StageStatus.Phase and the stage's own Required flag --
// see docs/REFACTOR_PLAN.md Phase 6's decision table:
//
//   - StageSucceeded: record, advance (next namespace, then next stage).
//   - StageRunning: stop this Walk call, report Outcome=Running; does
//     NOT stop remaining namespaces of the SAME stage already attempted
//     before this one in the same fan-out loop (matches the Phase 0
//     pinned "not gated sequentially" quirk).
//   - StageFailed, Required (default true): stop immediately, even if
//     other namespaces of the same stage haven't been attempted yet.
//   - StageFailed, Required=false: record the failure and advance
//     anyway -- this is the entire "optional/skippable" mechanism; no
//     4th StagePhase value is introduced for it.
//
// Walk never branches on a stage's Name or Kind itself -- Handlers/
// Runners lookups are the only place a string key selects behavior, and
// that's registry dispatch, not a switch statement.
func Walk(ctx context.Context, mr *modelopsv1alpha1.ModelRequest, in Input) (Result, error) {
	var progress []Progress

	for _, stage := range in.Stages {
		handler, ok := in.Handlers[stage.Name]
		if !ok {
			return Result{}, fmt.Errorf("stage %q: no StageHandler registered for stage name %q", stage.Name, stage.Name)
		}
		runner, ok := in.Runners[stage.Kind]
		if !ok {
			return Result{}, fmt.Errorf("stage %q: no StageRunner registered for kind %q", stage.Name, stage.Kind)
		}

		var namespaces []string
		if in.Namespaces != nil {
			namespaces = in.Namespaces(stage)
		}
		if len(namespaces) == 0 {
			namespaces = []string{""}
		}

		anyRunning := false
		runningMessage := ""
		for i, ns := range namespaces {
			if in.SetupNamespace != nil {
				if err := in.SetupNamespace(ctx, ns, stage); err != nil {
					return Result{}, fmt.Errorf("stage %q: preparing namespace %q: %w", stage.Name, ns, err)
				}
			}

			sc, err := in.BuildContext(stage, ns, i, len(namespaces))
			if err != nil {
				return Result{}, fmt.Errorf("stage %q: building stage context: %w", stage.Name, err)
			}
			spec, err := handler.BuildSpec(sc)
			if err != nil {
				return Result{}, fmt.Errorf("stage %q: building stage spec: %w", stage.Name, err)
			}

			status, err := runner.EnsureRun(ctx, mr, spec)
			if err != nil {
				return Result{}, fmt.Errorf("stage %q: %w", stage.Name, err)
			}

			progress = append(progress, Progress{
				Name:         stage.Name,
				Namespace:    ns,
				Phase:        status.Phase,
				RunRef:       status.RunRef,
				Message:      status.Message,
				DetailsURL:   status.DetailsURL,
				CheckResults: status.CheckResults,
			})

			switch status.Phase {
			case stagecommon.StageFailed:
				if stagecommon.IsRequired(stage) {
					return Result{
						Outcome:      OutcomeFailed,
						CurrentStage: stage.Name,
						Message:      status.Message,
						Progress:     progress,
					}, nil
				}
				// Optional stage failed: recorded above, keep looping
				// (this namespace's outcome is tolerated, not fatal).
			case stagecommon.StageRunning:
				anyRunning = true
				runningMessage = status.Message
				// Deliberately does not stop attempting the remaining
				// namespaces of THIS stage in the same Walk call --
				// see the doc comment above.
			case stagecommon.StageSucceeded:
				// Nothing to do; loop continues.
			}
		}

		if anyRunning {
			if runningMessage == "" {
				runningMessage = fmt.Sprintf("%s stage running", stage.Name)
			}
			return Result{
				Outcome:      OutcomeRunning,
				CurrentStage: stage.Name,
				Message:      runningMessage,
				Progress:     progress,
			}, nil
		}
	}

	return Result{Outcome: OutcomeSucceeded, Progress: progress}, nil
}
