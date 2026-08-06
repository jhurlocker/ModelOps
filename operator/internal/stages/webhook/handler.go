package webhook

import (
	"fmt"

	"github.com/jhurlocker/modelops-operator/internal/stagecommon"
)

// Handler builds the StageSpec for a webhook stage. The StageSpec is
// deliberately thin -- everything the StageRunner needs is in the
// WebhookProviderConfig CR (resolved via ProviderConfigRef at execution
// time), so the Handler only needs to set the deterministic RunName and
// pass the config reference through.
type Handler struct{}

var _ stagecommon.StageHandler = Handler{}

func (Handler) BuildSpec(sc stagecommon.StageContext) (stagecommon.StageSpec, error) {
	if sc.Stage.ProviderConfigRef == nil {
		return stagecommon.StageSpec{}, fmt.Errorf(
			"stage %q: webhook stages require a ProviderConfigRef", sc.Stage.Name)
	}
	return stagecommon.StageSpec{
		Name:              sc.Stage.Name,
		RunName:           fmt.Sprintf("%s-%s", sc.ModelRequest.Name, sc.Stage.Name),
		ProviderConfigRef: sc.Stage.ProviderConfigRef,
	}, nil
}
