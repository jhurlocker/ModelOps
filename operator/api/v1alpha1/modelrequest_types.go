package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ModelRequestSpec struct {
	Model                ModelIdentity      `json:"model"`
	DisplayName          string             `json:"displayName,omitempty"`
	BusinessJustification string            `json:"businessJustification,omitempty"`
	LifecycleProfile     string             `json:"lifecycleProfile"`
	PipelineRef          string             `json:"pipelineRef,omitempty"`
	Requirements         *ModelRequirements `json:"requirements,omitempty"`
	Access               *ModelAccess       `json:"access,omitempty"`
	RequestedBy          string             `json:"requestedBy,omitempty"`

	EvalHubSecretName     string `json:"evalhubSecretName,omitempty"`
	HuggingFaceSecretName string `json:"huggingfaceSecretName,omitempty"`
	ScanS3SecretName      string `json:"scanS3SecretName,omitempty"`
	ResultS3SecretName    string `json:"resultS3SecretName,omitempty"`

	// ResultS3Endpoint overrides the S3-compatible endpoint used for
	// scan/benchmark result storage. Credentials are never accepted here
	// -- they must be resolved via ResultS3SecretName's secretRef.
	ResultS3Endpoint string `json:"resultS3Endpoint,omitempty"`
	ResultS3Bucket   string `json:"resultS3Bucket,omitempty"`

	MaaS  *MaaSOverride `json:"maas,omitempty"`
}

type ModelIdentity struct {
	SourceType string `json:"sourceType"`
	URI        string `json:"uri"`
	Name       string `json:"name,omitempty"`
	Version    string `json:"version,omitempty"`
	Tokenizer  string `json:"tokenizer,omitempty"`
}

// ModelRequirements is composed of logical sub-structs grouping related
// concerns (GPU/hardware, benchmark targets, security, deployment). Each
// sub-struct is embedded anonymously with `json:",inline"` so its fields
// remain flat, top-level siblings on the wire -- this is what keeps an
// existing ModelRequest CR's `requirements:` YAML shape unchanged (e.g.
// `gpuCountOverride: ...` stays `gpuCountOverride: ...`, it does not
// become `gpuConfig.gpuCountOverride: ...`). See docs/REFACTOR_PLAN.md
// Phase 2 and docs/PHASE_LOG.md for the rationale.
//
// A handful of fields (target environment and namespace selection) don't
// fit any of the four grouped concerns cleanly and are left flat directly
// on ModelRequirements, matching their pre-Phase-2 shape.
type ModelRequirements struct {
	GPUConfig        `json:",inline"`
	BenchmarkTargets `json:",inline"`
	SecurityConfig   `json:",inline"`
	DeploymentConfig `json:",inline"`

	TargetEnvironment   string   `json:"targetEnvironment,omitempty"`
	SandboxNamespace    string   `json:"sandboxNamespace,omitempty"`
	StagingNamespace    string   `json:"stagingNamespace,omitempty"`
	PromotionNamespaces []string `json:"promotionNamespaces,omitempty"`
	AdvisorEndpoint     string   `json:"advisorEndpoint,omitempty"`
}

// GPUConfig groups GPU/hardware-related requirements: the explicit GPU
// count override, MIG/time-slicing allowance, and the isolation policy
// (hardware profile) governing them.
type GPUConfig struct {
	GPUIsolationPolicy string `json:"gpuIsolationPolicy,omitempty"`
	AllowTimeSlicing   *bool  `json:"allowTimeSlicing,omitempty"`
	AllowMIG           *bool  `json:"allowMIG,omitempty"`
	GPUCountOverride   string `json:"gpuCountOverride,omitempty"`
}

// BenchmarkTargets groups the benchmark/performance targets used to size
// capacity and validate sandbox/promotion runs: latency (TTFT), target
// throughput, expected concurrency, context length, and the GuideLLM
// request rate.
type BenchmarkTargets struct {
	ContextLength       int    `json:"contextLength,omitempty"`
	ExpectedConcurrency int    `json:"expectedConcurrency,omitempty"`
	RequestRate         string `json:"requestRate,omitempty"`
	TargetTTFT          string `json:"targetTTFT,omitempty"`
	TargetThroughput    string `json:"targetThroughput,omitempty"`
}

// SecurityConfig groups security/compliance-scan requirements: the CVE
// and general security severity thresholds, and an optional custom
// benchmark/scan file override.
type SecurityConfig struct {
	CVEThreshold        string `json:"cveThreshold,omitempty"`
	SecurityThreshold   string `json:"securityThreshold,omitempty"`
	CustomBenchmarkData bool   `json:"customBenchmarkData,omitempty"`
	CustomBenchmarkFile string `json:"customBenchmarkFile,omitempty"`
}

// DeploymentConfig groups deployment-shape requirements: raw Helm values
// content and the OpenShift console domain used to build UI links.
type DeploymentConfig struct {
	ValuesContent          string `json:"valuesContent,omitempty"`
	OpenShiftConsoleDomain string `json:"openshiftConsoleDomain,omitempty"`
}

type ModelAccess struct {
	AuthorizedViewers string `json:"authorizedViewers,omitempty"`
	AccessRole        string `json:"accessRole,omitempty"`
}

type MaaSOverride struct {
	Enabled          bool   `json:"enabled,omitempty"`
	GPUCount         string `json:"gpuCount,omitempty"`
	RuntimeImage     string `json:"runtimeImage,omitempty"`
	AuthorizedGroup  string `json:"authorizedGroup,omitempty"`
}

type ModelRequestStatus struct {
	Phase                       string             `json:"phase,omitempty"`
	PipelineRunName             string             `json:"pipelineRunName,omitempty"`
	SandboxPipelineRunName      string             `json:"sandboxPipelineRunName,omitempty"`
	PromotionPipelineRunName    string             `json:"promotionPipelineRunName,omitempty"`
	Message                     string             `json:"message,omitempty"`
	Conditions                  []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="PipelineRun",type=string,JSONPath=`.status.pipelineRunName`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.model.uri`
// +kubebuilder:printcolumn:name="Profile",type=string,JSONPath=`.spec.lifecycleProfile`

type ModelRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModelRequestSpec   `json:"spec,omitempty"`
	Status ModelRequestStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type ModelRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []ModelRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ModelRequest{}, &ModelRequestList{})
}
