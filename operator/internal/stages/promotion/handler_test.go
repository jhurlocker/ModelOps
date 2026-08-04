package promotion

// Characterization tests for Handler.BuildSpec, relocated (Phase 6) from
// internal/controller's pre-Phase-6 buildPromotionPipelineParams/
// promotionPipelineNameOrDefault/getPromotionNamespaces -- same
// params/precedence, same assertions, new package.

import (
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func boolPtr(b bool) *bool { return &b }

// fullCharacterizationFixture mirrors internal/controller's pre-Phase-6
// fixture of the same name.
func fullCharacterizationFixture() (*modelopsv1alpha1.ModelRequest, *modelopsv1alpha1.PlatformConfig, *modelopsv1alpha1.CapacityPlan, stagecommon.Secrets) {
	mr := &modelopsv1alpha1.ModelRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-1"},
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
		ResultS3Endpoint:      "http://result-s3:9000",
		ResultS3SecretName:    "result-s3-creds-secret",
	}

	return mr, cfg, plan, secrets
}

func TestBuildSpec_FirstAndLastNamespace_FullFixture_CharacterizesCurrentOutput(t *testing.T) {
	mr, cfg, plan, secrets := fullCharacterizationFixture()

	sc := stagecommon.StageContext{
		ModelRequest:   mr,
		PlatformConfig: cfg,
		CapacityPlan:   plan,
		Secrets:        secrets,
		Stage:          modelopsv1alpha1.ProfileStageSpec{Name: "promotion", PerNamespace: true},
		Namespace:      "prod-ns",
		NamespaceIndex: 0,
		NamespaceCount: 1,
	}

	spec, err := Handler{}.BuildSpec(sc)
	require.NoError(t, err)

	want := map[string]string{
		"model-id":               "quay.io/models/foo:v1",
		"model-name":             "foo-model",
		"model-version":          "v2",
		"model-tokenizer":        "foo-tokenizer",
		"model-source-type":      "oci",
		"display-name":           "Foo Model",
		"business-justification": "Because reasons",
		"requested-by":           "jane@example.com",
		"target-namespace":       "prod-ns",
		"plan-id":                "mr-1-promotion",
		"modelcar-repo":          "custom/modelcar-catalog",
		// KNOWN BEHAVIOR (unchanged): buildPromotionPipelineParams does
		// NOT check reqs.GPUConfig.GPUCountOverride at all -- it always
		// uses the CapacityPlan-derived value.
		"gpu-count-override":             "4",
		"context-length":                 "8192",
		"concurrency":                    "16",
		"allow-time-slicing":             "false",
		"allow-mig":                      "true",
		"gpu-isolation-policy":           "shared",
		"request-rate":                   "5.0",
		"target-ttft":                    "250ms",
		"target-throughput":              "200",
		"gpu-operator-namespace":         "custom-gpu-ns",
		"clusterpolicy-name":             "custom-cluster-policy",
		"time-slicing-configmap":         "custom-ts-cm",
		"max-time-slices":                "16",
		"advisor-endpoint":               "http://advisor.example.com",
		"advisor-secret-name":            "custom-advisor-secret",
		"advisor-timeout-seconds":        "600",
		"release-name":                   "foo-model",
		"chart-url":                      "https://charts.example.com/",
		"chart-version":                  "1.2.3",
		"values-content":                 "replicaCount: 3",
		"hardware-profile-name":          "custom-hw-profile",
		"hardware-profile-namespace":     "custom-hw-ns",
		"approval-api-url":               "http://approval.example.com",
		"approval-poll-interval-seconds": "30",
		"approval-timeout-seconds":       "7200",
		"evalhub-url":                    "http://evalhub.example.com",
		"evalhub-secret-name":            "evalhub-creds-secret",
		"openshift-console-domain":       "apps.example.com",
		"guidellm-profile":               "sweep",
		"guidellm-rate":                  "8.5",
		"guidellm-max-seconds":           "60",
		"guidellm-max-requests":          "10",
		"benchmark-target-url":           "http://custom-benchmark-target/v1",
		"custom-data":                    "true",
		"custom-filename":                "custom.json",
		"huggingface-secret-name":        "hf-creds-secret",
		"s3-api-endpoint":                "http://result-s3:9000",
		"result-s3-secret-name":          "result-s3-creds-secret",
		"mr-server":                      "http://registry.example.com",
		"mr-port":                        "9090",
		"model-reg-author":               "Team Author",
		"authorized-viewers":             "team-a,team-b",
		"access-role":                    "admin",
		"deploy-maas":                    "true",
		"maas-serving-ns":                "custom-maas-serving",
		"maas-policy-ns":                 "custom-maas-policy",
		"maas-gpu-count":                 "3", // spec.MaaS.GPUCount wins over cfg.Spec.MaaSGPUCount
		"maas-runtime-image":             "default-runtime:latest",
		"maas-authorized-group":          "default-group",
		"run-register":                   "true", // isLast=true (index 0 of count 1)
	}

	require.Equal(t, want, spec.Params)
	require.Equal(t, stagecommon.StageKindPromotion, spec.StageKind)

	// Phase 8 (docs/PHASE_LOG.md): credential values must never appear
	// here -- promotion.Handler inherits this property entirely from
	// BuildCommonModelParams (it never touches sc.Secrets directly).
	for _, leaked := range []string{"evalhub-token", "huggingface-token", "s3-access-key-id", "s3-secret-access-key"} {
		require.NotContains(t, spec.Params, leaked)
	}
}

