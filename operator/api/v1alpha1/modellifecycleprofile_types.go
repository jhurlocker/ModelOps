package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ModelLifecycleProfileSpec struct {
	Workflow          WorkflowRef    `json:"workflow"`
	PolicyRef         string         `json:"policyRef,omitempty"`
	PlatformConfigRef string         `json:"platformConfigRef,omitempty"`
	DefaultAccess     *ModelAccess   `json:"defaultAccess,omitempty"`
}

type WorkflowRef struct {
	Engine      string `json:"engine"`
	PipelineRef string `json:"pipelineRef"`
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
