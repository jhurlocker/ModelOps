package controller

// Phase 4 of REFACTOR_PLAN.md's actual proof that ModelRequestReconciler
// is decoupled from Tekton: these tests drive it with only
// stagecommon.FakeStageRunner and assert no tektonv1.PipelineRun is ever
// touched.
//
// Two variants, in increasing order of strictness:
//
//  1. TestModelRequest_FullLifecycle_DrivenEntirelyByFakeStageRunner_NoTektonInvolved
//     runs against this package's shared envtest apiserver (which DOES
//     have the PipelineRun CRD installed, since other tests in this
//     package need it) and asserts a PipelineRun List for the test's
//     namespace comes back empty at every step.
//  2. TestModelRequest_FullLifecycle_FakeClientWithoutTektonScheme goes
//     further: it builds its own controller-runtime fake client whose
//     scheme never registers tektonv1 at all. This proves not just
//     "zero PipelineRuns were created" but "this reconciler cannot even
//     construct a call against the Tekton API type," since the scheme
//     doesn't know it exists.

import (
	"context"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	"github.com/stretchr/testify/require"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func requireNoPipelineRuns(t *testing.T, ns string) {
	t.Helper()
	var list tektonv1.PipelineRunList
	require.NoError(t, k8sClient.List(context.Background(), &list, ctrlclient.InNamespace(ns)))
	require.Empty(t, list.Items, "reconciler must never create a real PipelineRun when driven by a FakeStageRunner")
}

func TestModelRequest_FullLifecycle_DrivenEntirelyByFakeStageRunner_NoTektonInvolved(t *testing.T) {
	ns := newTestNamespace(t)
	ensureNamespace(t, "staging")
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")

	fakeRunner := stagecommon.NewFakeStageRunner()
	r := &ModelRequestReconciler{Client: k8sClient, Scheme: testRuntimeScheme(), StageRunner: fakeRunner}

	reconcile := func() *modelopsv1alpha1.ModelRequest {
		t.Helper()
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "mr-1")})
		require.NoError(t, err)
		return getModelRequest(t, ns, "mr-1")
	}

	// 1. Sandbox stage still running (scripted): SandboxRunning, and
	// critically, no PipelineRun exists anywhere.
	fakeRunner.ScriptStage("sandbox", stagecommon.StageStatus{Phase: stagecommon.StageRunning})
	mr := reconcile()
	require.Equal(t, "SandboxRunning", mr.Status.Phase)
	require.Equal(t, "mr-1-sandbox", mr.Status.SandboxPipelineRunName,
		"the reconciler still tracks the RunName it chose, even though no real object was ever created")
	requireNoPipelineRuns(t, ns)

	// 2. Sandbox stage succeeds (scripted): reconciler advances to
	// promotion and immediately drives the (also scripted) promotion
	// stage for the default "staging" namespace.
	fakeRunner.ScriptStage("sandbox", stagecommon.StageStatus{Phase: stagecommon.StageSucceeded})
	fakeRunner.ScriptStage("promotion-staging", stagecommon.StageStatus{Phase: stagecommon.StageRunning})
	mr = reconcile()
	require.Equal(t, "PromotionRunning", mr.Status.Phase)
	requireNoPipelineRuns(t, ns)

	// Prove the reconciler built a real, correct StageSpec for the
	// promotion stage from the actual ModelRequest/profile/
	// platformConfig/CapacityPlan -- only execution and status-reading
	// are faked, not the domain logic that decides what to run.
	var promotionCall *stagecommon.StageSpec
	for i := range fakeRunner.Calls {
		if fakeRunner.Calls[i].Name == "promotion-staging" {
			promotionCall = &fakeRunner.Calls[i]
		}
	}
	require.NotNil(t, promotionCall, "reconciler must have invoked the promotion-staging stage")
	require.Equal(t, "mr-1-promotion-staging", promotionCall.RunName)
	require.Equal(t, "model-intake-promotion", promotionCall.WorkflowRef)
	require.Equal(t, "ibm-granite/granite-3.0-2b-instruct", promotionCall.Params["model-id"])
	require.Equal(t, "staging", promotionCall.Params["target-namespace"])
	require.Equal(t, "true", promotionCall.Params["run-register"], "the only promotion namespace is both first and last")

	// 3. Promotion stage succeeds (scripted): reconciler reports overall
	// success.
	fakeRunner.ScriptStage("promotion-staging", stagecommon.StageStatus{Phase: stagecommon.StageSucceeded})
	mr = reconcile()
	require.Equal(t, "Succeeded", mr.Status.Phase)
	requireNoPipelineRuns(t, ns)
}

