package controller

import (
	"context"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"
	"github.com/jhurlocker/modelops-operator/internal/stages/noop"
	"github.com/jhurlocker/modelops-operator/internal/stages/sandbox"

	"github.com/stretchr/testify/require"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestModelRequest_DecomposedAndCombinedCheckTypes_ProduceEquivalentGovernanceContent(t *testing.T) {
	sh := sandbox.Handler{}

	t.Run("decomposed", func(t *testing.T) {
		ns := newTestNamespace(t)
		ensureNamespace(t, "staging")
		newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})

		profileSpec := modelopsv1alpha1.ModelLifecycleProfileSpec{
			Workflow: modelopsv1alpha1.WorkflowRef{
				Engine:      "tekton",
				PipelineRef: "model-intake-sandbox",
			},
			PlatformConfigRef: "cfg-1",
			Stages: []modelopsv1alpha1.ProfileStageSpec{
				{Name: "capacity", Kind: "CapacityPlan"},
				{
					Name:       "sandbox-security",
					Kind:       "PipelineRun",
					CheckTypes: []modelopsv1alpha1.CheckType{modelopsv1alpha1.CheckTypeSecurityScan},
				},
				{
					Name:       "sandbox-compliance",
					Kind:       "PipelineRun",
					CheckTypes: []modelopsv1alpha1.CheckType{modelopsv1alpha1.CheckTypeComplianceScan},
				},
				{
					Name:       "sandbox-benchmark",
					Kind:       "PipelineRun",
					CheckTypes: []modelopsv1alpha1.CheckType{modelopsv1alpha1.CheckTypeBenchmark},
				},
				{
					Name:         "promotion",
					Kind:         "PipelineRun",
					PerNamespace: true,
					NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{
						EnsureRBAC: true,
						Labels:     map[string]string{"maas.opendatahub.io/gateway-access": "true"},
					},
				},
			},
		}
		newProfile(t, ns, "profile-decomp", profileSpec)

		newModelRequest(t, ns, "mr-decomp", "profile-decomp", nil)
		setupSucceededCapacityPlan(t, ns, "mr-decomp")

		handlers := newStageHandlers()
		handlers["sandbox-security"] = sh
		handlers["sandbox-compliance"] = sh
		handlers["sandbox-benchmark"] = sh

		r := &ModelRequestReconciler{
			Client:        k8sClient,
			Scheme:        testRuntimeScheme(),
			StageHandlers: handlers,
			StageRunners:  newStageRunners(k8sClient, testRuntimeScheme(), &noop.StageRunner{}),
		}
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "mr-decomp")})
		require.NoError(t, err)

		got := getModelRequest(t, ns, "mr-decomp")
		require.Equal(t, "Succeeded", got.Status.Phase)

		stageNames := make(map[string]bool, len(got.Status.Stages))
		for _, s := range got.Status.Stages {
			stageNames[s.Name] = true
		}
		for _, name := range []string{"capacity", "sandbox-security", "sandbox-compliance", "sandbox-benchmark", "promotion"} {
			require.True(t, stageNames[name], "stage %q must appear in Status.Stages for decomposed profile", name)
		}
		require.Len(t, got.Status.Stages, 5, "decomposed: capacity + 3 sandbox variants + promotion")
	})

	t.Run("combined", func(t *testing.T) {
		ns := newTestNamespace(t)
		ensureNamespace(t, "staging")
		newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})

		profileSpec := modelopsv1alpha1.ModelLifecycleProfileSpec{
			Workflow: modelopsv1alpha1.WorkflowRef{
				Engine:      "tekton",
				PipelineRef: "model-intake-sandbox",
			},
			PlatformConfigRef: "cfg-1",
			Stages: []modelopsv1alpha1.ProfileStageSpec{
				{Name: "capacity", Kind: "CapacityPlan"},
				{
					Name: "sandbox",
					Kind: "PipelineRun",
					CheckTypes: []modelopsv1alpha1.CheckType{
						modelopsv1alpha1.CheckTypeSecurityScan,
						modelopsv1alpha1.CheckTypeComplianceScan,
						modelopsv1alpha1.CheckTypeBenchmark,
					},
				},
				{
					Name:         "promotion",
					Kind:         "PipelineRun",
					PerNamespace: true,
					NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{
						EnsureRBAC: true,
						Labels:     map[string]string{"maas.opendatahub.io/gateway-access": "true"},
					},
				},
			},
		}
		newProfile(t, ns, "profile-combo", profileSpec)

		newModelRequest(t, ns, "mr-combo", "profile-combo", nil)
		setupSucceededCapacityPlan(t, ns, "mr-combo")

		r := &ModelRequestReconciler{
			Client:        k8sClient,
			Scheme:        testRuntimeScheme(),
			StageHandlers: newStageHandlers(),
			StageRunners:  newStageRunners(k8sClient, testRuntimeScheme(), &noop.StageRunner{}),
		}
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "mr-combo")})
		require.NoError(t, err)

		got := getModelRequest(t, ns, "mr-combo")
		require.Equal(t, "Succeeded", got.Status.Phase)

		stageNames := make(map[string]bool, len(got.Status.Stages))
		for _, s := range got.Status.Stages {
			stageNames[s.Name] = true
		}
		for _, name := range []string{"capacity", "sandbox", "promotion"} {
			require.True(t, stageNames[name], "stage %q must appear in Status.Stages for combined profile", name)
		}
		require.Len(t, got.Status.Stages, 3, "combined: capacity + sandbox + promotion")

		profile := getProfile(t, ns, "profile-combo")
		require.Len(t, profile.Spec.Stages, 3)
		sandboxStage := profile.Spec.Stages[1]
		require.Equal(t, "sandbox", sandboxStage.Name)
		require.Len(t, sandboxStage.CheckTypes, 3)
		require.Contains(t, sandboxStage.CheckTypes, modelopsv1alpha1.CheckTypeSecurityScan)
		require.Contains(t, sandboxStage.CheckTypes, modelopsv1alpha1.CheckTypeComplianceScan)
		require.Contains(t, sandboxStage.CheckTypes, modelopsv1alpha1.CheckTypeBenchmark)
	})
}

