package capacityplanning

// TDD: written before stagerunner.go existed. StageRunner.EnsureRun is
// genuinely new logic (Phase 6) -- prior to this phase, CapacityPlan
// creation/status-reading was hardcoded inline in
// ModelRequestReconciler.Reconcile, never behind the StageRunner
// contract at all.

import (
	"context"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, modelopsv1alpha1.AddToScheme(s))
	return s
}

func newFakeClient(t *testing.T) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithStatusSubresource(&modelopsv1alpha1.CapacityPlan{}).
		Build()
}

func newOwnerModelRequest(name, ns string) *modelopsv1alpha1.ModelRequest {
	return &modelopsv1alpha1.ModelRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: "test-uid"},
	}
}

func TestEnsureRun_NotFound_CreatesCapacityPlanFromNativeSpec_ReturnsRunning(t *testing.T) {
	c := newFakeClient(t)
	r := &StageRunner{Client: c, Scheme: testScheme(t)}
	mr := newOwnerModelRequest("mr-1", "ns-1")

	status, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name:    "capacity",
		RunName: "mr-1-capacity",
		NativeSpec: &modelopsv1alpha1.CapacityPlanSpec{
			ContextLength: 8192,
		},
	})
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageRunning, status.Phase)
	require.Equal(t, "mr-1-capacity", status.RunRef)

	var plan modelopsv1alpha1.CapacityPlan
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "mr-1-capacity", Namespace: "ns-1"}, &plan))
	require.Equal(t, 8192, plan.Spec.ContextLength)
	require.Len(t, plan.OwnerReferences, 1)
	require.Equal(t, "mr-1", plan.OwnerReferences[0].Name)
}

func TestEnsureRun_MissingOrWrongTypedNativeSpec_ReturnsClearError(t *testing.T) {
	c := newFakeClient(t)
	r := &StageRunner{Client: c, Scheme: testScheme(t)}
	mr := newOwnerModelRequest("mr-1", "ns-1")

	_, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name:    "capacity",
		RunName: "mr-1-capacity",
		// NativeSpec deliberately left nil.
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "NativeSpec")
}

func TestEnsureRun_SecondCall_DoesNotRecreate(t *testing.T) {
	c := newFakeClient(t)
	r := &StageRunner{Client: c, Scheme: testScheme(t)}
	mr := newOwnerModelRequest("mr-1", "ns-1")
	spec := stagecommon.StageSpec{Name: "capacity", RunName: "mr-1-capacity", NativeSpec: &modelopsv1alpha1.CapacityPlanSpec{}}

	_, err := r.EnsureRun(context.Background(), mr, spec)
	require.NoError(t, err)
	var before modelopsv1alpha1.CapacityPlan
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "mr-1-capacity", Namespace: "ns-1"}, &before))

	status, err := r.EnsureRun(context.Background(), mr, spec)
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageRunning, status.Phase, "no Status.Phase yet -> still Running")

	var after modelopsv1alpha1.CapacityPlan
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "mr-1-capacity", Namespace: "ns-1"}, &after))
	require.Equal(t, before.ResourceVersion, after.ResourceVersion, "must not recreate/modify an already-existing CapacityPlan")
}

func TestEnsureRun_ExistingPlan_PhaseEmpty_ReturnsRunning(t *testing.T) {
	c := newFakeClient(t)
	r := &StageRunner{Client: c, Scheme: testScheme(t)}
	mr := newOwnerModelRequest("mr-1", "ns-1")
	spec := stagecommon.StageSpec{Name: "capacity", RunName: "mr-1-capacity", NativeSpec: &modelopsv1alpha1.CapacityPlanSpec{}}
	_, err := r.EnsureRun(context.Background(), mr, spec)
	require.NoError(t, err)

	status, err := r.EnsureRun(context.Background(), mr, spec)
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageRunning, status.Phase)
}

func TestEnsureRun_ExistingPlan_PhaseSucceeded_ReturnsSucceeded(t *testing.T) {
	c := newFakeClient(t)
	r := &StageRunner{Client: c, Scheme: testScheme(t)}
	mr := newOwnerModelRequest("mr-1", "ns-1")
	spec := stagecommon.StageSpec{Name: "capacity", RunName: "mr-1-capacity", NativeSpec: &modelopsv1alpha1.CapacityPlanSpec{}}
	_, err := r.EnsureRun(context.Background(), mr, spec)
	require.NoError(t, err)

	var plan modelopsv1alpha1.CapacityPlan
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "mr-1-capacity", Namespace: "ns-1"}, &plan))
	plan.Status.Phase = "Succeeded"
	plan.Status.Message = "Capacity plan: 2 x NVIDIA-A100-40GB"
	require.NoError(t, c.Status().Update(context.Background(), &plan))

	status, err := r.EnsureRun(context.Background(), mr, spec)
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageSucceeded, status.Phase)
	require.Equal(t, "Capacity plan: 2 x NVIDIA-A100-40GB", status.Message)
}

func TestEnsureRun_ExistingPlan_PhaseFailed_ReturnsFailed(t *testing.T) {
	// CapacityPlanReconciler now sets Phase="Failed" when
	// Spec.MaxGPUsPerRequest is exceeded (Phase 7 of
	// REFACTOR_PLAN.md) -- this mapping was pre-built starting in
	// Phase 6, ready for exactly this producer. Constructed directly
	// here (not via a real CapacityPlanReconciler run) since this
	// package's own responsibility is only the StageStatus mapping,
	// not reproducing CapacityPlanReconciler's own heuristic.
	c := newFakeClient(t)
	r := &StageRunner{Client: c, Scheme: testScheme(t)}
	mr := newOwnerModelRequest("mr-1", "ns-1")
	spec := stagecommon.StageSpec{Name: "capacity", RunName: "mr-1-capacity", NativeSpec: &modelopsv1alpha1.CapacityPlanSpec{}}
	_, err := r.EnsureRun(context.Background(), mr, spec)
	require.NoError(t, err)

	var plan modelopsv1alpha1.CapacityPlan
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "mr-1-capacity", Namespace: "ns-1"}, &plan))
	plan.Status.Phase = "Failed"
	plan.Status.Message = "advisor unreachable"
	require.NoError(t, c.Status().Update(context.Background(), &plan))

	status, err := r.EnsureRun(context.Background(), mr, spec)
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageFailed, status.Phase)
	require.Equal(t, "advisor unreachable", status.Message)
}

// TestStageRunner_DoesNotImplementOwnedTypesProvider is the structural
// proof (Phase 7) that CapacityPlan ownership stays where it already
// was (ModelRequestReconciler.SetupWithManager's explicit
// .Owns(&modelopsv1alpha1.CapacityPlan{}) call -- CapacityPlan is a
// core lifecycle CRD, not provider-specific) rather than being
// generalized through stagecommon.OwnedTypesProvider the way
// tekton.StageRunner's PipelineRun ownership is. See
// docs/PHASE_LOG.md Phase 7.
func TestStageRunner_DoesNotImplementOwnedTypesProvider(t *testing.T) {
	var r stagecommon.StageRunner = &StageRunner{}
	_, ok := r.(stagecommon.OwnedTypesProvider)
	require.False(t, ok)
}
