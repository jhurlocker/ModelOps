package webhook

import (
	"context"
	"strings"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"
	"github.com/jhurlocker/modelops-operator/internal/webhookcore"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, modelopsv1alpha1.AddToScheme(s))
	return s
}

func newFakeClientWithObjects(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&modelopsv1alpha1.CapacityPlan{}).
		Build()
}

func newOwnerModelRequest(name, ns string) *modelopsv1alpha1.ModelRequest {
	return &modelopsv1alpha1.ModelRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: "test-uid"},
	}
}

func newWebhookProviderConfig(name, ns string, spec modelopsv1alpha1.WebhookProviderConfigSpec) *modelopsv1alpha1.WebhookProviderConfig {
	return &modelopsv1alpha1.WebhookProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       spec,
	}
}

// fakeCaller returns scripted results in order, one per Call invocation.
type fakeCaller struct {
	results []webhookcore.CallResult
	errs    []error
	callIdx int
	calls   []webhookcore.CallConfig
	t       *testing.T
}

func newFakeCaller(t *testing.T) *fakeCaller {
	return &fakeCaller{t: t}
}

func (f *fakeCaller) scriptResult(r webhookcore.CallResult) {
	f.results = append(f.results, r)
}

func (f *fakeCaller) scriptError(err error) {
	f.errs = append(f.errs, err)
}

func (f *fakeCaller) Call(_ context.Context, cfg webhookcore.CallConfig) (webhookcore.CallResult, error) {
	f.calls = append(f.calls, cfg)
	if f.callIdx < len(f.errs) {
		err := f.errs[f.callIdx]
		f.callIdx++
		return webhookcore.CallResult{}, err
	}
	if f.callIdx >= len(f.results) {
		f.t.Fatalf("fakeCaller: no result scripted for call %d", f.callIdx)
	}
	r := f.results[f.callIdx]
	f.callIdx++
	return r, nil
}

func newTestStageRunner(t *testing.T, c client.Client, caller *fakeCaller) *StageRunner {
	t.Helper()
	return &StageRunner{
		Client:    c,
		Caller:    caller,
		Scheme:    testScheme(t),
		Renderer:  webhookcore.Renderer{},
		Extractor: webhookcore.JSONPathExtractor{},
	}
}

func defaultWebhookConfigSpec() modelopsv1alpha1.WebhookProviderConfigSpec {
	return modelopsv1alpha1.WebhookProviderConfigSpec{
		ProviderType:       "webhook",
		SubmitEndpoint:     "https://example.com/api/jobs",
		Method:             "POST",
		SubmitJobIDJsonPath: "{$.jobId}",
		StatusEndpoint:      "https://example.com/api/jobs/{{.JobID}}",
		StatusMapping: modelopsv1alpha1.WebhookStatusMapping{
			PhaseJsonPath: "{$.status}",
			PhaseValueMap: map[string]modelopsv1alpha1.StagePhase{
				"COMPLETED": modelopsv1alpha1.WebhookPhaseSucceeded,
				"FAILED":    modelopsv1alpha1.WebhookPhaseFailed,
				"RUNNING":   modelopsv1alpha1.WebhookPhaseRunning,
			},
			MessageTemplate:    "Job {{.JobID}} status={{.Response.status}}",
			DetailsUrlTemplate: "https://example.com/console/jobs/{{.JobID}}",
		},
	}
}

// ---------------------------------------------------------------------------
// Submit tests
// ---------------------------------------------------------------------------

func TestEnsureRun_Submit_CreatesTrackingConfigMap_ReturnsRunning(t *testing.T) {
	cfg := newWebhookProviderConfig("wc-1", "ns-1", defaultWebhookConfigSpec())
	c := newFakeClientWithObjects(t, cfg)
	caller := newFakeCaller(t)
	caller.scriptResult(webhookcore.CallResult{StatusCode: 200, Body: []byte(`{"jobId":"j-12345"}`)})
	r := newTestStageRunner(t, c, caller)
	mr := newOwnerModelRequest("mr-1", "ns-1")

	status, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name: "webhook-check",
		RunName: "mr-1-webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1", Kind: "WebhookProviderConfig",
		},
	})
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageRunning, status.Phase)
	require.Equal(t, "mr-1-webhook-check", status.RunRef)

	var tracking corev1.ConfigMap
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "mr-1-webhook-check", Namespace: "ns-1"}, &tracking))
	require.Equal(t, "j-12345", tracking.Data["jobID"])
	require.Len(t, tracking.OwnerReferences, 1)
	require.Equal(t, "mr-1", tracking.OwnerReferences[0].Name)

	require.Len(t, caller.calls, 1)
	require.Equal(t, "POST", caller.calls[0].Method)
	require.Equal(t, "https://example.com/api/jobs", caller.calls[0].URL)
}

