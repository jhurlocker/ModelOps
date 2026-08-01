package stagecommon

import (
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/stretchr/testify/require"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

func boolPtr(b bool) *bool { return &b }

// paramsToMap converts a tektonv1.Params into a map[string]string,
// failing outright if any param name appears more than once.
func paramsToMap(t *testing.T, params tektonv1.Params) map[string]string {
	t.Helper()
	out := make(map[string]string, len(params))
	for _, p := range params {
		if _, exists := out[p.Name]; exists {
			t.Fatalf("duplicate param %q in output", p.Name)
		}
		out[p.Name] = p.Value.StringVal
	}
	return out
}

func TestBuildCommonModelParams_FullFixture_ProducesExpectedSharedParams(t *testing.T) {
	spec := modelopsv1alpha1.ModelRequestSpec{
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
	}

	reqs := &modelopsv1alpha1.ModelRequirements{
		GPUConfig: modelopsv1alpha1.GPUConfig{
			GPUIsolationPolicy: "shared",
			AllowTimeSlicing:   boolPtr(false),
			AllowMIG:           boolPtr(true),
		},
		BenchmarkTargets: modelopsv1alpha1.BenchmarkTargets{
			ContextLength:       8192,
			ExpectedConcurrency: 16,
			RequestRate:         "5.0",
			TargetTTFT:          "250ms",
			TargetThroughput:    "200",
		},
		DeploymentConfig: modelopsv1alpha1.DeploymentConfig{
			ValuesContent:          "replicaCount: 3",
			OpenShiftConsoleDomain: "apps.example.com",
		},
		AdvisorEndpoint: "http://advisor.example.com",
	}

	cfg := &modelopsv1alpha1.PlatformConfig{
		Spec: modelopsv1alpha1.PlatformConfigSpec{
			RegistryServer:           "http://registry.example.com",
			RegistryPort:             "9090",
			RegistryAuthor:           "Team Author",
			ModelCarRepo:             "custom/modelcar-catalog",
			GPUOperatorNamespace:     "custom-gpu-ns",
			ClusterPolicyName:        "custom-cluster-policy",
			TimeSlicingConfigMap:     "custom-ts-cm",
			MaxTimeSlices:            16,
			AdvisorSecretName:        "custom-advisor-secret",
			AdvisorTimeoutSeconds:    600,
			ChartURL:                 "https://charts.example.com/",
			ChartVersion:             "1.2.3",
			HardwareProfileName:      "custom-hw-profile",
			HardwareProfileNamespace: "custom-hw-ns",
			EvalHubURL:               "http://evalhub.example.com",
		},
	}

	secrets := Secrets{
		EvalHubToken:      "evalhub-tok",
		HuggingFaceToken:  "hf-tok",
		ResultS3Endpoint:  "http://result-s3:9000",
		ResultS3AccessKey: "result-access",
		ResultS3SecretKey: "result-secret",
	}

	params := BuildCommonModelParams(spec, reqs, cfg, secrets)
	got := paramsToMap(t, params)

	want := map[string]string{
		"model-id":                   "quay.io/models/foo:v1",
		"model-name":                 "foo-model",
		"model-version":              "v2",
		"model-tokenizer":            "foo-tokenizer",
		"model-source-type":          "oci",
		"display-name":               "Foo Model",
		"business-justification":     "Because reasons",
		"requested-by":               "jane@example.com",
		"modelcar-repo":              "custom/modelcar-catalog",
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
		"evalhub-url":                "http://evalhub.example.com",
		"evalhub-token":              "evalhub-tok",
		"openshift-console-domain":   "apps.example.com",
		"huggingface-token":          "hf-tok",
		"s3-api-endpoint":            "http://result-s3:9000",
		"s3-access-key-id":           "result-access",
		"s3-secret-access-key":       "result-secret",
		"mr-server":                  "http://registry.example.com",
		"mr-port":                    "9090",
		"model-reg-author":           "Team Author",
		// "modelcar-image" is always hardcoded "" -- addParam's
		// empty-value guard means it must never appear.
	}

	require.Equal(t, want, got)
	require.NotContains(t, got, "gpu-count-override",
		"gpu-count-override is deliberately NOT part of the shared helper -- each stage owns its own logic for this param")
}

func TestBuildCommonModelParams_DefaultsAppliedWhenFieldsEmpty(t *testing.T) {
	spec := modelopsv1alpha1.ModelRequestSpec{
		Model: modelopsv1alpha1.ModelIdentity{URI: "some/model"},
	}
	reqs := &modelopsv1alpha1.ModelRequirements{}
	cfg := &modelopsv1alpha1.PlatformConfig{}
	secrets := Secrets{}

	params := BuildCommonModelParams(spec, reqs, cfg, secrets)

	got := map[string]string{}
	for _, p := range params {
		got[p.Name] = p.Value.StringVal
	}

	require.Equal(t, "unknown", got["model-name"], "defaults to \"unknown\" when Model.Name is empty")
	require.Equal(t, "v1", got["model-version"], "defaults to \"v1\" when Model.Version is empty")
	require.Equal(t, "redhat-ai-services/modelcar-catalog", got["modelcar-repo"])
	require.Equal(t, "32768", got["context-length"])
	require.Equal(t, "4", got["concurrency"])
	require.Equal(t, "true", got["allow-time-slicing"])
	require.Equal(t, "false", got["allow-mig"])
	require.Equal(t, "dedicated", got["gpu-isolation-policy"])
	require.Equal(t, "nvidia-gpu-operator", got["gpu-operator-namespace"])
	require.Equal(t, "gpu-cluster-policy", got["clusterpolicy-name"])
	require.Equal(t, "modelops-time-slicing", got["time-slicing-configmap"])
	require.Equal(t, "8", got["max-time-slices"])
	require.Equal(t, "gpu-advisor-credentials", got["advisor-secret-name"])
	require.Equal(t, "300", got["advisor-timeout-seconds"])
	require.Equal(t, "https://redhat-ai-services.github.io/helm-charts/", got["chart-url"])
	require.Equal(t, "0.7.1", got["chart-version"])
	require.Equal(t, "gpu-profile", got["hardware-profile-name"])
	require.Equal(t, "redhat-ods-applications", got["hardware-profile-namespace"])
	require.Equal(t, "http://modelops-registry.rhoai-model-registries.svc.cluster.local", got["mr-server"])
	require.Equal(t, "8080", got["mr-port"])
	require.Equal(t, "ModelOps Platform Team", got["model-reg-author"])

	// Fields with no default that are simply omitted when empty.
	_, hasTokenizer := got["model-tokenizer"]
	require.False(t, hasTokenizer)
	_, hasAdvisorEndpoint := got["advisor-endpoint"]
	require.False(t, hasAdvisorEndpoint)
	_, hasEvalHubURL := got["evalhub-url"]
	require.False(t, hasEvalHubURL)
}
