package webhook

import (
	"context"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestResolveConfig_ValidConfig_ReturnsSpec(t *testing.T) {
	cfg := newWebhookProviderConfig("wc-1", "ns-1", modelopsv1alpha1.WebhookProviderConfigSpec{
		ProviderType:       "webhook",
		SubmitEndpoint:     "https://example.com/api/jobs",
		SubmitJobIDJsonPath: "{$.id}",
		StatusEndpoint:      "https://example.com/api/jobs/{{.JobID}}",
		StatusMapping: modelopsv1alpha1.WebhookStatusMapping{
			PhaseJsonPath: "{$.status}",
			PhaseValueMap: map[string]modelopsv1alpha1.StagePhase{
				"OK": modelopsv1alpha1.WebhookPhaseSucceeded,
			},
		},
	})
	k8sClient := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(cfg).Build()
	r := &StageRunner{Client: k8sClient}

	spec, err := r.resolveConfig(context.Background(), "ns-1", stagecommon.StageSpec{
		Name: "webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1", Kind: "WebhookProviderConfig",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "https://example.com/api/jobs", spec.SubmitEndpoint)
}

func TestResolveConfig_NilProviderConfigRef_ReturnsError(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	r := &StageRunner{Client: k8sClient}

	_, err := r.resolveConfig(context.Background(), "ns-1", stagecommon.StageSpec{
		Name: "webhook-check",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires a ProviderConfigRef")
}

func TestResolveConfig_MissingObject_ReturnsError(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	r := &StageRunner{Client: k8sClient}

	_, err := r.resolveConfig(context.Background(), "ns-1", stagecommon.StageSpec{
		Name: "webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-missing", Kind: "WebhookProviderConfig",
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "wc-missing")
}

func TestResolveConfig_WrongProviderType_ReturnsError(t *testing.T) {
	cfg := newWebhookProviderConfig("wc-1", "ns-1", modelopsv1alpha1.WebhookProviderConfigSpec{
		ProviderType:       "tekton",
		SubmitEndpoint:     "https://example.com/api/jobs",
		SubmitJobIDJsonPath: "{$.id}",
		StatusEndpoint:      "https://example.com/api/jobs/{{.JobID}}",
		StatusMapping: modelopsv1alpha1.WebhookStatusMapping{
			PhaseJsonPath: "{$.status}",
			PhaseValueMap: map[string]modelopsv1alpha1.StagePhase{
				"OK": modelopsv1alpha1.WebhookPhaseSucceeded,
			},
		},
	})
	k8sClient := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(cfg).Build()
	r := &StageRunner{Client: k8sClient}

	_, err := r.resolveConfig(context.Background(), "ns-1", stagecommon.StageSpec{
		Name: "webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1", Kind: "WebhookProviderConfig",
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "only supports")
}

func TestResolveConfig_UnsupportedKind_ReturnsError(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	r := &StageRunner{Client: c}

	_, err := r.resolveConfig(context.Background(), "ns-1", stagecommon.StageSpec{
		Name: "webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1", Kind: "IntakeProviderConfig",
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported provider config kind")
}

func TestResolveConfig_EmptyKind_DefaultsToWebhookProviderConfig(t *testing.T) {
	cfg := newWebhookProviderConfig("wc-1", "ns-1", modelopsv1alpha1.WebhookProviderConfigSpec{
		ProviderType:       "webhook",
		SubmitEndpoint:     "https://example.com/api/jobs",
		SubmitJobIDJsonPath: "{$.id}",
		StatusEndpoint:      "https://example.com/api/jobs/{{.JobID}}",
		StatusMapping: modelopsv1alpha1.WebhookStatusMapping{
			PhaseJsonPath: "{$.status}",
			PhaseValueMap: map[string]modelopsv1alpha1.StagePhase{
				"OK": modelopsv1alpha1.WebhookPhaseSucceeded,
			},
		},
	})
	k8sClient := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(cfg).Build()
	r := &StageRunner{Client: k8sClient}

	spec, err := r.resolveConfig(context.Background(), "ns-1", stagecommon.StageSpec{
		Name: "webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "https://example.com/api/jobs", spec.SubmitEndpoint)
}
