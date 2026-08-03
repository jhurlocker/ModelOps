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
	// DEPRECATED as of Phase 7: this top-level field is no longer
	// consulted by Reconcile at all. It used to be the source
	// defaultStages (Phase 6) copied onto each synthesized stage's own
	// ProfileStageSpec.ProviderConfigRef when Stages was left empty;
	// now that every profile must declare Stages explicitly (Phase 7),
	// each ProfileStageSpec carries its own ProviderConfigRef directly
	// instead. Left in place, non-functional, rather than removed --
	// same treatment as WorkflowRef.Engine below -- since removing a
	// field outright is a breaking CRD change this phase doesn't need
	// to make. See docs/PHASE_LOG.md Phase 7.
	ProviderConfigRef *ProviderConfigRef `json:"providerConfigRef,omitempty"`
	PolicyRef         string             `json:"policyRef,omitempty"`
	PlatformConfigRef string             `json:"platformConfigRef,omitempty"`
	DefaultAccess     *ModelAccess       `json:"defaultAccess,omitempty"`

	// Stages is the ordered lifecycle sequence for ModelRequests using
	// this profile: the data ModelRequestReconciler's generic stage
	// walker iterates instead of a hardcoded Go sequence. Introduced
	// in Phase 6 of REFACTOR_PLAN.md.
	//
	// Functionally required as of Phase 7 (not yet enforced at the CRD
	// schema level -- no +kubebuilder:validation:MinItems marker was
	// added this phase, so an empty/missing Stages is still a
	// syntactically valid object): the Phase 6 fallback that
	// synthesized the pre-Phase-6 3-stage default sequence when this
	// field was left empty (defaultStages, internal/controller) was
	// removed once every ModelLifecycleProfile in this repo migrated to
	// declaring Stages explicitly (see
	// gitops/components/runtime-config/lifecycleprofile.yaml). A
	// profile with no Stages configured now fails at reconcile time
	// with a visible "NoStagesConfigured" ModelRequest status reason
	// instead of silently walking zero stages. This is a real,
	// deliberate breaking change for any ModelLifecycleProfile that was
	// still relying on the implicit default -- see docs/PHASE_LOG.md's
	// Phase 7 entry. Adding schema-level MinItems=1 for earlier
	// (admission-time) feedback is a reasonable, low-risk follow-up,
	// not done this phase.
	Stages []ProfileStageSpec `json:"stages,omitempty"`
}

// ProfileStageSpec declares one named stage in a ModelLifecycleProfile's
// lifecycle sequence. See docs/REFACTOR_PLAN.md Phase 6 for the design
// rationale (this is what replaces the hardcoded capacity-planning ->
// sandbox -> promotion sequence in ModelRequestReconciler.Reconcile with
// data the generic stage walker iterates).
type ProfileStageSpec struct {
	// Name identifies this stage within the profile (must be unique
	// within Stages). Surfaces in ModelRequestStatus.CurrentStage/
	// Stages[], is used to derive the underlying execution object's
	// RunName (e.g. "<modelrequest>-<name>"), and is the key a
	// StageHandler is registered under in main.go.
	Name string `json:"name"`

	// Kind selects which StageRunner (registered in main.go, keyed by
	// this string) actually executes and tracks this stage's work --
	// e.g. "CapacityPlan" or "PipelineRun" today. The walker looks
	// this up in a map; it is never hardcoded/switched on in Go, so
	// adding a new execution engine never requires a walker change.
	Kind string `json:"kind"`

	// ProviderConfigRef, when set, is passed through unmodified to
	// whichever StageRunner handles Kind -- same passthrough contract
	// stagecommon.StageSpec.ProviderConfigRef already has (Phase 5).
	// Meaningless for kinds whose runner doesn't resolve one (e.g.
	// "CapacityPlan" ignores it).
	ProviderConfigRef *ProviderConfigRef `json:"providerConfigRef,omitempty"`

	// Required controls what happens when this stage's StageStatus is
	// StageFailed: true (the default) stops the whole walk and fails
	// the ModelRequest; false lets the walker record the failure
	// against this stage (still visible in Status.Stages[]) and
	// advance anyway. Does not affect Running/Succeeded handling.
	// +kubebuilder:default=true
	Required *bool `json:"required,omitempty"`

	// PerNamespace, when true, fans this stage out once per namespace
	// returned by the ModelRequest's own promotion-namespace selection
	// (spec.requirements.promotionNamespaces/stagingNamespace/
	// "staging" default) instead of running it once against the
	// ModelRequest's own namespace. This is what makes today's
	// promotion-per-namespace behavior expressible as data instead of
	// a Go special case: the walker branches on this bool, never on a
	// stage's name.
	PerNamespace bool `json:"perNamespace,omitempty"`

	// NamespaceSetup declares what infrastructure preparation the
	// walker performs, generically, for every namespace this stage
	// targets, before invoking it. Nil means no preparation is needed
	// (e.g. the CapacityPlan stage, which doesn't execute anything
	// inside a target namespace).
	NamespaceSetup *StageNamespaceSetup `json:"namespaceSetup,omitempty"`
}

// StageNamespaceSetup declares namespace-preparation side effects the
// walker performs generically (driven by this data, not by checking a
// stage's name) before invoking a stage in a given target namespace.
type StageNamespaceSetup struct {
	// EnsureRBAC provisions the "pipeline" ServiceAccount and the
	// RoleBindings/ClusterRoleBinding it needs in the target
	// namespace (today's ensurePromotionNamespaceRBAC, previously
	// called unconditionally for the sandbox stage's own namespace and
	// every promotion namespace).
	EnsureRBAC bool `json:"ensureRBAC,omitempty"`

	// Labels are applied idempotently to the target namespace before
	// the stage runs (today's ensureMaaSNamespaceLabels, previously
	// hardcoded to only fire in the promotion loop).
	Labels map[string]string `json:"labels,omitempty"`
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
	// Engine is DEPRECATED as of Phase 7 (REFACTOR_PLAN.md): it never
	// drove any runtime dispatch even before this phase (routing
	// between execution engines has always gone through
	// ProfileStageSpec.Kind + the StageRunners registry, since Phase
	// 6 -- confirmed by grep: no Go code outside this field's own
	// declaration and the (now-repointed) printcolumn marker below
	// ever read WorkflowRef.Engine). IntakeProviderConfigSpec.ProviderType
	// is the field that actually carries functional weight today (a
	// Go-level guard in internal/stages/tekton's resolveProviderDetails
	// confirming a resolved IntakeProviderConfig is meant for the
	// tekton StageRunner). Left in place, non-functional, rather than
	// removed -- removing a field outright is a breaking CRD change
	// this phase doesn't need to make. See docs/PHASE_LOG.md Phase 7.
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
// +kubebuilder:printcolumn:name="Provider",type=string,JSONPath=`.spec.providerConfigRef.name`
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
