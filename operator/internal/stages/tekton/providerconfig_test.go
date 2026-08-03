package tekton

// Written first, before resolveProviderDetails existed (TDD, per
// REFACTOR_PLAN.md's guiding principles and Phase 5's design review).
// resolveProviderDetails is the one place internal/stages/tekton
// interprets ModelLifecycleProfileSpec.ProviderConfigRef -- the core
// reconciler never fetches or inspects an IntakeProviderConfig object
// itself, only passes the reference through (see
// stagecommon.StageSpec.ProviderConfigRef's doc comment).

import (
	"context"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	"github.com/stretchr/testify/require"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestResolveProviderDetails_NilRef_ReturnsHardcodedDefaults(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	stage := stagecommon.StageSpec{
		Name:        "sandbox",
		WorkflowRef: "model-intake-sandbox",
		StageKind:   stagecommon.StageKindSandbox,
		// ProviderConfigRef deliberately nil -- the opt-in-fallback path.
	}

	details, err := resolveProviderDetails(context.Background(), c, "ns-1", stage)
	require.NoError(t, err)

	require.Equal(t, "model-intake-sandbox", details.pipelineName, "falls back to StageSpec.WorkflowRef")
	require.Equal(t, "pipeline", details.serviceAccountName)
	require.Equal(t, int64(0), int64(details.pipelineTimeout.Duration), "unbounded, matching today's hardcoded metav1.Duration{0}")
	require.Len(t, details.workspaces, 3)
	requireDefaultWorkspaceShape(t, details.workspaces)
}

func TestResolveProviderDetails_ResolvesSandboxPipelineName_FromCR(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(
		&modelopsv1alpha1.IntakeProviderConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "rhoai-tekton", Namespace: "ns-1"},
			Spec: modelopsv1alpha1.IntakeProviderConfigSpec{
				ProviderType:        "tekton",
				SandboxPipelineName: "cr-sandbox-pipeline",
			},
		},
	).Build()

	stage := stagecommon.StageSpec{
		Name:              "sandbox",
		WorkflowRef:       "model-intake-sandbox", // must be ignored once ProviderConfigRef resolves
		StageKind:         stagecommon.StageKindSandbox,
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{Name: "rhoai-tekton"},
	}

	details, err := resolveProviderDetails(context.Background(), c, "ns-1", stage)
	require.NoError(t, err)
	require.Equal(t, "cr-sandbox-pipeline", details.pipelineName)
}

func TestResolveProviderDetails_ResolvesPromotionPipelineName_FromCR(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(
		&modelopsv1alpha1.IntakeProviderConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "rhoai-tekton", Namespace: "ns-1"},
			Spec: modelopsv1alpha1.IntakeProviderConfigSpec{
				ProviderType:          "tekton",
				SandboxPipelineName:   "cr-sandbox-pipeline",
				PromotionPipelineName: "cr-promotion-pipeline",
			},
		},
	).Build()

	stage := stagecommon.StageSpec{
		Name:              "promotion-staging",
		WorkflowRef:       "model-intake-promotion",
		StageKind:         stagecommon.StageKindPromotion,
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{Name: "rhoai-tekton"},
	}

	details, err := resolveProviderDetails(context.Background(), c, "ns-1", stage)
	require.NoError(t, err)
	require.Equal(t, "cr-promotion-pipeline", details.pipelineName)
}

func TestResolveProviderDetails_FullCR_OverridesServiceAccountTimeoutAndWorkspaces(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(
		&modelopsv1alpha1.IntakeProviderConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "rhoai-tekton", Namespace: "ns-1"},
			Spec: modelopsv1alpha1.IntakeProviderConfigSpec{
				ProviderType:        "tekton",
				SandboxPipelineName: "cr-sandbox-pipeline",
				ServiceAccountName:  "custom-pipeline-sa",
				PipelineTimeout:     &metav1.Duration{Duration: 3600000000000}, // 1h, in ns
				Workspaces: []modelopsv1alpha1.IntakeProviderWorkspace{
					{Name: "shared-workspace", PersistentVolumeClaim: "custom-pvc"},
					{Name: "extra-config", ConfigMap: "extra-configmap"},
				},
			},
		},
	).Build()

	stage := stagecommon.StageSpec{
		Name:              "sandbox",
		StageKind:         stagecommon.StageKindSandbox,
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{Name: "rhoai-tekton"},
	}

	details, err := resolveProviderDetails(context.Background(), c, "ns-1", stage)
	require.NoError(t, err)
	require.Equal(t, "cr-sandbox-pipeline", details.pipelineName)
	require.Equal(t, "custom-pipeline-sa", details.serviceAccountName)
	require.Equal(t, int64(3600000000000), int64(details.pipelineTimeout.Duration))
	require.Len(t, details.workspaces, 2)
	require.Equal(t, "shared-workspace", details.workspaces[0].Name)
	require.NotNil(t, details.workspaces[0].PersistentVolumeClaim)
	require.Equal(t, "custom-pvc", details.workspaces[0].PersistentVolumeClaim.ClaimName)
	require.Equal(t, "extra-config", details.workspaces[1].Name)
	require.NotNil(t, details.workspaces[1].ConfigMap)
	require.Equal(t, "extra-configmap", details.workspaces[1].ConfigMap.Name)
}

