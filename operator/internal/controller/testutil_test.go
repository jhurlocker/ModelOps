package controller

// Shared fixtures for characterization tests in this package. Every test
// creates its own uniquely-named Namespace (envtest runs one shared
// kube-apiserver for the whole package -- see suite_test.go) so tests can
// run without interfering with each other; there is no per-test cleanup
// of child objects because the whole environment is torn down once at the
// end of the package's TestMain.

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"
	"github.com/jhurlocker/modelops-operator/internal/stages/capacityplanning"
	"github.com/jhurlocker/modelops-operator/internal/stages/promotion"
	"github.com/jhurlocker/modelops-operator/internal/stages/sandbox"
	tektonstage "github.com/jhurlocker/modelops-operator/internal/stages/tekton"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// newStageHandlers returns the real StageHandlers registry (keyed by
// ProfileStageSpec.Name, matching testDefaultStages' "capacity"/
// "sandbox"/"promotion" names) every test in this package uses -- these
// build *what* to run and never touch Tekton/a CapacityPlan object
// themselves, so it's safe to use the real handlers even in tests that
// fake the StageRunner side (e.g. modelrequest_stagerunner_test.go).
func newStageHandlers() map[string]stagecommon.StageHandler {
	return map[string]stagecommon.StageHandler{
		"capacity":  capacityplanning.Handler{},
		"sandbox":   sandbox.Handler{},
		"promotion": promotion.Handler{},
	}
}

// newStageRunners returns the StageRunners registry (keyed by
// ProfileStageSpec.Kind): "CapacityPlan" always maps to the real
// capacityplanning.StageRunner (it never touches Tekton, so there's no
// reason to fake it), "PipelineRun" maps to pipelineRunner -- pass nil
// for the real tekton.StageRunner, or a stagecommon.FakeStageRunner/
// noop.StageRunner to swap it out.
func newStageRunners(c client.Client, scheme *runtime.Scheme, pipelineRunner stagecommon.StageRunner) map[string]stagecommon.StageRunner {
	if pipelineRunner == nil {
		pipelineRunner = &tektonstage.StageRunner{Client: c, Scheme: scheme}
	}
	return map[string]stagecommon.StageRunner{
		"CapacityPlan": &capacityplanning.StageRunner{Client: c, Scheme: scheme},
		"PipelineRun":  pipelineRunner,
	}
}

func nsName(namespace, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: namespace, Name: name}
}

func testRuntimeScheme() *runtime.Scheme {
	return scheme.Scheme
}

func randSuffix() string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

// newTestNamespace creates a real Namespace via the envtest API server and
// returns its name. Each test should call this once and scope all of its
// objects to the returned namespace.
func newTestNamespace(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("t-%s", randSuffix())
	ensureNamespace(t, name)
	return name
}

// ensureNamespace creates a Namespace if it doesn't already exist,
// tolerating AlreadyExists. This is needed because
// ensurePromotionNamespaceRBAC creates a ServiceAccount inside the
// promotion target namespace, and envtest's real API server (unlike a
// fake client) enforces that the namespace actually exists first.
func ensureNamespace(t *testing.T, name string) {
	t.Helper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := k8sClient.Create(context.Background(), ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("failed to create namespace %s: %v", name, err)
	}
}

// ensureNamespaceWithLabels creates a Namespace with the given labels,
// or updates an existing namespace to have those labels if it already
// exists. Used by tests exercising
// StageNamespaceSetup.AllowedNamespaceSelector (Phase 9), where the
// target namespace's labels must match the selector for the stage to
// proceed.
func ensureNamespaceWithLabels(t *testing.T, name string, labels map[string]string) {
	t.Helper()
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}
	if err := k8sClient.Create(context.Background(), ns); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			t.Fatalf("failed to create namespace %s: %v", name, err)
		}
		// Namespace already exists -- update its labels.
		_ = k8sClient.Get(context.Background(), types.NamespacedName{Name: name}, ns)
		ns.Labels = labels
		if err := k8sClient.Update(context.Background(), ns); err != nil {
			t.Fatalf("failed to update namespace %s labels: %v", name, err)
		}
	}
}

