package tekton

import (
	"context"
	"fmt"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// defaultIntakeProviderConfigKind is the Kind value
// stagecommon.StageSpec.ProviderConfigRef is treated as referencing when
// its Kind field is left empty -- matches
// ModelLifecycleProfileSpec.ProviderConfigRef's
// +kubebuilder:default=IntakeProviderConfig.
const defaultIntakeProviderConfigKind = "IntakeProviderConfig"

// supportedProviderType is the only IntakeProviderConfigSpec.ProviderType
// value this StageRunner understands. The CRD's own
// +kubebuilder:validation:Enum=tekton only enforces this through a real
// API server -- this is the Go-level guard for the same invariant.
const supportedProviderType = "tekton"

// providerDetails is everything buildPipelineRun needs beyond the
// caller-supplied name/namespace/params/owner: it's produced either by
// resolving an IntakeProviderConfig CR (when stage.ProviderConfigRef is
// set) or by defaultProviderDetails (the DEPRECATED fallback, matching
// today's hardcoded shape exactly).
type providerDetails struct {
	pipelineName       string
	serviceAccountName string
	pipelineTimeout    metav1.Duration
	workspaces         []tektonv1.WorkspaceBinding
}

// defaultProviderDetails reproduces, byte-for-byte, the values that used
// to be hardcoded directly in buildPipelineRun prior to Phase 5: the
// service account "pipeline", an unbounded pipeline timeout, and the 3
// workspace bindings (shared-workspace/guidellm-output-pvc PVC,
// manifests/mmlu-manifest ConfigMap, custom-mmlu/custom-mmlu ConfigMap).
// This is the single source of truth for those defaults now -- nothing
// else in this package hardcodes them a second time.
func defaultProviderDetails(pipelineName string) providerDetails {
	return providerDetails{
		pipelineName:       pipelineName,
		serviceAccountName: "pipeline",
		pipelineTimeout:    metav1.Duration{Duration: 0},
		workspaces: []tektonv1.WorkspaceBinding{
			{
				Name: "shared-workspace",
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: "guidellm-output-pvc",
				},
			},
			{
				Name: "manifests",
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "mmlu-manifest"},
				},
			},
			{
				Name: "custom-mmlu",
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "custom-mmlu"},
				},
			},
		},
	}
}

// resolveProviderDetails is the one place internal/stages/tekton
// interprets stagecommon.StageSpec.ProviderConfigRef. The core
// reconciler (internal/controller) never fetches or inspects an
// IntakeProviderConfig object itself -- it only passes the reference
// through unmodified (see stagecommon.StageSpec's doc comments).
//
// Behavior:
//   - stage.ProviderConfigRef == nil: skip any lookup entirely, return
//     defaultProviderDetails(stage.WorkflowRef) -- the DEPRECATED
//     inline-WorkflowRef fallback path, unchanged from pre-Phase-5
//     behavior.
//   - Kind set to anything other than "IntakeProviderConfig" (or
//     empty): an explicit error, without attempting a Get at all.
//   - The referenced object doesn't exist, or the Get otherwise fails:
//     the error is returned as-is to the caller (EnsureRun), which
//     propagates it through the reconciler's existing generic
//     transient-error path. See docs/REFACTOR_PLAN.md's Phase 7 note
//     for the known follow-up (a dedicated status reason instead of a
//     silent-retry error) not addressed in this phase.
//   - Spec.ProviderType != "tekton": an explicit error, not a silent
//     default.
//   - The resolved CR is only partially populated: any field left unset
//     (empty string / nil / empty slice) falls back to the
//     corresponding defaultProviderDetails value field-by-field, not as
//     an all-or-nothing choice.
func resolveProviderDetails(ctx context.Context, c client.Client, namespace string, stage stagecommon.StageSpec) (providerDetails, error) {
	fallback := defaultProviderDetails(stage.WorkflowRef)
	if stage.ProviderConfigRef == nil {
		return fallback, nil
	}

	kind := stage.ProviderConfigRef.Kind
	if kind == "" {
		kind = defaultIntakeProviderConfigKind
	}
	if kind != defaultIntakeProviderConfigKind {
		return providerDetails{}, fmt.Errorf(
			"stage %q: unsupported provider config kind %q (only %q is supported)",
			stage.Name, kind, defaultIntakeProviderConfigKind)
	}

	var cfg modelopsv1alpha1.IntakeProviderConfig
	key := types.NamespacedName{Name: stage.ProviderConfigRef.Name, Namespace: namespace}
	if err := c.Get(ctx, key, &cfg); err != nil {
		return providerDetails{}, fmt.Errorf("stage %q: resolving IntakeProviderConfig %q: %w", stage.Name, stage.ProviderConfigRef.Name, err)
	}

	if cfg.Spec.ProviderType != supportedProviderType {
		return providerDetails{}, fmt.Errorf(
			"stage %q: IntakeProviderConfig %q has providerType %q, the tekton StageRunner only supports %q",
			stage.Name, cfg.Name, cfg.Spec.ProviderType, supportedProviderType)
	}

	details := fallback

	switch stage.StageKind {
	case stagecommon.StageKindSandbox:
		if cfg.Spec.SandboxPipelineName != "" {
			details.pipelineName = cfg.Spec.SandboxPipelineName
		}
	case stagecommon.StageKindPromotion:
		if cfg.Spec.PromotionPipelineName != "" {
			details.pipelineName = cfg.Spec.PromotionPipelineName
		}
	}
	if cfg.Spec.ServiceAccountName != "" {
		details.serviceAccountName = cfg.Spec.ServiceAccountName
	}
	if cfg.Spec.PipelineTimeout != nil {
		details.pipelineTimeout = *cfg.Spec.PipelineTimeout
	}
	if len(cfg.Spec.Workspaces) > 0 {
		details.workspaces = toWorkspaceBindings(cfg.Spec.Workspaces)
	}

	return details, nil
}

// toWorkspaceBindings converts the CRD's provider-agnostic
// IntakeProviderWorkspace list into tektonv1.WorkspaceBinding.
func toWorkspaceBindings(ws []modelopsv1alpha1.IntakeProviderWorkspace) []tektonv1.WorkspaceBinding {
	out := make([]tektonv1.WorkspaceBinding, 0, len(ws))
	for _, w := range ws {
		binding := tektonv1.WorkspaceBinding{Name: w.Name}
		if w.PersistentVolumeClaim != "" {
			binding.PersistentVolumeClaim = &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: w.PersistentVolumeClaim,
			}
		}
		if w.ConfigMap != "" {
			binding.ConfigMap = &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: w.ConfigMap},
			}
		}
		out = append(out, binding)
	}
	return out
}
