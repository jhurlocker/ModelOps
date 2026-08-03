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
	tektonstage "github.com/jhurlocker/modelops-operator/internal/stages/tekton"

	"github.com/stretchr/testify/require"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// newModelRequestReconciler wires the reconciler with the REAL
// tekton.StageRunner (not a fake), so all of this file's
// PipelineRun-shaped assertions (PipelineRef, OwnerReferences, Params,
// conditions) continue to exercise genuine Tekton behavior end-to-end --
// this is Phase 4's regression net for "relocate, don't change" (see
// docs/PHASE_LOG.md). Tests that specifically want to prove the
// reconciler works with zero Tekton involvement construct a
// ModelRequestReconciler directly with a stagecommon.FakeStageRunner
// instead of using this helper.
func newModelRequestReconciler() *ModelRequestReconciler {
	scheme := testRuntimeScheme()
	return &ModelRequestReconciler{
		Client: k8sClient,
		Scheme: scheme,
		StageRunner: &tektonstage.StageRunner{
			Client: k8sClient,
			Scheme: scheme,
		},
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
	require.Equal(t, "CapacityPlanning", mr.Status.Phase)

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
	require.Equal(t, "CapacityPlanning", mr.Status.Phase)
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
	require.Equal(t, "SandboxRunning", mr.Status.Phase)
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
	require.Equal(t, "SandboxRunning", mr.Status.Phase)

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
	require.Equal(t, "PromotionRunning", mr.Status.Phase)

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
	require.Equal(t, "PromotionRunning", mr.Status.Phase)

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
	require.Equal(t, "PromotionRunning", mr.Status.Phase)
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

// --- resolveSecrets ---

func TestResolveSecrets_SecretNamesConfigured_ReturnsSecretDerivedCredentials(t *testing.T) {
	// Phase 1 fix: with ScanS3SecretName/ResultS3SecretName set (the
	// newModelRequest default), resolveSecrets returns the values from
	// the referenced Secret -- never a hardcoded credential pair.
	ns := newTestNamespace(t)
	mr := newModelRequest(t, ns, "mr-1", "unused-profile", nil)

	r := newModelRequestReconciler()
	secrets, err := r.resolveSecrets(context.Background(), mr)
	require.NoError(t, err)
	require.Equal(t, "test-access-key", secrets.scanS3AccessKey)
	require.Equal(t, "test-secret-key", secrets.scanS3SecretKey)
	require.Equal(t, "test-access-key", secrets.resultS3AccessKey)
	require.Equal(t, "test-secret-key", secrets.resultS3SecretKey)
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
	require.Equal(t, "test-access-key", secrets.resultS3AccessKey, "credentials still come only from the Secret")
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

// --- Param builder golden tests (regression net for Phase 3's dedup) ---

func TestBuildSandboxPipelineParams_ExplicitOverride_TakesPrecedenceAndAppearsExactlyOnce(t *testing.T) {
	// Phase 1 fix: when both the CapacityPlan's derived GPU count and an
	// explicit reqs.GPUCountOverride are set, exactly ONE
	// "gpu-count-override" param must be added, using the override.
	r := newModelRequestReconciler()
	mr := &modelopsv1alpha1.ModelRequest{
		Spec: modelopsv1alpha1.ModelRequestSpec{
			Model: modelopsv1alpha1.ModelIdentity{URI: "some/model"},
			Requirements: &modelopsv1alpha1.ModelRequirements{
				GPUConfig: modelopsv1alpha1.GPUConfig{GPUCountOverride: "7"},
			},
		},
	}
	cfg := &modelopsv1alpha1.PlatformConfig{}
	plan := &modelopsv1alpha1.CapacityPlan{Status: modelopsv1alpha1.CapacityPlanStatus{GPUsNeeded: 4}}
	secrets := &resolvedSecrets{}

	params := r.buildSandboxPipelineParams(mr, nil, cfg, plan, secrets)

	// Since buildSandboxPipelineParams now returns map[string]string
	// (Phase 4), a duplicate AddParam call for the same name can no
	// longer produce two entries by construction -- see
	// stagecommon.AddParam's doc comment. Asserting the single value is
	// therefore the meaningful check here; the "appears exactly once"
	// guarantee moved from a runtime test assertion to a structural
	// property of the map type itself.
	require.Equal(t, "7", params["gpu-count-override"], "explicit override must win over the plan-derived value")
}

func TestBuildSandboxPipelineParams_NoOverride_FallsBackToPlanDerivedGPUCount(t *testing.T) {
	r := newModelRequestReconciler()
	mr := &modelopsv1alpha1.ModelRequest{
		Spec: modelopsv1alpha1.ModelRequestSpec{
			Model: modelopsv1alpha1.ModelIdentity{URI: "some/model"},
		},
	}
	cfg := &modelopsv1alpha1.PlatformConfig{}
	plan := &modelopsv1alpha1.CapacityPlan{Status: modelopsv1alpha1.CapacityPlanStatus{GPUsNeeded: 4}}
	secrets := &resolvedSecrets{}

	params := r.buildSandboxPipelineParams(mr, nil, cfg, plan, secrets)

	require.Equal(t, "4", params["gpu-count-override"])
}

func TestBuildSandboxPipelineParams_NoOverrideAndNoPlan_OmitsParam(t *testing.T) {
	r := newModelRequestReconciler()
	mr := &modelopsv1alpha1.ModelRequest{
		Spec: modelopsv1alpha1.ModelRequestSpec{
			Model: modelopsv1alpha1.ModelIdentity{URI: "some/model"},
		},
	}
	cfg := &modelopsv1alpha1.PlatformConfig{}
	secrets := &resolvedSecrets{}

	params := r.buildSandboxPipelineParams(mr, nil, cfg, nil, secrets)

	_, ok := params["gpu-count-override"]
	require.False(t, ok)
}

// --- Phase 3 dedup regression net: full-fixture characterization tests ---
//
// buildSandboxPipelineParams and buildPromotionPipelineParams share a lot
// of param-building logic (model identity, GPU/benchmark config,
// deployment/chart config, registry config, result-S3 config). Phase 3
// extracts that shared logic into a common helper. These tests populate
// every relevant field on the inputs (so no addParam call is skipped for
// being empty) and pin the *complete* resulting param set -- name and
// value, and a check that no name appears more than once -- for both
// functions as they exist today, BEFORE the extraction. After Phase 3's
// refactor, these tests must still pass unmodified: same params in, same
// params out, just produced with less duplicated code.

func boolPtr(b bool) *bool { return &b }

// paramsToMap used to convert a tektonv1.Params into a map[string]string
// here, failing the test outright if any param name appeared more than
// once (the exact shape of the Phase 1 gpu-count-override duplicate-param
// bug this suite guards against). Phase 4 changed
// buildSandboxPipelineParams/buildPromotionPipelineParams to return
// map[string]string directly, so that conversion -- and the duplicate
// check, which is now a structural property of the map type itself
// rather than something a test needs to detect -- is no longer needed.

// fullCharacterizationFixture returns a ModelRequest/PlatformConfig/
// CapacityPlan/resolvedSecrets tuple with every field buildSandboxPipelineParams
// and buildPromotionPipelineParams read from populated with a distinct,
// non-empty/non-zero value, so the full param set both functions produce
// (nothing skipped by addParam's empty-string guard) can be pinned
// exactly.
func fullCharacterizationFixture() (*modelopsv1alpha1.ModelRequest, *modelopsv1alpha1.PlatformConfig, *modelopsv1alpha1.CapacityPlan, *resolvedSecrets) {
	mr := &modelopsv1alpha1.ModelRequest{
		Spec: modelopsv1alpha1.ModelRequestSpec{
			Model: modelopsv1alpha1.ModelIdentity{
				SourceType: "oci",
				URI:        "quay.io/models/foo:v1",
				Name:       "foo-model",
				Version:    "v2",
				Tokenizer:  "foo-tokenizer",
			},
			DisplayName:           "Foo Model",
			BusinessJustification: "Because reasons",
			RequestedBy:           "jane@example.com",
			ResultS3Bucket:        "custom-result-bucket",
			Requirements: &modelopsv1alpha1.ModelRequirements{
				GPUConfig: modelopsv1alpha1.GPUConfig{
					GPUIsolationPolicy: "shared",
					AllowTimeSlicing:   boolPtr(false),
					AllowMIG:           boolPtr(true),
					GPUCountOverride:   "7",
				},
				BenchmarkTargets: modelopsv1alpha1.BenchmarkTargets{
					ContextLength:       8192,
					ExpectedConcurrency: 16,
					RequestRate:         "5.0",
					TargetTTFT:          "250ms",
					TargetThroughput:    "200",
				},
				SecurityConfig: modelopsv1alpha1.SecurityConfig{
					CVEThreshold:        "high",
					SecurityThreshold:   "warn",
					CustomBenchmarkData: true,
					CustomBenchmarkFile: "custom.json",
				},
				DeploymentConfig: modelopsv1alpha1.DeploymentConfig{
					ValuesContent:          "replicaCount: 3",
					OpenShiftConsoleDomain: "apps.example.com",
				},
				SandboxNamespace:    "my-sandbox",
				StagingNamespace:    "my-staging",
				PromotionNamespaces: []string{"my-staging", "prod"},
				AdvisorEndpoint:     "http://advisor.example.com",
			},
			Access: &modelopsv1alpha1.ModelAccess{
				AuthorizedViewers: "team-a,team-b",
				AccessRole:        "admin",
			},
			MaaS: &modelopsv1alpha1.MaaSOverride{
				Enabled:         true,
				GPUCount:        "3",
				RuntimeImage:    "custom-runtime:latest",
				AuthorizedGroup: "custom-group",
			},
		},
	}

	cfg := &modelopsv1alpha1.PlatformConfig{
		Spec: modelopsv1alpha1.PlatformConfigSpec{
			ComplianceS3Bucket:          "comp-bucket",
			SecurityS3Bucket:            "sec-bucket",
			RegistryServer:              "http://registry.example.com",
			RegistryPort:                "9090",
			RegistryAuthor:              "Team Author",
			ComplianceScanImage:         "scan-image:latest",
			ComplianceIgnoreUnfixed:     "false",
			ComplianceAllowedArch:       []string{"amd64", "arm64"},
			ModelCarRepo:                "custom/modelcar-catalog",
			GPUOperatorNamespace:        "custom-gpu-ns",
			ClusterPolicyName:           "custom-cluster-policy",
			TimeSlicingConfigMap:        "custom-ts-cm",
			MaxTimeSlices:               16,
			AdvisorSecretName:           "custom-advisor-secret",
			AdvisorTimeoutSeconds:       600,
			ChartURL:                    "https://charts.example.com/",
			ChartVersion:                "1.2.3",
			HardwareProfileName:         "custom-hw-profile",
			HardwareProfileNamespace:    "custom-hw-ns",
			EvalHubURL:                  "http://evalhub.example.com",
			ApprovalApiUrl:              "http://approval.example.com",
			ApprovalPollIntervalSeconds: 30,
			ApprovalTimeoutSeconds:      7200,
			BenchmarkProfile:            "sweep",
			BenchmarkRate:               8.5,
			BenchmarkMaxSeconds:         60,
			BenchmarkMaxRequests:        10,
			BenchmarkTargetUrl:          "http://custom-benchmark-target/v1",
			MaaSServingNS:               "custom-maas-serving",
			MaaSPolicyNS:                "custom-maas-policy",
			MaaSGPUCount:                "2",
			MaaSRuntimeImage:            "default-runtime:latest",
			MaaSAuthorizedGroup:         "default-group",
		},
	}

	plan := &modelopsv1alpha1.CapacityPlan{Status: modelopsv1alpha1.CapacityPlanStatus{GPUsNeeded: 4}}

	secrets := &resolvedSecrets{
		evalhubToken:      "evalhub-tok",
		huggingfaceToken:  "hf-tok",
		scanS3Endpoint:    "http://scan-s3:9000",
		scanS3AccessKey:   "scan-access",
		scanS3SecretKey:   "scan-secret",
		resultS3Endpoint:  "http://result-s3:9000",
		resultS3AccessKey: "result-access",
		resultS3SecretKey: "result-secret",
	}

	return mr, cfg, plan, secrets
}

func TestBuildSandboxPipelineParams_FullFixture_CharacterizesCurrentOutput(t *testing.T) {
	r := newModelRequestReconciler()
	mr, cfg, plan, secrets := fullCharacterizationFixture()

	got := r.buildSandboxPipelineParams(mr, nil, cfg, plan, secrets)

	want := map[string]string{
		"model-id":                   "quay.io/models/foo:v1",
		"model-name":                 "foo-model",
		"model-version":              "v2",
		"model-tokenizer":            "foo-tokenizer",
		"model-source-type":          "oci",
		"display-name":               "Foo Model",
		"business-justification":     "Because reasons",
		"requested-by":               "jane@example.com",
		"target-namespace":           "my-sandbox",
		"modelcar-repo":              "custom/modelcar-catalog",
		"artifact-scan-image":        "scan-image:latest",
		"artifact-cve-threshold":     "high",
		"ignore-unfixed":             "false",
		"allowed-architectures":      "amd64,arm64",
		"gpu-count-override":         "7", // explicit override wins over plan-derived (4)
		"context-length":             "8192",
		"concurrency":                "16",
		"allow-time-slicing":         "false",
		"allow-mig":                  "true",
		"gpu-isolation-policy":       "shared",
		"request-rate":               "5.0",
		"target-ttft":                "250ms",
		"target-throughput":          "200",
		"gpu-operator-namespace":     "custom-gpu-ns",
		"clusterpolicy-name":         "custom-cluster-policy",
		"time-slicing-configmap":     "custom-ts-cm",
		"max-time-slices":            "16",
		"advisor-endpoint":           "http://advisor.example.com",
		"advisor-secret-name":        "custom-advisor-secret",
		"advisor-timeout-seconds":    "600",
		"release-name":               "foo-model",
		"chart-url":                  "https://charts.example.com/",
		"chart-version":              "1.2.3",
		"values-content":             "replicaCount: 3",
		"hardware-profile-name":      "custom-hw-profile",
		"hardware-profile-namespace": "custom-hw-ns",
		"severity-threshold":         "warn",
		"evalhub-url":                "http://evalhub.example.com",
		"evalhub-token":              "evalhub-tok",
		"tenant-ns":                  "my-sandbox",
		"openshift-console-domain":   "apps.example.com",
		"huggingface-token":          "hf-tok",
		"scan-s3-endpoint":           "http://scan-s3:9000",
		"scan-s3-access-key-id":      "scan-access",
		"scan-s3-secret-access-key":  "scan-secret",
		"compliance-s3-bucket":       "custom-result-bucket", // spec.ResultS3Bucket wins over cfg.Spec.ComplianceS3Bucket
		"security-s3-bucket":         "custom-result-bucket", // spec.ResultS3Bucket wins over cfg.Spec.SecurityS3Bucket
		"s3-api-endpoint":            "http://result-s3:9000",
		"s3-access-key-id":           "result-access",
		"s3-secret-access-key":       "result-secret",
		"mr-server":                  "http://registry.example.com",
		"mr-port":                    "9090",
		"model-reg-author":           "Team Author",
		// "modelcar-image" and "s3-ui-route" are always hardcoded "" in
		// this function, so addParam's empty-value guard always omits
		// them -- they must NOT appear in the output.
	}

	require.Equal(t, want, got)
}

func TestBuildPromotionPipelineParams_FirstAndLastNamespace_FullFixture_CharacterizesCurrentOutput(t *testing.T) {
	r := newModelRequestReconciler()
	mr, cfg, plan, secrets := fullCharacterizationFixture()

	got := r.buildPromotionPipelineParams(mr, nil, cfg, plan, secrets, "prod-ns", "plan-123", true, true)

	want := map[string]string{
		"model-id":               "quay.io/models/foo:v1",
		"model-name":             "foo-model",
		"model-version":          "v2",
		"model-tokenizer":        "foo-tokenizer",
		"model-source-type":      "oci",
		"display-name":           "Foo Model",
		"business-justification": "Because reasons",
		"requested-by":           "jane@example.com",
		"target-namespace":       "prod-ns",
		"plan-id":                "plan-123",
		"modelcar-repo":          "custom/modelcar-catalog",
		// KNOWN BEHAVIOR (see below): unlike buildSandboxPipelineParams,
		// buildPromotionPipelineParams does NOT check
		// reqs.GPUConfig.GPUCountOverride at all -- it always uses the
		// CapacityPlan-derived value. Fixture sets GPUCountOverride="7"
		// but the plan-derived value (4) is what appears here.
		"gpu-count-override":             "4",
		"context-length":                 "8192",
		"concurrency":                    "16",
		"allow-time-slicing":             "false",
		"allow-mig":                      "true",
		"gpu-isolation-policy":           "shared",
		"request-rate":                   "5.0",
		"target-ttft":                    "250ms",
		"target-throughput":              "200",
		"gpu-operator-namespace":         "custom-gpu-ns",
		"clusterpolicy-name":             "custom-cluster-policy",
		"time-slicing-configmap":         "custom-ts-cm",
		"max-time-slices":                "16",
		"advisor-endpoint":               "http://advisor.example.com",
		"advisor-secret-name":            "custom-advisor-secret",
		"advisor-timeout-seconds":        "600",
		"release-name":                   "foo-model",
		"chart-url":                      "https://charts.example.com/",
		"chart-version":                  "1.2.3",
		"values-content":                 "replicaCount: 3",
		"hardware-profile-name":          "custom-hw-profile",
		"hardware-profile-namespace":     "custom-hw-ns",
		"approval-api-url":               "http://approval.example.com",
		"approval-poll-interval-seconds": "30",
		"approval-timeout-seconds":       "7200",
		"evalhub-url":                    "http://evalhub.example.com",
		"evalhub-token":                  "evalhub-tok",
		"openshift-console-domain":       "apps.example.com",
		"guidellm-profile":               "sweep",
		"guidellm-rate":                  "8.5",
		"guidellm-max-seconds":           "60",
		"guidellm-max-requests":          "10",
		"benchmark-target-url":           "http://custom-benchmark-target/v1",
		"custom-data":                    "true",
		"custom-filename":                "custom.json",
		"huggingface-token":              "hf-tok",
		"s3-api-endpoint":                "http://result-s3:9000",
		"s3-access-key-id":               "result-access",
		"s3-secret-access-key":           "result-secret",
		"mr-server":                      "http://registry.example.com",
		"mr-port":                        "9090",
		"model-reg-author":               "Team Author",
		"authorized-viewers":             "team-a,team-b",
		"access-role":                    "admin",
		"deploy-maas":                    "true",
		"maas-serving-ns":                "custom-maas-serving",
		"maas-policy-ns":                 "custom-maas-policy",
		"maas-gpu-count":                 "3", // spec.MaaS.GPUCount wins over cfg.Spec.MaaSGPUCount
		"maas-runtime-image":             "default-runtime:latest",
		"maas-authorized-group":          "default-group",
		"run-register":                   "true", // isLast=true
		// "modelcar-image" is always hardcoded "" in this function, so
		// addParam's empty-value guard always omits it.
	}

	require.Equal(t, want, got)
}

func TestBuildPromotionPipelineParams_MiddleNamespace_OmitsApprovalURL_AndRunRegisterFalse(t *testing.T) {
	// isFirst=false, isLast=false: only the first promotion namespace
	// gets an approval gate, and only the last has run-register=true.
	// This is the current behavior for every namespace between the
	// first and last in a multi-namespace promotion sequence.
	r := newModelRequestReconciler()
	mr, cfg, plan, secrets := fullCharacterizationFixture()

	got := r.buildPromotionPipelineParams(mr, nil, cfg, plan, secrets, "staging-ns", "plan-123", false, false)

	_, hasApprovalURL := got["approval-api-url"]
	require.False(t, hasApprovalURL, "approval-api-url must be omitted (empty string) when isFirst=false")
	require.Equal(t, "false", got["run-register"], "run-register must be false when isLast=false")
	require.Equal(t, "staging-ns", got["target-namespace"])
	// approval-poll-interval-seconds/approval-timeout-seconds are NOT
	// gated on isFirst -- they're always present.
	require.Equal(t, "30", got["approval-poll-interval-seconds"])
	require.Equal(t, "7200", got["approval-timeout-seconds"])
}

func TestPromotionPipelineNameOrDefault_UsesProfileOverrideWhenSet(t *testing.T) {
	// Phase 1 fix: promotionPipelineNameOrDefault must actually select a
	// per-profile promotion pipeline name via
	// profile.Spec.Workflow.PromotionPipelineRef, rather than ignoring
	// the profile argument entirely.
	r := newModelRequestReconciler()
	profile := &modelopsv1alpha1.ModelLifecycleProfile{
		Spec: modelopsv1alpha1.ModelLifecycleProfileSpec{
			Workflow: modelopsv1alpha1.WorkflowRef{
				Engine:               "tekton",
				PipelineRef:          "some-sandbox-pipeline",
				PromotionPipelineRef: "a-totally-different-promotion-pipeline",
			},
		},
	}
	mr := &modelopsv1alpha1.ModelRequest{}

	require.Equal(t, "a-totally-different-promotion-pipeline", r.promotionPipelineNameOrDefault(profile, mr))
}

func TestPromotionPipelineNameOrDefault_DefaultsWhenProfileHasNoOverride(t *testing.T) {
	r := newModelRequestReconciler()
	profile := &modelopsv1alpha1.ModelLifecycleProfile{
		Spec: modelopsv1alpha1.ModelLifecycleProfileSpec{
			Workflow: modelopsv1alpha1.WorkflowRef{Engine: "tekton", PipelineRef: "some-sandbox-pipeline"},
		},
	}
	mr := &modelopsv1alpha1.ModelRequest{}

	require.Equal(t, "model-intake-promotion", r.promotionPipelineNameOrDefault(profile, mr))
}

func TestPromotionPipelineNameOrDefault_NilProfile_Defaults(t *testing.T) {
	r := newModelRequestReconciler()
	mr := &modelopsv1alpha1.ModelRequest{}

	require.Equal(t, "model-intake-promotion", r.promotionPipelineNameOrDefault(nil, mr))
}

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
	// idempotency helper using the exact object the reconciler would
	// build, proving the reconciler's own Create call site is safe
	// against this race without needing genuine goroutine concurrency.
	ns := newTestNamespace(t)
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	profile := newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
	mr := newModelRequest(t, ns, "mr-1", "profile-1", nil)

	r := newModelRequestReconciler()
	var cfg modelopsv1alpha1.PlatformConfig
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "cfg-1"), &cfg))

	plan := r.buildCapacityPlan(mr, "mr-1-capacity", profile, &cfg)
	// Someone else creates it first.
	winner := plan
	require.NoError(t, k8sClient.Create(context.Background(), &winner))

	// The reconciler's own attempt now loses the race.
	loser := plan
	created, err := createIgnoringAlreadyExists(context.Background(), r.Client, &loser)
	require.NoError(t, err, "losing a Create race to an equivalent object must not fail the reconcile")
	require.False(t, created)
}
