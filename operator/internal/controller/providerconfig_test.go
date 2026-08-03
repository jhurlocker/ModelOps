package controller

// Phase 5 of REFACTOR_PLAN.md: ModelLifecycleProfile.Spec.ProviderConfigRef
// and the trivial internal/stages/noop.StageRunner.
//
// Two things are proved here, deliberately kept in separate test
// functions:
//
//  1. TestModelRequest_ProviderConfigRef_ResolvesRealPipelineRunShape_EndToEnd
//     -- a profile using ProviderConfigRef (and NO inline
//     Workflow.PipelineRef) produces a PipelineRun whose pipelineRef,
//     serviceAccountName, and workspaces all come from the referenced
//     IntakeProviderConfig CR, not from any hardcoded default. This is
//     the "prove the new path works" characterization test, parallel to
//     Phase 1's TestModelRequest_PromotionUsesProfilePromotionPipelineRef_EndToEnd.
//  2. TestModelRequest_FullLifecycle_TektonAndNoopStageRunners_ReachSameTerminalPhase
//     -- the actual evidence the provider abstraction is real: the same
//     Reconcile code path (capacity-plan gating, RBAC provisioning,
//     phase-transition logic) drives a ModelRequest to the identical
//     terminal Status.Phase == "Succeeded" whether it's wired to the
//     real tekton.StageRunner or the noop.StageRunner stub.
//
// Every fixture below uses newProfile/newModelRequest/
// setupSucceededCapacityPlan exactly as every pre-Phase-5 test in this
// package does -- confirming ProviderConfigRef is additive, not a
// change to how any *existing* fixture behaves.

import (
	"context"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stages/noop"

	"github.com/stretchr/testify/require"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// newProfileWithProviderConfigRef mirrors defaultProfileSpec/newProfile
// but deliberately omits Workflow.PipelineRef, so the resulting profile
// can only resolve a pipeline name via ProviderConfigRef -- proving the
// new path works on its own, not merely as an inert addition alongside
// the deprecated fallback.
func newProfileWithProviderConfigRef(t *testing.T, ns, name, platformConfigName, providerConfigName string) *modelopsv1alpha1.ModelLifecycleProfile {
	t.Helper()
	return newProfile(t, ns, name, modelopsv1alpha1.ModelLifecycleProfileSpec{
		Workflow:          modelopsv1alpha1.WorkflowRef{Engine: "tekton"},
		PlatformConfigRef: platformConfigName,
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{Name: providerConfigName},
	})
}

func newIntakeProviderConfig(t *testing.T, ns, name string, spec modelopsv1alpha1.IntakeProviderConfigSpec) *modelopsv1alpha1.IntakeProviderConfig {
	t.Helper()
	cfg := &modelopsv1alpha1.IntakeProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       spec,
	}
	if err := k8sClient.Create(context.Background(), cfg); err != nil {
		t.Fatalf("failed to create IntakeProviderConfig %s/%s: %v", ns, name, err)
	}
	return cfg
}

func TestModelRequest_ProviderConfigRef_ResolvesRealPipelineRunShape_EndToEnd(t *testing.T) {
	ns := newTestNamespace(t)
	ensureNamespace(t, "staging")
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newIntakeProviderConfig(t, ns, "provider-cfg-1", modelopsv1alpha1.IntakeProviderConfigSpec{
		ProviderType:          "tekton",
		SandboxPipelineName:   "cr-sandbox-pipeline",
		PromotionPipelineName: "cr-promotion-pipeline",
		ServiceAccountName:    "cr-pipeline-sa",
		Workspaces: []modelopsv1alpha1.IntakeProviderWorkspace{
			{Name: "shared-workspace", PersistentVolumeClaim: "cr-output-pvc"},
		},
	})
	newProfileWithProviderConfigRef(t, ns, "profile-1", "cfg-1", "provider-cfg-1")
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")

	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "SandboxRunning", mr.Status.Phase)

	var pr tektonv1.PipelineRun
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "mr-1-sandbox"), &pr))
	require.Equal(t, "cr-sandbox-pipeline", pr.Spec.PipelineRef.Name, "sandbox pipeline name must come from the IntakeProviderConfig CR, not any hardcoded default")
	require.Equal(t, "cr-pipeline-sa", pr.Spec.TaskRunTemplate.ServiceAccountName)
	require.Len(t, pr.Spec.Workspaces, 1)
	require.Equal(t, "shared-workspace", pr.Spec.Workspaces[0].Name)
	require.NotNil(t, pr.Spec.Workspaces[0].PersistentVolumeClaim)
	require.Equal(t, "cr-output-pvc", pr.Spec.Workspaces[0].PersistentVolumeClaim.ClaimName)

	// Drive to promotion and confirm the promotion PipelineRun resolves
	// its own (different) pipeline name from the same CR.
	setPipelineRunCondition(t, ns, "mr-1-sandbox", corev1.ConditionTrue, "sandbox ok")
	mr, _, err = reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "PromotionRunning", mr.Status.Phase)

	var promoPR tektonv1.PipelineRun
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "mr-1-promotion-staging"), &promoPR))
	require.Equal(t, "cr-promotion-pipeline", promoPR.Spec.PipelineRef.Name)
	require.Equal(t, "cr-pipeline-sa", promoPR.Spec.TaskRunTemplate.ServiceAccountName)
}

