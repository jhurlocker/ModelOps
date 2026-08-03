package stagecommon

// ProviderConfigError marks a stagecommon.StageSpec.ProviderConfigRef
// resolution failure (missing/malformed reference, unsupported Kind,
// unsupported providerType -- see internal/stages/tekton/providerconfig.go's
// resolveProviderDetails) returned from a StageRunner.EnsureRun
// implementation.
//
// This is the shared seam that lets ModelRequestReconciler recognize
// this specific failure class via errors.As and surface a dedicated
// "ProviderConfigLookupFailed" status reason, instead of falling into
// the generic silent-retry error path every other EnsureRun error hits.
// It lives in stagecommon (not internal/stages/tekton) because the
// error must be recognizable from internal/controller, which never
// imports internal/stages/tekton directly (see main.go's StageRunners
// registry) -- stagecommon is the shared package both sides already
// depend on. See docs/REFACTOR_PLAN.md Phase 7.
type ProviderConfigError struct {
	Err error
}

func (e *ProviderConfigError) Error() string { return e.Err.Error() }
func (e *ProviderConfigError) Unwrap() error { return e.Err }
