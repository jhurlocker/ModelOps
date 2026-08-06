// Package webhookcore provides shared HTTP-calling, Go-template rendering,
// JSONPath extraction, and auth-header construction primitives for any
// webhook-based StageRunner or monitor. It intentionally imports nothing
// from internal/stagecommon, internal/stages/*, or api/v1alpha1 -- only
// the Go standard library, k8s.io/client-go/util/jsonpath, and the
// controller-runtime client (for Secret reads in BuildAuthHeader).
//
// This package is designed so a future WebhookMonitorConfig consumer
// (mapping to a MonitorStatus shape instead of StageStatus) can reuse the
// same Renderer, Extractor, Caller, and BuildAuthHeader without this
// package needing to change -- it has no business knowing about lifecycle
// phases, stage outcomes, or any terminal-three-phase assumption.
package webhookcore
