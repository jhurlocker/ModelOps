package controller

// Characterization tests for ModelRequestReconciler's CURRENT behavior
// (Phase 0 of REFACTOR_PLAN.md). These pin today's actual
// capacity-planning -> sandbox -> promotion sequence, param output, and
// status transitions as tests, so Phases 2-6 (which relocate this logic
// without intending to change it) have a regression net.
//
// Several tests below are explicitly labeled "KnownBehavior": they pin
// something not yet fixed (promotion namespaces not being gated on the
// previous namespace's success -- a later-phase target). These are
// captured as-is, not treated as behavior to preserve forever.
//
// Phase 1 of REFACTOR_PLAN.md landed the fixes for the *other* items this
// comment used to describe (the duplicate gpu-count-override param, the
// ignored `profile` argument in promotionPipelineNameOrDefault, the
// hardcoded minioadmin credential fallback, and Create-call idempotency)
// -- see the tests below without a "KnownBug"/"KnownBehavior" prefix for
// the new, corrected behavior.

import (
	"context"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"
	"github.com/jhurlocker/modelops-operator/internal/stages/capacityplanning"

	"github.com/stretchr/testify/require"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// newModelRequestReconciler wires the reconciler with the REAL
// tekton.StageRunner (not a fake) for the "PipelineRun" kind and the
// real capacityplanning.StageRunner for "CapacityPlan", so all of this
// file's PipelineRun-shaped assertions (PipelineRef, OwnerReferences,
// Params, conditions) continue to exercise genuine Tekton behavior
// end-to-end -- this is Phase 4/6's regression net for "relocate, don't
// change" (see docs/PHASE_LOG.md). Tests that specifically want to
// prove the reconciler works with zero Tekton involvement construct a
// ModelRequestReconciler directly with a stagecommon.FakeStageRunner
// registered under "PipelineRun" instead of using this helper.
func newModelRequestReconciler() *ModelRequestReconciler {
	scheme := testRuntimeScheme()
	return &ModelRequestReconciler{
		Client:        k8sClient,
		Scheme:        scheme,
		StageHandlers: newStageHandlers(),
		StageRunners:  newStageRunners(k8sClient, scheme, nil),
	}
}

func reconcileModelRequest(t *testing.T, ns, name string) (*modelopsv1alpha1.ModelRequest, ctrl.Result, error) {
	t.Helper()
	r := newModelRequestReconciler()
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, name)})
	mr := getModelRequest(t, ns, name)
	return mr, res, err
}

func findParam(params tektonv1.Params, name string) (string, bool) {
	for _, p := range params {
		if p.Name == name {
			return p.Value.StringVal, true
		}
	}
	return "", false
}

func findAllParams(params tektonv1.Params, name string) []string {
	var out []string
	for _, p := range params {
		if p.Name == name {
			out = append(out, p.Value.StringVal)
		}
	}
	return out
}

// setPipelineRunCondition sets the PipelineRun's Succeeded condition via
// the status subresource, matching what a real Tekton controller would
// do, so ModelRequestReconciler's Status.GetCondition("Succeeded") calls
// see a real value.
func setPipelineRunCondition(t *testing.T, ns, name string, status corev1.ConditionStatus, message string) {
	t.Helper()
	var pr tektonv1.PipelineRun
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, name), &pr))
	pr.Status = tektonv1.PipelineRunStatus{
		Status: duckv1.Status{
			Conditions: duckv1.Conditions{
				{Type: apis.ConditionSucceeded, Status: status, Message: message},
			},
		},
	}
	require.NoError(t, k8sClient.Status().Update(context.Background(), &pr))
}

// --- Lookup failure branches ---

func TestModelRequest_MissingProfile_SetsProfileLookupFailed(t *testing.T) {
	ns := newTestNamespace(t)
	newModelRequest(t, ns, "mr-1", "does-not-exist", nil)

	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "ProfileLookupFailed", mr.Status.Phase)
}

// TestModelRequest_ProfileWithNoStages_SetsNoStagesConfigured is Phase 7's
// regression test for the guard that replaced the removed defaultStages()
// fallback: a ModelLifecycleProfile with an empty/nil Spec.Stages used to
// be silently synthesized into the pre-Phase-6 3-stage default; now it's
// a real, visible configuration error instead of a silent no-op walk of
// zero stages.
func TestModelRequest_ProfileWithNoStages_SetsNoStagesConfigured(t *testing.T) {
	ns := newTestNamespace(t)
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", modelopsv1alpha1.ModelLifecycleProfileSpec{
		Workflow:          modelopsv1alpha1.WorkflowRef{Engine: "tekton", PipelineRef: "model-intake-sandbox"},
		PlatformConfigRef: "cfg-1",
		// Stages deliberately left nil/empty.
	})
	newModelRequest(t, ns, "mr-1", "profile-1", nil)

	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "NoStagesConfigured", mr.Status.Phase)
	require.Contains(t, mr.Status.Message, "profile-1")
}

func TestModelRequest_MissingPlatformConfig_SetsPlatformConfigLookupFailed(t *testing.T) {
	ns := newTestNamespace(t)
	newProfile(t, ns, "profile-1", modelopsv1alpha1.ModelLifecycleProfileSpec{
		Workflow:          modelopsv1alpha1.WorkflowRef{Engine: "tekton", PipelineRef: "model-intake-sandbox"},
		PlatformConfigRef: "does-not-exist",
	})
	newModelRequest(t, ns, "mr-1", "profile-1", nil)

	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "PlatformConfigLookupFailed", mr.Status.Phase)
}

// --- Capacity planning phase ---

func TestModelRequest_FirstReconcile_CreatesCapacityPlan(t *testing.T) {
	ns := newTestNamespace(t)
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
	newModelRequest(t, ns, "mr-1", "profile-1", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.Requirements = &modelopsv1alpha1.ModelRequirements{
			BenchmarkTargets: modelopsv1alpha1.BenchmarkTargets{ContextLength: 8192, ExpectedConcurrency: 4},
		}
	})

	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "capacityRunning", mr.Status.Phase)

	var plan modelopsv1alpha1.CapacityPlan
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "mr-1-capacity"), &plan))
	require.Equal(t, 8192, plan.Spec.ContextLength)
	require.Equal(t, 4, plan.Spec.Concurrency)
	require.Len(t, plan.OwnerReferences, 1, "CapacityPlan should be owned by the ModelRequest for GC")
	require.Equal(t, "mr-1", plan.OwnerReferences[0].Name)
}

