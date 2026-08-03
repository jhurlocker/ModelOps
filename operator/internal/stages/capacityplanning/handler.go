package capacityplanning

import (
	"fmt"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"
)

// Handler builds the CapacityPlanSpec (via StageSpec.NativeSpec, see
// stagecommon.StageSpec's doc comment) for the capacity-planning stage.
// Relocated, field-for-field, from
// ModelRequestReconciler.buildCapacityPlan (Phase 0-5).
type Handler struct{}

var _ stagecommon.StageHandler = Handler{}

func (Handler) BuildSpec(sc stagecommon.StageContext) (stagecommon.StageSpec, error) {
	mr := sc.ModelRequest
	cfg := sc.PlatformConfig
	reqs := mr.Spec.Requirements
	if reqs == nil {
		reqs = &modelopsv1alpha1.ModelRequirements{}
	}

	native := &modelopsv1alpha1.CapacityPlanSpec{
		ModelRef: modelopsv1alpha1.CapacityPlanModelRef{
			ModelRequestName: mr.Name,
		},
		ContextLength:         stagecommon.IntOrDefault(reqs.BenchmarkTargets.ContextLength, 32768),
		Concurrency:           stagecommon.IntOrDefault(reqs.BenchmarkTargets.ExpectedConcurrency, 4),
		AllowTimeSlicing:      stagecommon.BoolOrDefault(reqs.GPUConfig.AllowTimeSlicing, true),
		AllowMIG:              stagecommon.BoolOrDefault(reqs.GPUConfig.AllowMIG, false),
		IsolationPolicy:       stagecommon.StrOrDefault(reqs.GPUConfig.GPUIsolationPolicy, "dedicated"),
		AdvisorEndpoint:       reqs.AdvisorEndpoint,
		AdvisorSecretName:     cfg.Spec.AdvisorSecretName,
		AdvisorTimeoutSeconds: cfg.Spec.AdvisorTimeoutSeconds,
		GPUOperatorNamespace:  cfg.Spec.GPUOperatorNamespace,
		ClusterPolicyName:     cfg.Spec.ClusterPolicyName,
		TimeSlicingConfigMap:  cfg.Spec.TimeSlicingConfigMap,
		MaxTimeSlices:         stagecommon.IntOrDefault(cfg.Spec.MaxTimeSlices, 8),
		// MaxGPUsPerRequest deliberately has NO default applied here
		// (unlike MaxTimeSlices above): 0 means "no configured
		// ceiling," which must stay 0, not become a default cap. See
		// docs/REFACTOR_PLAN.md Phase 7.
		MaxGPUsPerRequest: cfg.Spec.MaxGPUsPerRequest,
	}

	return stagecommon.StageSpec{
		Name:       sc.Stage.Name,
		RunName:    fmt.Sprintf("%s-%s", mr.Name, sc.Stage.Name),
		NativeSpec: native,
	}, nil
}