// testDefaultStages reproduces, for test fixtures only, the exact
// 3-stage sequence internal/controller's now-removed defaultStages()
// used to synthesize automatically for any profile with an empty
// Spec.Stages. That implicit synthesis was removed in Phase 7 of
// REFACTOR_PLAN.md once the live standard-generative-onboarding profile
// migrated to declaring Stages explicitly (see
// gitops/components/runtime-config/lifecycleprofile.yaml and
// docs/PHASE_LOG.md's Phase 7 entry) -- every profile must now set
// Spec.Stages itself. This helper keeps that declaration in one place
// instead of duplicating the same 3-entry literal across every
// characterization test fixture in this package that exercises the full
// capacity -> sandbox -> promotion flow.
//
// providerConfigRef is threaded onto the sandbox/promotion stages
// exactly the way the removed defaultStages() threaded
// ModelLifecycleProfileSpec.ProviderConfigRef onto them -- pass nil for
// the deprecated Workflow.PipelineRef/PromotionPipelineRef fallback
// path.
func testDefaultStages(providerConfigRef *modelopsv1alpha1.ProviderConfigRef) []modelopsv1alpha1.ProfileStageSpec {
	return []modelopsv1alpha1.ProfileStageSpec{
		{Name: "capacity", Kind: "CapacityPlan"},
		{
			Name:              "sandbox",
			Kind:              "PipelineRun",
			ProviderConfigRef: providerConfigRef,
			NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{
				EnsureRBAC: true,
				Labels:     map[string]string{"evalhub.trustyai.opendatahub.io/tenant": ""},
			},
		},
		{
			Name:              "promotion",
			Kind:              "PipelineRun",
			ProviderConfigRef: providerConfigRef,
			PerNamespace:      true,
			NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{
				EnsureRBAC: true,
				Labels: map[string]string{
					"opendatahub.io/generated-namespace": "true",
					"maas.opendatahub.io/gateway-access": "true",
					"opendatahub.io/dashboard":           "true",
				},
			},
		},
	}
}

// defaultProfileSpec returns a minimal, valid ModelLifecycleProfileSpec
// pointing at the given PlatformConfig name, matching the shape the real
// UI/controller expects (Workflow.Engine/PipelineRef required), with the
// standard 3-stage Spec.Stages declared explicitly (see
// testDefaultStages).
func defaultProfileSpec(platformConfigName string) modelopsv1alpha1.ModelLifecycleProfileSpec {
	return modelopsv1alpha1.ModelLifecycleProfileSpec{
		Workflow: modelopsv1alpha1.WorkflowRef{
			Engine:      "tekton",
			PipelineRef: "model-intake-sandbox",
		},
		PlatformConfigRef: platformConfigName,
		Stages:            testDefaultStages(nil),
	}
}

func newProfile(t *testing.T, ns, name string, spec modelopsv1alpha1.ModelLifecycleProfileSpec) *modelopsv1alpha1.ModelLifecycleProfile {
	t.Helper()
	p := &modelopsv1alpha1.ModelLifecycleProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       spec,
	}
	if err := k8sClient.Create(context.Background(), p); err != nil {
		t.Fatalf("failed to create ModelLifecycleProfile %s/%s: %v", ns, name, err)
	}
	return p
}

func newPlatformConfig(t *testing.T, ns, name string, spec modelopsv1alpha1.PlatformConfigSpec) *modelopsv1alpha1.PlatformConfig {
	t.Helper()
	c := &modelopsv1alpha1.PlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       spec,
	}
	if err := k8sClient.Create(context.Background(), c); err != nil {
		t.Fatalf("failed to create PlatformConfig %s/%s: %v", ns, name, err)
	}
	return c
}