func TestEnsureRun_Submit_WithAuthHeader(t *testing.T) {
	spec := defaultWebhookConfigSpec()
	spec.AuthSecretRef = &modelopsv1alpha1.SecretKeyRef{Name: "auth-s", Key: "token"}
	cfg := newWebhookProviderConfig("wc-1", "ns-1", spec)
	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "auth-s", Namespace: "ns-1"},
		Data:       map[string][]byte{"token": []byte("my-bearer-token")},
	}
	c := newFakeClientWithObjects(t, cfg, authSecret)
	caller := newFakeCaller(t)
	caller.scriptResult(webhookcore.CallResult{StatusCode: 200, Body: []byte(`{"jobId":"j-1"}`)})
	r := newTestStageRunner(t, c, caller)
	mr := newOwnerModelRequest("mr-1", "ns-1")

	_, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name: "webhook-check",
		RunName: "mr-1-webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1", Kind: "WebhookProviderConfig",
		},
	})
	require.NoError(t, err)
	require.Len(t, caller.calls, 1)
	require.Equal(t, "Bearer my-bearer-token", caller.calls[0].Header)
}

func TestEnsureRun_Submit_WithRequestTemplate(t *testing.T) {
	spec := defaultWebhookConfigSpec()
	spec.RequestTemplate = `{"modelId":"{{.ModelRequest.Spec.Model.URI}}","namespace":"{{.Namespace}}"}`
	cfg := newWebhookProviderConfig("wc-1", "ns-1", spec)
	c := newFakeClientWithObjects(t, cfg)
	caller := newFakeCaller(t)
	caller.scriptResult(webhookcore.CallResult{StatusCode: 200, Body: []byte(`{"jobId":"j-1"}`)})
	r := newTestStageRunner(t, c, caller)
	mr := newOwnerModelRequest("mr-1", "ns-1")
	mr.Spec.Model.URI = "ibm-granite/granite-3.0-2b"

	_, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name: "webhook-check",
		RunName: "mr-1-webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1", Kind: "WebhookProviderConfig",
		},
	})
	require.NoError(t, err)
	require.Len(t, caller.calls, 1)
	require.Equal(t, `{"modelId":"ibm-granite/granite-3.0-2b","namespace":"ns-1"}`, caller.calls[0].Body)
}

func TestEnsureRun_Submit_TrackingConfigMapAlreadyExists_Polls(t *testing.T) {
	cfg := newWebhookProviderConfig("wc-1", "ns-1", defaultWebhookConfigSpec())
	existingTracking := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-1-webhook-check", Namespace: "ns-1"},
		Data:       map[string]string{"jobID": "existing-id"},
	}
	c := newFakeClientWithObjects(t, cfg, existingTracking)
	caller := newFakeCaller(t)
	caller.scriptResult(webhookcore.CallResult{StatusCode: 200, Body: []byte(`{"status":"RUNNING"}`)})
	r := newTestStageRunner(t, c, caller)
	mr := newOwnerModelRequest("mr-1", "ns-1")

	status, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name: "webhook-check",
		RunName: "mr-1-webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1", Kind: "WebhookProviderConfig",
		},
	})
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageRunning, status.Phase)
	require.Len(t, caller.calls, 1)
	require.Equal(t, "GET", caller.calls[0].Method)
	require.Equal(t, "https://example.com/api/jobs/existing-id", caller.calls[0].URL)
}

