package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WebhookProviderConfigSpec holds the configuration for one install-time-
// extensible HTTP+JSON stage execution provider. Unlike
// IntakeProviderConfig (which requires writing Go code and recompiling
// this operator for each new backend), a single, generic Webhook-provider
// StageRunner (internal/stages/webhook) interprets instances of this CRD
// without any Go code change. See docs/PHASE_LOG.md Phase A.
type WebhookProviderConfigSpec struct {
	// ProviderType discriminates which StageRunner implementation
	// understands this config. Only "webhook" exists today.
	// +kubebuilder:validation:Enum=webhook
	ProviderType string `json:"providerType"`

	// SubmitEndpoint is the URL the StageRunner POSTs to for starting a
	// new job (the "submit" call). This URL is used as-is, not
	// templated -- requestTemplate's rendered output is the body of
	// this request.
	SubmitEndpoint string `json:"submitEndpoint"`

	// Method is the HTTP method for the submit call. Defaults to POST.
	// +kubebuilder:default=POST
	// +kubebuilder:validation:Enum=POST;PUT
	Method string `json:"method,omitempty"`

	// AuthSecretRef references a key within a Secret (same namespace as
	// the ModelRequest) whose value is used to construct an
	// Authorization header on every HTTP call (submit AND polling,
	// resolved fresh on each call). When nil, no Authorization header
	// is sent at all -- an explicit choice, not a default, for
	// providers whose endpoint is unauthenticated (e.g. cluster-local
	// mock services, private internal APIs).
	// +optional
	AuthSecretRef *SecretKeyRef `json:"authSecretRef,omitempty"`

	// AuthHeaderPrefix replaces the default "Bearer " prefix on the
	// Authorization header. Set to an explicit empty string ("") to
	// send the raw Secret value with no prefix (e.g. a provider
	// expecting "Authorization: <api-key>" without a "Bearer" keyword).
	// Only consulted when authSecretRef is set.
	// +optional
	AuthHeaderPrefix *string `json:"authHeaderPrefix,omitempty"`

	// RequestTemplate is a Go template rendered against a webhook
	// context (see internal/stages/webhook for the exact fields
	// available -- guaranteed to contain no credential values) to
	// produce the request body sent to SubmitEndpoint. If unset or
	// empty, requests carry no body (e.g. a provider whose submit
	// endpoint is a simple POST with no payload, or whose job config
	// is entirely in the URL/headers).
	// +optional
	RequestTemplate string `json:"requestTemplate,omitempty"`

	// SubmitJobIDJsonPath extracts the job identifier from the submit
	// response body, e.g. "$.id" or "$.data.jobId". Required -- the
	// StageRunner must be able to construct the statusEndpoint URL
	// from this value and to track the job across reconciles.
	SubmitJobIDJsonPath string `json:"submitJobIDJsonPath"`

	// StatusEndpoint is a Go template rendered against a webhook
	// context to produce the poll URL. The resolved job ID is
	// available as {{.JobID}} in the template.
	StatusEndpoint string `json:"statusEndpoint"`

	// StatusMapping translates the provider's own vocabulary into
	// stagecommon.StagePhase (Running/Succeeded/Failed) and produces
	// human-readable status messages and detail URLs.
	StatusMapping WebhookStatusMapping `json:"statusMapping"`

	// SubmitTimeout bounds the submit HTTP call. Default 30s.
	// +optional
	SubmitTimeout *metav1.Duration `json:"submitTimeout,omitempty"`

	// PollTimeout bounds each poll HTTP call. Default 30s.
	// +optional
	PollTimeout *metav1.Duration `json:"pollTimeout,omitempty"`

	// SubmitRetry configures retry for the submit call only.
	// Exhausted retries surface as an EnsureRun error (the reconciler
	// requeues with its global transient-error backoff). Distinct from
	// any retry logic the remote provider does on its own side. Nil
	// means 3 attempts, 2s backoff.
	// +optional
	SubmitRetry *RetryPolicy `json:"submitRetry,omitempty"`

	// PollRetry configures retry for each poll call. Exhausted retries
	// on a poll call are treated as a temporary polling failure --
	// StageRunning (keep trying on the next reconcile), not
	// StageFailed.
	// +optional
	PollRetry *RetryPolicy `json:"pollRetry,omitempty"`

	// PollInterval is the minimum time between poll calls enforced by
	// the StageRunner -- a poll-response-vs-pollInterval check
	// prevents hammering the provider on every controller-manager
	// resync or unrelated watch event. Default 30s.
	// +optional
	PollInterval *metav1.Duration `json:"pollInterval,omitempty"`
}

// WebhookStatusMapping translates a provider's native status vocabulary
// into stagecommon.StagePhase and human-readable output.
type WebhookStatusMapping struct {
	// PhaseJsonPath extracts the provider's status value from each
	// poll response body, e.g. "$.status" or "$.state".
	PhaseJsonPath string `json:"phaseJsonPath"`

	// PhaseValueMap maps the provider's own status strings into
	// StagePhase values. Any value the provider returns that is NOT a
	// key in this map is mapped to StageRunning (non-terminal safe
	// default) with a clear "unrecognized provider phase: <value>"
	// prefix in StageStatus.Message -- the operator sees this, knows
	// their mapping is incomplete, and fixes it without a silent
	// misclassification.
	// +kubebuilder:validation:MinProperties=1
	PhaseValueMap map[string]StagePhase `json:"phaseValueMap"`

	// MessageTemplate is a Go template rendered against a webhook
	// context to produce StageStatus.Message. The poll response body
	// is available as {{.Response}} and individual keys are
	// traversable via Go template field access. If unset or its
	// rendered output is empty, Message is left empty.
	// +optional
	MessageTemplate string `json:"messageTemplate,omitempty"`

	// DetailsUrlTemplate is a Go template rendered against a webhook
	// context to produce StageStatus.DetailsURL -- a human-facing link
	// out to the provider's own console, logs, or job page, for
	// debugging. If unset or its rendered output is empty, DetailsURL
	// is left empty.
	// +optional
	DetailsUrlTemplate string `json:"detailsUrlTemplate,omitempty"`
}

// StagePhase mirrors stagecommon.StagePhase so the CRD's own enum
// validation can reject typos at API-server admission time (e.g.
// "Runing" instead of "Running"). Kept in sync via a compile-time test
// in internal/stages/webhook.
// +kubebuilder:validation:Enum=Running;Succeeded;Failed
type StagePhase string

const (
	WebhookPhaseRunning   StagePhase = "Running"
	WebhookPhaseSucceeded StagePhase = "Succeeded"
	WebhookPhaseFailed    StagePhase = "Failed"
)

// SecretKeyRef identifies a single key within a named Kubernetes Secret
// (same namespace as the referencing object). Used for authSecretRef --
// never an inline credential value.
type SecretKeyRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// RetryPolicy configures HTTP call retry behavior for submit and poll
// operations independently.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts (1 = no retry).
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	MaxAttempts int `json:"maxAttempts"`

	// Backoff is the delay between attempts. Default 2s.
	// +optional
	Backoff *metav1.Duration `json:"backoff,omitempty"`
}

type WebhookProviderConfigStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="ProviderType",type=string,JSONPath=`.spec.providerType`

type WebhookProviderConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WebhookProviderConfigSpec   `json:"spec,omitempty"`
	Status WebhookProviderConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type WebhookProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []WebhookProviderConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WebhookProviderConfig{}, &WebhookProviderConfigList{})
}