func TestModelRequest_ProviderConfigRef_MissingCR_SurfacesResolveErrorNotSilentDefault(t *testing.T) {
	ns := newTestNamespace(t)
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfileWithProviderConfigRef(t, ns, "profile-1", "cfg-1", "does-not-exist")
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")

	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.Error(t, err, "an unresolvable ProviderConfigRef must surface as a reconcile error, not silently fall back to a default pipeline")
	require.Contains(t, err.Error(), "does-not-exist")

	var pr tektonv1.PipelineRun
	getErr := k8sClient.Get(context.Background(), nsName(ns, "mr-1-sandbox"), &pr)
	require.Error(t, getErr, "no PipelineRun should ever be created when the provider config cannot be resolved")
}

// requireServiceAccountExists confirms ensurePromotionNamespaceRBAC's
// "pipeline" ServiceAccount was provisioned in ns -- used below to prove
// the reconciler's RBAC-provisioning decision logic ran identically
// regardless of which StageRunner is injected.
func requireServiceAccountExists(t *testing.T, ns string) {
	t.Helper()
	var sa corev1.ServiceAccount
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "pipeline"), &sa))
}

// TestModelRequest_FullLifecycle_TektonAndNoopStageRunners_ReachSameTerminalPhase
// is the concrete proof, per REFACTOR_PLAN.md Phase 5, that
// ModelRequestReconciler's core logic is genuinely engine-agnostic: the
// exact same reconciler code (capacity-plan gating, RBAC provisioning,
// phase-transition decisions) drives a ModelRequest to the identical
// terminal phase whether it's wired to the real tekton.StageRunner or
// the trivial noop.StageRunner stub.
//
// The two subtests necessarily reconcile a different number of times --
// tekton.StageRunner needs its PipelineRun's condition flipped between
// reconciles (same as every other test in this file), while
// noop.StageRunner reports every stage immediately Succeeded, so a
// single Reconcile call falls through sandbox -> promotion -> Succeeded
// in one pass. That timing difference is expected, not a discrepancy;
// the identical terminal Status.Phase and identical RBAC side effects
// are what this test actually asserts on.
func TestModelRequest_FullLifecycle_TektonAndNoopStageRunners_ReachSameTerminalPhase(t *testing.T) {
	t.Run("tekton.StageRunner", func(t *testing.T) {
		ns := newTestNamespace(t)
		ensureNamespace(t, "staging")
		newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
		newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
		newModelRequest(t, ns, "mr-1", "profile-1", nil)
		setupSucceededCapacityPlan(t, ns, "mr-1")

		mr, _, err := reconcileModelRequest(t, ns, "mr-1")
		require.NoError(t, err)
		require.Equal(t, "SandboxRunning", mr.Status.Phase)

		setPipelineRunCondition(t, ns, "mr-1-sandbox", corev1.ConditionTrue, "sandbox ok")
		mr, _, err = reconcileModelRequest(t, ns, "mr-1")
		require.NoError(t, err)
		require.Equal(t, "PromotionRunning", mr.Status.Phase)

		setPipelineRunCondition(t, ns, "mr-1-promotion-staging", corev1.ConditionTrue, "promotion ok")
		mr, _, err = reconcileModelRequest(t, ns, "mr-1")
		require.NoError(t, err)
		require.Equal(t, "Succeeded", mr.Status.Phase)

		requireServiceAccountExists(t, ns)
		requireServiceAccountExists(t, "staging")
	})

	t.Run("noop.StageRunner", func(t *testing.T) {
		ns := newTestNamespace(t)
		ensureNamespace(t, "staging")
		newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
		newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
		newModelRequest(t, ns, "mr-1", "profile-1", nil)
		setupSucceededCapacityPlan(t, ns, "mr-1")

		r := &ModelRequestReconciler{
			Client:        k8sClient,
			Scheme:        testRuntimeScheme(),
			StageHandlers: newStageHandlers(),
			StageRunners:  newStageRunners(k8sClient, testRuntimeScheme(), &noop.StageRunner{}),
		}
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "mr-1")})
		require.NoError(t, err)

		mr := getModelRequest(t, ns, "mr-1")
		require.Equal(t, "Succeeded", mr.Status.Phase, "noop.StageRunner completes every stage in a single reconcile pass")

		requireServiceAccountExists(t, ns)
		requireServiceAccountExists(t, "staging")

		var pr tektonv1.PipelineRun
		getErr := k8sClient.Get(context.Background(), nsName(ns, "mr-1-sandbox"), &pr)
		require.Error(t, getErr, "noop.StageRunner must never create a real PipelineRun")
	})
}