func TestModelRequest_CapacityPlanNotYetSucceeded_StaysInCapacityPlanningPhase(t *testing.T) {
	ns := newTestNamespace(t)
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
	newModelRequest(t, ns, "mr-1", "profile-1", nil)

	// First reconcile creates the CapacityPlan (still empty Phase).
	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)

	// Second reconcile: CapacityPlan exists but its Phase is still "".
	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "capacityRunning", mr.Status.Phase)
	require.Contains(t, mr.Status.Message, "Waiting for capacity plan")
}

// --- Sandbox phase ---

func setupSucceededCapacityPlan(t *testing.T, ns, mrName string) {
	t.Helper()
	plan := &modelopsv1alpha1.CapacityPlan{
		ObjectMeta: metav1.ObjectMeta{Name: mrName + "-capacity", Namespace: ns},
		Spec:       modelopsv1alpha1.CapacityPlanSpec{ModelRef: modelopsv1alpha1.CapacityPlanModelRef{ModelRequestName: mrName}},
	}
	require.NoError(t, k8sClient.Create(context.Background(), plan))
	plan.Status.Phase = "Succeeded"
	plan.Status.GPUsNeeded = 2
	plan.Status.GPUModel = "NVIDIA-A100-40GB"
	require.NoError(t, k8sClient.Status().Update(context.Background(), plan))
}

func TestModelRequest_CapacityPlanSucceeded_CreatesSandboxPipelineRun(t *testing.T) {
	ns := newTestNamespace(t)
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")

	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "sandboxRunning", mr.Status.Phase)
	require.Equal(t, "mr-1-sandbox", mr.Status.SandboxPipelineRunName)

	var pr tektonv1.PipelineRun
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "mr-1-sandbox"), &pr))
	require.Equal(t, "model-intake-sandbox", pr.Spec.PipelineRef.Name)
	require.Len(t, pr.OwnerReferences, 1)

	gpuOverride, ok := findParam(pr.Spec.Params, "gpu-count-override")
	require.True(t, ok)
	require.Equal(t, "2", gpuOverride, "gpu-count-override should come from CapacityPlan.Status.GPUsNeeded when no override is set")
}

func TestModelRequest_SandboxPipelineNameOrDefault_PrecedenceOrder(t *testing.T) {
	ns := newTestNamespace(t)
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	// Profile sets a non-default pipeline ref.
	newProfile(t, ns, "profile-1", modelopsv1alpha1.ModelLifecycleProfileSpec{
		Workflow:          modelopsv1alpha1.WorkflowRef{Engine: "tekton", PipelineRef: "profile-sandbox-pipeline"},
		PlatformConfigRef: "cfg-1",
		Stages:            testDefaultStages(nil),
	})
	// mr.Spec.PipelineRef, if set, wins over the profile's.
	newModelRequest(t, ns, "mr-1", "profile-1", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.PipelineRef = "request-level-pipeline"
	})
	setupSucceededCapacityPlan(t, ns, "mr-1")

	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)

	var pr tektonv1.PipelineRun
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "mr-1-sandbox"), &pr))
	require.Equal(t, "request-level-pipeline", pr.Spec.PipelineRef.Name)
}

func TestModelRequest_SandboxPipelineRunPending_NoDuplicateCreated(t *testing.T) {
	ns := newTestNamespace(t)
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")

	// First reconcile creates the sandbox PipelineRun (no condition set yet).
	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)

	var before tektonv1.PipelineRun
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "mr-1-sandbox"), &before))

	// Second reconcile: PipelineRun exists with no/unknown condition.
	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "sandboxRunning", mr.Status.Phase)

	var after tektonv1.PipelineRun
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "mr-1-sandbox"), &after))
	require.Equal(t, before.ResourceVersion, after.ResourceVersion, "must not recreate/modify an already-running sandbox PipelineRun")
}

func TestModelRequest_SandboxPipelineRunFailed_SetsFailedPhase(t *testing.T) {
	ns := newTestNamespace(t)
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")

	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)

	setPipelineRunCondition(t, ns, "mr-1-sandbox", corev1.ConditionFalse, "compliance scan failed")

	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "Failed", mr.Status.Phase)
	require.Contains(t, mr.Status.Message, "compliance scan failed")
}

// --- Promotion phase ---

func setupSucceededSandbox(t *testing.T, ns, mrName string) {
	t.Helper()
	_, _, err := reconcileModelRequest(t, ns, mrName)
	require.NoError(t, err)
	setPipelineRunCondition(t, ns, mrName+"-sandbox", corev1.ConditionTrue, "All Tasks Completed")
}

func TestModelRequest_SandboxSucceeded_CreatesPromotionPipelineRun_DefaultNamespace(t *testing.T) {
	ns := newTestNamespace(t)
	ensureNamespace(t, "staging")
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")
	setupSucceededSandbox(t, ns, "mr-1")

	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "promotionRunning", mr.Status.Phase)

	var pr tektonv1.PipelineRun
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "mr-1-promotion-staging"), &pr),
		"default promotion namespace is 'staging' when Requirements is nil")

	runRegister, ok := findParam(pr.Spec.Params, "run-register")
	require.True(t, ok)
	require.Equal(t, "true", runRegister, "the only namespace is both first and last")
}

