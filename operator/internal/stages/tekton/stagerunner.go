package tekton

import (
	"context"
	"fmt"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// StageRunner is the Tekton-backed stagecommon.StageRunner
// implementation. It owns every piece of PipelineRun-specific behavior
// that used to live inline in
// internal/controller/modelrequest_controller.go: workspace bindings,
// the shared PVC/ConfigMap names, the map[string]string ->
// tektonv1.Params conversion, and condition-reading. This is a verbatim
// relocation -- see docs/REFACTOR_PLAN.md Phase 4 / docs/PHASE_LOG.md
// for the characterization tests that prove behavior is unchanged.
type StageRunner struct {
	Client client.Client
	Scheme *runtime.Scheme
}

var _ stagecommon.StageRunner = (*StageRunner)(nil)

// EnsureRun looks up the PipelineRun named stage.RunName in req.Namespace,
// creating it (idempotently) if absent, and reports its current status.
//
// Matches the pre-Phase-4 inline behavior exactly: a freshly created
// PipelineRun is reported as StageRunning without inspecting any
// condition (there isn't one yet); an existing PipelineRun's "Succeeded"
// condition is read and mapped per mapCondition below.
func (r *StageRunner) EnsureRun(ctx context.Context, req *modelopsv1alpha1.ModelRequest, stage stagecommon.StageSpec) (stagecommon.StageStatus, error) {
	var run tektonv1.PipelineRun
	key := types.NamespacedName{Name: stage.RunName, Namespace: req.Namespace}
	err := r.Client.Get(ctx, key, &run)

	if apierrors.IsNotFound(err) {
		newRun := buildPipelineRun(stage.RunName, req.Namespace, stage.WorkflowRef, toTektonParams(stage.Params), req, r.Scheme)
		if createErr := r.Client.Create(ctx, &newRun); createErr != nil && !apierrors.IsAlreadyExists(createErr) {
			return stagecommon.StageStatus{}, createErr
		}
		return stagecommon.StageStatus{
			Phase:   stagecommon.StageRunning,
			Reason:  "Started",
			Message: fmt.Sprintf("%s pipeline started", stage.Name),
			RunRef:  stage.RunName,
		}, nil
	}
	if err != nil {
		return stagecommon.StageStatus{}, err
	}

	return mapCondition(stage, &run), nil
}

// mapCondition maps a PipelineRun's "Succeeded" condition into a
// stagecommon.StageStatus:
//
//   - no condition, or Status == Unknown -> StageRunning
//   - Status == False                    -> StageFailed
//   - Status == True                     -> StageSucceeded
//
// Reason/Message pass through verbatim from the Tekton condition, same
// as the pre-Phase-4 inline logic did.
func mapCondition(stage stagecommon.StageSpec, run *tektonv1.PipelineRun) stagecommon.StageStatus {
	cond := run.Status.GetCondition("Succeeded")
	if cond == nil || cond.Status == corev1.ConditionUnknown {
		reason, message := "Running", fmt.Sprintf("%s pipeline is running", stage.Name)
		if cond != nil {
			reason, message = cond.Reason, cond.Message
		}
		return stagecommon.StageStatus{Phase: stagecommon.StageRunning, Reason: reason, Message: message, RunRef: stage.RunName}
	}
	if cond.Status == corev1.ConditionFalse {
		return stagecommon.StageStatus{Phase: stagecommon.StageFailed, Reason: cond.Reason, Message: cond.Message, RunRef: stage.RunName}
	}
	return stagecommon.StageStatus{Phase: stagecommon.StageSucceeded, Reason: cond.Reason, Message: cond.Message, RunRef: stage.RunName}
}

// toTektonParams converts a provider-agnostic param map into
// tektonv1.Params, applying the same empty-value guard
// stagecommon.AddParam uses. Building from a map means no name can ever
// appear twice in the result (a stronger fix for the Phase 1
// duplicate-param bug class than the previous slice-based
// detection -- see stagecommon.AddParam's doc comment).
func toTektonParams(params map[string]string) tektonv1.Params {
	p := tektonv1.Params{}
	for name, value := range params {
		if value == "" {
			continue
		}
		p = append(p, tektonv1.Param{
			Name: name,
			Value: tektonv1.ParamValue{
				Type:      tektonv1.ParamTypeString,
				StringVal: value,
			},
		})
	}
	return p
}

// buildPipelineRun constructs the PipelineRun object, verbatim-relocated
// from internal/controller/modelrequest_controller.go's buildPipelineRun
// (Phase 0-3): identical workspace bindings (shared-workspace PVC,
// manifests/custom-mmlu ConfigMaps), ServiceAccountName, and unbounded
// pipeline timeout.
func buildPipelineRun(name, namespace, pipelineName string, params tektonv1.Params, modelReq *modelopsv1alpha1.ModelRequest, scheme *runtime.Scheme) tektonv1.PipelineRun {
	pr := tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"modelops.example.io/model-request": modelReq.Name,
			},
		},
		Spec: tektonv1.PipelineRunSpec{
			PipelineRef: &tektonv1.PipelineRef{
				Name: pipelineName,
			},
			Params: params,
			TaskRunTemplate: tektonv1.PipelineTaskRunTemplate{
				ServiceAccountName: "pipeline",
			},
			Timeouts: &tektonv1.TimeoutFields{
				Pipeline: &metav1.Duration{Duration: 0},
			},
			Workspaces: []tektonv1.WorkspaceBinding{
				{
					Name: "shared-workspace",
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: "guidellm-output-pvc",
					},
				},
				{
					Name: "manifests",
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "mmlu-manifest",
						},
					},
				},
				{
					Name: "custom-mmlu",
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "custom-mmlu",
						},
					},
				},
			},
		},
	}
	controllerutil.SetControllerReference(modelReq, &pr, scheme)
	return pr
}
