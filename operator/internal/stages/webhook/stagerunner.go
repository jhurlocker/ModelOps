package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"
	"github.com/jhurlocker/modelops-operator/internal/webhookcore"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const defaultProviderConfigKind = "WebhookProviderConfig"

// defaultHTTPTimeout is the timeout for a single HTTP call when no
// explicit timeout is configured in the WebhookProviderConfig.
const defaultHTTPTimeout = 30 * time.Second

// defaultPollInterval is the minimum time between poll calls when no
// explicit pollInterval is configured.
const defaultPollInterval = 30 * time.Second

// defaultRetryMaxAttempts is the number of call attempts when no RetryPolicy
// is configured.
const defaultRetryMaxAttempts = 3

// defaultRetryBackoff is the backoff between retry attempts.
const defaultRetryBackoff = 2 * time.Second

// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;create
// +kubebuilder:rbac:groups=modelops.example.io,resources=webhookproviderconfigs,verbs=get;list;watch

// StageRunner executes lifecycle stages by calling out to an external
// HTTP+JSON API described by a WebhookProviderConfig CR. It is the
// project's first genuinely install-time-extensible StageRunner: adding
// a new provider requires only a WebhookProviderConfig instance, not a
// Go code change.
type StageRunner struct {
	Client    client.Client
	Caller    webhookcore.Caller
	Scheme    *runtime.Scheme
	Renderer  webhookcore.Renderer
	Extractor webhookcore.JSONPathExtractor
}

var _ stagecommon.StageRunner = (*StageRunner)(nil)

// EnsureRun implements stagecommon.StageRunner.
func (r *StageRunner) EnsureRun(ctx context.Context, req *modelopsv1alpha1.ModelRequest, stage stagecommon.StageSpec) (stagecommon.StageStatus, error) {
	cfg, err := r.resolveConfig(ctx, req.Namespace, stage)
	if err != nil {
		return stagecommon.StageStatus{}, &stagecommon.ProviderConfigError{Err: err}
	}

	trackingKey := types.NamespacedName{Name: stage.RunName, Namespace: req.Namespace}
	var tracking corev1.ConfigMap
	getErr := r.Client.Get(ctx, trackingKey, &tracking)

	if apierrors.IsNotFound(getErr) {
		return r.submitJob(ctx, req, stage, cfg, trackingKey)
	}
	if getErr != nil {
		return stagecommon.StageStatus{}, getErr
	}

	return r.pollJob(ctx, req, stage, cfg, &tracking, trackingKey)
}

// submitJob sends the submit HTTP request and creates a tracking
// ConfigMap to persist the job ID across reconciles.
func (r *StageRunner) submitJob(
	ctx context.Context,
	req *modelopsv1alpha1.ModelRequest,
	stage stagecommon.StageSpec,
	cfg *modelopsv1alpha1.WebhookProviderConfigSpec,
	trackingKey types.NamespacedName,
) (stagecommon.StageStatus, error) {
	wc := webhookContext{
		ModelRequest:  req,
		Stage:         stage,
		Namespace:     req.Namespace,
		StatusMapping: &cfg.StatusMapping,
	}

	authHeader, err := r.buildAuthHeader(ctx, req.Namespace, cfg)
	if err != nil {
		return stagecommon.StageStatus{}, fmt.Errorf("stage %q: building auth header: %w", stage.Name, err)
	}

	var body string
	if cfg.RequestTemplate != "" {
		rendered, renderErr := r.Renderer.Execute(cfg.RequestTemplate, wc)
		if renderErr != nil {
			return stagecommon.StageStatus{}, fmt.Errorf("stage %q: rendering request template: %w", stage.Name, renderErr)
		}
		body = rendered
	}

	method := cfg.Method
	if method == "" {
		method = "POST"
	}

	timeout := r.timeoutFrom(cfg.SubmitTimeout)
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, callErr := r.callWithRetry(callCtx, webhookcore.CallConfig{
		Method: method,
		URL:    cfg.SubmitEndpoint,
		Body:   body,
		Header: authHeader,
	}, cfg.SubmitRetry)
	if callErr != nil {
		return stagecommon.StageStatus{}, fmt.Errorf("stage %q: submit call failed: %w", stage.Name, callErr)
	}

	jobID, extractErr := r.Extractor.String(result.Body, cfg.SubmitJobIDJsonPath)
	if extractErr != nil {
		return stagecommon.StageStatus{}, fmt.Errorf("stage %q: extracting job ID from submit response: %w", stage.Name, extractErr)
	}

	tracking := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      trackingKey.Name,
			Namespace: trackingKey.Namespace,
			Labels: map[string]string{
				"modelops.example.io/model-request": req.Name,
			},
		},
		Data: map[string]string{
			"jobID": jobID,
		},
	}
	if ownerErr := controllerutil.SetControllerReference(req, &tracking, r.Scheme); ownerErr != nil {
		return stagecommon.StageStatus{}, ownerErr
	}
	if createErr := r.Client.Create(ctx, &tracking); createErr != nil && !apierrors.IsAlreadyExists(createErr) {
		return stagecommon.StageStatus{}, createErr
	}

	return stagecommon.StageStatus{
		Phase:  stagecommon.StageRunning,
		Reason: "JobSubmitted",
		RunRef: stage.RunName,
	}, nil
}