func TestModelRequest_MultiplePromotionNamespaces_KnownBehavior_AllCreatedInSameReconcilePass(t *testing.T) {
	// This pins a specific, surprising piece of CURRENT behavior: the
	// promotion loop in Reconcile does not return/break after creating a
	// missing PipelineRun for one namespace -- it `continue`s to the next
	// namespace in the *same* reconcile call. So if namespace[0] and
	// namespace[1] both lack a PipelineRun, BOTH get created in one pass,
	// before namespace[0] has even started, let alone succeeded. This
	// looks like it contradicts "promotion is sequential unless a
	// profile explicitly permits parallel execution" (see AGENTS.md).
	// This test captures today's actual behavior exactly as-is, and is
	// flagged as a likely target for a deliberate fix in a later phase
	// -- not something to silently "fix" inside this test.
	ns := newTestNamespace(t)
	ensureNamespace(t, "staging")
	ensureNamespace(t, "preprod")
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{ApprovalApiUrl: "https://approve.example.com"})
	newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
	newModelRequest(t, ns, "mr-1", "profile-1", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.Requirements = &modelopsv1alpha1.ModelRequirements{
			PromotionNamespaces: []string{"staging", "preprod"},
		}
	})
	setupSucceededCapacityPlan(t, ns, "mr-1")
	setupSucceededSandbox(t, ns, "mr-1")

	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "promotionRunning", mr.Status.Phase)

	var stagingPR, preprodPR tektonv1.PipelineRun
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "mr-1-promotion-staging"), &stagingPR),
		"first namespace's PipelineRun should exist")
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "mr-1-promotion-preprod"), &preprodPR),
		"KNOWN BEHAVIOR: second namespace's PipelineRun is ALSO created in the same pass, "+
			"without waiting for the first namespace to succeed")

	approvalURL, hasApproval := findParam(stagingPR.Spec.Params, "approval-api-url")
	require.True(t, hasApproval)
	require.Equal(t, "https://approve.example.com", approvalURL)

	_, preprodHasApproval := findParam(preprodPR.Spec.Params, "approval-api-url")
	require.False(t, preprodHasApproval, "only the first promotion namespace gets an approval gate")

	stagingRegister, _ := findParam(stagingPR.Spec.Params, "run-register")
	preprodRegister, _ := findParam(preprodPR.Spec.Params, "run-register")
	require.Equal(t, "false", stagingRegister, "first namespace is not last")
	require.Equal(t, "true", preprodRegister, "second namespace is last")
}

func TestModelRequest_OnePromotionSucceededOneRunning_StaysPromotionRunning(t *testing.T) {
	ns := newTestNamespace(t)
	ensureNamespace(t, "staging")
	ensureNamespace(t, "preprod")
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
	newModelRequest(t, ns, "mr-1", "profile-1", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.Requirements = &modelopsv1alpha1.ModelRequirements{PromotionNamespaces: []string{"staging", "preprod"}}
	})
	setupSucceededCapacityPlan(t, ns, "mr-1")
	setupSucceededSandbox(t, ns, "mr-1")

	// Creates both promotion PipelineRuns (see KnownBehavior test above).
	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)

	setPipelineRunCondition(t, ns, "mr-1-promotion-staging", corev1.ConditionTrue, "done")
	// preprod left with no condition (still running).

	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "promotionRunning", mr.Status.Phase)
}

func TestModelRequest_PromotionFailed_ReportsFirstFailureEncountered(t *testing.T) {
	ns := newTestNamespace(t)
	ensureNamespace(t, "staging")
	ensureNamespace(t, "preprod")
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
	newModelRequest(t, ns, "mr-1", "profile-1", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.Requirements = &modelopsv1alpha1.ModelRequirements{PromotionNamespaces: []string{"staging", "preprod"}}
	})
	setupSucceededCapacityPlan(t, ns, "mr-1")
	setupSucceededSandbox(t, ns, "mr-1")

	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)

	setPipelineRunCondition(t, ns, "mr-1-promotion-staging", corev1.ConditionFalse, "boom-staging")
	setPipelineRunCondition(t, ns, "mr-1-promotion-preprod", corev1.ConditionFalse, "boom-preprod")

	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "Failed", mr.Status.Phase)
	require.Contains(t, mr.Status.Message, "staging")
	require.Contains(t, mr.Status.Message, "boom-staging")
	require.NotContains(t, mr.Status.Message, "preprod",
		"loop returns on the FIRST failed namespace it encounters, in PromotionNamespaces order")
}

func TestModelRequest_AllPromotionsSucceeded_SetsSucceededPhase(t *testing.T) {
	ns := newTestNamespace(t)
	ensureNamespace(t, "staging")
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")
	setupSucceededSandbox(t, ns, "mr-1")

	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	setPipelineRunCondition(t, ns, "mr-1-promotion-staging", corev1.ConditionTrue, "done")

	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "Succeeded", mr.Status.Phase)
	require.Equal(t, "Model onboarding completed successfully", mr.Status.Message)
}

// TestModelRequest_AllPromotionsSucceeded_SetsPromotionPipelineRunName is a
// regression test for a real bug caught only by live-cluster
// verification, not envtest or any other Phase 0-5 characterization
// test: stagewalk.Progress.Name is the ProfileStageSpec's own Name
// ("promotion"), with the target namespace recorded separately in
// Progress.Namespace -- not "promotion-<namespace>" the way the
// per-invocation StageSpec.Name built by promotion.Handler is. The
// first draft of lastPromotionProgress (Phase 6) assumed the latter
// (prefix-matched "promotion-"), so it never matched anything and
// Status.PromotionPipelineRunName silently stayed empty forever. No
// pre-existing test asserted on this field's value at all, which is
// exactly why a real cluster (`kubectl get modelrequest -o yaml`)
// caught it and envtest/unit tests didn't -- pinning it now.
func TestModelRequest_AllPromotionsSucceeded_SetsPromotionPipelineRunName(t *testing.T) {
	ns := newTestNamespace(t)
	ensureNamespace(t, "staging")
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")
	setupSucceededSandbox(t, ns, "mr-1")

	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	setPipelineRunCondition(t, ns, "mr-1-promotion-staging", corev1.ConditionTrue, "done")

	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "Succeeded", mr.Status.Phase)
	require.Equal(t, "mr-1-promotion-staging", mr.Status.PromotionPipelineRunName)
	require.Equal(t, "mr-1-promotion-staging", mr.Status.PipelineRunName)

	require.Len(t, mr.Status.Stages, 3)
	promoStage := mr.Status.Stages[2]
	require.Equal(t, "promotion", promoStage.Name)
	require.Equal(t, "staging", promoStage.Namespace)
	require.Equal(t, "Succeeded", promoStage.Phase)
	require.Equal(t, "mr-1-promotion-staging", promoStage.RunRef)
}