func TestEnsureRun_Submit_NoProviderConfigRef_ReturnsError(t *testing.T) {
	c := newFakeClientWithObjects(t)
	caller := newFakeCaller(t)
	r := newTestStageRunner(t, c, caller)
	mr := newOwnerModelRequest("mr-1", "ns-1")

	_, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name:    "webhook-check",
		RunName: "mr-1-webhook-check",
	})
	require.Error(t, err)
	var pce *stagecommon.ProviderConfigError
	require.ErrorAs(t, err, &pce)
}

func TestEnsureRun_Submit_UnsupportedConfigKind_ReturnsError(t *testing.T) {
	c := newFakeClientWithObjects(t)
	caller := newFakeCaller(t)
	r := newTestStageRunner(t, c, caller)
	mr := newOwnerModelRequest("mr-1", "ns-1")

	_, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name: "webhook-check",
		RunName: "mr-1-webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1", Kind: "IntakeProviderConfig",
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported provider config kind")
}

func TestEnsureRun_Submit_SubmitCallFails_ReturnsError(t *testing.T) {
	spec := defaultWebhookConfigSpec()
	spec.SubmitRetry = &modelopsv1alpha1.RetryPolicy{MaxAttempts: 1}
	cfg := newWebhookProviderConfig("wc-1", "ns-1", spec)
	c := newFakeClientWithObjects(t, cfg)
	caller := newFakeCaller(t)
	caller.scriptError(context.DeadlineExceeded)
	r := newTestStageRunner(t, c, caller)
	mr := newOwnerModelRequest("mr-1", "ns-1")

	_, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name: "webhook-check",
		RunName: "mr-1-webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1", Kind: "WebhookProviderConfig",
		},
	})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Poll tests
// ---------------------------------------------------------------------------

func TestEnsureRun_Poll_Running_ReturnsRunning(t *testing.T) {
	cfg := newWebhookProviderConfig("wc-1", "ns-1", defaultWebhookConfigSpec())
	tracking := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-1-webhook-check", Namespace: "ns-1",
			CreationTimestamp: metav1.Now()},
		Data: map[string]string{"jobID": "j-12345"},
	}
	c := newFakeClientWithObjects(t, cfg, tracking)
	caller := newFakeCaller(t)
	caller.scriptResult(webhookcore.CallResult{StatusCode: 200, Body: []byte(`{"status":"RUNNING"}`)})
	r := newTestStageRunner(t, c, caller)
	mr := newOwnerModelRequest("mr-1", "ns-1")

	status, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name: "webhook-check",
		RunName: "mr-1-webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1", Kind: "WebhookProviderConfig",
		},
	})
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageRunning, status.Phase)
	require.Equal(t, "Job j-12345 status=RUNNING", status.Message)
	require.Equal(t, "https://example.com/console/jobs/j-12345", status.DetailsURL)
	require.Equal(t, 1, len(caller.calls))
	require.Equal(t, "https://example.com/api/jobs/j-12345", caller.calls[0].URL)
	require.Equal(t, "GET", caller.calls[0].Method)
}

func TestEnsureRun_Poll_Succeeded_ReturnsSucceeded(t *testing.T) {
	cfg := newWebhookProviderConfig("wc-1", "ns-1", defaultWebhookConfigSpec())
	tracking := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-1-webhook-check", Namespace: "ns-1",
			CreationTimestamp: metav1.Now()},
		Data: map[string]string{"jobID": "j-12345"},
	}
	c := newFakeClientWithObjects(t, cfg, tracking)
	caller := newFakeCaller(t)
	caller.scriptResult(webhookcore.CallResult{StatusCode: 200, Body: []byte(`{"status":"COMPLETED"}`)})
	r := newTestStageRunner(t, c, caller)
	mr := newOwnerModelRequest("mr-1", "ns-1")

	status, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name: "webhook-check",
		RunName: "mr-1-webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1", Kind: "WebhookProviderConfig",
		},
	})
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageSucceeded, status.Phase)
	require.Equal(t, "JobSucceeded", status.Reason)
	require.Equal(t, "https://example.com/console/jobs/j-12345", status.DetailsURL)
}

