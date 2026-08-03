package stagecommon

import (
	"strconv"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
)

// Secrets holds the resolved credential/endpoint fields BuildCommonModelParams
// needs (deliberately excludes scan-specific S3 credentials, which are
// sandbox-only). Callers build one of these from their own resolved-secrets
// type, so this package never needs to import internal/controller.
type Secrets struct {
	EvalHubToken      string
	HuggingFaceToken  string
	ResultS3Endpoint  string
	ResultS3AccessKey string
	ResultS3SecretKey string
}

// BuildCommonModelParams builds the params identical, byte-for-byte,
// between the sandbox and promotion pipeline stages: model identity,
// modelcar reference, GPU/benchmark config, deployment/chart config,
// EvalHub config, OpenShift console domain, HuggingFace token, result-S3
// config, and model-registry config.
//
// Returns a provider-agnostic map[string]string (Phase 4 of
// REFACTOR_PLAN.md) rather than tektonv1.Params: this is what lets
// callers (and, ultimately, ModelRequestReconciler) stop importing
// tektonv1 to build a stage's inputs. internal/stages/tekton.StageRunner
// is solely responsible for converting this map into the Tekton-native
// param type at the last mile, when it actually constructs a
// PipelineRun.
//
// gpu-count-override is deliberately excluded: sandbox lets an explicit
// reqs.GPUConfig.GPUCountOverride win over the CapacityPlan-derived value
// (the Phase 1 duplicate-param fix), while promotion always uses only the
// CapacityPlan-derived value. Folding it in here would either change
// promotion's behavior or re-introduce the very if/else duplication this
// helper exists to remove -- each caller keeps its own single AddParam
// call for it. See REFACTOR_PLAN.md/PHASE_LOG.md Phase 3 for the full
// rationale.
func BuildCommonModelParams(
	spec modelopsv1alpha1.ModelRequestSpec,
	reqs *modelopsv1alpha1.ModelRequirements,
	cfg *modelopsv1alpha1.PlatformConfig,
	secrets Secrets,
) map[string]string {
	p := map[string]string{}

	AddParam(p, "model-id", spec.Model.URI)
	AddParam(p, "model-name", StrOrDefault(spec.Model.Name, "unknown"))
	AddParam(p, "model-version", StrOrDefault(spec.Model.Version, "v1"))
	AddParam(p, "model-tokenizer", spec.Model.Tokenizer)
	AddParam(p, "model-source-type", spec.Model.SourceType)
	AddParam(p, "display-name", spec.DisplayName)
	AddParam(p, "business-justification", spec.BusinessJustification)
	AddParam(p, "requested-by", spec.RequestedBy)

	AddParam(p, "modelcar-repo", StrOrDefault(cfg.Spec.ModelCarRepo, "redhat-ai-services/modelcar-catalog"))
	AddParam(p, "modelcar-image", "")

	AddParam(p, "context-length", strconv.Itoa(IntOrDefault(reqs.BenchmarkTargets.ContextLength, 32768)))
	AddParam(p, "concurrency", strconv.Itoa(IntOrDefault(reqs.BenchmarkTargets.ExpectedConcurrency, 4)))
	AddParam(p, "allow-time-slicing", strconv.FormatBool(BoolOrDefault(reqs.GPUConfig.AllowTimeSlicing, true)))
	AddParam(p, "allow-mig", strconv.FormatBool(BoolOrDefault(reqs.GPUConfig.AllowMIG, false)))
	AddParam(p, "gpu-isolation-policy", StrOrDefault(reqs.GPUConfig.GPUIsolationPolicy, "dedicated"))
	AddParam(p, "request-rate", reqs.BenchmarkTargets.RequestRate)
	AddParam(p, "target-ttft", reqs.BenchmarkTargets.TargetTTFT)
	AddParam(p, "target-throughput", reqs.BenchmarkTargets.TargetThroughput)
	AddParam(p, "gpu-operator-namespace", StrOrDefault(cfg.Spec.GPUOperatorNamespace, "nvidia-gpu-operator"))
	AddParam(p, "clusterpolicy-name", StrOrDefault(cfg.Spec.ClusterPolicyName, "gpu-cluster-policy"))
	AddParam(p, "time-slicing-configmap", StrOrDefault(cfg.Spec.TimeSlicingConfigMap, "modelops-time-slicing"))
	AddParam(p, "max-time-slices", strconv.Itoa(IntOrDefault(cfg.Spec.MaxTimeSlices, 8)))
	AddParam(p, "advisor-endpoint", reqs.AdvisorEndpoint)
	AddParam(p, "advisor-secret-name", StrOrDefault(cfg.Spec.AdvisorSecretName, "gpu-advisor-credentials"))
	AddParam(p, "advisor-timeout-seconds", strconv.Itoa(IntOrDefault(cfg.Spec.AdvisorTimeoutSeconds, 300)))

	AddParam(p, "release-name", StrOrDefault(spec.Model.Name, "unknown"))
	AddParam(p, "chart-url", StrOrDefault(cfg.Spec.ChartURL, "https://redhat-ai-services.github.io/helm-charts/"))
	AddParam(p, "chart-version", StrOrDefault(cfg.Spec.ChartVersion, "0.7.1"))
	AddParam(p, "values-content", reqs.DeploymentConfig.ValuesContent)
	AddParam(p, "hardware-profile-name", StrOrDefault(cfg.Spec.HardwareProfileName, "gpu-profile"))
	AddParam(p, "hardware-profile-namespace", StrOrDefault(cfg.Spec.HardwareProfileNamespace, "redhat-ods-applications"))

	AddParam(p, "evalhub-url", cfg.Spec.EvalHubURL)
	AddParam(p, "evalhub-token", secrets.EvalHubToken)

	AddParam(p, "openshift-console-domain", reqs.DeploymentConfig.OpenShiftConsoleDomain)

	AddParam(p, "huggingface-token", secrets.HuggingFaceToken)

	AddParam(p, "s3-api-endpoint", secrets.ResultS3Endpoint)
	AddParam(p, "s3-access-key-id", secrets.ResultS3AccessKey)
	AddParam(p, "s3-secret-access-key", secrets.ResultS3SecretKey)

	AddParam(p, "mr-server", StrOrDefault(cfg.Spec.RegistryServer, "http://modelops-registry.rhoai-model-registries.svc.cluster.local"))
	AddParam(p, "mr-port", StrOrDefault(cfg.Spec.RegistryPort, "8080"))
	AddParam(p, "model-reg-author", StrOrDefault(cfg.Spec.RegistryAuthor, "ModelOps Platform Team"))

	return p
}

// AddParam sets params[name] = value, unless value is empty (in which
// case the param is omitted entirely -- matching Tekton's historical
// addParam guard this function replaces).
//
// Building directly into a map means a second AddParam call for a name
// already set silently overwrites the first, rather than producing two
// entries the way appending to a tektonv1.Params slice used to. That's
// a deliberate, stronger fix for the bug class Phase 1/3's
// duplicate-param tests guarded against (a duplicate can no longer
// reach a real PipelineRun's Spec.Params at all, by construction) at
// the cost of losing the loud "duplicate detected" test failure those
// tests previously gave for a *logic* mistake (two unrelated params
// accidentally sharing a name) -- exported so internal/controller's
// stage-specific param builders can reuse it too, instead of keeping a
// second, drift-prone private copy.
func AddParam(params map[string]string, name, value string) {
	if value != "" {
		params[name] = value
	}
}

func StrOrDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}

func IntOrDefault(val, def int) int {
	if val == 0 {
		return def
	}
	return val
}

func BoolOrDefault(val *bool, def bool) bool {
	if val == nil {
		return def
	}
	return *val
}