// --- resolveSecrets ---

func TestResolveSecrets_SecretNamesConfigured_ReturnsSecretNameReferencesNotValues(t *testing.T) {
	// Phase 1 fix: with ScanS3SecretName/ResultS3SecretName set (the
	// newModelRequest default), resolveSecrets validates the referenced
	// Secret -- never a hardcoded credential pair. Phase 8
	// (docs/PHASE_LOG.md): the returned resolvedSecrets carries the
	// Secret's own NAME, never the accessKeyId/secretAccessKey values
	// themselves -- those never leave this function.
	ns := newTestNamespace(t)
	mr := newModelRequest(t, ns, "mr-1", "unused-profile", nil)
	secretName := "mr-1-s3-credentials"

	r := newModelRequestReconciler()
	secrets, err := r.resolveSecrets(context.Background(), mr)
	require.NoError(t, err)
	require.Equal(t, secretName, secrets.scanS3SecretName)
	require.Equal(t, secretName, secrets.resultS3SecretName)
}

func TestResolveSecrets_ResultS3EndpointOverride_StillHonored(t *testing.T) {
	// ResultS3Endpoint (a non-credential URL override) was intentionally
	// NOT removed in Phase 1 -- only the plaintext
	// ResultS3AccessKey/ResultS3SecretKey fields were.
	ns := newTestNamespace(t)
	mr := newModelRequest(t, ns, "mr-1", "unused-profile", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.ResultS3Endpoint = "http://custom-s3:9000"
	})

	r := newModelRequestReconciler()
	secrets, err := r.resolveSecrets(context.Background(), mr)
	require.NoError(t, err)
	require.Equal(t, "http://custom-s3:9000", secrets.resultS3Endpoint)
	require.Equal(t, "mr-1-s3-credentials", secrets.resultS3SecretName, "the Secret's name still flows through unchanged")
}

func TestResolveSecrets_NoScanS3SecretNameConfigured_FailsWithClearError(t *testing.T) {
	// Phase 1 fix: no hardcoded minioadmin/minioadmin credential
	// fallback -- resolveSecrets must fail loudly instead.
	ns := newTestNamespace(t)
	mr := newModelRequest(t, ns, "mr-1", "unused-profile", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.ScanS3SecretName = ""
	})

	r := newModelRequestReconciler()
	_, err := r.resolveSecrets(context.Background(), mr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no scan storage credentials configured")
}

func TestResolveSecrets_NoResultS3SecretNameConfigured_FailsWithClearError(t *testing.T) {
	ns := newTestNamespace(t)
	mr := newModelRequest(t, ns, "mr-1", "unused-profile", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.ResultS3SecretName = ""
	})

	r := newModelRequestReconciler()
	_, err := r.resolveSecrets(context.Background(), mr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no result storage credentials configured")
}

func TestResolveSecrets_MissingReferencedSecret_ReturnsError(t *testing.T) {
	ns := newTestNamespace(t)
	mr := newModelRequest(t, ns, "mr-1", "unused-profile", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.ResultS3SecretName = "does-not-exist"
	})

	r := newModelRequestReconciler()
	_, err := r.resolveSecrets(context.Background(), mr)
	require.Error(t, err)
}

// --- resolveSecrets: EvalHub (Phase 8 bug fix + secret-reference design) ---

// evalhubTokenSecretName mirrors the controller's own naming for the
// ephemeral, owned EvalHub token Secret -- duplicated here (rather than
// exported) so this test file pins the exact contract without needing
// to reach into controller internals beyond what resolveSecrets itself
// already returns.
func evalhubTokenSecretNameForTest(mrName string) string {
	return mrName + "-evalhub-token"
}

func TestResolveSecrets_EvalHubSecretHasURLAndToken_UsesTokenVerbatim_NeverGeneratesOrOverwrites(t *testing.T) {
	// The original bug (docs/PHASE_LOG.md Phase 8): resolveSecrets read
	// the Secret's "url" key into the wrong field (scanS3Endpoint) and
	// never read "token" at all, always overwriting it with a freshly
	// generated ServiceAccount token. This pins the corrected contract:
	// url lands in evalhubURL, an explicit token in the Secret is
	// honored verbatim, and no ephemeral token Secret is ever created.
	ns := newTestNamespace(t)
	newPipelineServiceAccount(t, ns)
	newSecret(t, ns, "evalhub-creds", map[string]string{
		"url":   "https://evalhub.example.com",
		"token": "operator-provided-evalhub-token",
	})
	mr := newModelRequest(t, ns, "mr-1", "unused-profile", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.EvalHubSecretName = "evalhub-creds"
	})

	r := newModelRequestReconciler()
	secrets, err := r.resolveSecrets(context.Background(), mr)
	require.NoError(t, err)
	require.Equal(t, "https://evalhub.example.com", secrets.evalhubURL,
		"the Secret's \"url\" key must land in evalhubURL, not scanS3Endpoint (the original bug)")
	require.Equal(t, "evalhub-creds", secrets.evalhubSecretName,
		"an explicit token in the operator's own Secret must be honored by reusing that Secret's name, not a generated one")

	// No ephemeral token Secret should have been created -- the
	// operator's own Secret already has a token.
	var ephemeral corev1.Secret
	err = k8sClient.Get(context.Background(), nsName(ns, evalhubTokenSecretNameForTest("mr-1")), &ephemeral)
	require.True(t, apierrors.IsNotFound(err), "no ephemeral EvalHub token Secret should be created when the operator's own Secret already has a token")
}