func TestEnsureRun_Poll_Failed_ReturnsFailed(t *testing.T) {
	spec := defaultWebhookConfigSpec()
	spec.StatusMapping.PhaseValueMap["FAILED"] = modelopsv1alpha1.WebhookPhaseFailed
	cfg := newWebhookProviderConfig("wc-1", "ns-1", spec)
	tracking := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-1-webhook-check", Namespace: "ns-1",
			CreationTimestamp: metav1.Now()},
		Data: map[string]string{"jobID": "j-12345"},
	}
	c := newFakeClientWithObjects(t, cfg, tracking)
	caller := newFakeCaller(t)
	caller.scriptResult(webhookcore.CallResult{StatusCode: 200, Body: []byte(`{"status":"FAILED"}`)})
	r := newTestStageRunner(t, c, caller)
	mr := newOwnerModelRequest("mr-1", "ns-1")

	status, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name: "webhook-check",
		RunName: "mr-1-webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1", Kind: "WebhookProviderConfig",
		},
	})
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageFailed, status.Phase)
	require.Equal(t, "JobFailed", status.Reason)
}

func TestEnsureRun_Poll_UnrecognizedProviderPhase_ReturnsRunningWithClearMessage(t *testing.T) {
	cfg := newWebhookProviderConfig("wc-1", "ns-1", defaultWebhookConfigSpec())
	tracking := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-1-webhook-check", Namespace: "ns-1",
			CreationTimestamp: metav1.Now()},
		Data: map[string]string{"jobID": "j-12345"},
	}
	c := newFakeClientWithObjects(t, cfg, tracking)
	caller := newFakeCaller(t)
	caller.scriptResult(webhookcore.CallResult{StatusCode: 200, Body: []byte(`{"status":"DEPLOYING"}`)})
	r := newTestStageRunner(t, c, caller)
	mr := newOwnerModelRequest("mr-1", "ns-1")

	status, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name: "webhook-check",
		RunName: "mr-1-webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1", Kind: "WebhookProviderConfig",
		},
	})
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageRunning, status.Phase)
	require.Equal(t, "UnrecognizedPhase", status.Reason)
	require.Contains(t, status.Message, "unrecognized provider phase: DEPLOYING")
	require.Equal(t, "", status.DetailsURL, "no details URL rendered since message template produced the unrecognized-phase message instead")
}

func TestEnsureRun_Poll_AuthHeaderSent(t *testing.T) {
	spec := defaultWebhookConfigSpec()
	spec.AuthSecretRef = &modelopsv1alpha1.SecretKeyRef{Name: "auth-s", Key: "token"}
	cfg := newWebhookProviderConfig("wc-1", "ns-1", spec)
	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "auth-s", Namespace: "ns-1"},
		Data:       map[string][]byte{"token": []byte("poll-token")},
	}
	tracking := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-1-webhook-check", Namespace: "ns-1"},
		Data:       map[string]string{"jobID": "j-12345"},
	}
	c := newFakeClientWithObjects(t, cfg, authSecret, tracking)
	caller := newFakeCaller(t)
	caller.scriptResult(webhookcore.CallResult{StatusCode: 200, Body: []byte(`{"status":"RUNNING"}`)})
	r := newTestStageRunner(t, c, caller)
	mr := newOwnerModelRequest("mr-1", "ns-1")

	_, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name: "webhook-check",
		RunName: "mr-1-webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1", Kind: "WebhookProviderConfig",
		},
	})
	require.NoError(t, err)
	require.Len(t, caller.calls, 1)
	require.Equal(t, "Bearer poll-token", caller.calls[0].Header, "poll call must send the same auth header as submit")
}

func TestEnsureRun_Poll_PollCallFails_ReturnsRunning(t *testing.T) {
	spec := defaultWebhookConfigSpec()
	spec.PollRetry = &modelopsv1alpha1.RetryPolicy{MaxAttempts: 1}
	cfg := newWebhookProviderConfig("wc-1", "ns-1", spec)
	tracking := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-1-webhook-check", Namespace: "ns-1"},
		Data:       map[string]string{"jobID": "j-12345"},
	}
	c := newFakeClientWithObjects(t, cfg, tracking)
	caller := newFakeCaller(t)
	caller.scriptError(context.DeadlineExceeded)
	r := newTestStageRunner(t, c, caller)
	mr := newOwnerModelRequest("mr-1", "ns-1")

	status, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name: "webhook-check",
		RunName: "mr-1-webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1", Kind: "WebhookProviderConfig",
		},
	})
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageRunning, status.Phase)
	require.Contains(t, status.Message, "poll call failed")
}