func TestModelRequest_SandboxFails_UsingFakeStageRunner_ReportsFailedPhase_NoTektonInvolved(t *testing.T) {
	ns := newTestNamespace(t)
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
	newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
	newModelRequest(t, ns, "mr-1", "profile-1", nil)
	setupSucceededCapacityPlan(t, ns, "mr-1")

	fakeRunner := stagecommon.NewFakeStageRunner()
	fakeRunner.ScriptStage("sandbox", stagecommon.StageStatus{
		Phase:   stagecommon.StageFailed,
		Message: "fake compliance scan failed",
	})
	r := &ModelRequestReconciler{Client: k8sClient, Scheme: testRuntimeScheme(), StageRunner: fakeRunner}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "mr-1")})
	require.NoError(t, err)

	mr := getModelRequest(t, ns, "mr-1")
	require.Equal(t, "Failed", mr.Status.Phase)
	require.Contains(t, mr.Status.Message, "fake compliance scan failed")
	requireNoPipelineRuns(t, ns)
}

// TestModelRequest_FullLifecycle_FakeClientWithoutTektonScheme is the
// strongest form of the Phase 4 proof: the client's scheme never
// registers tektonv1 at all, so the reconciler cannot construct a
// tektonv1.PipelineRun object even if some code path tried to.
func TestModelRequest_FullLifecycle_FakeClientWithoutTektonScheme(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, modelopsv1alpha1.AddToScheme(scheme))
	// Deliberately NOT registering tektonv1.AddToScheme(scheme) -- the
	// whole point of this test.

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&modelopsv1alpha1.CapacityPlan{}, &modelopsv1alpha1.ModelRequest{}).
		Build()
	ctx := context.Background()
	ns := "ns-1"

	require.NoError(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}))
	require.NoError(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "staging"}}))

	secretName := "mr-1-s3-credentials"
	require.NoError(t, c.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
		Data: map[string][]byte{
			"endpoint":        []byte("http://test-minio.example.svc.cluster.local:9000"),
			"accessKeyId":     []byte("test-access-key"),
			"secretAccessKey": []byte("test-secret-key"),
		},
	}))

	require.NoError(t, c.Create(ctx, &modelopsv1alpha1.PlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "cfg-1", Namespace: ns},
	}))
	require.NoError(t, c.Create(ctx, &modelopsv1alpha1.ModelLifecycleProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "profile-1", Namespace: ns},
		Spec:       defaultProfileSpec("cfg-1"),
	}))
	require.NoError(t, c.Create(ctx, &modelopsv1alpha1.ModelRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-1", Namespace: ns},
		Spec: modelopsv1alpha1.ModelRequestSpec{
			Model: modelopsv1alpha1.ModelIdentity{
				SourceType: "huggingface",
				URI:        "ibm-granite/granite-3.0-2b-instruct",
				Name:       "mr-1",
				Version:    "v1",
			},
			LifecycleProfile:  "profile-1",
			ScanS3SecretName:  secretName,
			ResultS3SecretName: secretName,
		},
	}))

	plan := &modelopsv1alpha1.CapacityPlan{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-1-capacity", Namespace: ns},
		Spec: modelopsv1alpha1.CapacityPlanSpec{
			ModelRef: modelopsv1alpha1.CapacityPlanModelRef{ModelRequestName: "mr-1"},
		},
	}
	require.NoError(t, c.Create(ctx, plan))
	plan.Status.Phase = "Succeeded"
	plan.Status.GPUsNeeded = 2
	require.NoError(t, c.Status().Update(ctx, plan))

	fakeRunner := stagecommon.NewFakeStageRunner()
	fakeRunner.ScriptStage("sandbox", stagecommon.StageStatus{Phase: stagecommon.StageSucceeded})
	fakeRunner.ScriptStage("promotion-staging", stagecommon.StageStatus{Phase: stagecommon.StageSucceeded})

	r := &ModelRequestReconciler{Client: c, Scheme: scheme, StageRunner: fakeRunner}

	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "mr-1", Namespace: ns}})
	require.NoError(t, err)

	var got modelopsv1alpha1.ModelRequest
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "mr-1", Namespace: ns}, &got))
	require.Equal(t, "Succeeded", got.Status.Phase)

	require.Len(t, fakeRunner.Calls, 2, "both the sandbox and promotion-staging stages were invoked")
	require.Equal(t, "sandbox", fakeRunner.Calls[0].Name)
	require.Equal(t, "promotion-staging", fakeRunner.Calls[1].Name)
}
