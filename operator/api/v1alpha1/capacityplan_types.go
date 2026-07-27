package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type CapacityPlanSpec struct {
	ModelRef                CapacityPlanModelRef `json:"modelRef"`
	GPUs                    int                  `json:"gpus,omitempty"`
	GPUType                 string               `json:"gpuType,omitempty"`
	GPUsPerReplica          int                  `json:"gpusPerReplica,omitempty"`
	TimeSlicingConfig       string               `json:"timeSlicingConfig,omitempty"`
	IsolationPolicy         string               `json:"isolationPolicy,omitempty"`
	ContextLength           int                  `json:"contextLength,omitempty"`
	Concurrency             int                  `json:"concurrency,omitempty"`
	AllowTimeSlicing        bool                 `json:"allowTimeSlicing,omitempty"`
	AllowMIG                bool                 `json:"allowMIG,omitempty"`
	AdvisorEndpoint         string               `json:"advisorEndpoint,omitempty"`
	AdvisorSecretName       string               `json:"advisorSecretName,omitempty"`
	AdvisorTimeoutSeconds   int                  `json:"advisorTimeoutSeconds,omitempty"`
	GPUOperatorNamespace    string               `json:"gpuOperatorNamespace,omitempty"`
	ClusterPolicyName       string               `json:"clusterPolicyName,omitempty"`
	TimeSlicingConfigMap    string               `json:"timeSlicingConfigMap,omitempty"`
	MaxTimeSlices           int                  `json:"maxTimeSlices,omitempty"`
}

type CapacityPlanModelRef struct {
	ModelRequestName string `json:"modelRequestName"`
}

type CapacityPlanStatus struct {
	Phase      string             `json:"phase,omitempty"`
	GPUsNeeded int                `json:"gpusNeeded,omitempty"`
	GPUModel   string             `json:"gpuModel,omitempty"`
	Message    string             `json:"message,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="GPUs",type=integer,JSONPath=`.status.gpusNeeded`

type CapacityPlan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CapacityPlanSpec   `json:"spec,omitempty"`
	Status CapacityPlanStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type CapacityPlanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []CapacityPlan `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CapacityPlan{}, &CapacityPlanList{})
}
