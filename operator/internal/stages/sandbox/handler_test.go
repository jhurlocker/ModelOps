package sandbox

// Characterization tests for Handler.BuildSpec, relocated (Phase 6) from
// internal/controller's pre-Phase-6 buildSandboxPipelineParams/
// sandboxPipelineNameOrDefault -- same params/precedence, same
// assertions, new package. See docs/PHASE_LOG.md Phase 3/4 entries for
// why this golden-value fixture exists (Phase 1's gpu-count-override
// duplicate-param bug regression net).

import (
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func boolPtr(b bool) *bool { return &b }

func TestBuildSpec_ExplicitOverride_TakesPrecedenceAndAppearsExactlyOnce(t *testing.T) {
	// Phase 1 fix: when both the CapacityPlan's derived GPU count and an
	// explicit reqs.GPUCountOverride are set, exactly ONE
	// "gpu-count-override" param must be added, using the override.
	sc := stagecommon.StageContext{
		ModelRequest: &modelopsv1alpha1.ModelRequest{
			Spec: modelopsv1alpha1.ModelRequestSpec{
				Model: modelopsv1alpha1.ModelIdentity{URI: "some/model"},
				Requirements: &modelopsv1alpha1.ModelRequirements{
					GPUConfig: modelopsv1alpha1.GPUConfig{GPUCountOverride: "7"},
				},
			},
		},
		PlatformConfig: &modelopsv1alpha1.PlatformConfig{},
		CapacityPlan:   &modelopsv1alpha1.CapacityPlan{Status: modelopsv1alpha1.CapacityPlanStatus{GPUsNeeded: 4}},
		Stage:          modelopsv1alpha1.ProfileStageSpec{Name: "sandbox"},
	}

	spec, err := Handler{}.BuildSpec(sc)
	require.NoError(t, err)
	require.Equal(t, "7", spec.Params["gpu-count-override"], "explicit override must win over the plan-derived value")
}

func TestBuildSpec_NoOverride_FallsBackToPlanDerivedGPUCount(t *testing.T) {
	sc := stagecommon.StageContext{
		ModelRequest: &modelopsv1alpha1.ModelRequest{
			Spec: modelopsv1alpha1.ModelRequestSpec{Model: modelopsv1alpha1.ModelIdentity{URI: "some/model"}},
		},
		PlatformConfig: &modelopsv1alpha1.PlatformConfig{},
		CapacityPlan:   &modelopsv1alpha1.CapacityPlan{Status: modelopsv1alpha1.CapacityPlanStatus{GPUsNeeded: 4}},
		Stage:          modelopsv1alpha1.ProfileStageSpec{Name: "sandbox"},
	}

	spec, err := Handler{}.BuildSpec(sc)
	require.NoError(t, err)
	require.Equal(t, "4", spec.Params["gpu-count-override"])
}

func TestBuildSpec_NoOverrideAndNoPlan_OmitsParam(t *testing.T) {
	sc := stagecommon.StageContext{
		ModelRequest: &modelopsv1alpha1.ModelRequest{
			Spec: modelopsv1alpha1.ModelRequestSpec{Model: modelopsv1alpha1.ModelIdentity{URI: "some/model"}},
		},
		PlatformConfig: &modelopsv1alpha1.PlatformConfig{},
		Stage:          modelopsv1alpha1.ProfileStageSpec{Name: "sandbox"},
	}

	spec, err := Handler{}.BuildSpec(sc)
	require.NoError(t, err)
	_, ok := spec.Params["gpu-count-override"]
	require.False(t, ok)
}

// fullCharacterizationFixture mirrors internal/controller's pre-Phase-6
// fixture of the same name (every field buildSandboxPipelineParams
// read from populated with a distinct, non-empty/non-zero value).
func fullCharacterizationFixture() stagecommon.StageContext {
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

	secrets := stagecommon.Secrets{
		EvalHubSecretName:     "evalhub-creds-secret",
		HuggingFaceSecretName: "hf-creds-secret",
		ScanS3Endpoint:        "http://scan-s3:9000",
		ScanS3SecretName:      "scan-s3-creds-secret",
		ResultS3Endpoint:      "http://result-s3:9000",
		ResultS3SecretName:    "result-s3-creds-secret",
	}

	return stagecommon.StageContext{
		ModelRequest:   mr,
		PlatformConfig: cfg,
		CapacityPlan:   plan,
		Secrets:        secrets,
		Stage:          modelopsv1alpha1.ProfileStageSpec{Name: "sandbox"},
	}
}

func TestBuildSpec_FullFixture_CharacterizesCurrentOutput(t *testing.T) {
	sc := fullCharacterizationFixture()

	spec, err := Handler{}.BuildSpec(sc)
	require.NoError(t, err)

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
		"evalhub-secret-name":        "evalhub-creds-secret",
		"tenant-ns":                  "my-sandbox",
		"openshift-console-domain":   "apps.example.com",
		"huggingface-secret-name":    "hf-creds-secret",
		"scan-s3-endpoint":           "http://scan-s3:9000",
		"scan-s3-secret-name":        "scan-s3-creds-secret",
		"compliance-s3-bucket":       "custom-result-bucket", // spec.ResultS3Bucket wins over cfg.Spec.ComplianceS3Bucket
		"security-s3-bucket":         "custom-result-bucket", // spec.ResultS3Bucket wins over cfg.Spec.SecurityS3Bucket
		"s3-api-endpoint":            "http://result-s3:9000",
		"result-s3-secret-name":      "result-s3-creds-secret",
		"mr-server":                  "http://registry.example.com",
		"mr-port":                    "9090",
		"model-reg-author":           "Team Author",
		// "modelcar-image" and "s3-ui-route" are always hardcoded "" in
		// this function, so AddParam's empty-value guard always omits
		// them -- they must NOT appear in the output.
	}

	require.Equal(t, want, spec.Params)
	require.Equal(t, "sandbox", spec.Name)
	require.Equal(t, stagecommon.StageKindSandbox, spec.StageKind)

	// Phase 8 (docs/PHASE_LOG.md): scan-s3-access-key-id/
	// scan-s3-secret-access-key must never be produced by this
	// Handler -- credentials flow by Secret name only
	// (scan-s3-secret-name).
	for _, leaked := range []string{"scan-s3-access-key-id", "scan-s3-secret-access-key"} {
		require.NotContains(t, spec.Params, leaked)
	}
}

