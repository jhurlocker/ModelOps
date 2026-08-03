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
// the deprecated fallback. Stages carries its own per-stage
// ProviderConfigRef (see testDefaultStages) -- since Phase 7, the
// top-level Spec.ProviderConfigRef set here is no longer consulted by
// Reconcile at all (it was only ever read by the now-removed
// defaultStages()); it's kept set anyway to mirror the live
// gitops/components/runtime-config/lifecycleprofile.yaml profile's
// shape (both set, only Stages' copy actually doing anything).
func newProfileWithProviderConfigRef(t *testing.T, ns, name, platformConfigName, providerConfigName string) *modelopsv1alpha1.ModelLifecycleProfile {
	t.Helper()
	ref := &modelopsv1alpha1.ProviderConfigRef{Name: providerConfigName}
	return newProfile(t, ns, name, modelopsv1alpha1.ModelLifecycleProfileSpec{
		Workflow:          modelopsv1alpha1.WorkflowRef{Engine: "tekton"},
		PlatformConfigRef: platformConfigName,
		ProviderConfigRef: ref,
		Stages:            testDefaultStages(ref),
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
	require.Equal(t, "sandboxRunning", mr.Status.Phase)

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
	require.Equal(t, "promotionRunning", mr.Status.Phase)

	var promoPR tektonv1.PipelineRun
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "mr-1-promotion-staging"), &promoPR))
	require.Equal(t, "cr-promotion-pipeline", promoPR.Spec.PipelineRef.Name)
	require.Equal(t, "cr-pipeline-sa", promoPR.Spec.TaskRunTemplate.ServiceAccountName)
}

// TestModelRequest_ProviderConfigRef_MissingCR_SetsProviderConfigLookupFailed_WithBoundedRequeue
// was TestModelRequest_ProviderConfigRef_MissingCR_SurfacesResolveErrorNotSilentDefault
// prior to Phase 7 of REFACTOR_PLAN.md: an unresolvable ProviderConfigRef
// used to surface as a raw *reconcile* error (ctrl.Result{RequeueAfter:
// 5s}, err), the same generic silent-retry path every other EnsureRun
// error fell into, with no visible ModelRequest.Status change at all.
// Phase 7 gives this its own "ProviderConfigLookupFailed" status reason
// (matching the existing ProfileLookupFailed/PlatformConfigLookupFailed
// pattern) with a distinct, longer 30s bounded requeue -- long enough to
// tolerate the referenced IntakeProviderConfig being created moments
// later by a separate GitOps sync, without masking the failure as
// permanent (no status update at all) or hot-looping at the 5s
// transientErrorRequeueDelay every other stage-walk error uses.
func TestModelRequest_ProviderConfigRef_MissingCR_SetsProviderConfigLookupFailed_WithBoundedRequeue(t *testing.T) {
	ns := newTestNamespace(t)
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfileWithProviderConfigRef(t, ns, "profile-1", "cfg-1", "does-not-exist")
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")

	mr, res, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err, "a ProviderConfigRef lookup failure must be a visible status update, not a raw reconcile error")
	require.Equal(t, "ProviderConfigLookupFailed", mr.Status.Phase)
	require.Contains(t, mr.Status.Message, "does-not-exist")
	require.Equal(t, providerConfigLookupRequeueDelay, res.RequeueAfter,
		"must use the dedicated 30s bounded requeue, distinct from the generic 5s transientErrorRequeueDelay")

	var pr tektonv1.PipelineRun
	getErr := k8sClient.Get(context.Background(), nsName(ns, "mr-1-sandbox"), &pr)
	require.Error(t, getErr, "no PipelineRun should ever be created when the provider config cannot be resolved")
}

// TestModelRequest_ProviderConfigRef_UnsupportedKind_SetsProviderConfigLookupFailed
// exercises the "unsupported Kind" branch of resolveProviderDetails
// (deterministic -- no Get/NotFound timing involved at all, unlike the
// missing-CR case above), proving ProviderConfigLookupFailed covers
// every resolveProviderDetails failure class, not just NotFound.
func TestModelRequest_ProviderConfigRef_UnsupportedKind_SetsProviderConfigLookupFailed(t *testing.T) {
	ns := newTestNamespace(t)
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", modelopsv1alpha1.ModelLifecycleProfileSpec{
		Workflow:          modelopsv1alpha1.WorkflowRef{Engine: "tekton"},
		PlatformConfigRef: "cfg-1",
		Stages: testDefaultStages(&modelopsv1alpha1.ProviderConfigRef{
			Name: "whatever", Kind: "SomeOtherKind",
		}),
	})
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")

	mr, res, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "ProviderConfigLookupFailed", mr.Status.Phase)
	require.Contains(t, mr.Status.Message, "unsupported provider config kind")
	require.Equal(t, providerConfigLookupRequeueDelay, res.RequeueAfter)
}

// TestModelRequest_ProviderConfigLookupFailed_KeepsRequeueingUntilFixed
// pins that the bounded requeue is re-issued on every reconcile of the
// same unresolved state, not just the first -- distinguishing this
// reason from the older *LookupFailed reasons (failRequest), which stop
// requeueing entirely after the first status write (relying solely on
// the next unrelated watch event or cache resync). See
// docs/REFACTOR_PLAN.md Phase 7's backlog note: closing that gap for
// all four reasons via Watches() is deliberately deferred, not solved
// here.
func TestModelRequest_ProviderConfigLookupFailed_KeepsRequeueingUntilFixed(t *testing.T) {
	ns := newTestNamespace(t)
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfileWithProviderConfigRef(t, ns, "profile-1", "cfg-1", "does-not-exist")
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")

	_, res1, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, providerConfigLookupRequeueDelay, res1.RequeueAfter)

	// Second reconcile: Phase/Message are now identical to what's
	// already persisted, but the requeue must still be present.
	_, res2, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, providerConfigLookupRequeueDelay, res2.RequeueAfter,
		"must keep requeueing even once Phase/Message stop changing, unlike failRequest's other reasons")
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
		require.Equal(t, "sandboxRunning", mr.Status.Phase)

		setPipelineRunCondition(t, ns, "mr-1-sandbox", corev1.ConditionTrue, "sandbox ok")
		mr, _, err = reconcileModelRequest(t, ns, "mr-1")
		require.NoError(t, err)
		require.Equal(t, "promotionRunning", mr.Status.Phase)

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