func TestResolveSecrets_EvalHubSecretHasURLButNoToken_GeneratesAndPersistsEphemeralSecret(t *testing.T) {
	ns := newTestNamespace(t)
	newPipelineServiceAccount(t, ns)
	newSecret(t, ns, "evalhub-creds", map[string]string{
		"url": "https://evalhub.example.com",
		// no "token" key
	})
	mr := newModelRequest(t, ns, "mr-1", "unused-profile", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.EvalHubSecretName = "evalhub-creds"
	})

	r := newModelRequestReconciler()
	secrets, err := r.resolveSecrets(context.Background(), mr)
	require.NoError(t, err)
	require.Equal(t, "https://evalhub.example.com", secrets.evalhubURL)

	wantSecretName := evalhubTokenSecretNameForTest("mr-1")
	require.Equal(t, wantSecretName, secrets.evalhubSecretName,
		"no explicit token in the configured Secret -- must fall back to the generated, owned, ephemeral Secret")

	var ephemeral corev1.Secret
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, wantSecretName), &ephemeral))
	require.NotEmpty(t, ephemeral.Data["token"], "the ephemeral Secret must actually hold the generated token")
	require.Len(t, ephemeral.OwnerReferences, 1, "the ephemeral Secret must be owned by the ModelRequest for GC")
	require.Equal(t, "mr-1", ephemeral.OwnerReferences[0].Name)
}

func TestResolveSecrets_NoEvalHubSecretConfigured_GeneratesAndPersistsEphemeralSecret(t *testing.T) {
	ns := newTestNamespace(t)
	newPipelineServiceAccount(t, ns)
	mr := newModelRequest(t, ns, "mr-1", "unused-profile", nil) // EvalHubSecretName left unset

	r := newModelRequestReconciler()
	secrets, err := r.resolveSecrets(context.Background(), mr)
	require.NoError(t, err)
	require.Empty(t, secrets.evalhubURL)

	wantSecretName := evalhubTokenSecretNameForTest("mr-1")
	require.Equal(t, wantSecretName, secrets.evalhubSecretName)

	var ephemeral corev1.Secret
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, wantSecretName), &ephemeral))
	require.NotEmpty(t, ephemeral.Data["token"])
}

func TestResolveSecrets_EvalHubEphemeralSecret_UpsertIsIdempotentAcrossReconciles(t *testing.T) {
	// The ephemeral EvalHub token Secret is refreshed (not duplicated)
	// on every resolveSecrets call, mirroring the
	// createIgnoringAlreadyExists pattern used elsewhere in this file
	// for other owned child objects.
	ns := newTestNamespace(t)
	newPipelineServiceAccount(t, ns)
	mr := newModelRequest(t, ns, "mr-1", "unused-profile", nil)

	r := newModelRequestReconciler()
	secrets1, err := r.resolveSecrets(context.Background(), mr)
	require.NoError(t, err)

	var first corev1.Secret
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, secrets1.evalhubSecretName), &first))

	secrets2, err := r.resolveSecrets(context.Background(), mr)
	require.NoError(t, err)
	require.Equal(t, secrets1.evalhubSecretName, secrets2.evalhubSecretName)

	var second corev1.Secret
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, secrets2.evalhubSecretName), &second))
	require.Equal(t, first.UID, second.UID, "the second call must update the existing Secret in place, not create a duplicate")
}

// --- resolveSecrets: HuggingFace (name-only, Phase 8) ---

func TestResolveSecrets_HuggingFaceSecretName_ReturnsNameNotValue(t *testing.T) {
	ns := newTestNamespace(t)
	newSecret(t, ns, "hf-creds", map[string]string{"token": "some-real-hf-token"})
	mr := newModelRequest(t, ns, "mr-1", "unused-profile", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.HuggingFaceSecretName = "hf-creds"
	})

	r := newModelRequestReconciler()
	secrets, err := r.resolveSecrets(context.Background(), mr)
	require.NoError(t, err)
	require.Equal(t, "hf-creds", secrets.huggingfaceSecretName)
}

func TestResolveSecrets_NoHuggingFaceSecretNameConfigured_LeavesItEmpty(t *testing.T) {
	ns := newTestNamespace(t)
	mr := newModelRequest(t, ns, "mr-1", "unused-profile", nil) // HuggingFaceSecretName left unset

	r := newModelRequestReconciler()
	secrets, err := r.resolveSecrets(context.Background(), mr)
	require.NoError(t, err)
	require.Empty(t, secrets.huggingfaceSecretName)
}

func TestResolveSecrets_MissingHuggingFaceSecret_ReturnsError(t *testing.T) {
	ns := newTestNamespace(t)
	mr := newModelRequest(t, ns, "mr-1", "unused-profile", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.HuggingFaceSecretName = "does-not-exist"
	})

	r := newModelRequestReconciler()
	_, err := r.resolveSecrets(context.Background(), mr)
	require.Error(t, err)
}

func TestModelRequest_NoS3CredentialsConfigured_SetsSecretLookupFailedPhase(t *testing.T) {
	// Reconciler-level proof that the resolveSecrets fix actually stops
	// the request, via the existing SecretLookupFailed status path,
	// instead of silently proceeding with a guessed credential pair.
	ns := newTestNamespace(t)
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
	newModelRequest(t, ns, "mr-1", "profile-1", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.ScanS3SecretName = ""
		mr.Spec.ResultS3SecretName = ""
	})
	setupSucceededCapacityPlan(t, ns, "mr-1")

	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "SecretLookupFailed", mr.Status.Phase)
	require.Contains(t, mr.Status.Message, "no scan storage credentials configured")
}

// --- Param-builder golden tests relocated (Phase 6) to internal/stages/sandbox
// and internal/stages/promotion -- same assertions, new packages. See
// docs/REFACTOR_PLAN.md Phase 6/docs/PHASE_LOG.md for the relocation.

func TestModelRequest_PromotionUsesProfilePromotionPipelineRef_EndToEnd(t *testing.T) {
	ns := newTestNamespace(t)
	ensureNamespace(t, "staging")
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", modelopsv1alpha1.ModelLifecycleProfileSpec{
		Workflow: modelopsv1alpha1.WorkflowRef{
			Engine:               "tekton",
			PipelineRef:          "model-intake-sandbox",
			PromotionPipelineRef: "profile-promotion-pipeline",
		},
		PlatformConfigRef: "cfg-1",
		Stages:            testDefaultStages(nil),
	})
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")
	setupSucceededSandbox(t, ns, "mr-1")

	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)

	var pr tektonv1.PipelineRun
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "mr-1-promotion-staging"), &pr))
	require.Equal(t, "profile-promotion-pipeline", pr.Spec.PipelineRef.Name)
}

