package promotion

import (
	"fmt"
	"strconv"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"
)

// Handler builds the params/RunName/WorkflowRef for the promotion
// stage, once per target namespace. Relocated, field-for-field, from
// ModelRequestReconciler.buildPromotionPipelineParams/
// promotionPipelineNameOrDefault (Phase 0-5). isFirst/isLast (approval
// gate, run-register) are derived from
// StageContext.NamespaceIndex/NamespaceCount rather than being passed
// in as explicit bools -- the generic stage walker supplies those for
// any PerNamespace stage; this package decides what they mean.
type Handler struct{}

var _ stagecommon.StageHandler = Handler{}

// floatOrDefault mirrors internal/controller's pre-Phase-6 private
// helper, used only by guidellm-rate formatting -- never part of the
// sandbox/promotion-shared param set, so it stays private here rather
// than moving to stagecommon.
func floatOrDefault(val, def float64) float64 {
	if val == 0.0 {
		return def
	}
	return val
}

func (Handler) BuildSpec(sc stagecommon.StageContext) (stagecommon.StageSpec, error) {
	mr := sc.ModelRequest
	cfg := sc.PlatformConfig
	reqs := mr.Spec.Requirements
	if reqs == nil {
		reqs = &modelopsv1alpha1.ModelRequirements{}
	}

	p := stagecommon.BuildCommonModelParams(mr.Spec, reqs, cfg, sc.Secrets)

	// modelcar-image is where the promotion stage learns the specific
	// ModelCar OCI image built during the sandbox stage (the
	// build-modelcar Task's "image-ref" result, forwarded generically by
	// the walker via StageContext.Results). When the sandbox produced
	// one, prefer it; when it didn't (oci/s3 source -> build-modelcar
	// skipped -> no image-ref result), leave modelcar-image unset
	// exactly as BuildCommonModelParams did, so model-id remains the
	// sole source and the pre-existing derivation is unchanged. See
	// stagecommon.ResultImageRef and docs/PHASE_LOG.md Phase C.
	for _, r := range sc.Results {
		if r.Name == stagecommon.ResultImageRef && r.Value != "" {
			stagecommon.AddParam(p, "modelcar-image", r.Value)
			break
		}
	}

	stagecommon.AddParam(p, "target-namespace", sc.Namespace)
	stagecommon.AddParam(p, "plan-id", fmt.Sprintf("%s-promotion", mr.Name))

	isFirst := sc.NamespaceIndex == 0
	isLast := sc.NamespaceCount == 0 || sc.NamespaceIndex == sc.NamespaceCount-1

	// KNOWN BEHAVIOR, unchanged: unlike sandbox.Handler, this never
	// checks reqs.GPUConfig.GPUCountOverride -- only the
	// CapacityPlan-derived value is ever used here. See
	// stagecommon/params.go's doc comment for why gpu-count-override is
	// deliberately kept out of the shared helper instead of being
	// unified with sandbox's override-aware logic.
	if sc.CapacityPlan != nil && sc.CapacityPlan.Status.GPUsNeeded > 0 {
		stagecommon.AddParam(p, "gpu-count-override", strconv.Itoa(sc.CapacityPlan.Status.GPUsNeeded))
	}

	approvalURL := stagecommon.StrOrDefault(cfg.Spec.ApprovalApiUrl, "")
	if !isFirst {
		approvalURL = ""
	}
	stagecommon.AddParam(p, "approval-api-url", approvalURL)
	stagecommon.AddParam(p, "approval-poll-interval-seconds", strconv.Itoa(stagecommon.IntOrDefault(cfg.Spec.ApprovalPollIntervalSeconds, 15)))
	stagecommon.AddParam(p, "approval-timeout-seconds", strconv.Itoa(stagecommon.IntOrDefault(cfg.Spec.ApprovalTimeoutSeconds, 3600)))

	stagecommon.AddParam(p, "guidellm-profile", stagecommon.StrOrDefault(cfg.Spec.BenchmarkProfile, "constant"))
	stagecommon.AddParam(p, "guidellm-rate", fmt.Sprintf("%.1f", floatOrDefault(cfg.Spec.BenchmarkRate, 4.0)))
	stagecommon.AddParam(p, "guidellm-max-seconds", strconv.Itoa(stagecommon.IntOrDefault(cfg.Spec.BenchmarkMaxSeconds, 15)))
	stagecommon.AddParam(p, "guidellm-max-requests", strconv.Itoa(stagecommon.IntOrDefault(cfg.Spec.BenchmarkMaxRequests, 2)))
	if cfg.Spec.BenchmarkTargetUrl != "" {
		stagecommon.AddParam(p, "benchmark-target-url", cfg.Spec.BenchmarkTargetUrl)
	} else if mr.Spec.MaaS != nil && mr.Spec.MaaS.Enabled {
		stagecommon.AddParam(p, "benchmark-target-url", fmt.Sprintf("https://%s-kserve-workload-svc.%s.svc.cluster.local:8000/v1", stagecommon.StrOrDefault(mr.Spec.Model.Name, "unknown"), sc.Namespace))
	} else {
		stagecommon.AddParam(p, "benchmark-target-url", fmt.Sprintf("http://%s-predictor.%s.svc.cluster.local:8080/v1", stagecommon.StrOrDefault(mr.Spec.Model.Name, "unknown"), sc.Namespace))
	}
	stagecommon.AddParam(p, "custom-data", strconv.FormatBool(reqs.SecurityConfig.CustomBenchmarkData))
	stagecommon.AddParam(p, "custom-filename", stagecommon.StrOrDefault(reqs.SecurityConfig.CustomBenchmarkFile, "no-file"))

	if mr.Spec.Access != nil {
		stagecommon.AddParam(p, "authorized-viewers", mr.Spec.Access.AuthorizedViewers)
		stagecommon.AddParam(p, "access-role", stagecommon.StrOrDefault(mr.Spec.Access.AccessRole, "view"))
	}

	maasGPU := stagecommon.StrOrDefault(cfg.Spec.MaaSGPUCount, "1")
	if mr.Spec.MaaS != nil {
		stagecommon.AddParam(p, "deploy-maas", strconv.FormatBool(mr.Spec.MaaS.Enabled))
		maasGPU = stagecommon.StrOrDefault(mr.Spec.MaaS.GPUCount, maasGPU)
	} else {
		stagecommon.AddParam(p, "deploy-maas", "false")
	}
	stagecommon.AddParam(p, "maas-serving-ns", stagecommon.StrOrDefault(cfg.Spec.MaaSServingNS, sc.Namespace))
	stagecommon.AddParam(p, "maas-policy-ns", stagecommon.StrOrDefault(cfg.Spec.MaaSPolicyNS, sc.Namespace))
	stagecommon.AddParam(p, "maas-gpu-count", maasGPU)
	stagecommon.AddParam(p, "maas-runtime-image", stagecommon.StrOrDefault(cfg.Spec.MaaSRuntimeImage, "registry.redhat.io/rhaiis/vllm-cuda-rhel9:3.3.0"))
	stagecommon.AddParam(p, "maas-authorized-group", stagecommon.StrOrDefault(cfg.Spec.MaaSAuthorizedGroup, "system:authenticated"))

	stagecommon.AddParam(p, "run-register", strconv.FormatBool(isLast))

	return stagecommon.StageSpec{
		Name:              fmt.Sprintf("%s-%s", sc.Stage.Name, sc.Namespace),
		RunName:           fmt.Sprintf("%s-%s-%s", mr.Name, sc.Stage.Name, sc.Namespace),
		WorkflowRef:       PipelineNameOrDefault(sc.Profile),
		ProviderConfigRef: sc.Stage.ProviderConfigRef,
		StageKind:         stagecommon.StageKindPromotion,
		Params:            p,
	}, nil
}

