package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IntakeProviderConfigSpec holds execution-engine-specific details that
// ModelLifecycleProfile used to embed directly (WorkflowRef.PipelineRef/
// PromotionPipelineRef) or that were hardcoded Go constants inside
// internal/stages/tekton.StageRunner (ServiceAccountName, workspace
// bindings, pipeline timeout). See docs/REFACTOR_PLAN.md Phase 5.
//
// This is deliberately one generic kind with a ProviderType
// discriminator rather than a separate typed kind per backend
// (RHOAIIntakeConfig, SageMakerIntakeConfig, ...) -- per Phase 5's own
// text, split into typed kinds only once a second real backend is
// implemented. Today only "tekton" is supported; the fields below this
// comment only apply when ProviderType == "tekton".
type IntakeProviderConfigSpec struct {
	// ProviderType discriminates which StageRunner implementation
	// understands this config. Only "tekton" exists today -- the enum
	// is intentionally restrictive rather than a free string, so a
	// typo is a rejected write, not a silent runtime fallback.
	// +kubebuilder:validation:Enum=tekton
	ProviderType string `json:"providerType"`

	// SandboxPipelineName is the Tekton Pipeline run for the sandbox
	// stage. Was: WorkflowRef.PipelineRef / mr.Spec.PipelineRef
	// fallback chain in ModelRequestReconciler.
	SandboxPipelineName string `json:"sandboxPipelineName,omitempty"`

	// PromotionPipelineName is the Tekton Pipeline run for each
	// promotion-namespace stage. Was: WorkflowRef.PromotionPipelineRef.
	PromotionPipelineName string `json:"promotionPipelineName,omitempty"`

	// ServiceAccountName is the PipelineRun's
	// TaskRunTemplate.ServiceAccountName. Was: hardcoded "pipeline" in
	// internal/stages/tekton.buildPipelineRun.
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// PipelineTimeout bounds the PipelineRun. nil/zero means unbounded,
	// matching today's hardcoded metav1.Duration{Duration: 0}.
	PipelineTimeout *metav1.Duration `json:"pipelineTimeout,omitempty"`

	// Workspaces are the PipelineRun workspace bindings. Was: 3
	// hardcoded WorkspaceBinding entries (shared-workspace ->
	// guidellm-output-pvc PVC, manifests -> mmlu-manifest ConfigMap,
	// custom-mmlu -> custom-mmlu ConfigMap).
	Workspaces []IntakeProviderWorkspace `json:"workspaces,omitempty"`

	// CheckResultMappings maps Tekton PipelineRun result names to
	// CheckTypes for per-check evidence extraction from combined-stage
	// PipelineRuns. Each entry declares which PipelineRun result name
	// holds the pass/fail value for which CheckType. If the result is
	// missing or has an unexpected value, that entry is silently
	// omitted -- no error, just no evidence for that check. When
	// unset or empty, no per-check evidence is extracted.
	// +optional
	CheckResultMappings []CheckResultMapping `json:"checkResultMappings,omitempty"`
}

// CheckResultMapping maps a single Tekton PipelineRun result to a
// CheckType for per-check evidence extraction.
type CheckResultMapping struct {
	ResultName  string    `json:"resultName"`
	CheckType   CheckType `json:"checkType"`
	PassedValue string    `json:"passedValue"`
}

// IntakeProviderWorkspace is a single Tekton workspace binding, reduced
// to the two binding kinds internal/stages/tekton.buildPipelineRun has
// ever produced (PersistentVolumeClaim or ConfigMap by name).
type IntakeProviderWorkspace struct {
	Name                  string `json:"name"`
	PersistentVolumeClaim string `json:"persistentVolumeClaim,omitempty"`
	ConfigMap             string `json:"configMap,omitempty"`
}

// IntakeProviderConfigStatus follows the same (currently unwritten)
// Conditions-only precedent already set by ModelLifecycleProfileStatus
// and PlatformConfigStatus -- no controller reconciles this kind's
// status yet.
type IntakeProviderConfigStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="ProviderType",type=string,JSONPath=`.spec.providerType`

type IntakeProviderConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IntakeProviderConfigSpec   `json:"spec,omitempty"`
	Status IntakeProviderConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type IntakeProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []IntakeProviderConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IntakeProviderConfig{}, &IntakeProviderConfigList{})
}