// --- Status update no-op guard ---

func TestModelRequest_UpdateStatus_NoOpWhenPhaseAndMessageUnchanged(t *testing.T) {
	ns := newTestNamespace(t)
	mr := newModelRequest(t, ns, "mr-1", "unused-profile", nil)

	r := newModelRequestReconciler()
	ctx := context.Background()

	_, err := r.updateStatus(ctx, mr, "SomePhase", "some message")
	require.NoError(t, err)
	rv1 := mr.ResourceVersion

	_, err = r.updateStatus(ctx, mr, "SomePhase", "some message")
	require.NoError(t, err)
	require.Equal(t, rv1, mr.ResourceVersion, "identical phase+message must not trigger another Status().Update()")

	_, err = r.updateStatus(ctx, mr, "DifferentPhase", "some message")
	require.NoError(t, err)
	require.NotEqual(t, rv1, mr.ResourceVersion, "a real phase change must still be persisted")
}

// --- createIgnoringAlreadyExists idempotency helper ---
//
// Phase 1 fix: anywhere the controller calls r.Create(...) for a child
// object (CapacityPlan, PipelineRun, RBAC objects), an AlreadyExists
// error (e.g. from a race between a prior Get-not-found and this Create)
// must be treated as a harmless no-op, not surfaced as a raw reconcile
// error.

func TestCreateIgnoringAlreadyExists_ObjectAbsent_CreatesAndReportsCreated(t *testing.T) {
	ns := newTestNamespace(t)
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm-absent", Namespace: ns}}

	created, err := createIgnoringAlreadyExists(context.Background(), k8sClient, obj)
	require.NoError(t, err)
	require.True(t, created)

	var got corev1.ConfigMap
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "cm-absent"), &got))
}

func TestCreateIgnoringAlreadyExists_ObjectAlreadyExists_TreatsAsSuccessNotError(t *testing.T) {
	ns := newTestNamespace(t)
	existing := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm-existing", Namespace: ns}}
	require.NoError(t, k8sClient.Create(context.Background(), existing))

	dup := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm-existing", Namespace: ns}}
	created, err := createIgnoringAlreadyExists(context.Background(), k8sClient, dup)
	require.NoError(t, err, "AlreadyExists must not be surfaced as an error")
	require.False(t, created, "an object that already existed must be reported as not newly created")
}

func TestCreateIgnoringAlreadyExists_OtherErrors_ArePropagated(t *testing.T) {
	// A Namespace that doesn't exist produces a real (non-AlreadyExists)
	// error from the API server, which must still be surfaced so the
	// caller can back off/retry rather than silently swallowing it.
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm-no-ns", Namespace: "does-not-exist-ns-xyz"}}

	created, err := createIgnoringAlreadyExists(context.Background(), k8sClient, obj)
	require.Error(t, err)
	require.False(t, created)
}

// TestEnsurePromotionNamespaceRBAC_ObjectsAlreadyExist_DoesNotReattemptCreate
// is a regression test for a bug caught only by live-cluster
// verification, not envtest: an earlier draft of the Phase 1 idempotency
// fix called Create() unconditionally for every RBAC object (relying on
// createIgnoringAlreadyExists to swallow AlreadyExists), on the theory
// that this was simpler and closed a Get/Create race. On the real
// sandbox cluster this broke a ClusterRoleBinding that already existed
// (created out-of-band, granting permissions the operator's own
// ServiceAccount doesn't itself hold): Kubernetes' RBAC
// privilege-escalation check runs on *every* Create attempt, before the
// "already exists" conflict is even evaluated, so a redundant Create
// against an already-correct object was rejected with Forbidden (not
// AlreadyExists) -- envtest's admin test client bypasses this check
// entirely and couldn't catch it. The fix restores a Get-before-Create
// guard (only attempt Create when confirmed absent), using
// createIgnoringAlreadyExists only for the narrow Get/Create race
// window. This test pins that contract: objects that already exist must
// never see a Create attempt (proven here via ResourceVersion staying
// unchanged, since a real cluster's RBAC escalation check isn't
// reproducible against envtest's admin client).
func TestEnsurePromotionNamespaceRBAC_ObjectsAlreadyExist_DoesNotReattemptCreate(t *testing.T) {
	ns := newTestNamespace(t)
	sourceNS := newTestNamespace(t)

	existingSA := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "pipeline", Namespace: ns}}
	require.NoError(t, k8sClient.Create(context.Background(), existingSA))

	r := newModelRequestReconciler()
	require.NoError(t, r.ensurePromotionNamespaceRBAC(context.Background(), ns, sourceNS))

	var afterSA corev1.ServiceAccount
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "pipeline"), &afterSA))
	require.Equal(t, existingSA.ResourceVersion, afterSA.ResourceVersion,
		"an already-existing ServiceAccount must not see a Create attempt")
}

func TestModelRequest_CapacityPlanCreateRace_AlreadyExists_DoesNotFailReconcile(t *testing.T) {
	// Simulates the race the fix targets: something else (e.g. an
	// earlier/concurrent reconcile) creates the CapacityPlan the
	// controller is about to create, between the controller's Get
	// (NotFound) and its Create call. Exercised directly against the
	// idempotency helper using the exact object
	// capacityplanning.StageRunner would build (Phase 6 relocated
	// buildCapacityPlan into capacityplanning.Handler/StageRunner; see
	// that package's own TestEnsureRun_SecondCall_DoesNotRecreate for
	// the EnsureRun-level regression net this test used to be, now
	// exercised at the shared createIgnoringAlreadyExists level).
	ns := newTestNamespace(t)
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
	mr := newModelRequest(t, ns, "mr-1", "profile-1", nil)

	r := newModelRequestReconciler()
	var cfg modelopsv1alpha1.PlatformConfig
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "cfg-1"), &cfg))

	spec, err := capacityplanning.Handler{}.BuildSpec(stagecommon.StageContext{
		ModelRequest:   mr,
		PlatformConfig: &cfg,
		Stage:          modelopsv1alpha1.ProfileStageSpec{Name: "capacity"},
	})
	require.NoError(t, err)
	native := spec.NativeSpec.(*modelopsv1alpha1.CapacityPlanSpec)
	plan := modelopsv1alpha1.CapacityPlan{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-1-capacity", Namespace: ns},
		Spec:       *native,
	}
	// Someone else creates it first.
	winner := plan
	require.NoError(t, k8sClient.Create(context.Background(), &winner))

	// The reconciler's own attempt now loses the race.
	loser := plan
	created, err := createIgnoringAlreadyExists(context.Background(), r.Client, &loser)
	require.NoError(t, err, "losing a Create race to an equivalent object must not fail the reconcile")
	require.False(t, created)
}