// PipelineNameOrDefault resolves the promotion stage's DEPRECATED
// WorkflowRef fallback pipeline name (honored only when
// stage.ProviderConfigRef -- resolved entirely inside
// internal/stages/tekton -- is nil). Relocated from
// ModelRequestReconciler.promotionPipelineNameOrDefault.
func PipelineNameOrDefault(profile *modelopsv1alpha1.ModelLifecycleProfile) string {
	if profile != nil && profile.Spec.Workflow.PromotionPipelineRef != "" {
		return profile.Spec.Workflow.PromotionPipelineRef
	}
	return "model-intake-promotion"
}

// GetNamespaces resolves the ModelRequest's own promotion-namespace fan-out
// list: spec.requirements.promotionNamespaces, then .stagingNamespace,
// then a fixed "staging" default. Relocated from
// ModelRequestReconciler.getPromotionNamespaces. This is what the
// generic stage walker's Namespaces callback calls for any stage marked
// PerNamespace -- driven by that bool, not by checking a stage's name.
func GetNamespaces(mr *modelopsv1alpha1.ModelRequest) []string {
	reqs := mr.Spec.Requirements
	if reqs == nil {
		return []string{"staging"}
	}
	if len(reqs.PromotionNamespaces) > 0 {
		return reqs.PromotionNamespaces
	}
	if reqs.StagingNamespace != "" {
		return []string{reqs.StagingNamespace}
	}
	return []string{"staging"}
}
