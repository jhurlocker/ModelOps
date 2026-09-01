package controller

import (
	"context"
	"strings"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	"github.com/stretchr/testify/require"
	ctrl "sigs.k8s.io/controller-runtime"
)

// TestModelRequest_ImageRefResult_SetsPromotionModelcarImage_AndPersists is
// the Phase C happy-path full-path test: when the sandbox stage produces the
// Zot-built image reference ("image-ref"), it must (a) survive into the
// persisted Status.Stages[] (lineage/evidence) and (b) be carried forward by
// the walker into promotion.Handler's StageContext, so the promotion
// PipelineRun's built params bind modelcar-image to the same reference.
func TestModelRequest_ImageRefResult_SetsPromotionModelcarImage_AndPersists(t *testing.T) {
	ns := newTestNamespace(t)
	ensureNamespace(t, "staging")
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})

	fakeRunner := stagecommon.NewFakeStageRunner()

	profileSpec := modelopsv1alpha1.ModelLifecycleProfileSpec{
		Workflow:          modelopsv1alpha1.WorkflowRef{Engine: "tekton", PipelineRef: "model-intake-sandbox"},
		PlatformConfigRef: "cfg-1",
		Stages: []modelopsv1alpha1.ProfileStageSpec{
			{Name: "capacity", Kind: "CapacityPlan"},
			{Name: "sandbox", Kind: "PipelineRun"},
			{Name: "promotion", Kind: "PipelineRun", PerNamespace: true},
		},
	}
	newProfile(t, ns, "profile-ir", profileSpec)

	newModelRequest(t, ns, "mr-ir", "profile-ir", nil)
	setupSucceededCapacityPlan(t, ns, "mr-ir")

	const ref = "zot.modelops-zot.svc.cluster.local:5000/mr-ir:v1"
	fakeRunner.ScriptStage("sandbox", stagecommon.StageStatus{
		Phase:   stagecommon.StageSucceeded,
		Reason:  "Succeeded",
		Message: "sandbox complete",
		Results: []stagecommon.StageResult{{Name: stagecommon.ResultImageRef, Value: ref}},
	})

	r := &ModelRequestReconciler{
		Client:        k8sClient,
		Scheme:        testRuntimeScheme(),
		StageHandlers: newStageHandlers(),
		StageRunners:  newStageRunners(k8sClient, testRuntimeScheme(), fakeRunner),
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "mr-ir")})
	require.NoError(t, err)

	got := getModelRequest(t, ns, "mr-ir")
	require.Len(t, got.Status.Stages, 3, "capacity + sandbox + promotion")

	// (a) full-path survival into the persisted CRD status.
	var sandboxResults []modelopsv1alpha1.StageResult
	for _, s := range got.Status.Stages {
		if s.Name == "sandbox" {
			sandboxResults = s.Results
		}
	}
	require.Len(t, sandboxResults, 1)
	require.Equal(t, modelopsv1alpha1.StageResult{Name: "image-ref", Value: ref}, sandboxResults[0])

	// (b) the promotion stage's built params carried the reference.
	var promoParams map[string]string
	for _, call := range fakeRunner.Calls {
		if strings.HasPrefix(call.Name, "promotion") {
			promoParams = call.Params
		}
	}
	require.NotNil(t, promoParams, "promotion stage must have been attempted")
	require.Equal(t, ref, promoParams["modelcar-image"],
		"promotion's Params must bind modelcar-image to the sandbox-produced image-ref")
}

// TestModelRequest_NoImageRefResult_PromotionParamsUnchanged covers the
// oci/s3 negative control: the sandbox stage produces no image-ref result
// (build-modelcar was skipped), so promotion's built params must be
// byte-identical to today -- no modelcar-image key at all.
func TestModelRequest_NoImageRefResult_PromotionParamsUnchanged(t *testing.T) {
	ns := newTestNamespace(t)
	ensureNamespace(t, "staging")
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})

	fakeRunner := stagecommon.NewFakeStageRunner()

	profileSpec := modelopsv1alpha1.ModelLifecycleProfileSpec{
		Workflow:          modelopsv1alpha1.WorkflowRef{Engine: "tekton", PipelineRef: "model-intake-sandbox"},
		PlatformConfigRef: "cfg-1",
		Stages: []modelopsv1alpha1.ProfileStageSpec{
			{Name: "capacity", Kind: "CapacityPlan"},
			{Name: "sandbox", Kind: "PipelineRun"},
			{Name: "promotion", Kind: "PipelineRun", PerNamespace: true},
		},
	}
	newProfile(t, ns, "profile-noir", profileSpec)

	// oci source type: the path that skips build-modelcar entirely.
	newModelRequest(t, ns, "mr-noir", "profile-noir", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.Model.SourceType = "oci"
		mr.Spec.Model.URI = "quay.io/models/foo:v1"
	})
	setupSucceededCapacityPlan(t, ns, "mr-noir")

	// Sandbox succeeds with NO results: the build was skipped, so nothing
	// to forward. (The fake runner models the "no image-ref" outcome.)
	fakeRunner.ScriptStage("sandbox", stagecommon.StageStatus{
		Phase:   stagecommon.StageSucceeded,
		Reason:  "Succeeded",
		Message: "sandbox complete (no build; oci source)",
	})

	r := &ModelRequestReconciler{
		Client:        k8sClient,
		Scheme:        testRuntimeScheme(),
		StageHandlers: newStageHandlers(),
		StageRunners:  newStageRunners(k8sClient, testRuntimeScheme(), fakeRunner),
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "mr-noir")})
	require.NoError(t, err)

	got := getModelRequest(t, ns, "mr-noir")
	for _, s := range got.Status.Stages {
		if s.Name == "sandbox" {
			require.Empty(t, s.Results, "sandbox must persist no results for the oci/s3 path")
		}
	}

	for _, call := range fakeRunner.Calls {
		if strings.HasPrefix(call.Name, "promotion") {
			_, hasModelcarImage := call.Params["modelcar-image"]
			require.False(t, hasModelcarImage,
				"promotion's params must remain byte-identical (no modelcar-image) when sandbox produced no image-ref")
		}
	}
}