func TestEnsureRun_Poll_TrackingContainsCorruptedJobID_DeletesAndResubmits(t *testing.T) {
	cfg := newWebhookProviderConfig("wc-1", "ns-1", defaultWebhookConfigSpec())
	tracking := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-1-webhook-check", Namespace: "ns-1"},
		Data:       map[string]string{}, // empty jobID
	}
	c := newFakeClientWithObjects(t, cfg, tracking)
	caller := newFakeCaller(t)
	caller.scriptResult(webhookcore.CallResult{StatusCode: 200, Body: []byte(`{"jobId":"j-resubmitted"}`)})
	r := newTestStageRunner(t, c, caller)
	mr := newOwnerModelRequest("mr-1", "ns-1")

	status, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name: "webhook-check",
		RunName: "mr-1-webhook-check",
		ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
			Name: "wc-1", Kind: "WebhookProviderConfig",
		},
	})
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageRunning, status.Phase)
	// Should have deleted and resubmitted.
	require.Len(t, caller.calls, 1)
	require.Equal(t, "POST", caller.calls[0].Method)
}

// ---------------------------------------------------------------------------
// mapPhase tests
// ---------------------------------------------------------------------------

func TestMapPhase_RecognizedValues(t *testing.T) {
	phaseMap := map[string]modelopsv1alpha1.StagePhase{
		"COMPLETED": modelopsv1alpha1.WebhookPhaseSucceeded,
		"FAILED":    modelopsv1alpha1.WebhookPhaseFailed,
		"RUNNING":   modelopsv1alpha1.WebhookPhaseRunning,
	}

	phase, ok := mapPhase("COMPLETED", phaseMap)
	require.True(t, ok)
	require.Equal(t, stagecommon.StageSucceeded, phase)

	phase, ok = mapPhase("FAILED", phaseMap)
	require.True(t, ok)
	require.Equal(t, stagecommon.StageFailed, phase)

	phase, ok = mapPhase("RUNNING", phaseMap)
	require.True(t, ok)
	require.Equal(t, stagecommon.StageRunning, phase)
}

func TestMapPhase_UnrecognizedValue_ReturnsRunningFalse(t *testing.T) {
	phaseMap := map[string]modelopsv1alpha1.StagePhase{
		"COMPLETED": modelopsv1alpha1.WebhookPhaseSucceeded,
	}
	phase, ok := mapPhase("QUEUED", phaseMap)
	require.False(t, ok)
	require.Equal(t, stagecommon.StageRunning, phase)
}

// ---------------------------------------------------------------------------
// StageRunner does not implement OwnedTypesProvider
// ---------------------------------------------------------------------------

func TestStageRunner_DoesNotImplementOwnedTypesProvider(t *testing.T) {
	var r stagecommon.StageRunner = &StageRunner{}
	_, ok := r.(stagecommon.OwnedTypesProvider)
	require.False(t, ok, "webhook.StageRunner creates no K8s child objects that SetupWithManager needs to watch -- only tracking ConfigMaps, handled via explicit Get/Create, not OwnedTypesProvider")
}

// ---------------------------------------------------------------------------
// Cross-package StagePhase sync test
// ---------------------------------------------------------------------------

func TestStagePhaseConstants_SyncedWithStagecommon(t *testing.T) {
	require.Equal(t, string(modelopsv1alpha1.WebhookPhaseRunning), string(stagecommon.StageRunning),
		"WebhookPhaseRunning must match stagecommon.StageRunning")
	require.Equal(t, string(modelopsv1alpha1.WebhookPhaseSucceeded), string(stagecommon.StageSucceeded),
		"WebhookPhaseSucceeded must match stagecommon.StageSucceeded")
	require.Equal(t, string(modelopsv1alpha1.WebhookPhaseFailed), string(stagecommon.StageFailed),
		"WebhookPhaseFailed must match stagecommon.StageFailed")
}

// ---------------------------------------------------------------------------
// Handler tests
// ---------------------------------------------------------------------------

