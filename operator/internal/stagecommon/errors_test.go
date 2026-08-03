package stagecommon

// Phase 7 of REFACTOR_PLAN.md: ProviderConfigError is the shared
// sentinel type that lets ModelRequestReconciler recognize a
// stagecommon.StageSpec.ProviderConfigRef resolution failure (raised by
// internal/stages/tekton, a sibling stage package internal/controller
// never imports directly) via errors.As, without internal/controller
// needing a new direct import of internal/stages/tekton. Written first,
// against a not-yet-existing ProviderConfigError (TDD).

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProviderConfigError_ErrorReturnsUnderlyingMessage(t *testing.T) {
	underlying := errors.New("provider config kind unsupported")
	pcErr := &ProviderConfigError{Err: underlying}

	require.Equal(t, "provider config kind unsupported", pcErr.Error())
}

func TestProviderConfigError_UnwrapsToUnderlyingError(t *testing.T) {
	underlying := errors.New("intakeproviderconfig not found")
	pcErr := &ProviderConfigError{Err: underlying}

	require.ErrorIs(t, pcErr, underlying)
}

func TestProviderConfigError_SurvivesFmtErrorfWrapping_StillMatchedByErrorsAs(t *testing.T) {
	// Mirrors exactly how internal/stagewalk.Walk wraps a StageRunner's
	// EnsureRun error (fmt.Errorf("stage %q: %w", ...)) before
	// returning it to Reconcile -- proving errors.As still finds the
	// ProviderConfigError through that extra layer, the same guarantee
	// namespaceSetupError/secretLookupError already rely on in
	// internal/controller.
	pcErr := &ProviderConfigError{Err: errors.New("unsupported provider config kind")}
	wrapped := fmt.Errorf("stage %q: %w", "sandbox", pcErr)

	var got *ProviderConfigError
	require.True(t, errors.As(wrapped, &got))
	require.Same(t, pcErr, got)
}
