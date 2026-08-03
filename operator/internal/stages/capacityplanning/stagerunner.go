package capacityplanning

import (
	"context"
	"fmt"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// StageRunner is the stagecommon.StageRunner adapter for the
// CapacityPlan stage kind. It does not compute GPU sizing itself --
// that stays owned by CapacityPlanReconciler (a separate, pre-existing
// controller reconciling CapacityPlan objects directly, unchanged by
// this phase). EnsureRun only Get-or-Creates the child CapacityPlan and
// maps its Status.Phase into stagecommon.StageStatus, mirroring what
// internal/stages/tekton.StageRunner does for PipelineRun.
type StageRunner struct {
	Client client.Client
	Scheme *runtime.Scheme
}

var _ stagecommon.StageRunner = (*StageRunner)(nil)

// EnsureRun looks up the CapacityPlan named stage.RunName in
// req.Namespace, creating it (idempotently, from stage.NativeSpec) if
// absent, and reports its current status.
func (r *StageRunner) EnsureRun(ctx context.Context, req *modelopsv1alpha1.ModelRequest, stage stagecommon.StageSpec) (stagecommon.StageStatus, error) {
	var plan modelopsv1alpha1.CapacityPlan
	key := types.NamespacedName{Name: stage.RunName, Namespace: req.Namespace}
	err := r.Client.Get(ctx, key, &plan)

	if apierrors.IsNotFound(err) {
		spec, ok := stage.NativeSpec.(*modelopsv1alpha1.CapacityPlanSpec)
		if !ok || spec == nil {
			return stagecommon.StageStatus{}, fmt.Errorf(
				"stage %q: capacityplanning.StageRunner requires a *modelopsv1alpha1.CapacityPlanSpec NativeSpec, got %T", stage.Name, stage.NativeSpec)
		}

		newPlan := modelopsv1alpha1.CapacityPlan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      stage.RunName,
				Namespace: req.Namespace,
				Labels: map[string]string{
					"modelops.example.io/model-request": req.Name,
				},
			},
			Spec: *spec,
		}
		if setErr := controllerutil.SetControllerReference(req, &newPlan, r.Scheme); setErr != nil {
			return stagecommon.StageStatus{}, setErr
		}
		if createErr := r.Client.Create(ctx, &newPlan); createErr != nil && !apierrors.IsAlreadyExists(createErr) {
			return stagecommon.StageStatus{}, createErr
		}
		return stagecommon.StageStatus{
			Phase:   stagecommon.StageRunning,
			Reason:  "Started",
			Message: "Capacity plan created, waiting for GPU advisor",
			RunRef:  stage.RunName,
		}, nil
	}
	if err != nil {
		return stagecommon.StageStatus{}, err
	}

	return mapStatus(stage, &plan), nil
}

// mapStatus maps a CapacityPlan's Status.Phase into a
// stagecommon.StageStatus:
//
//   - "" (or anything other than Succeeded/Failed) -> StageRunning
//   - "Failed"                                     -> StageFailed
//   - "Succeeded"                                  -> StageSucceeded
//
// CapacityPlanReconciler never sets Phase="Failed" today (see
// docs/REFACTOR_PLAN.md Phase 7 backlog note: "give CapacityPlan a real
// Failed path so Required:true is meaningful for that stage") -- this
// mapping is ready for when it does, without needing a second change to
// this package.
func mapStatus(stage stagecommon.StageSpec, plan *modelopsv1alpha1.CapacityPlan) stagecommon.StageStatus {
	switch plan.Status.Phase {
	case "Succeeded":
		return stagecommon.StageStatus{Phase: stagecommon.StageSucceeded, Reason: "Succeeded", Message: plan.Status.Message, RunRef: stage.RunName}
	case "Failed":
		return stagecommon.StageStatus{Phase: stagecommon.StageFailed, Reason: "Failed", Message: plan.Status.Message, RunRef: stage.RunName}
	default:
		return stagecommon.StageStatus{
			Phase:   stagecommon.StageRunning,
			Reason:  "Running",
			Message: fmt.Sprintf("Waiting for capacity plan: %s", plan.Status.Phase),
			RunRef:  stage.RunName,
		}
	}
}
