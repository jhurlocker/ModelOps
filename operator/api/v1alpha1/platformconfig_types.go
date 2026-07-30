package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PlatformConfigSpec struct {
	ComplianceS3Bucket  string   `json:"complianceS3Bucket,omitempty"`
	SecurityS3Bucket    string   `json:"securityS3Bucket,omitempty"`

	RegistryServer      string `json:"registryServer,omitempty"`
	RegistryPort        string `json:"registryPort,omitempty"`
	RegistryAuthor      string `json:"registryAuthor,omitempty"`

	ComplianceScanImage     string   `json:"complianceScanImage,omitempty"`
	ComplianceIgnoreUnfixed string   `json:"complianceIgnoreUnfixed,omitempty"`
	ComplianceAllowedArch   []string `json:"complianceAllowedArch,omitempty"`
	ModelCarRepo            string   `json:"modelCarRepo,omitempty"`

	GPUOperatorNamespace string `json:"gpuOperatorNamespace,omitempty"`
	ClusterPolicyName    string `json:"clusterPolicyName,omitempty"`
	TimeSlicingConfigMap string `json:"timeSlicingConfigMap,omitempty"`
	MaxTimeSlices        int    `json:"maxTimeSlices,omitempty"`

	AdvisorSecretName     string `json:"advisorSecretName,omitempty"`
	AdvisorTimeoutSeconds int    `json:"advisorTimeoutSeconds,omitempty"`

	ChartURL                 string `json:"chartUrl,omitempty"`
	ChartVersion             string `json:"chartVersion,omitempty"`
	HardwareProfileName      string `json:"hardwareProfileName,omitempty"`
	HardwareProfileNamespace string `json:"hardwareProfileNamespace,omitempty"`

	EvalHubURL                     string  `json:"evalhubUrl,omitempty"`
	ApprovalApiUrl                 string  `json:"approvalApiUrl,omitempty"`
	ApprovalPollIntervalSeconds    int     `json:"approvalPollIntervalSeconds,omitempty"`
	ApprovalTimeoutSeconds         int     `json:"approvalTimeoutSeconds,omitempty"`
	BenchmarkProfile               string  `json:"benchmarkProfile,omitempty"`
	BenchmarkRate                  float64 `json:"benchmarkRate,omitempty"`
	BenchmarkMaxSeconds            int     `json:"benchmarkMaxSeconds,omitempty"`
	BenchmarkMaxRequests           int     `json:"benchmarkMaxRequests,omitempty"`
	BenchmarkTargetUrl             string  `json:"benchmarkTargetUrl,omitempty"`

	MaaSServingNS       string `json:"maasServingNs,omitempty"`
	MaaSPolicyNS        string `json:"maasPolicyNs,omitempty"`
	MaaSGPUCount        string `json:"maasGpuCount,omitempty"`
	MaaSRuntimeImage    string `json:"maasRuntimeImage,omitempty"`
	MaaSAuthorizedGroup string `json:"maasAuthorizedGroup,omitempty"`

	LMEvalJobName string `json:"lmEvalJobName,omitempty"`
}

type PlatformConfigStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

type PlatformConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PlatformConfigSpec   `json:"spec,omitempty"`
	Status PlatformConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type PlatformConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []PlatformConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PlatformConfig{}, &PlatformConfigList{})
}
