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
//
// As of Phase 5, it also owns resolving
// stagecommon.StageSpec.ProviderConfigRef into an IntakeProviderConfig
// CR (see providerconfig.go's resolveProviderDetails) -- the core
// reconciler never fetches or interprets that CR itself.

// +kubebuilder:rbac:groups=modelops.example.io,resources=intakeproviderconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=tekton.dev,resources=pipelineruns,verbs=get;list;watch;create;update;patch;delete

type StageRunner struct {
	Client client.Client
	Scheme *runtime.Scheme
}

var _ stagecommon.StageRunner = (*StageRunner)(nil)
var _ stagecommon.OwnedTypesProvider = (*StageRunner)(nil)

// OwnedTypes declares that this StageRunner creates tektonv1.PipelineRun
// child objects, so ModelRequestReconciler.SetupWithManager can .Owns()
// them generically -- see stagecommon.OwnedTypesProvider's doc comment
// and docs/REFACTOR_PLAN.md/docs/PHASE_LOG.md Phase 7. This is what
// lets internal/controller drop its last remaining tektonv1 import
// (previously only needed for this exact .Owns() registration).
func (r *StageRunner) OwnedTypes() []client.Object {
	return []client.Object{&tektonv1.PipelineRun{}}
}

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
		details, resolveErr := resolveProviderDetails(ctx, r.Client, req.Namespace, stage)
		if resolveErr != nil {
			// Wrapped in stagecommon.ProviderConfigError so
			// ModelRequestReconciler can recognize this specific
			// failure class via errors.As and surface a dedicated
			// "ProviderConfigLookupFailed" status reason instead of
			// the generic silent-retry error path every other
			// EnsureRun error falls into. See docs/REFACTOR_PLAN.md
			// Phase 7.
			return stagecommon.StageStatus{}, &stagecommon.ProviderConfigError{Err: resolveErr}
		}
		newRun := buildPipelineRun(stage.RunName, req.Namespace, details, toTektonParams(stage.Params), req, r.Scheme)
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

// buildPipelineRun constructs the PipelineRun object. Prior to Phase 5
// this hardcoded the workspace bindings, ServiceAccountName, and
// pipeline timeout directly (verbatim-relocated from
// internal/controller/modelrequest_controller.go in Phase 0-3); those
// values now come from providerDetails, resolved by
// resolveProviderDetails (providerconfig.go) -- either from an
// IntakeProviderConfig CR, or, when stage.ProviderConfigRef is nil,
// defaultProviderDetails reproducing the exact same hardcoded shape as
// before. This function's own body is otherwise unchanged.
func buildPipelineRun(name, namespace string, details providerDetails, params tektonv1.Params, modelReq *modelopsv1alpha1.ModelRequest, scheme *runtime.Scheme) tektonv1.PipelineRun {
	timeout := details.pipelineTimeout
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
				Name: details.pipelineName,
			},
			Params: params,
			TaskRunTemplate: tektonv1.PipelineTaskRunTemplate{
				ServiceAccountName: details.serviceAccountName,
			},
			Timeouts: &tektonv1.TimeoutFields{
				Pipeline: &timeout,
			},
			Workspaces: details.workspaces,
		},
	}
	controllerutil.SetControllerReference(modelReq, &pr, scheme)
	return pr
}