func TestBuildSpec_MiddleNamespace_OmitsApprovalURL_AndRunRegisterFalse(t *testing.T) {
	// index 1 of 3: neither first nor last.
	mr, cfg, plan, secrets := fullCharacterizationFixture()

	sc := stagecommon.StageContext{
		ModelRequest:   mr,
		PlatformConfig: cfg,
		CapacityPlan:   plan,
		Secrets:        secrets,
		Stage:          modelopsv1alpha1.ProfileStageSpec{Name: "promotion", PerNamespace: true},
		Namespace:      "staging-ns",
		NamespaceIndex: 1,
		NamespaceCount: 3,
	}

	spec, err := Handler{}.BuildSpec(sc)
	require.NoError(t, err)

	_, hasApprovalURL := spec.Params["approval-api-url"]
	require.False(t, hasApprovalURL, "approval-api-url must be omitted when this isn't the first namespace")
	require.Equal(t, "false", spec.Params["run-register"], "run-register must be false when this isn't the last namespace")
	require.Equal(t, "staging-ns", spec.Params["target-namespace"])
	require.Equal(t, "30", spec.Params["approval-poll-interval-seconds"])
	require.Equal(t, "7200", spec.Params["approval-timeout-seconds"])
}

func TestPromotionPipelineNameOrDefault_UsesProfileOverrideWhenSet(t *testing.T) {
	profile := &modelopsv1alpha1.ModelLifecycleProfile{
		Spec: modelopsv1alpha1.ModelLifecycleProfileSpec{
			Workflow: modelopsv1alpha1.WorkflowRef{
				PipelineRef:          "some-sandbox-pipeline",
				PromotionPipelineRef: "a-totally-different-promotion-pipeline",
			},
		},
	}

	require.Equal(t, "a-totally-different-promotion-pipeline", PipelineNameOrDefault(profile))
}

func TestPromotionPipelineNameOrDefault_DefaultsWhenProfileHasNoOverride(t *testing.T) {
	profile := &modelopsv1alpha1.ModelLifecycleProfile{
		Spec: modelopsv1alpha1.ModelLifecycleProfileSpec{Workflow: modelopsv1alpha1.WorkflowRef{PipelineRef: "some-sandbox-pipeline"}},
	}
	require.Equal(t, "model-intake-promotion", PipelineNameOrDefault(profile))
}

func TestPromotionPipelineNameOrDefault_NilProfile_Defaults(t *testing.T) {
	require.Equal(t, "model-intake-promotion", PipelineNameOrDefault(nil))
}

func TestGetNamespaces_NilRequirements_DefaultsToStaging(t *testing.T) {
	require.Equal(t, []string{"staging"}, GetNamespaces(&modelopsv1alpha1.ModelRequest{}))
}

func TestGetNamespaces_PromotionNamespacesSet_UsesThem(t *testing.T) {
	mr := &modelopsv1alpha1.ModelRequest{Spec: modelopsv1alpha1.ModelRequestSpec{
		Requirements: &modelopsv1alpha1.ModelRequirements{PromotionNamespaces: []string{"a", "b"}},
	}}
	require.Equal(t, []string{"a", "b"}, GetNamespaces(mr))
}

func TestGetNamespaces_StagingNamespaceFallback(t *testing.T) {
	mr := &modelopsv1alpha1.ModelRequest{Spec: modelopsv1alpha1.ModelRequestSpec{
		Requirements: &modelopsv1alpha1.ModelRequirements{StagingNamespace: "my-staging"},
	}}
	require.Equal(t, []string{"my-staging"}, GetNamespaces(mr))
}

func TestBuildSpec_RunNameAndSpecNameIncludeNamespace(t *testing.T) {
	sc := stagecommon.StageContext{
		ModelRequest:   &modelopsv1alpha1.ModelRequest{ObjectMeta: metav1.ObjectMeta{Name: "mr-1"}},
		PlatformConfig: &modelopsv1alpha1.PlatformConfig{},
		Stage:          modelopsv1alpha1.ProfileStageSpec{Name: "promotion", PerNamespace: true},
		Namespace:      "staging",
		NamespaceIndex: 0,
		NamespaceCount: 1,
	}
	spec, err := Handler{}.BuildSpec(sc)
	require.NoError(t, err)
	require.Equal(t, "mr-1-promotion-staging", spec.RunName)
	require.Equal(t, "promotion-staging", spec.Name)
}