// newS3CredentialsSecret creates a Secret shaped the way resolveSecrets
// expects a ScanS3SecretName/ResultS3SecretName reference to look
// (endpoint/accessKeyId/secretAccessKey keys), so tests can exercise the
// real secretRef-resolution path instead of any hardcoded fallback.
func newS3CredentialsSecret(t *testing.T, ns, name string) *corev1.Secret {
	t.Helper()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Data: map[string][]byte{
			"endpoint":        []byte("http://test-minio.example.svc.cluster.local:9000"),
			"accessKeyId":     []byte("test-access-key"),
			"secretAccessKey": []byte("test-secret-key"),
		},
	}
	if err := k8sClient.Create(context.Background(), secret); err != nil {
		t.Fatalf("failed to create Secret %s/%s: %v", ns, name, err)
	}
	return secret
}

// newSecret creates an arbitrary Secret for tests exercising a shape
// newS3CredentialsSecret doesn't cover (e.g. the EvalHub Secret's
// url/token keys, or a HuggingFace token Secret).
func newSecret(t *testing.T, ns, name string, data map[string]string) *corev1.Secret {
	t.Helper()
	byteData := map[string][]byte{}
	for k, v := range data {
		byteData[k] = []byte(v)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Data:       byteData,
	}
	if err := k8sClient.Create(context.Background(), secret); err != nil {
		t.Fatalf("failed to create Secret %s/%s: %v", ns, name, err)
	}
	return secret
}

// newPipelineServiceAccount creates the "pipeline" ServiceAccount
// resolveSecrets' EvalHub-token-generation fallback (generateServiceAccountToken)
// requires to exist before a TokenRequest against it can succeed.
// Normally provisioned by ensurePromotionNamespaceRBAC as part of a
// stage's NamespaceSetup during a full Reconcile; tests that call
// r.resolveSecrets directly (bypassing Reconcile) must create it
// themselves.
func newPipelineServiceAccount(t *testing.T, ns string) {
	t.Helper()
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "pipeline", Namespace: ns}}
	if err := k8sClient.Create(context.Background(), sa); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("failed to create pipeline ServiceAccount in %s: %v", ns, err)
	}
}

// newModelRequest creates a ModelRequest referencing profileName in ns,
// with a minimal valid model identity, and returns it (with server-set
// fields such as UID/ResourceVersion populated).
//
// It also provisions a real S3-credentials Secret and points
// spec.scanS3SecretName/spec.resultS3SecretName at it by default, so
// every test exercises the real secretRef-resolution path in
// resolveSecrets rather than relying on a hardcoded credential fallback
// (removed in Phase 1). Tests that specifically want to exercise the
// "no secret configured" failure path should clear these fields via
// mutate.
func newModelRequest(t *testing.T, ns, name, profileName string, mutate func(*modelopsv1alpha1.ModelRequest)) *modelopsv1alpha1.ModelRequest {
	t.Helper()
	secretName := name + "-s3-credentials"
	newS3CredentialsSecret(t, ns, secretName)
	mr := &modelopsv1alpha1.ModelRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: modelopsv1alpha1.ModelRequestSpec{
			Model: modelopsv1alpha1.ModelIdentity{
				SourceType: "huggingface",
				URI:        "ibm-granite/granite-3.0-2b-instruct",
				Name:       name,
				Version:    "v1",
			},
			LifecycleProfile:   profileName,
			RequestedBy:        "test-suite",
			ScanS3SecretName:   secretName,
			ResultS3SecretName: secretName,
		},
	}
	if mutate != nil {
		mutate(mr)
	}
	if err := k8sClient.Create(context.Background(), mr); err != nil {
		t.Fatalf("failed to create ModelRequest %s/%s: %v", ns, name, err)
	}
	return mr
}

func getModelRequest(t *testing.T, ns, name string) *modelopsv1alpha1.ModelRequest {
	t.Helper()
	var mr modelopsv1alpha1.ModelRequest
	if err := k8sClient.Get(context.Background(), nsName(ns, name), &mr); err != nil {
		t.Fatalf("failed to get ModelRequest %s/%s: %v", ns, name, err)
	}
	return &mr
}
