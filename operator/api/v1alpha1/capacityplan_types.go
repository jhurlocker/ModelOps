package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CapacityPlanSpec is the desired state for a capacity-planning run.
// The sizing logic in CapacityPlanReconciler is a static heuristic
// (table-driven ContextLength/Concurrency -> GPU count/model mapping
// with a configurable MaxGPUsPerRequest ceiling), not a real
// GPU-inventory-aware or advisor-backed placement decision. See
// internal/stages/capacityplanning/doc.go and
// internal/controller/capacityplan_controller.go's reconciler doc
// comment for the full honesty label.
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
	// MaxGPUsPerRequest, when set (> 0), is a configured ceiling
	// CapacityPlanReconciler compares its unclamped GPU recommendation
	// against: if the recommendation would exceed this value,
	// Status.Phase is set to "Failed" instead of silently clamping to
	// the reconciler's own internal 8-GPU cap. Zero/unset preserves the
	// exact pre-Phase-7 clamping behavior (always succeeds, silently
	// capped at 8). Populated from PlatformConfigSpec.MaxGPUsPerRequest
	// by internal/stages/capacityplanning.Handler.BuildSpec, the same
	// way GPUOperatorNamespace/ClusterPolicyName already are. See
	// docs/REFACTOR_PLAN.md Phase 7.
	MaxGPUsPerRequest int `json:"maxGPUsPerRequest,omitempty"`
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
