package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ModelLifecycleProfileSpec struct {
	Workflow WorkflowRef `json:"workflow"`
	// ProviderConfigRef points at an IntakeProviderConfig (or, in
	// principle, another kind sharing the same name+kind reference
	// shape) holding execution-engine-specific details -- pipeline
	// names, service account, workspace bindings. Introduced in Phase
	// 5 of REFACTOR_PLAN.md.
	//
	// This is additive, not a breaking change: when nil, the sandbox
	// and promotion stages resolve their pipeline name exactly as
	// before this field existed, via Workflow.PipelineRef/
	// PromotionPipelineRef (now deprecated fallbacks, still fully
	// honored) and the hardcoded execution defaults in
	// internal/stages/tekton. Existing ModelLifecycleProfile objects
	// need no changes to keep working.
	ProviderConfigRef *ProviderConfigRef `json:"providerConfigRef,omitempty"`
	PolicyRef         string             `json:"policyRef,omitempty"`
	PlatformConfigRef string             `json:"platformConfigRef,omitempty"`
	DefaultAccess     *ModelAccess       `json:"defaultAccess,omitempty"`
}

// ProviderConfigRef is a name+kind reference to a provider-specific
// configuration object in the same namespace as the referencing object.
type ProviderConfigRef struct {
	// Name of the provider config object.
	Name string `json:"name"`
	// Kind of the referenced object. Defaults to "IntakeProviderConfig"
	// when empty.
	// +kubebuilder:default=IntakeProviderConfig
	Kind string `json:"kind,omitempty"`
}

// WorkflowRef.PipelineRef and PromotionPipelineRef are DEPRECATED as of
// Phase 5 (REFACTOR_PLAN.md): honored only as a fallback when
// ModelLifecycleProfileSpec.ProviderConfigRef is unset, so existing
// ModelLifecycleProfile objects keep working unmodified. Prefer
// ProviderConfigRef -> IntakeProviderConfig for new profiles.
type WorkflowRef struct {
	Engine string `json:"engine"`
	// PipelineRef was required prior to Phase 5; now optional so a
	// profile can rely solely on ProviderConfigRef instead.
	PipelineRef string `json:"pipelineRef,omitempty"`
	// PromotionPipelineRef selects the pipeline used for promotion runs.
	// If empty, the controller falls back to a fixed default
	// ("model-intake-promotion") rather than reusing PipelineRef, which
	// names the sandbox stage's pipeline.
	PromotionPipelineRef string `json:"promotionPipelineRef,omitempty"`
}

type ModelLifecycleProfileStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Engine",type=string,JSONPath=`.spec.workflow.engine`
// +kubebuilder:printcolumn:name="Pipeline",type=string,JSONPath=`.spec.workflow.pipelineRef`

type ModelLifecycleProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModelLifecycleProfileSpec   `json:"spec,omitempty"`
	Status ModelLifecycleProfileStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type ModelLifecycleProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []ModelLifecycleProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ModelLifecycleProfile{}, &ModelLifecycleProfileList{})
}
