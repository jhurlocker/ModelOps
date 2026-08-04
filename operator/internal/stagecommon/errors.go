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

// NamespaceApprovalError marks a StageNamespaceSetup.AllowedNamespaceSelector
// check failure: the candidate promotion namespace either doesn't exist
// or its labels don't match the selector. Both variants produce the same
// "NamespaceNotApproved" ModelRequest status reason (see
// internal/controller/modelrequest_controller.go's failRequest call
// site) -- distinct messages distinguish NotFound ("wait for GitOps") vs.
// exists-but-mismatch ("relabel the namespace or adjust the selector").
//
// Unlike ProviderConfigError, this uses a no-requeue failure path
// (failRequest, not failRequestWithRequeue). The two error classes are
// qualitatively different:
//
//   - ProviderConfigLookupFailed means a modelops-owned CRD (created and
//     synced by the same GitOps process as the profile) is missing -- a
//     real race window of tens of seconds, where a bounded requeue
//     reliably self-heals without human intervention. Phase 7's 30s
//     requeue was designed for exactly that shape.
//
//   - NamespaceNotApproved means either the target namespace doesn't
//     exist at all, or it exists but wasn't labeled as a promotion
//     target. Namespaces are long-lived cluster infrastructure, not a
//     dynamically-provisioned CRD, and the operator has no reason to
//     believe the namespace will spontaneously acquire the right labels
//     (or come into existence) on its own. Even the NotFound sub-case --
//     the closest this gets to a GitOps race -- needs not just the
//     namespace to be created, but also the correct labels to be applied
//     to it. A bounded requeue just re-checks a state that won't change
//     without human action.
//
// This lives in stagecommon (not internal/controller) because the check
// itself may need to be invoked from any namespace-setup consumer in the
// future, and the error must be recognizable via errors.As without
// importing the controller package backward.
type NamespaceApprovalError struct {
	Err error
}

func (e *NamespaceApprovalError) Error() string { return e.Err.Error() }
func (e *NamespaceApprovalError) Unwrap() error { return e.Err }