func TestModelRequest_CheckResults_SurvivesFullPathFromStageRunnerToPersistedStatus(t *testing.T) {
	ns := newTestNamespace(t)
	ensureNamespace(t, "staging")
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})

	fakeRunner := stagecommon.NewFakeStageRunner()

	profileSpec := modelopsv1alpha1.ModelLifecycleProfileSpec{
		Workflow: modelopsv1alpha1.WorkflowRef{
			Engine:      "tekton",
			PipelineRef: "model-intake-sandbox",
		},
		PlatformConfigRef: "cfg-1",
		Stages: []modelopsv1alpha1.ProfileStageSpec{
			{Name: "capacity", Kind: "CapacityPlan"},
			{
				Name: "sandbox",
				Kind: "PipelineRun",
				CheckTypes: []modelopsv1alpha1.CheckType{
					modelopsv1alpha1.CheckTypeSecurityScan,
					modelopsv1alpha1.CheckTypeComplianceScan,
				},
			},
			{
				Name:         "promotion",
				Kind:         "PipelineRun",
				PerNamespace: true,
				NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{
					EnsureRBAC: true,
					Labels:     map[string]string{"maas.opendatahub.io/gateway-access": "true"},
				},
			},
		},
	}
	newProfile(t, ns, "profile-cr", profileSpec)

	newModelRequest(t, ns, "mr-cr", "profile-cr", nil)
	setupSucceededCapacityPlan(t, ns, "mr-cr")

	fakeRunner.ScriptStage("sandbox", stagecommon.StageStatus{
		Phase:   stagecommon.StageSucceeded,
		Reason:  "JobSucceeded",
		Message: "all checks passed",
		CheckResults: []stagecommon.CheckResult{
			{Type: "SecurityScan", Passed: true, Reason: "no-cves"},
			{Type: "ComplianceScan", Passed: true, Reason: "policy-ok"},
		},
	})
	fakeRunner.ScriptStage("promotion", stagecommon.StageStatus{
		Phase:   stagecommon.StageSucceeded,
		Reason:  "JobSucceeded",
		Message: "promoted",
	})

	r := &ModelRequestReconciler{
		Client:        k8sClient,
		Scheme:        testRuntimeScheme(),
		StageHandlers: newStageHandlers(),
		StageRunners:  newStageRunners(k8sClient, testRuntimeScheme(), fakeRunner),
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "mr-cr")})
	require.NoError(t, err)
	got := getModelRequest(t, ns, "mr-cr")
	require.Equal(t, "promotionRunning", got.Status.Phase, "sandbox completes in first pass, promotion is pending")

	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "mr-cr")})
	require.NoError(t, err)
	got = getModelRequest(t, ns, "mr-cr")
	require.Len(t, got.Status.Stages, 3)

	sandboxStatus := got.Status.Stages[1]
	require.Equal(t, "sandbox", sandboxStatus.Name)
	require.Len(t, sandboxStatus.CheckResults, 2)

	require.Equal(t, modelopsv1alpha1.CheckTypeSecurityScan, sandboxStatus.CheckResults[0].Type)
	require.True(t, sandboxStatus.CheckResults[0].Passed)
	require.Equal(t, "no-cves", sandboxStatus.CheckResults[0].Reason)

	require.Equal(t, modelopsv1alpha1.CheckTypeComplianceScan, sandboxStatus.CheckResults[1].Type)
	require.True(t, sandboxStatus.CheckResults[1].Passed)
	require.Equal(t, "policy-ok", sandboxStatus.CheckResults[1].Reason)
}

