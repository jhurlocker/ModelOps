package capacityplanning

// Characterization test for Handler.BuildSpec, relocated (Phase 6) from
// internal/controller's pre-Phase-6 buildCapacityPlan -- same field
// mapping/defaults, now producing a stagecommon.StageSpec with a
// *modelopsv1alpha1.CapacityPlanSpec NativeSpec instead of a bare
// modelopsv1alpha1.CapacityPlan object.

import (
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	"github.com/stretchr/testify/require"
)

func boolPtr(b bool) *bool { return &b }

func TestHandler_BuildSpec_UsesRequirementsAndPlatformConfigWithDefaults(t *testing.T) {
	mr := &modelopsv1alpha1.ModelRequest{
		Spec: modelopsv1alpha1.ModelRequestSpec{
			Requirements: &modelopsv1alpha1.ModelRequirements{
				BenchmarkTargets: modelopsv1alpha1.BenchmarkTargets{ContextLength: 8192, ExpectedConcurrency: 4},
				GPUConfig:        modelopsv1alpha1.GPUConfig{AllowMIG: boolPtr(true), GPUIsolationPolicy: "shared"},
				AdvisorEndpoint:  "http://advisor.example.com",
			},
		},
	}
	cfg := &modelopsv1alpha1.PlatformConfig{
		Spec: modelopsv1alpha1.PlatformConfigSpec{
			AdvisorSecretName:     "advisor-secret",
			AdvisorTimeoutSeconds: 120,
			GPUOperatorNamespace:  "gpu-ns",
			ClusterPolicyName:     "cluster-policy",
			TimeSlicingConfigMap:  "ts-cm",
			MaxTimeSlices:         4,
			MaxGPUsPerRequest:     6,
		},
	}

	sc := stagecommon.StageContext{
		ModelRequest:   mr,
		PlatformConfig: cfg,
		Stage:          modelopsv1alpha1.ProfileStageSpec{Name: "capacity", Kind: "CapacityPlan"},
	}

	spec, err := Handler{}.BuildSpec(sc)
	require.NoError(t, err)
	require.Equal(t, "capacity", spec.Name)
	require.Empty(t, spec.Params, "CapacityPlan uses NativeSpec, not the string param bag")

	native, ok := spec.NativeSpec.(*modelopsv1alpha1.CapacityPlanSpec)
	require.True(t, ok, "NativeSpec must be a *CapacityPlanSpec")
	require.Equal(t, 8192, native.ContextLength)
	require.Equal(t, 4, native.Concurrency)
	require.True(t, native.AllowMIG)
	require.True(t, native.AllowTimeSlicing, "defaults to true when unset")
	require.Equal(t, "shared", native.IsolationPolicy)
	require.Equal(t, "http://advisor.example.com", native.AdvisorEndpoint)
	require.Equal(t, "advisor-secret", native.AdvisorSecretName)
	require.Equal(t, 120, native.AdvisorTimeoutSeconds)
	require.Equal(t, "gpu-ns", native.GPUOperatorNamespace)
	require.Equal(t, "cluster-policy", native.ClusterPolicyName)
	require.Equal(t, "ts-cm", native.TimeSlicingConfigMap)
	require.Equal(t, 4, native.MaxTimeSlices)
	require.Equal(t, 6, native.MaxGPUsPerRequest)
}

func TestHandler_BuildSpec_NilRequirements_UsesAllDefaults(t *testing.T) {
	mr := &modelopsv1alpha1.ModelRequest{}
	cfg := &modelopsv1alpha1.PlatformConfig{}

	sc := stagecommon.StageContext{
		ModelRequest:   mr,
		PlatformConfig: cfg,
		Stage:          modelopsv1alpha1.ProfileStageSpec{Name: "capacity", Kind: "CapacityPlan"},
	}

	spec, err := Handler{}.BuildSpec(sc)
	require.NoError(t, err)
	native := spec.NativeSpec.(*modelopsv1alpha1.CapacityPlanSpec)
	require.Equal(t, 32768, native.ContextLength)
	require.Equal(t, 4, native.Concurrency)
	require.True(t, native.AllowTimeSlicing)
	require.False(t, native.AllowMIG)
	require.Equal(t, "dedicated", native.IsolationPolicy)
	require.Equal(t, 8, native.MaxTimeSlices)
	require.Equal(t, 0, native.MaxGPUsPerRequest, "unset MaxGPUsPerRequest must not gain a default -- 0 means no configured ceiling")
}

func TestHandler_BuildSpec_SetsModelRefFromModelRequestName(t *testing.T) {
	mr := &modelopsv1alpha1.ModelRequest{}
	mr.Name = "mr-1"
	cfg := &modelopsv1alpha1.PlatformConfig{}

	sc := stagecommon.StageContext{
		ModelRequest:   mr,
		PlatformConfig: cfg,
		Stage:          modelopsv1alpha1.ProfileStageSpec{Name: "capacity", Kind: "CapacityPlan"},
	}

	spec, err := Handler{}.BuildSpec(sc)
	require.NoError(t, err)
	native := spec.NativeSpec.(*modelopsv1alpha1.CapacityPlanSpec)
	require.Equal(t, "mr-1", native.ModelRef.ModelRequestName)
}
