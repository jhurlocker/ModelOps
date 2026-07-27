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

// +kubebuilder:rbac:groups=modelops.example.io,resources=capacityplans,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=modelops.example.io,resources=capacityplans/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=modelops.example.io,resources=capacityplans/finalizers,verbs=update

func (r *CapacityPlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var plan modelopsv1alpha1.CapacityPlan
	if err := r.Get(ctx, req.NamespacedName, &plan); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if plan.Status.Phase == "Succeeded" {
		return ctrl.Result{}, nil
	}

	spec := plan.Spec
	baseGPUs := 1

	if spec.ContextLength > 16384 {
		baseGPUs = 4
	} else if spec.ContextLength > 8192 {
		baseGPUs = 2
	}

	if spec.Concurrency > 8 {
		baseGPUs = int(math.Min(float64(baseGPUs*2), 8))
	} else if spec.Concurrency > 4 {
		baseGPUs = int(math.Min(float64(baseGPUs+1), 8))
	}

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
