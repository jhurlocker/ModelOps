package controller

import (
	"context"
	"fmt"
	"math"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type CapacityPlanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=modelops.example.io,resources=capacityplans,verbs=get;list;watch
// +kubebuilder:rbac:groups=modelops.example.io,resources=capacityplans/status,verbs=get;update;patch

func (r *CapacityPlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var plan modelopsv1alpha1.CapacityPlan
	if err := r.Get(ctx, req.NamespacedName, &plan); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if plan.Status.Phase == "Succeeded" || plan.Status.Phase == "Failed" {
		return ctrl.Result{}, nil
	}

	spec := plan.Spec

	// rawGPUs is the heuristic's logical GPU recommendation, computed
	// independent of the hard 8-GPU ceiling applied below. This is what
	// MaxGPUsPerRequest (Phase 7 of REFACTOR_PLAN.md) is compared
	// against: a configured ceiling below 8 can now actually reject a
	// request the old silent math.Min(...,8) clamp would otherwise have
	// quietly accepted at a wrong, capped GPU count. Mathematically
	// equivalent to the pre-Phase-7 per-branch-clamped computation for
	// every case where no ceiling is configured (see
	// TestCapacityPlan_MaxGPUsPerRequestUnset_PreservesExactPreviousClampingBehavior):
	// the only branch that could ever clamp below the sum of the two
	// steps is doubling exactly to 8, which a single final
	// math.Min(rawGPUs, 8) reproduces identically.
	rawGPUs := 1
	if spec.ContextLength > 16384 {
		rawGPUs = 4
	} else if spec.ContextLength > 8192 {
		rawGPUs = 2
	}
	if spec.Concurrency > 8 {
		rawGPUs *= 2
	} else if spec.Concurrency > 4 {
		rawGPUs++
	}

	// A configured ceiling is a deterministic, input-derived condition
	// that will never resolve by retrying -- a genuine Failed, not a
	// transient/Running condition (see docs/REFACTOR_PLAN.md Phase 7:
	// real GPU-inventory/advisor-based feasibility checking, which
	// WOULD have transient failure modes, is explicitly out of scope
	// for this pass -- see the Phase 7 backlog note added to that
	// document).
	if spec.MaxGPUsPerRequest > 0 && rawGPUs > spec.MaxGPUsPerRequest {
		plan.Status.Phase = "Failed"
		plan.Status.Message = fmt.Sprintf(
			"requested capacity (%d GPUs) exceeds configured maximum (%d)",
			rawGPUs, spec.MaxGPUsPerRequest,
		)
		if err := r.Status().Update(ctx, &plan); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("capacity plan failed: requested GPUs exceed configured maximum",
			"requested", rawGPUs, "max", spec.MaxGPUsPerRequest)
		return ctrl.Result{}, nil
	}

	baseGPUs := int(math.Min(float64(rawGPUs), 8))

	gpuModel := "NVIDIA-A100-40GB"
	if spec.ContextLength <= 8192 && spec.Concurrency <= 4 {
		gpuModel = "NVIDIA-L40S"
	}

	if spec.AllowMIG {
		gpuModel += " (MIG enabled)"
	}

	plan.Status.Phase = "Succeeded"
	plan.Status.GPUsNeeded = baseGPUs
	plan.Status.GPUModel = gpuModel
	plan.Status.Message = fmt.Sprintf(
		"Capacity plan: %d x %s for context=%d concurrency=%d time-slicing=%v",
		baseGPUs, gpuModel, spec.ContextLength, spec.Concurrency, spec.AllowTimeSlicing,
	)

	if err := r.Status().Update(ctx, &plan); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("capacity plan complete", "gpus", baseGPUs, "model", gpuModel)
	return ctrl.Result{}, nil
}

func (r *CapacityPlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&modelopsv1alpha1.CapacityPlan{}).
		Complete(r)
}