func TestModelRequest_DetailsURL_SurvivesFullPathFromStageRunnerToPersistedStatus(t *testing.T) {
	ns := newTestNamespace(t)
	ensureNamespace(t, "staging")
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})

	fakeRunner := stagecommon.NewFakeStageRunner()

	profileSpec := modelopsv1alpha1.ModelLifecycleProfileSpec{
		Workflow: modelopsv1alpha1.WorkflowRef{
			Engine:      "tekton",
			PipelineRef: "model-intake-sandbox",
		},
		PlatformConfigRef: "cfg-1",
		Stages: []modelopsv1alpha1.ProfileStageSpec{
			{Name: "capacity", Kind: "CapacityPlan"},
			{Name: "sandbox", Kind: "PipelineRun"},
			{
				Name:         "promotion",
				Kind:         "PipelineRun",
				PerNamespace: true,
				NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{
					EnsureRBAC: true,
					Labels:     map[string]string{"maas.opendatahub.io/gateway-access": "true"},
				},
			},
		},
	}
	newProfile(t, ns, "profile-dr", profileSpec)

	newModelRequest(t, ns, "mr-dr", "profile-dr", nil)
	setupSucceededCapacityPlan(t, ns, "mr-dr")

	fakeRunner.ScriptStage("sandbox", stagecommon.StageStatus{
		Phase:      stagecommon.StageSucceeded,
		Reason:     "JobSucceeded",
		Message:    "sandbox complete",
		DetailsURL: "https://provider.example.com/console/jobs/j-12345",
	})
	fakeRunner.ScriptStage("promotion", stagecommon.StageStatus{
		Phase:   stagecommon.StageSucceeded,
		Reason:  "JobSucceeded",
		Message: "promoted",
	})

	r := &ModelRequestReconciler{
		Client:        k8sClient,
		Scheme:        testRuntimeScheme(),
		StageHandlers: newStageHandlers(),
		StageRunners:  newStageRunners(k8sClient, testRuntimeScheme(), fakeRunner),
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "mr-dr")})
	require.NoError(t, err)
	got := getModelRequest(t, ns, "mr-dr")
	require.Equal(t, "promotionRunning", got.Status.Phase, "sandbox completes in first pass, promotion is pending")

	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "mr-dr")})
	require.NoError(t, err)
	got = getModelRequest(t, ns, "mr-dr")
	require.Len(t, got.Status.Stages, 3)

	sandboxStatus := got.Status.Stages[1]
	require.Equal(t, "sandbox", sandboxStatus.Name)
	require.Equal(t, "https://provider.example.com/console/jobs/j-12345", sandboxStatus.DetailsURL)
}

func getProfile(t *testing.T, ns, name string) *modelopsv1alpha1.ModelLifecycleProfile {
	t.Helper()
	var p modelopsv1alpha1.ModelLifecycleProfile
	if err := k8sClient.Get(context.Background(), nsName(ns, name), &p); err != nil {
		t.Fatalf("failed to get ModelLifecycleProfile %s/%s: %v", ns, name, err)
	}
	return &p
}