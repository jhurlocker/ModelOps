package sandbox

import (
	"fmt"
	"strconv"
	"strings"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"
)

// Handler builds the params/RunName/WorkflowRef for the sandbox stage.
// Relocated, field-for-field, from
// ModelRequestReconciler.buildSandboxPipelineParams/
// sandboxPipelineNameOrDefault (Phase 0-5).
type Handler struct{}

var _ stagecommon.StageHandler = Handler{}

func (Handler) BuildSpec(sc stagecommon.StageContext) (stagecommon.StageSpec, error) {
	mr := sc.ModelRequest
	cfg := sc.PlatformConfig
	reqs := mr.Spec.Requirements
	if reqs == nil {
		reqs = &modelopsv1alpha1.ModelRequirements{}
	}

	p := stagecommon.BuildCommonModelParams(mr.Spec, reqs, cfg, sc.Secrets)

	stagecommon.AddParam(p, "target-namespace", stagecommon.StrOrDefault(reqs.SandboxNamespace, "sandbox"))

	stagecommon.AddParam(p, "artifact-scan-image", stagecommon.StrOrDefault(cfg.Spec.ComplianceScanImage, "registry.access.redhat.com/ubi9/python-311:latest"))
	stagecommon.AddParam(p, "artifact-cve-threshold", stagecommon.StrOrDefault(reqs.SecurityConfig.CVEThreshold, "critical"))
	stagecommon.AddParam(p, "ignore-unfixed", stagecommon.StrOrDefault(cfg.Spec.ComplianceIgnoreUnfixed, "true"))
	stagecommon.AddParam(p, "allowed-architectures", strings.Join(cfg.Spec.ComplianceAllowedArch, ","))

	// Exactly one gpu-count-override param: an explicit
	// reqs.GPUConfig.GPUCountOverride always wins over the
	// CapacityPlan-derived value; the plan-derived value is only used as
	// a fallback when no override was set. This stays here (not in
	// stagecommon.BuildCommonModelParams) because promotion.Handler
	// computes this param differently -- see stagecommon/params.go's doc
	// comment for why folding it into the shared helper isn't safe.
	if reqs.GPUConfig.GPUCountOverride != "" {
		stagecommon.AddParam(p, "gpu-count-override", reqs.GPUConfig.GPUCountOverride)
	} else if sc.CapacityPlan != nil && sc.CapacityPlan.Status.GPUsNeeded > 0 {
		stagecommon.AddParam(p, "gpu-count-override", strconv.Itoa(sc.CapacityPlan.Status.GPUsNeeded))
	}

	stagecommon.AddParam(p, "severity-threshold", stagecommon.StrOrDefault(reqs.SecurityConfig.SecurityThreshold, "block"))
	stagecommon.AddParam(p, "tenant-ns", stagecommon.StrOrDefault(reqs.SandboxNamespace, "vllm"))

	stagecommon.AddParam(p, "scan-s3-endpoint", sc.Secrets.ScanS3Endpoint)
	stagecommon.AddParam(p, "scan-s3-access-key-id", sc.Secrets.ScanS3AccessKey)
	stagecommon.AddParam(p, "scan-s3-secret-access-key", sc.Secrets.ScanS3SecretKey)
	compBucket := stagecommon.StrOrDefault(mr.Spec.ResultS3Bucket, stagecommon.StrOrDefault(cfg.Spec.ComplianceS3Bucket, "compliance-artifact-results"))
	secBucket := stagecommon.StrOrDefault(mr.Spec.ResultS3Bucket, stagecommon.StrOrDefault(cfg.Spec.SecurityS3Bucket, "security-scan-results"))
	stagecommon.AddParam(p, "compliance-s3-bucket", compBucket)
	stagecommon.AddParam(p, "security-s3-bucket", secBucket)
	stagecommon.AddParam(p, "s3-ui-route", "")

	return stagecommon.StageSpec{
		Name:              sc.Stage.Name,
		RunName:           fmt.Sprintf("%s-%s", mr.Name, sc.Stage.Name),
		WorkflowRef:       PipelineNameOrDefault(sc.Profile, mr),
		ProviderConfigRef: sc.Stage.ProviderConfigRef,
		StageKind:         stagecommon.StageKindSandbox,
		Params:            p,
	}, nil
}

// PipelineNameOrDefault resolves the sandbox stage's DEPRECATED
// WorkflowRef fallback pipeline name (honored only when
// stage.ProviderConfigRef -- resolved entirely inside
// internal/stages/tekton -- is nil): mr.Spec.PipelineRef, then
// profile.Spec.Workflow.PipelineRef, then a fixed default. Relocated
// from ModelRequestReconciler.sandboxPipelineNameOrDefault.
func PipelineNameOrDefault(profile *modelopsv1alpha1.ModelLifecycleProfile, mr *modelopsv1alpha1.ModelRequest) string {
	if mr.Spec.PipelineRef != "" {
		return mr.Spec.PipelineRef
	}
	if profile != nil && profile.Spec.Workflow.PipelineRef != "" {
		return profile.Spec.Workflow.PipelineRef
	}
	return "model-intake-sandbox"
}
