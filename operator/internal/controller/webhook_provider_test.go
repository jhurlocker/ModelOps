package controller

import (
	"context"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"
	"github.com/jhurlocker/modelops-operator/internal/stages/noop"
	"github.com/jhurlocker/modelops-operator/internal/stages/webhook"
	"github.com/jhurlocker/modelops-operator/internal/webhookcore"

	"github.com/stretchr/testify/require"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// TestModelRequest_FullLifecycle_ThreeStageRunners_ReachSameTerminalPhase
// is the decisive proof (Phase A) that the StageRunner abstraction is
// real against a second REAL backend (webhook, not just the noop stub
// from Phase 5). Three subtests run the identical fixture (same profile
// shape, PlatformConfig, ModelRequest, CapacityPlan) through Reconcile
// with a different StageRunner injected for the sandbox stage each time:
// tekton, noop, and webhook. All three reach Status.Phase == "Succeeded"
// and all three produce the same RBAC side effects.
func TestModelRequest_FullLifecycle_ThreeStageRunners_ReachSameTerminalPhase(t *testing.T) {
	t.Run("tekton.StageRunner", func(t *testing.T) {
		ns := newTestNamespace(t)
		ensureNamespace(t, "staging")
		newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
		newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
		newModelRequest(t, ns, "mr-1", "profile-1", nil)
		setupSucceededCapacityPlan(t, ns, "mr-1")

		mr, _, err := reconcileModelRequest(t, ns, "mr-1")
		require.NoError(t, err)
		require.Equal(t, "sandboxRunning", mr.Status.Phase)

		setPipelineRunCondition(t, ns, "mr-1-sandbox", corev1.ConditionTrue, "sandbox ok")
		mr, _, err = reconcileModelRequest(t, ns, "mr-1")
		require.NoError(t, err)
		require.Equal(t, "promotionRunning", mr.Status.Phase)

		setPipelineRunCondition(t, ns, "mr-1-promotion-staging", corev1.ConditionTrue, "promotion ok")
		mr, _, err = reconcileModelRequest(t, ns, "mr-1")
		require.NoError(t, err)
		require.Equal(t, "Succeeded", mr.Status.Phase)

		requireServiceAccountExists(t, ns)
		requireServiceAccountExists(t, "staging")
	})

	t.Run("noop.StageRunner", func(t *testing.T) {
		ns := newTestNamespace(t)
		ensureNamespace(t, "staging")
		newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})
		newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))
		newModelRequest(t, ns, "mr-1", "profile-1", nil)
		setupSucceededCapacityPlan(t, ns, "mr-1")

		r := &ModelRequestReconciler{
			Client:        k8sClient,
			Scheme:        testRuntimeScheme(),
			StageHandlers: newStageHandlers(),
			StageRunners:  newStageRunners(k8sClient, testRuntimeScheme(), &noop.StageRunner{}),
		}
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "mr-1")})
		require.NoError(t, err)

		mr := getModelRequest(t, ns, "mr-1")
		require.Equal(t, "Succeeded", mr.Status.Phase, "noop.StageRunner completes every stage in a single reconcile pass")

		requireServiceAccountExists(t, ns)
		requireServiceAccountExists(t, "staging")

		var pr tektonv1.PipelineRun
		getErr := k8sClient.Get(context.Background(), nsName(ns, "mr-1-sandbox"), &pr)
		require.Error(t, getErr, "noop.StageRunner must never create a real PipelineRun")
	})

	t.Run("webhook.StageRunner", func(t *testing.T) {
		ns := newTestNamespace(t)
		ensureNamespace(t, "staging")

		newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{})

		wcCfg := &modelopsv1alpha1.WebhookProviderConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "wc-sandbox", Namespace: ns},
			Spec: modelopsv1alpha1.WebhookProviderConfigSpec{
				ProviderType:        "webhook",
				SubmitEndpoint:      "https://example.com/api/jobs",
				SubmitJobIDJsonPath: "{$.jobId}",
				StatusEndpoint:      "https://example.com/api/jobs/{{.JobID}}",
				StatusMapping: modelopsv1alpha1.WebhookStatusMapping{
					PhaseJsonPath: "{$.status}",
					PhaseValueMap: map[string]modelopsv1alpha1.StagePhase{
						"COMPLETED": modelopsv1alpha1.WebhookPhaseSucceeded,
						"FAILED":    modelopsv1alpha1.WebhookPhaseFailed,
						"RUNNING":   modelopsv1alpha1.WebhookPhaseRunning,
					},
				},
			},
		}
		require.NoError(t, k8sClient.Create(context.Background(), wcCfg))

		webhookSandboxStage := modelopsv1alpha1.ProfileStageSpec{
			Name: "sandbox",
			Kind: "Webhook",
			ProviderConfigRef: &modelopsv1alpha1.ProviderConfigRef{
				Name: "wc-sandbox",
				Kind: "WebhookProviderConfig",
			},
			NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{
				EnsureRBAC: true,
				Labels:     map[string]string{"evalhub.trustyai.opendatahub.io/tenant": ""},
			},
		}
		promotionStage := testDefaultStages(nil)[2]
		promotionStage.Kind = "PipelineRun"
		promotionStage.ProviderConfigRef = nil

		profileSpec := modelopsv1alpha1.ModelLifecycleProfileSpec{
			Workflow: modelopsv1alpha1.WorkflowRef{
				Engine:      "tekton",
				PipelineRef: "model-intake-sandbox",
			},
			PlatformConfigRef: "cfg-1",
			Stages:            []modelopsv1alpha1.ProfileStageSpec{testDefaultStages(nil)[0], webhookSandboxStage, promotionStage},
		}
		newProfile(t, ns, "profile-1", profileSpec)

		newModelRequest(t, ns, "mr-1", "profile-1", nil)
		setupSucceededCapacityPlan(t, ns, "mr-1")

		fakeCaller := newFakeHTTPCaller(t)
		fakeCaller.scriptResult(webhookcore.CallResult{StatusCode: 200, Body: []byte(`{"jobId":"j-1"}`)})    // reconcile 1: submit
		fakeCaller.scriptResult(webhookcore.CallResult{StatusCode: 200, Body: []byte(`{"status":"RUNNING"}`)}) // reconcile 2: poll
		fakeCaller.scriptResult(webhookcore.CallResult{StatusCode: 200, Body: []byte(`{"status":"COMPLETED"}`)})
		// reconcile 3: poll (COMPLETED -> Succeeded, walker advances to promotion)
		fakeCaller.scriptResult(webhookcore.CallResult{StatusCode: 200, Body: []byte(`{"status":"COMPLETED"}`)})
		// reconcile 4: poll again (walker re-checks all stages; sandbox already succeeded)

		webhookRunner := &webhook.StageRunner{
			Client:    k8sClient,
			Caller:    fakeCaller,
			Scheme:    testRuntimeScheme(),
			Renderer:  webhookcore.Renderer{},
			Extractor: webhookcore.JSONPathExtractor{},
		}
		tektonRunner := newStageRunners(k8sClient, testRuntimeScheme(), nil)["PipelineRun"]

		handlers := newStageHandlers()
		handlers["sandbox"] = webhook.Handler{}

		runners := map[string]stagecommon.StageRunner{
			"CapacityPlan": newStageRunners(k8sClient, testRuntimeScheme(), nil)["CapacityPlan"],
			"Webhook":      webhookRunner,
			"PipelineRun":  tektonRunner,
		}

		r := &ModelRequestReconciler{
			Client:        k8sClient,
			Scheme:        testRuntimeScheme(),
			StageHandlers: handlers,
			StageRunners:  runners,
		}

		// Reconcile 1: capacity + webhook submit → SandboxRunning
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "mr-1")})
		require.NoError(t, err)
		mr1 := getModelRequest(t, ns, "mr-1")
		require.Equal(t, "sandboxRunning", mr1.Status.Phase)

		// Reconcile 2: webhook poll (RUNNING) → still SandboxRunning
		_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "mr-1")})
		require.NoError(t, err)
		mr2 := getModelRequest(t, ns, "mr-1")
		require.Equal(t, "sandboxRunning", mr2.Status.Phase)

		// Reconcile 3: webhook poll (COMPLETED) → sandbox done, walker
		// advances to promotion → promotion PipelineRun Running.
		_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "mr-1")})
		require.NoError(t, err)
		mr3 := getModelRequest(t, ns, "mr-1")
		require.Equal(t, "promotionRunning", mr3.Status.Phase)

		setPipelineRunCondition(t, ns, "mr-1-promotion-staging", corev1.ConditionTrue, "promotion ok")

		// Reconcile 4: walker re-checks all stages; sandbox polls
		// (already COMPLETED, same response), promotion succeeds.

		_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: nsName(ns, "mr-1")})
		require.NoError(t, err)
		mr4 := getModelRequest(t, ns, "mr-1")
		require.Equal(t, "Succeeded", mr4.Status.Phase)

		var pr tektonv1.PipelineRun
		getErr := k8sClient.Get(context.Background(), nsName(ns, "mr-1-sandbox"), &pr)
		require.Error(t, getErr, "webhook.StageRunner must never create a real PipelineRun")

		requireServiceAccountExists(t, ns)
		requireServiceAccountExists(t, "staging")
	})
}

// fakeHTTPCaller is a test seam for webhookcore.Caller that returns
// scripted results in order, one per Call invocation, within the
// controller test package's envtest environment.
type fakeHTTPCaller struct {
	t       *testing.T
	results []webhookcore.CallResult
	errs    []error
	pos     int
}

func newFakeHTTPCaller(t *testing.T) *fakeHTTPCaller {
	return &fakeHTTPCaller{t: t}
}

func (f *fakeHTTPCaller) scriptResult(r webhookcore.CallResult) {
	f.results = append(f.results, r)
}

func (f *fakeHTTPCaller) scriptError(err error) {
	f.errs = append(f.errs, err)
}

func (f *fakeHTTPCaller) Call(_ context.Context, _ webhookcore.CallConfig) (webhookcore.CallResult, error) {
	if f.pos < len(f.errs) {
		err := f.errs[f.pos]
		f.pos++
		return webhookcore.CallResult{}, err
	}
	if f.pos >= len(f.results) {
		f.t.Fatalf("[fakeHTTPCaller] no result scripted for call index %d", f.pos)
	}
	r := f.results[f.pos]
	f.pos++
	return r, nil
}