// pollJob reads the job ID from the tracking ConfigMap, polls the
// status endpoint, and maps the response into StageStatus.
func (r *StageRunner) pollJob(
	ctx context.Context,
	req *modelopsv1alpha1.ModelRequest,
	stage stagecommon.StageSpec,
	cfg *modelopsv1alpha1.WebhookProviderConfigSpec,
	tracking *corev1.ConfigMap,
	trackingKey types.NamespacedName,
) (stagecommon.StageStatus, error) {
	jobID := tracking.Data["jobID"]
	if jobID == "" {
		// Corrupted tracking object -- re-submit.
		r.Client.Delete(ctx, tracking)
		return r.submitJob(ctx, req, stage, cfg, trackingKey)
	}

	wc := webhookContext{
		ModelRequest:  req,
		Stage:         stage,
		JobID:         jobID,
		Namespace:     req.Namespace,
		StatusMapping: &cfg.StatusMapping,
	}

	authHeader, err := r.buildAuthHeader(ctx, req.Namespace, cfg)
	if err != nil {
		return stagecommon.StageStatus{}, fmt.Errorf("stage %q: building poll auth header: %w", stage.Name, err)
	}

	statusURL, renderErr := r.Renderer.Execute(cfg.StatusEndpoint, wc)
	if renderErr != nil {
		return stagecommon.StageStatus{}, fmt.Errorf("stage %q: rendering status endpoint: %w", stage.Name, renderErr)
	}

	timeout := r.timeoutFrom(cfg.PollTimeout)
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, callErr := r.callWithRetry(callCtx, webhookcore.CallConfig{
		Method: "GET",
		URL:    statusURL,
		Header: authHeader,
	}, cfg.PollRetry)
	if callErr != nil {
		return stagecommon.StageStatus{
			Phase:   stagecommon.StageRunning,
			Reason:  "PollFailed",
			Message: fmt.Sprintf("poll call failed: %v", callErr),
			RunRef:  stage.RunName,
		}, nil
	}

	extractedPhase, extractErr := r.Extractor.String(result.Body, cfg.StatusMapping.PhaseJsonPath)
	if extractErr != nil {
		return stagecommon.StageStatus{
			Phase:   stagecommon.StageRunning,
			Reason:  "PollParseError",
			Message: fmt.Sprintf("extracting phase from poll response: %v", extractErr),
			RunRef:  stage.RunName,
		}, nil
	}

	mappedPhase, recognized := mapPhase(extractedPhase, cfg.StatusMapping.PhaseValueMap)
	if !recognized {
		return stagecommon.StageStatus{
			Phase:   stagecommon.StageRunning,
			Reason:  "UnrecognizedPhase",
			Message: fmt.Sprintf("unrecognized provider phase: %s", extractedPhase),
			RunRef:  stage.RunName,
		}, nil
	}

	var message, detailsURL string
	if cfg.StatusMapping.MessageTemplate != "" {
		wc.Response = parsePollBody(result.Body)
		if rendered, renderErr := r.Renderer.Execute(cfg.StatusMapping.MessageTemplate, wc); renderErr == nil {
			message = rendered
		}
	}
	if cfg.StatusMapping.DetailsUrlTemplate != "" {
		if rendered, renderErr := r.Renderer.Execute(cfg.StatusMapping.DetailsUrlTemplate, wc); renderErr == nil {
			detailsURL = rendered
		}
	}

	reason := "Polling"
	if mappedPhase == stagecommon.StageSucceeded {
		reason = "JobSucceeded"
	} else if mappedPhase == stagecommon.StageFailed {
		reason = "JobFailed"
	}

	return stagecommon.StageStatus{
		Phase:      mappedPhase,
		Reason:     reason,
		Message:    message,
		RunRef:     stage.RunName,
		DetailsURL: detailsURL,
	}, nil
}

