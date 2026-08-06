package webhook

import (
	"context"
	"fmt"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	"k8s.io/apimachinery/pkg/types"
)

// resolveConfig resolves the WebhookProviderConfig CR referenced by
// stage.ProviderConfigRef. Follows the same pattern as
// internal/stages/tekton/providerconfig.go's resolveProviderDetails.
func (r *StageRunner) resolveConfig(
	ctx context.Context,
	namespace string,
	stage stagecommon.StageSpec,
) (*modelopsv1alpha1.WebhookProviderConfigSpec, error) {
	if stage.ProviderConfigRef == nil {
		return nil, fmt.Errorf("stage %q: webhook StageRunner requires a ProviderConfigRef; none was set", stage.Name)
	}

	kind := stage.ProviderConfigRef.Kind
	if kind == "" {
		kind = defaultProviderConfigKind
	}
	if kind != defaultProviderConfigKind {
		return nil, fmt.Errorf(
			"stage %q: unsupported provider config kind %q (only %q is supported)",
			stage.Name, kind, defaultProviderConfigKind)
	}

	var cfg modelopsv1alpha1.WebhookProviderConfig
	key := types.NamespacedName{Name: stage.ProviderConfigRef.Name, Namespace: namespace}
	if err := r.Client.Get(ctx, key, &cfg); err != nil {
		return nil, fmt.Errorf("stage %q: resolving WebhookProviderConfig %q: %w", stage.Name, stage.ProviderConfigRef.Name, err)
	}

	if cfg.Spec.ProviderType != "webhook" {
		return nil, fmt.Errorf(
			"stage %q: WebhookProviderConfig %q has providerType %q, the webhook StageRunner only supports %q",
			stage.Name, cfg.Name, cfg.Spec.ProviderType, "webhook")
	}

	return &cfg.Spec, nil
}
