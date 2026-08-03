package noop

import (
	"context"
	"fmt"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// StageRunner is a trivial, no-op stagecommon.StageRunner: it creates no
// child object of any kind, performs no execution-engine work, and
// unconditionally reports every stage as immediately succeeded. See
// doc.go for why this package exists.
type StageRunner struct{}

var _ stagecommon.StageRunner = (*StageRunner)(nil)

// EnsureRun logs the stage it was asked to run and returns
// StageSucceeded immediately -- no PipelineRun, Job, or any other child
// object is ever created.
func (StageRunner) EnsureRun(ctx context.Context, _ *modelopsv1alpha1.ModelRequest, stage stagecommon.StageSpec) (stagecommon.StageStatus, error) {
	log.FromContext(ctx).Info("noop stage runner: acknowledging stage, no execution performed",
		"stage", stage.Name, "runName", stage.RunName, "workflowRef", stage.WorkflowRef)

	return stagecommon.StageStatus{
		Phase:   stagecommon.StageSucceeded,
		Reason:  "NoopCompleted",
		Message: fmt.Sprintf("noop stage runner acknowledged %q; no execution performed", stage.Name),
		RunRef:  stage.RunName,
	}, nil
}