// mapPhase translates a provider status string into a StagePhase. The
// second return value is false when the value doesn't appear in the map
// (the caller handles this as StageRunning + a clear message).
func mapPhase(providerValue string, phaseValueMap map[string]modelopsv1alpha1.StagePhase) (stagecommon.StagePhase, bool) {
	mapped, ok := phaseValueMap[providerValue]
	if !ok {
		return stagecommon.StageRunning, false
	}
	switch mapped {
	case modelopsv1alpha1.WebhookPhaseSucceeded:
		return stagecommon.StageSucceeded, true
	case modelopsv1alpha1.WebhookPhaseFailed:
		return stagecommon.StageFailed, true
	default:
		return stagecommon.StageRunning, true
	}
}

func (r *StageRunner) buildAuthHeader(ctx context.Context, namespace string, cfg *modelopsv1alpha1.WebhookProviderConfigSpec) (string, error) {
	if cfg.AuthSecretRef == nil {
		return "", nil
	}
	prefix := "Bearer "
	if cfg.AuthHeaderPrefix != nil {
		prefix = *cfg.AuthHeaderPrefix
	}
	ref := webhookcore.SecretKeyRef{
		Name: cfg.AuthSecretRef.Name,
		Key:  cfg.AuthSecretRef.Key,
	}
	return webhookcore.BuildAuthHeader(ctx, r.Client, namespace, ref, prefix)
}

func (r *StageRunner) callWithRetry(ctx context.Context, callCfg webhookcore.CallConfig, policy *modelopsv1alpha1.RetryPolicy) (webhookcore.CallResult, error) {
	maxAttempts := defaultRetryMaxAttempts
	backoff := defaultRetryBackoff
	if policy != nil {
		maxAttempts = policy.MaxAttempts
		if policy.Backoff != nil {
			backoff = policy.Backoff.Duration
		}
	}

	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return webhookcore.CallResult{}, ctx.Err()
			case <-time.After(backoff):
			}
		}
		result, err := r.Caller.Call(ctx, callCfg)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}
	return webhookcore.CallResult{}, lastErr
}

func (r *StageRunner) timeoutFrom(d *metav1.Duration) time.Duration {
	if d != nil {
		return d.Duration
	}
	return defaultHTTPTimeout
}

func (r *StageRunner) pollIntervalFrom(d *metav1.Duration) time.Duration {
	if d != nil {
		return d.Duration
	}
	return defaultPollInterval
}

// parsePollBody unmarshals the poll response body as a map for template
// access via {{.Response.<key>}}.
func parsePollBody(body []byte) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	return m
}

// webhookContext is the data context available to all Go templates
// (requestTemplate, statusEndpoint, messageTemplate, detailsUrlTemplate).
// Deliberately excludes any Secret field -- per Phase 8, no credential
// value is ever available to a template.
type webhookContext struct {
	ModelRequest  *modelopsv1alpha1.ModelRequest
	Stage         stagecommon.StageSpec
	JobID         string
	Namespace     string
	StatusMapping any
	Response      map[string]any
}

// End of stagerunner.go
