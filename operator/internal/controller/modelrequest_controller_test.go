package controller

// Characterization tests for ModelRequestReconciler's CURRENT behavior
// (Phase 0 of REFACTOR_PLAN.md). These pin today's actual
// capacity-planning -> sandbox -> promotion sequence, param output, and
// status transitions as tests, so Phases 2-6 (which relocate this logic
// without intending to change it) have a regression net.
//
// Several tests below are explicitly labeled "KnownBehavior" or
// "KnownBug": they pin something Phase 1 of REFACTOR_PLAN.md is already
// scoped to change (the duplicate gpu-count-override param, the ignored
// `profile` argument in promotionPipelineNameOrDefault, the hardcoded
// minioadmin credential fallback, and promotion namespaces not being
// gated on the previous namespace's success). These are captured as-is,
// not treated as behavior to preserve forever -- expect these specific
// tests to be intentionally updated when Phase 1 lands.

import (
	"context"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func newModelRequestReconciler() *ModelRequestReconciler {
	return &ModelRequestReconciler{Client: k8sClient, Scheme: testRuntimeScheme()}
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
		mr.Spec.Requirements = &modelopsv1alpha1.ModelRequirements{ContextLength: 8192, ExpectedConcurrency: 4}
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

func TestResolveSecrets_NoSecretsConfigured_KnownBehavior_DefaultsToHardcodedMinioCredentials(t *testing.T) {
	// KNOWN BEHAVIOR (Phase 1 target): with no *SecretName fields set on
	// the ModelRequest, resolveSecrets silently falls back to a
	// hardcoded minioadmin/minioadmin credential pair rather than
	// failing the reconcile. Captured as-is.
	ns := newTestNamespace(t)
	mr := newModelRequest(t, ns, "mr-1", "unused-profile", nil)

	r := newModelRequestReconciler()
	secrets, err := r.resolveSecrets(context.Background(), mr)
	require.NoError(t, err)
	require.Equal(t, "http://minio.modelops-storage.svc.cluster.local:9000", secrets.scanS3Endpoint)
	require.Equal(t, "minioadmin", secrets.scanS3AccessKey)
	require.Equal(t, "minioadmin", secrets.scanS3SecretKey)
	require.Equal(t, "http://minio.modelops-storage.svc.cluster.local:9000", secrets.resultS3Endpoint)
	require.Equal(t, "minioadmin", secrets.resultS3AccessKey)
	require.Equal(t, "minioadmin", secrets.resultS3SecretKey)
}

func TestResolveSecrets_DeprecatedPlaintextSpecFields_KnownBehavior_OverrideDefaults(t *testing.T) {
	// KNOWN BEHAVIOR (Phase 1 removes these fields entirely): today,
	// mr.Spec.ResultS3Endpoint/AccessKey/SecretKey -- plaintext spec
	// fields -- take precedence over both the secretRef-derived value
	// and the hardcoded default.
	ns := newTestNamespace(t)
	mr := newModelRequest(t, ns, "mr-1", "unused-profile", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.ResultS3Endpoint = "http://custom-s3:9000"
		mr.Spec.ResultS3AccessKey = "custom-key"
		mr.Spec.ResultS3SecretKey = "custom-secret"
	})

	r := newModelRequestReconciler()
	secrets, err := r.resolveSecrets(context.Background(), mr)
	require.NoError(t, err)
	require.Equal(t, "http://custom-s3:9000", secrets.resultS3Endpoint)
	require.Equal(t, "custom-key", secrets.resultS3AccessKey)
	require.Equal(t, "custom-secret", secrets.resultS3SecretKey)
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

// --- Param builder golden tests (regression net for Phase 3's dedup) ---

func TestBuildSandboxPipelineParams_KnownBug_GPUCountOverrideAddedTwice(t *testing.T) {
	// KNOWN BUG (Phase 1 item 3 target): when both the CapacityPlan's
	// derived GPU count and an explicit reqs.GPUCountOverride are set,
	// buildSandboxPipelineParams appends TWO params both named
	// "gpu-count-override" instead of having the override take
	// precedence. Captured as-is.
	r := newModelRequestReconciler()
	mr := &modelopsv1alpha1.ModelRequest{
		Spec: modelopsv1alpha1.ModelRequestSpec{
			Model: modelopsv1alpha1.ModelIdentity{URI: "some/model"},
			Requirements: &modelopsv1alpha1.ModelRequirements{
				GPUCountOverride: "7",
			},
		},
	}
	cfg := &modelopsv1alpha1.PlatformConfig{}
	plan := &modelopsv1alpha1.CapacityPlan{Status: modelopsv1alpha1.CapacityPlanStatus{GPUsNeeded: 4}}
	secrets := &resolvedSecrets{}

	params := r.buildSandboxPipelineParams(mr, nil, cfg, plan, secrets)

	values := findAllParams(params, "gpu-count-override")
	require.Equal(t, []string{"4", "7"}, values, "two gpu-count-override params: plan-derived first, then the explicit override")
}

func TestPromotionPipelineNameOrDefault_KnownBug_IgnoresProfileArgument(t *testing.T) {
	// KNOWN BUG (Phase 1 item 4 target): promotionPipelineNameOrDefault
	// always returns the same hardcoded name, regardless of what the
	// profile says. Captured as-is.
	r := newModelRequestReconciler()
	profile := &modelopsv1alpha1.ModelLifecycleProfile{
		Spec: modelopsv1alpha1.ModelLifecycleProfileSpec{
			Workflow: modelopsv1alpha1.WorkflowRef{Engine: "tekton", PipelineRef: "a-totally-different-promotion-pipeline"},
		},
	}
	mr := &modelopsv1alpha1.ModelRequest{}

	require.Equal(t, "model-intake-promotion", r.promotionPipelineNameOrDefault(profile, mr))
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
