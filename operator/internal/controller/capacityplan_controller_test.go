package controller

// Characterization tests for CapacityPlanReconciler's CURRENT behavior
// (Phase 0 of REFACTOR_PLAN.md). These pin today's GPU-sizing heuristic
// and status transitions exactly as they exist today, so later phases
// that relocate this logic into internal/stages/capacityplanning can
// prove they haven't changed it.

import (
	"context"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func newCapacityPlan(t *testing.T, ns, name string, spec modelopsv1alpha1.CapacityPlanSpec) *modelopsv1alpha1.CapacityPlan {
	t.Helper()
	if spec.ModelRef.ModelRequestName == "" {
		spec.ModelRef.ModelRequestName = name
	}
	plan := &modelopsv1alpha1.CapacityPlan{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       spec,
	}
	require.NoError(t, k8sClient.Create(context.Background(), plan))
	return plan
}

func reconcileCapacityPlan(t *testing.T, ns, name string) (*modelopsv1alpha1.CapacityPlan, ctrl.Result, error) {
	t.Helper()
	r := &CapacityPlanReconciler{Client: k8sClient, Scheme: testRuntimeScheme()}
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, name)})

	var plan modelopsv1alpha1.CapacityPlan
	getErr := k8sClient.Get(context.Background(), nsName(ns, name), &plan)
	if getErr != nil {
		return nil, res, err
	}
	return &plan, res, err
}

func TestCapacityPlan_SmallContextLowConcurrency_UsesL40S(t *testing.T) {
	ns := newTestNamespace(t)
	newCapacityPlan(t, ns, "plan-1", modelopsv1alpha1.CapacityPlanSpec{
		ContextLength: 8192,
		Concurrency:   4,
	})

	plan, _, err := reconcileCapacityPlan(t, ns, "plan-1")
	require.NoError(t, err)
	require.Equal(t, "Succeeded", plan.Status.Phase)
	require.Equal(t, 1, plan.Status.GPUsNeeded)
	require.Equal(t, "NVIDIA-L40S", plan.Status.GPUModel)
}

func TestCapacityPlan_MidContext_UsesA100AndTwoGPUs(t *testing.T) {
	ns := newTestNamespace(t)
	newCapacityPlan(t, ns, "plan-1", modelopsv1alpha1.CapacityPlanSpec{
		ContextLength: 16384,
		Concurrency:   1,
	})

	plan, _, err := reconcileCapacityPlan(t, ns, "plan-1")
	require.NoError(t, err)
	require.Equal(t, 2, plan.Status.GPUsNeeded)
	require.Equal(t, "NVIDIA-A100-40GB", plan.Status.GPUModel)
}

func TestCapacityPlan_LargeContext_UsesFourGPUs(t *testing.T) {
	ns := newTestNamespace(t)
	newCapacityPlan(t, ns, "plan-1", modelopsv1alpha1.CapacityPlanSpec{
		ContextLength: 32768,
		Concurrency:   1,
	})

	plan, _, err := reconcileCapacityPlan(t, ns, "plan-1")
	require.NoError(t, err)
	require.Equal(t, 4, plan.Status.GPUsNeeded)
	require.Equal(t, "NVIDIA-A100-40GB", plan.Status.GPUModel)
}

func TestCapacityPlan_ModerateConcurrency_AddsOneGPU(t *testing.T) {
	ns := newTestNamespace(t)
	// contextLength<=8192 -> baseGPUs=1; concurrency in (4,8] -> +1 = 2
	newCapacityPlan(t, ns, "plan-1", modelopsv1alpha1.CapacityPlanSpec{
		ContextLength: 8192,
		Concurrency:   8,
	})

	plan, _, err := reconcileCapacityPlan(t, ns, "plan-1")
	require.NoError(t, err)
	require.Equal(t, 2, plan.Status.GPUsNeeded)
}

func TestCapacityPlan_HighConcurrency_DoublesGPUs(t *testing.T) {
	ns := newTestNamespace(t)
	// contextLength<=8192 -> baseGPUs=1; concurrency>8 -> *2 = 2
	newCapacityPlan(t, ns, "plan-1", modelopsv1alpha1.CapacityPlanSpec{
		ContextLength: 8192,
		Concurrency:   16,
	})

	plan, _, err := reconcileCapacityPlan(t, ns, "plan-1")
	require.NoError(t, err)
	require.Equal(t, 2, plan.Status.GPUsNeeded)
}

