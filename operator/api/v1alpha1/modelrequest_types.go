package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ModelRequestSpec struct {
	Model             ModelIdentity      `json:"model"`
	LifecycleProfile  string             `json:"lifecycleProfile"`
	PipelineRef       string             `json:"pipelineRef,omitempty"`
	Requirements      *ModelRequirements `json:"requirements,omitempty"`
	Access            *ModelAccess       `json:"access,omitempty"`
	RequestedBy       string             `json:"requestedBy,omitempty"`

	EvalHubSecretName     string `json:"evalhubSecretName,omitempty"`
	HuggingFaceSecretName string `json:"huggingfaceSecretName,omitempty"`
	ScanS3SecretName      string `json:"scanS3SecretName,omitempty"`
	ResultS3SecretName    string `json:"resultS3SecretName,omitempty"`

	MaaS  *MaaSOverride `json:"maas,omitempty"`
}

type ModelIdentity struct {
	SourceType string `json:"sourceType"`
	URI        string `json:"uri"`
	Name       string `json:"name,omitempty"`
	Version    string `json:"version,omitempty"`
}

type ModelRequirements struct {
	ContextLength       int    `json:"contextLength,omitempty"`
	ExpectedConcurrency int    `json:"expectedConcurrency,omitempty"`
	GPUIsolationPolicy  string `json:"gpuIsolationPolicy,omitempty"`
	AllowTimeSlicing    *bool  `json:"allowTimeSlicing,omitempty"`
	AllowMIG            *bool  `json:"allowMIG,omitempty"`
	CVEThreshold        string `json:"cveThreshold,omitempty"`
	SecurityThreshold   string `json:"securityThreshold,omitempty"`
	TargetEnvironment      string   `json:"targetEnvironment,omitempty"`
	SandboxNamespace       string   `json:"sandboxNamespace,omitempty"`
	StagingNamespace       string   `json:"stagingNamespace,omitempty"`
	PromotionNamespaces    []string `json:"promotionNamespaces,omitempty"`
	AdvisorEndpoint        string   `json:"advisorEndpoint,omitempty"`
	GPUCountOverride    string `json:"gpuCountOverride,omitempty"`
	ValuesContent       string `json:"valuesContent,omitempty"`
	CustomBenchmarkData bool   `json:"customBenchmarkData,omitempty"`
	CustomBenchmarkFile string `json:"customBenchmarkFile,omitempty"`
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
