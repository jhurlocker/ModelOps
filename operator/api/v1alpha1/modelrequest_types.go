package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ModelRequestSpec struct {
	ModelSource       string `json:"modelSource"`
	ModelURI          string `json:"modelURI"`
	TargetEnvironment string `json:"targetEnvironment,omitempty"`
	PipelineRef       string `json:"pipelineRef,omitempty"`

	ModelName    string `json:"modelName,omitempty"`
	ModelVersion string `json:"modelVersion,omitempty"`
	RequestedBy  string `json:"requestedBy,omitempty"`

	ModelCar          *ModelCarConfig      `json:"modelCar,omitempty"`
	Namespaces        *NamespaceConfig     `json:"namespaces,omitempty"`
	Compliance        *ComplianceConfig    `json:"compliance,omitempty"`
	GPUAdvisor       *GPUAdvisorConfig    `json:"gpuAdvisor,omitempty"`
	Deploy            *DeployConfig        `json:"deploy,omitempty"`
	SecurityScan      *SecurityScanConfig  `json:"securityScan,omitempty"`
	Approval          *ApprovalConfig      `json:"approval,omitempty"`
	Benchmark         *BenchmarkConfig     `json:"benchmark,omitempty"`
	S3Storage         *S3Config            `json:"s3Storage,omitempty"`
	ScanS3Storage     *S3Config            `json:"scanS3Storage,omitempty"`
	ModelRegistry     *ModelRegistryConfig `json:"modelRegistry,omitempty"`
	ModelAccess       *ModelAccessConfig   `json:"modelAccess,omitempty"`
	MaaS              *MaaSConfig          `json:"maas,omitempty"`
	LMEval            *LMEvalConfig        `json:"lmEval,omitempty"`
}

type ModelCarConfig struct {
	Repo  string `json:"repo,omitempty"`
	Image string `json:"image,omitempty"`
}

type NamespaceConfig struct {
	Sandbox string `json:"sandbox,omitempty"`
	Staging string `json:"staging,omitempty"`
}

type ComplianceConfig struct {
	ArtifactScanImage string   `json:"artifactScanImage,omitempty"`
	CVeThreshold      string   `json:"cveThreshold,omitempty"`
	IgnoreUnfixed     string   `json:"ignoreUnfixed,omitempty"`
	AllowedArchitectures []string `json:"allowedArchitectures,omitempty"`
}

type GPUAdvisorConfig struct {
	ContextLength          int    `json:"contextLength,omitempty"`
	Concurrency            int    `json:"concurrency,omitempty"`
	AllowTimeSlicing       bool   `json:"allowTimeSlicing,omitempty"`
	AllowMIG               bool   `json:"allowMIG,omitempty"`
	GPUIsolationPolicy     string `json:"gpuIsolationPolicy,omitempty"`
	GPUOperatorNamespace   string `json:"gpuOperatorNamespace,omitempty"`
	ClusterPolicyName      string `json:"clusterPolicyName,omitempty"`
	TimeSlicingConfigMap   string `json:"timeSlicingConfigMap,omitempty"`
	MaxTimeSlices          int    `json:"maxTimeSlices,omitempty"`
	AdvisorEndpoint        string `json:"advisorEndpoint,omitempty"`
	AdvisorSecretName      string `json:"advisorSecretName,omitempty"`
	AdvisorTimeoutSeconds  int    `json:"advisorTimeoutSeconds,omitempty"`
}

type DeployConfig struct {
	ReleaseName              string `json:"releaseName,omitempty"`
	ChartURL                 string `json:"chartUrl,omitempty"`
	ChartVersion             string `json:"chartVersion,omitempty"`
	ValuesContent            string `json:"valuesContent,omitempty"`
	GPUCountOverride         string `json:"gpuCountOverride,omitempty"`
	HardwareProfileName      string `json:"hardwareProfileName,omitempty"`
	HardwareProfileNamespace string `json:"hardwareProfileNamespace,omitempty"`
}

type SecurityScanConfig struct {
	SeverityThreshold string `json:"severityThreshold,omitempty"`
	EvalHubURL        string `json:"evalhubUrl,omitempty"`
	EvalHubToken      string `json:"evalhubToken,omitempty"`
	TenantNamespace   string `json:"tenantNamespace,omitempty"`
	OpenShiftConsoleDomain string `json:"openshiftConsoleDomain,omitempty"`
}

type ApprovalConfig struct {
	APIURL            string `json:"apiUrl,omitempty"`
	PollIntervalSeconds int  `json:"pollIntervalSeconds,omitempty"`
	TimeoutSeconds    int    `json:"timeoutSeconds,omitempty"`
}

type BenchmarkConfig struct {
	Profile     string  `json:"profile,omitempty"`
	Rate        float64 `json:"rate,omitempty"`
	MaxSeconds  int     `json:"maxSeconds,omitempty"`
	MaxRequests int     `json:"maxRequests,omitempty"`
	CustomData  bool    `json:"customData,omitempty"`
	CustomFilename string `json:"customFilename,omitempty"`
	HuggingFaceToken string `json:"huggingfaceToken,omitempty"`
}

type S3Config struct {
	Endpoint        string `json:"endpoint,omitempty"`
	AccessKeyID     string `json:"accessKeyId,omitempty"`
	SecretAccessKey string `json:"secretAccessKey,omitempty"`
	Bucket          string `json:"bucket,omitempty"`
	UIRoute         string `json:"uiRoute,omitempty"`
}

type ModelRegistryConfig struct {
	Server string `json:"server,omitempty"`
	Port   string `json:"port,omitempty"`
	Author string `json:"author,omitempty"`
}

type ModelAccessConfig struct {
	AuthorizedViewers string `json:"authorizedViewers,omitempty"`
	AccessRole        string `json:"accessRole,omitempty"`
}

type MaaSConfig struct {
	Enabled           bool   `json:"enabled,omitempty"`
	ServingNamespace  string `json:"servingNamespace,omitempty"`
	PolicyNamespace   string `json:"policyNamespace,omitempty"`
	GPUCount          string `json:"gpuCount,omitempty"`
	RuntimeImage      string `json:"runtimeImage,omitempty"`
	AuthorizedGroup   string `json:"authorizedGroup,omitempty"`
}

type LMEvalConfig struct {
	Enabled    bool   `json:"enabled,omitempty"`
	JobName    string `json:"jobName,omitempty"`
	UseCustom  bool   `json:"useCustom,omitempty"`
}

type ModelRequestStatus struct {
	Phase           string            `json:"phase,omitempty"`
	PipelineRunName string            `json:"pipelineRunName,omitempty"`
	Message         string            `json:"message,omitempty"`
	Conditions      []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="PipelineRun",type=string,JSONPath=`.status.pipelineRunName`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.modelURI`

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