func TestCapacityPlan_LargeContextAndHighConcurrency_CapsAtEightGPUs(t *testing.T) {
	ns := newTestNamespace(t)
	// contextLength>16384 -> baseGPUs=4; concurrency>8 -> min(4*2, 8) = 8
	newCapacityPlan(t, ns, "plan-1", modelopsv1alpha1.CapacityPlanSpec{
		ContextLength: 32768,
		Concurrency:   16,
	})

	plan, _, err := reconcileCapacityPlan(t, ns, "plan-1")
	require.NoError(t, err)
	require.Equal(t, 8, plan.Status.GPUsNeeded)
}

func TestCapacityPlan_MIGEnabled_AppendsSuffixToGPUModel(t *testing.T) {
	ns := newTestNamespace(t)
	newCapacityPlan(t, ns, "plan-1", modelopsv1alpha1.CapacityPlanSpec{
		ContextLength: 8192,
		Concurrency:   4,
		AllowMIG:      true,
	})

	plan, _, err := reconcileCapacityPlan(t, ns, "plan-1")
	require.NoError(t, err)
	require.Equal(t, "NVIDIA-L40S (MIG enabled)", plan.Status.GPUModel)
}

func TestCapacityPlan_ZeroValueSpec_FallsIntoSmallestTier(t *testing.T) {
	ns := newTestNamespace(t)
	// Pins today's behavior for an all-zero-value spec: the reconciler
	// itself does no defaulting (that happens upstream in
	// ModelRequestReconciler.buildCapacityPlan via intOrDefault); a
	// CapacityPlan with ContextLength=0, Concurrency=0 falls into the
	// same <=8192/<=4 branch as an explicitly-small request.
	newCapacityPlan(t, ns, "plan-1", modelopsv1alpha1.CapacityPlanSpec{})

	plan, _, err := reconcileCapacityPlan(t, ns, "plan-1")
	require.NoError(t, err)
	require.Equal(t, 1, plan.Status.GPUsNeeded)
	require.Equal(t, "NVIDIA-L40S", plan.Status.GPUModel)
}

func TestCapacityPlan_AlreadySucceeded_IsANoOp(t *testing.T) {
	ns := newTestNamespace(t)
	newCapacityPlan(t, ns, "plan-1", modelopsv1alpha1.CapacityPlanSpec{
		ContextLength: 8192,
		Concurrency:   4,
	})

	// First reconcile computes and persists the plan.
	plan, _, err := reconcileCapacityPlan(t, ns, "plan-1")
	require.NoError(t, err)
	require.Equal(t, "Succeeded", plan.Status.Phase)
	rvAfterFirst := plan.ResourceVersion

	// Second reconcile against an already-Succeeded plan must not
	// recompute or re-issue a status update.
	plan2, _, err := reconcileCapacityPlan(t, ns, "plan-1")
	require.NoError(t, err)
	require.Equal(t, rvAfterFirst, plan2.ResourceVersion, "reconciling an already-Succeeded CapacityPlan must not write status again")
}

func TestCapacityPlan_DeletedBeforeReconcile_IsIgnored(t *testing.T) {
	ns := newTestNamespace(t)
	// No CapacityPlan named "does-not-exist" was ever created in ns.
	r := &CapacityPlanReconciler{Client: k8sClient, Scheme: testRuntimeScheme()}
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "does-not-exist")})
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, res)
}

func TestCapacityPlan_MessageFormat_MatchesCurrentTemplate(t *testing.T) {
	ns := newTestNamespace(t)
	newCapacityPlan(t, ns, "plan-1", modelopsv1alpha1.CapacityPlanSpec{
		ContextLength:    8192,
		Concurrency:      4,
		AllowTimeSlicing: true,
	})

	plan, _, err := reconcileCapacityPlan(t, ns, "plan-1")
	require.NoError(t, err)
	require.Equal(t,
		"Capacity plan: 1 x NVIDIA-L40S for context=8192 concurrency=4 time-slicing=true",
		plan.Status.Message,
	)
}