func TestHandler_BuildSpec_ReturnsSpecWithProviderConfigRef(t *testing.T) {
	h := Handler{}
	ref := &modelopsv1alpha1.ProviderConfigRef{Name: "wc-1", Kind: "WebhookProviderConfig"}
	spec, err := h.BuildSpec(stagecommon.StageContext{
		ModelRequest: &modelopsv1alpha1.ModelRequest{
			ObjectMeta: metav1.ObjectMeta{Name: "mr-1"},
		},
		Stage: modelopsv1alpha1.ProfileStageSpec{
			Name:              "webhook-check",
			ProviderConfigRef: ref,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "webhook-check", spec.Name)
	require.Equal(t, "mr-1-webhook-check", spec.RunName)
	require.Equal(t, ref, spec.ProviderConfigRef)
}

func TestHandler_BuildSpec_MissingProviderConfigRef_ReturnsError(t *testing.T) {
	h := Handler{}
	_, err := h.BuildSpec(stagecommon.StageContext{
		ModelRequest: &modelopsv1alpha1.ModelRequest{
			ObjectMeta: metav1.ObjectMeta{Name: "mr-1"},
		},
		Stage: modelopsv1alpha1.ProfileStageSpec{
			Name: "webhook-check",
		},
	})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "ProviderConfigRef"), "error must mention ProviderConfigRef")
}

func TestExtractCheckResults_FullFixture_ExtractsAllFields(t *testing.T) {
	body := []byte(`{
		"checks": [
			{"type": "SecurityScan", "passed": true, "reason": "no-cves", "message": "All CVEs below threshold"},
			{"type": "ComplianceScan", "passed": true, "reason": "policy-ok"},
			{"type": "Benchmark", "passed": false, "reason": "throughput-low", "message": "Throughput below target"}
		]
	}`)

	extractor := &webhookcore.JSONPathExtractor{}
	cr := extractCheckResults(body, "$.checks", extractor)

	require.Len(t, cr, 3)

	require.Equal(t, "SecurityScan", cr[0].Type)
	require.True(t, cr[0].Passed)
	require.Equal(t, "no-cves", cr[0].Reason)
	require.Equal(t, "All CVEs below threshold", cr[0].Message)

	require.Equal(t, "ComplianceScan", cr[1].Type)
	require.True(t, cr[1].Passed)
	require.Equal(t, "policy-ok", cr[1].Reason)

	require.Equal(t, "Benchmark", cr[2].Type)
	require.False(t, cr[2].Passed)
	require.Equal(t, "throughput-low", cr[2].Reason)
	require.Equal(t, "Throughput below target", cr[2].Message)
}

func TestExtractCheckResults_EmptyJsonPath_ReturnsNil(t *testing.T) {
	body := []byte(`{"checks": [{"type": "SecurityScan", "passed": true}]}`)
	extractor := &webhookcore.JSONPathExtractor{}
	cr := extractCheckResults(body, "", extractor)
	require.Nil(t, cr)
}

func TestExtractCheckResults_EmptyArray_ReturnsNil(t *testing.T) {
	body := []byte(`{"checks": []}`)
	extractor := &webhookcore.JSONPathExtractor{}
	cr := extractCheckResults(body, "$.checks", extractor)
	require.Nil(t, cr)
}

func TestExtractCheckResults_NonArrayAtPath_ReturnsNil(t *testing.T) {
	body := []byte(`{"checks": "not-an-array"}`)
	extractor := &webhookcore.JSONPathExtractor{}
	cr := extractCheckResults(body, "$.checks", extractor)
	require.Nil(t, cr)
}

func TestExtractCheckResults_SkipsNonMapEntries(t *testing.T) {
	body := []byte(`{
		"checks": [
			{"type": "SecurityScan", "passed": true},
			"not-a-map",
			{"type": "ComplianceScan", "passed": false}
		]
	}`)

	extractor := &webhookcore.JSONPathExtractor{}
	cr := extractCheckResults(body, "$.checks", extractor)

	require.Len(t, cr, 2)
	require.Equal(t, "SecurityScan", cr[0].Type)
	require.Equal(t, "ComplianceScan", cr[1].Type)
}