func TestResolveProviderDetails_PartiallyPopulatedCR_FallsBackToDefaultsForUnsetFields(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(
		&modelopsv1alpha1.IntakeProviderConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "rhoai-tekton", Namespace: "ns-1"},
			Spec: modelopsv1alpha1.IntakeProviderConfigSpec{
				ProviderType:        "tekton",
				SandboxPipelineName: "cr-sandbox-pipeline",
				// ServiceAccountName, PipelineTimeout, Workspaces all left unset.
			},
		},
	).Build()

	stage := stagecommon.StageSpec{
		Name:              "sandbox",
		StageKind:         stagecommon.StageKindSandbox,
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{Name: "rhoai-tekton"},
	}

	details, err := resolveProviderDetails(context.Background(), c, "ns-1", stage)
	require.NoError(t, err)
	require.Equal(t, "cr-sandbox-pipeline", details.pipelineName, "set field is honored")
	require.Equal(t, "pipeline", details.serviceAccountName, "unset field falls back to today's hardcoded default")
	require.Equal(t, int64(0), int64(details.pipelineTimeout.Duration), "unset field falls back to unbounded")
	require.Len(t, details.workspaces, 3, "unset field falls back to the 3 hardcoded bindings")
	requireDefaultWorkspaceShape(t, details.workspaces)
}

func TestResolveProviderDetails_MissingCR_ReturnsError(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	stage := stagecommon.StageSpec{
		Name:              "sandbox",
		StageKind:         stagecommon.StageKindSandbox,
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{Name: "does-not-exist"},
	}

	_, err := resolveProviderDetails(context.Background(), c, "ns-1", stage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does-not-exist")
}

func TestResolveProviderDetails_UnsupportedKind_ReturnsErrorWithoutLookup(t *testing.T) {
	// Deliberately no IntakeProviderConfig object exists in the fake
	// client -- if resolveProviderDetails attempted a Get anyway before
	// checking Kind, this would surface as a NotFound error instead of
	// the "unsupported kind" error this test asserts on.
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	stage := stagecommon.StageSpec{
		Name:              "sandbox",
		StageKind:         stagecommon.StageKindSandbox,
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{Name: "whatever", Kind: "SomeOtherKind"},
	}

	_, err := resolveProviderDetails(context.Background(), c, "ns-1", stage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported provider config kind")
	require.Contains(t, err.Error(), "SomeOtherKind")
}

func TestResolveProviderDetails_EmptyKind_DefaultsToIntakeProviderConfig(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(
		&modelopsv1alpha1.IntakeProviderConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "rhoai-tekton", Namespace: "ns-1"},
			Spec:       modelopsv1alpha1.IntakeProviderConfigSpec{ProviderType: "tekton", SandboxPipelineName: "cr-sandbox-pipeline"},
		},
	).Build()
	stage := stagecommon.StageSpec{
		Name:              "sandbox",
		StageKind:         stagecommon.StageKindSandbox,
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{Name: "rhoai-tekton", Kind: ""},
	}

	details, err := resolveProviderDetails(context.Background(), c, "ns-1", stage)
	require.NoError(t, err)
	require.Equal(t, "cr-sandbox-pipeline", details.pipelineName)
}

func TestResolveProviderDetails_UnsupportedProviderType_ReturnsError(t *testing.T) {
	// Go-level defensive check: the CRD's +kubebuilder:validation:Enum
	// only restricts writes through a real API server, which the fake
	// client used here does not enforce -- so this exercises
	// resolveProviderDetails' own guard, not the apiserver's.
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(
		&modelopsv1alpha1.IntakeProviderConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "rhoai-tekton", Namespace: "ns-1"},
			Spec:       modelopsv1alpha1.IntakeProviderConfigSpec{ProviderType: "sagemaker"},
		},
	).Build()
	stage := stagecommon.StageSpec{
		Name:              "sandbox",
		StageKind:         stagecommon.StageKindSandbox,
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{Name: "rhoai-tekton"},
	}

	_, err := resolveProviderDetails(context.Background(), c, "ns-1", stage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "sagemaker")
	require.Contains(t, err.Error(), "tekton")
}

// requireDefaultWorkspaceShape asserts the exact 3-binding shape
// buildPipelineRun has hardcoded since Phase 0-4 (shared-workspace PVC,
// manifests/custom-mmlu ConfigMaps).
func requireDefaultWorkspaceShape(t *testing.T, workspaces []tektonv1.WorkspaceBinding) {
	t.Helper()
	byName := map[string]tektonv1.WorkspaceBinding{}
	for _, w := range workspaces {
		byName[w.Name] = w
	}

	shared, ok := byName["shared-workspace"]
	require.True(t, ok)
	require.NotNil(t, shared.PersistentVolumeClaim)
	require.Equal(t, "guidellm-output-pvc", shared.PersistentVolumeClaim.ClaimName)

	manifests, ok := byName["manifests"]
	require.True(t, ok)
	require.NotNil(t, manifests.ConfigMap)
	require.Equal(t, "mmlu-manifest", manifests.ConfigMap.Name)

	customMMLU, ok := byName["custom-mmlu"]
	require.True(t, ok)
	require.NotNil(t, customMMLU.ConfigMap)
	require.Equal(t, "custom-mmlu", customMMLU.ConfigMap.Name)
}
