// Package webhook implements a generic, install-time-extensible
// stagecommon.StageRunner driven entirely by WebhookProviderConfig CRD
// instances. It requires no Go code change or operator rebuild to add a
// new HTTP+JSON-backed execution provider -- configure a
// WebhookProviderConfig CR, reference it from a ProfileStageSpec with
// Kind: "Webhook", and the dispatcher routes to this StageRunner
// automatically.
//
// This package shares no import with internal/stages/tekton,
// internal/stages/noop, or internal/stages/sandbox, etc. It depends only
// on internal/stagecommon (the StageRunner contract),
// api/v1alpha1 (CRD types), and internal/webhookcore (HTTP/template/
// JSONPath plumbing).
package webhook
