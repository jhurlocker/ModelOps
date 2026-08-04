package stagecommon

import (
	"strconv"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
)

// Secrets holds resolved credential *references* (Secret names) and
// non-secret endpoint strings BuildCommonModelParams needs, plus (as of
// Phase 6) the scan-specific S3 reference internal/stages/sandbox.Handler
// reads directly -- BuildCommonModelParams itself still never reads
// ScanS3*, only sandbox's own handler does. Callers build one of these
// from their own resolved-secrets type, so this package never needs to
// import internal/controller.
//
// As of Phase 8 (docs/PHASE_LOG.md), this struct deliberately carries no
// credential VALUES at all -- only Secret names and non-secret
// endpoints. internal/controller.resolveSecrets still Gets/inspects the
// underlying Secret's data for validation (does it exist, does it have
// the expected keys, is a fail-loud error warranted), but only the
// Secret's own name escapes that function. Every credential-bearing
// Tekton param this package builds is therefore a Secret *reference*
// (consumed via a Task's own env.valueFrom.secretKeyRef, keyed by this
// name, per the pre-existing advisor-secret-name/ADVISOR_API_KEY
// pattern in gpu-advisor-task.yaml) rather than a plaintext value in
// PipelineRun.spec.params. See docs/REVIEW_RESPONSE_PLAN.md Phase 8 and
// AGENTS.md ("secret names may appear in resources; secret values must
// not").
type Secrets struct {
	// EvalHubURL is the EvalHub base URL. Not a credential -- resolved
	// from the EvalHub Secret's "url" key when present, otherwise left
	// empty (BuildCommonModelParams falls back to
	// PlatformConfig.Spec.EvalHubURL).
	EvalHubURL string
	// EvalHubSecretName names a Secret (same namespace as the
	// PipelineRun) holding a "token" key: either the
	// operator-configured EvalHubSecretName (when it has a "token" key)
	// or a controller-managed, owned, ephemeral Secret holding a
	// freshly generated ServiceAccount token when no explicit token was
	// configured (see ModelRequestReconciler.ensureEvalHubTokenSecret).
	// Never the token value itself.
	EvalHubSecretName string
	// HuggingFaceSecretName names a Secret holding a "token" key, or ""
	// if none was configured (HuggingFace auth is optional). Never the
	// token value itself.
	HuggingFaceSecretName string
	// ResultS3Endpoint is the result-storage S3 endpoint URL. Not a
	// credential.
	ResultS3Endpoint string
	// ResultS3SecretName names a Secret holding
	// accessKeyId/secretAccessKey keys for result storage. Never the
	// credential values themselves.
	ResultS3SecretName string
	// ScanS3Endpoint is the scan-result-storage S3 endpoint URL
	// (sandbox-stage-only). Not a credential. BuildCommonModelParams
	// never reads this -- only sandbox.Handler does.
	ScanS3Endpoint string
	// ScanS3SecretName names a Secret holding
	// accessKeyId/secretAccessKey keys for scan-result storage
	// (sandbox-stage-only). Never the credential values themselves.
	// BuildCommonModelParams never reads this -- only sandbox.Handler
	// does.
	ScanS3SecretName string
}

// defaultEvalHubSecretName/defaultHuggingFaceSecretName are the
// fallback Secret names emitted when no explicit EvalHub/HuggingFace
// Secret is configured. These deliberately name a Secret that need not
// actually exist -- the consuming Task's env.valueFrom.secretKeyRef
// sets optional:true for both, so a nonexistent Secret with this name
// simply results in the env var not being set (functionally identical
// to the pre-Phase-8 "empty string" behavior for these two genuinely
// optional credentials). A non-empty placeholder is required here
// because Kubernetes' API validation rejects an empty
// secretKeyRef.name outright, regardless of optional:true -- unlike
// "the referenced Secret doesn't exist," "no name was given at all" is
// not something optional:true tolerates.
const (
	defaultEvalHubSecretName     = "evalhub-credentials"
	defaultHuggingFaceSecretName = "huggingface-credentials"
)

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

	// evalhub-url: the per-request Secret's "url" key (when the
	// operator set one) wins over the PlatformConfig-wide default --
	// see resolveSecrets' Phase 8 fix (docs/PHASE_LOG.md) for why this
	// field exists on Secrets at all (it used to be silently discarded
	// into the wrong field entirely).
	AddParam(p, "evalhub-url", StrOrDefault(secrets.EvalHubURL, cfg.Spec.EvalHubURL))
	// evalhub-secret-name/huggingface-secret-name: Secret *references*,
	// never values -- see Secrets' doc comment and
	// docs/PHASE_LOG.md Phase 8. Always emitted (StrOrDefault, not
	// AddParam's bare empty-guard) so the Task's secretKeyRef always has
	// a syntactically valid (if not necessarily existent) name to
	// resolve, per defaultEvalHubSecretName/defaultHuggingFaceSecretName's
	// doc comment.
	AddParam(p, "evalhub-secret-name", StrOrDefault(secrets.EvalHubSecretName, defaultEvalHubSecretName))

	AddParam(p, "openshift-console-domain", reqs.DeploymentConfig.OpenShiftConsoleDomain)

	AddParam(p, "huggingface-secret-name", StrOrDefault(secrets.HuggingFaceSecretName, defaultHuggingFaceSecretName))

	AddParam(p, "s3-api-endpoint", secrets.ResultS3Endpoint)
	// result-s3-secret-name: no Go-side default, unlike evalhub/
	// huggingface above -- resolveSecrets' fail-loud validation (Phase
	// 1) guarantees a real ModelRequest never reaches this function
	// without one, so there's no placeholder to fall back to, and this
	// param is simply omitted (AddParam's ordinary empty-value guard)
	// in the synthetic/test-fixture case where it's unset.
	AddParam(p, "result-s3-secret-name", secrets.ResultS3SecretName)

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