// Phase 9 — Namespace RBAC governance (AllowedNamespaceSelector).
// See docs/REFACTOR_PLAN.md Phase 9 for the full design proposal.

func requireServiceAccountNotExists(t *testing.T, ns string) {
	t.Helper()
	var sa corev1.ServiceAccount
	err := k8sClient.Get(context.Background(), nsName(ns, "pipeline"), &sa)
	require.True(t, apierrors.IsNotFound(err), "expected no pipeline ServiceAccount in %s, got error: %v", ns, err)
}

func requireRoleBindingNotExists(t *testing.T, ns, name string) {
	t.Helper()
	var rb rbacv1.RoleBinding
	err := k8sClient.Get(context.Background(), nsName(ns, name), &rb)
	require.True(t, apierrors.IsNotFound(err), "expected no RoleBinding %s/%s, got error: %v", ns, name, err)
}

// 4a — Namespace fails the selector, no RBAC provisioned.
func TestModelRequest_NamespaceFailsAllowedNamespaceSelector_NoRBACProvisioned_NamespaceNotApproved(t *testing.T) {
	ns := newTestNamespace(t)
	promotionNS := "promo-" + randSuffix()
	ensureNamespaceWithLabels(t, promotionNS, map[string]string{"env": "staging"})

	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	spec := defaultProfileSpec("cfg-1")
	reqTrue := true
	spec.Stages = []modelopsv1alpha1.ProfileStageSpec{
		{Name: "capacity", Kind: "CapacityPlan"},
		{Name: "promotion", Kind: "PipelineRun", PerNamespace: true,
			Required: &reqTrue,
			NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{
				EnsureRBAC: true,
				AllowedNamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "production"},
				},
			},
		},
	}
	newProfile(t, ns, "profile-1", spec)
	newModelRequest(t, ns, "mr-1", "profile-1", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.Requirements = &modelopsv1alpha1.ModelRequirements{PromotionNamespaces: []string{promotionNS}}
	})
	setupSucceededCapacityPlan(t, ns, "mr-1")

	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)

	mr := getModelRequest(t, ns, "mr-1")
	require.Equal(t, "NamespaceNotApproved", mr.Status.Phase)
	require.Contains(t, mr.Status.Message, promotionNS)
	require.Contains(t, mr.Status.Message, "allowedNamespaceSelector")

	requireServiceAccountNotExists(t, promotionNS)
	requireRoleBindingNotExists(t, promotionNS, "pipeline-edit")
}

// 4b — Profile without selector is completely unaffected (backward compat).
func TestModelRequest_ProfileWithoutAllowedNamespaceSelector_CompletelyUnaffected(t *testing.T) {
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
	require.NotContains(t, mr.Status.Phase, "NamespaceNotApproved")

	requireServiceAccountExists(t, ns)
	requireServiceAccountExists(t, "staging")
}

// 4c — Namespace matches selector, RBAC provisioned normally.
func TestModelRequest_NamespaceMatchesAllowedNamespaceSelector_RBACProvisionedNormally(t *testing.T) {
	ns := newTestNamespace(t)
	promotionNS := "promo-" + randSuffix()
	ensureNamespaceWithLabels(t, promotionNS, map[string]string{"modelops.io/tier": "promotion-target"})

	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	spec := defaultProfileSpec("cfg-1")
	spec.Stages = []modelopsv1alpha1.ProfileStageSpec{
		{Name: "capacity", Kind: "CapacityPlan"},
		{Name: "sandbox", Kind: "PipelineRun",
			NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{EnsureRBAC: true},
		},
		{Name: "promotion", Kind: "PipelineRun", PerNamespace: true,
			NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{
				EnsureRBAC: true,
				AllowedNamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"modelops.io/tier": "promotion-target"},
				},
			},
		},
	}
	newProfile(t, ns, "profile-1", spec)
	newModelRequest(t, ns, "mr-1", "profile-1", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.Requirements = &modelopsv1alpha1.ModelRequirements{PromotionNamespaces: []string{promotionNS}}
	})
	setupSucceededCapacityPlan(t, ns, "mr-1")

	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	mr := getModelRequest(t, ns, "mr-1")
	require.Equal(t, "sandboxRunning", mr.Status.Phase)

	setPipelineRunCondition(t, ns, "mr-1-sandbox", corev1.ConditionTrue, "sandbox ok")
	mr, _, err = reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "promotionRunning", mr.Status.Phase)

	setPipelineRunCondition(t, ns, "mr-1-promotion-"+promotionNS, corev1.ConditionTrue, "promotion ok")
	mr, _, err = reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "Succeeded", mr.Status.Phase)

	requireServiceAccountExists(t, promotionNS)
}

// 4d — Explicit nil selector = identical to absent (backward compatible).
func TestModelRequest_AllowedNamespaceSelectorNil_BehaviorIdenticalToAbsent(t *testing.T) {
	ns := newTestNamespace(t)
	ensureNamespace(t, "staging")

	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	spec := defaultProfileSpec("cfg-1")
	spec.Stages[2].NamespaceSetup.AllowedNamespaceSelector = nil
	newProfile(t, ns, "profile-1", spec)
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")

	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	mr := getModelRequest(t, ns, "mr-1")
	require.Equal(t, "sandboxRunning", mr.Status.Phase)

	setPipelineRunCondition(t, ns, "mr-1-sandbox", corev1.ConditionTrue, "sandbox ok")
	mr, _, err = reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "promotionRunning", mr.Status.Phase)

	setPipelineRunCondition(t, ns, "mr-1-promotion-staging", corev1.ConditionTrue, "promotion ok")
	mr, _, err = reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "Succeeded", mr.Status.Phase)

	requireServiceAccountExists(t, "staging")
}