func TestSandboxPipelineNameOrDefault_PrecedenceOrder(t *testing.T) {
	mr := &modelopsv1alpha1.ModelRequest{Spec: modelopsv1alpha1.ModelRequestSpec{PipelineRef: "request-level-pipeline"}}
	profile := &modelopsv1alpha1.ModelLifecycleProfile{
		Spec: modelopsv1alpha1.ModelLifecycleProfileSpec{
			Workflow: modelopsv1alpha1.WorkflowRef{PipelineRef: "profile-sandbox-pipeline"},
		},
	}

	require.Equal(t, "request-level-pipeline", PipelineNameOrDefault(profile, mr), "mr.Spec.PipelineRef wins over the profile's")
}

func TestSandboxPipelineNameOrDefault_FallsBackToProfile(t *testing.T) {
	mr := &modelopsv1alpha1.ModelRequest{}
	profile := &modelopsv1alpha1.ModelLifecycleProfile{
		Spec: modelopsv1alpha1.ModelLifecycleProfileSpec{
			Workflow: modelopsv1alpha1.WorkflowRef{PipelineRef: "profile-sandbox-pipeline"},
		},
	}

	require.Equal(t, "profile-sandbox-pipeline", PipelineNameOrDefault(profile, mr))
}

func TestSandboxPipelineNameOrDefault_NilProfileAndUnset_Defaults(t *testing.T) {
	require.Equal(t, "model-intake-sandbox", PipelineNameOrDefault(nil, &modelopsv1alpha1.ModelRequest{}))
}

func TestBuildSpec_RunNameIsModelRequestNamePlusSandbox(t *testing.T) {
	sc := stagecommon.StageContext{
		ModelRequest:   &modelopsv1alpha1.ModelRequest{ObjectMeta: metav1.ObjectMeta{Name: "mr-1"}},
		PlatformConfig: &modelopsv1alpha1.PlatformConfig{},
		Stage:          modelopsv1alpha1.ProfileStageSpec{Name: "sandbox"},
	}
	spec, err := Handler{}.BuildSpec(sc)
	require.NoError(t, err)
	require.Equal(t, "mr-1-sandbox", spec.RunName)
}
