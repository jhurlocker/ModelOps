package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStagePhaseConstants_KnownLiteralValues(t *testing.T) {
	assert.Equal(t, "Running", string(WebhookPhaseRunning))
	assert.Equal(t, "Succeeded", string(WebhookPhaseSucceeded))
	assert.Equal(t, "Failed", string(WebhookPhaseFailed))
}