// 4e — matchExpressions (set-based selector) works correctly.
func TestModelRequest_AllowedNamespaceSelectorMatchExpressions_LabelsFetchedCorrectly(t *testing.T) {
	ns := newTestNamespace(t)
	promotionNS := "promo-" + randSuffix()
	ensureNamespaceWithLabels(t, promotionNS, map[string]string{"tier": "prod"})

	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	spec := defaultProfileSpec("cfg-1")
	spec.Stages = []modelopsv1alpha1.ProfileStageSpec{
		{Name: "capacity", Kind: "CapacityPlan"},
		{Name: "sandbox", Kind: "PipelineRun",
			NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{EnsureRBAC: true},
		},
		{Name: "promotion", Kind: "PipelineRun", PerNamespace: true,
			NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{
				EnsureRBAC: true,
				AllowedNamespaceSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"prod", "staging"}},
					},
				},
			},
		},
	}
	newProfile(t, ns, "profile-1", spec)
	newModelRequest(t, ns, "mr-1", "profile-1", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.Requirements = &modelopsv1alpha1.ModelRequirements{PromotionNamespaces: []string{promotionNS}}
	})
	setupSucceededCapacityPlan(t, ns, "mr-1")

	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	mr := getModelRequest(t, ns, "mr-1")
	require.Equal(t, "sandboxRunning", mr.Status.Phase)

	setPipelineRunCondition(t, ns, "mr-1-sandbox", corev1.ConditionTrue, "sandbox ok")
	mr, _, err = reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "promotionRunning", mr.Status.Phase)

	setPipelineRunCondition(t, ns, "mr-1-promotion-"+promotionNS, corev1.ConditionTrue, "promotion ok")
	mr, _, err = reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "Succeeded", mr.Status.Phase)

	requireServiceAccountExists(t, promotionNS)
}

// 4f — Multi-namespace: first approved namespace gets RBAC, the second
// (unapproved) namespace fails the selector check and the walk stops.
// The approved namespace's RBAC is already provisioned (sequential,
// not transactional), but the unapproved namespace gets nothing -- the
// fail-closed guarantee: no unapproved namespace ever gets RBAC.
func TestModelRequest_MultiNamespacePromotion_FirstPassesSecondFails_NoRBACInEither(t *testing.T) {
	ns := newTestNamespace(t)
	nsA := "promo-a-" + randSuffix()
	nsB := "promo-b-" + randSuffix()
	ensureNamespaceWithLabels(t, nsA, map[string]string{"env": "approved"})
	ensureNamespace(t, nsB)

	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	spec := defaultProfileSpec("cfg-1")
	spec.Stages = []modelopsv1alpha1.ProfileStageSpec{
		{Name: "capacity", Kind: "CapacityPlan"},
		{Name: "promotion", Kind: "PipelineRun", PerNamespace: true,
			NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{
				EnsureRBAC: true,
				AllowedNamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "approved"},
				},
			},
		},
	}
	newProfile(t, ns, "profile-1", spec)
	newModelRequest(t, ns, "mr-1", "profile-1", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.Requirements = &modelopsv1alpha1.ModelRequirements{PromotionNamespaces: []string{nsA, nsB}}
	})
	setupSucceededCapacityPlan(t, ns, "mr-1")

	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)

	mr := getModelRequest(t, ns, "mr-1")
	require.Equal(t, "NamespaceNotApproved", mr.Status.Phase)
	require.Contains(t, mr.Status.Message, nsB)

	requireServiceAccountExists(t, nsA)
	requireServiceAccountNotExists(t, nsB)
}

// 4h — Sandbox stage (PerNamespace: false) with matching selector on own ns.
func TestModelRequest_SandboxStageWithAllowedNamespaceSelector_AppliedToOwnNamespace(t *testing.T) {
	ns := newTestNamespace(t)
	ensureNamespaceWithLabels(t, ns, map[string]string{"modelops.io/stage": "sandbox"})

	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	spec := defaultProfileSpec("cfg-1")
	spec.Stages = []modelopsv1alpha1.ProfileStageSpec{
		{Name: "capacity", Kind: "CapacityPlan"},
		{Name: "sandbox", Kind: "PipelineRun",
			NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{
				EnsureRBAC: true,
				AllowedNamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"modelops.io/stage": "sandbox"},
				},
			},
		},
	}
	newProfile(t, ns, "profile-1", spec)
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")

	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	mr := getModelRequest(t, ns, "mr-1")
	require.Equal(t, "sandboxRunning", mr.Status.Phase)

	requireServiceAccountExists(t, ns)
}

// 4i — Sandbox stage with non-matching selector on own ns → blocked.
func TestModelRequest_SandboxStageOwnNamespaceFailsSelector_NamespaceNotApproved(t *testing.T) {
	ns := newTestNamespace(t)
	ensureNamespace(t, ns)

	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	spec := defaultProfileSpec("cfg-1")
	spec.Stages = []modelopsv1alpha1.ProfileStageSpec{
		{Name: "capacity", Kind: "CapacityPlan"},
		{Name: "sandbox", Kind: "PipelineRun",
			NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{
				EnsureRBAC: true,
				AllowedNamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"modelops.io/stage": "production"},
				},
			},
		},
	}
	newProfile(t, ns, "profile-1", spec)
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")

	_, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)

	mr := getModelRequest(t, ns, "mr-1")
	require.Equal(t, "NamespaceNotApproved", mr.Status.Phase)
	require.Contains(t, mr.Status.Message, ns)

	requireServiceAccountNotExists(t, ns)

	var pr tektonv1.PipelineRun
	getErr := k8sClient.Get(context.Background(), nsName(ns, "mr-1-sandbox"), &pr)
	require.True(t, apierrors.IsNotFound(getErr), "sandbox PipelineRun must not be created when own namespace fails approval")
}
