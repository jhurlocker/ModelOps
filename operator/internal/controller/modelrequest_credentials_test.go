package controller

// Phase 8 (docs/PHASE_LOG.md / docs/REVIEW_RESPONSE_PLAN.md) decisive
// evidence test: seeds every credential-bearing Secret with
// distinctive, unmistakable "canary" values that could only end up in a
// PipelineRun's spec.params via the exact leak this phase closes (raw
// credential values flowing through stagecommon.Secrets/StageSpec.Params
// instead of Secret name references), then asserts those values never
// appear anywhere in either the sandbox or promotion PipelineRun's
// spec.params -- by exact match AND substring containment -- while the
// corresponding *-secret-name params ARE present with the expected
// Secret names. This is not a restructuring check (the unit tests in
// stagecommon/sandbox/promotion already cover that); it's proof against
// the actual object Tekton would execute against.

import (
	"context"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"

	"github.com/stretchr/testify/require"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
)

// canaryCredentialValues are long, distinctive, random-looking strings
// that could never collide with a legitimate default or an unrelated
// param value -- if any of these ever shows up in a PipelineRun's
// spec.params, it can only have gotten there via the leak this phase
// closes.
var canaryCredentialValues = []string{
	"AKIA-CANARY-SCAN-9f3e21",
	"CANARY-SCAN-SECRET-b71a9f",
	"AKIA-CANARY-RESULT-2b6c88",
	"CANARY-RESULT-SECRET-77fe40",
	"CANARY-EVALHUB-TOKEN-b71a9f44",
	"CANARY-HF-TOKEN-44d1c02a",
}

// assertNoCredentialValuesInParams fails the test if any of
// canaryCredentialValues appears in params, either as an exact param
// value (the direct leak) or as a substring of a param value (a
// defensive check against the value being concatenated/embedded into
// some other string).
func assertNoCredentialValuesInParams(t *testing.T, prName string, params tektonv1.Params) {
	t.Helper()
	for _, p := range params {
		for _, canary := range canaryCredentialValues {
			require.NotEqual(t, canary, p.Value.StringVal,
				"PipelineRun %q param %q carries a raw credential value -- the Phase 8 leak is NOT closed", prName, p.Name)
			require.NotContains(t, p.Value.StringVal, canary,
				"PipelineRun %q param %q contains a raw credential value as a substring -- the Phase 8 leak is NOT closed", prName, p.Name)
		}
	}
}

func TestModelRequest_SandboxAndPromotionPipelineRuns_NeverContainRawCredentialValues(t *testing.T) {
	ns := newTestNamespace(t)
	ensureNamespace(t, "staging")
	newPlatformConfig(t, ns, "cfg-1", modelopsv1alpha1.PlatformConfigSpec{
		EvalHubURL: "http://platform-default-evalhub.example.com", // must never win over the Secret's own url
	})
	newProfile(t, ns, "profile-1", defaultProfileSpec("cfg-1"))

	newSecret(t, ns, "canary-scan-s3", map[string]string{
		"endpoint":        "http://scan-s3.example.com:9000",
		"accessKeyId":     "AKIA-CANARY-SCAN-9f3e21",
		"secretAccessKey": "CANARY-SCAN-SECRET-b71a9f",
	})
	newSecret(t, ns, "canary-result-s3", map[string]string{
		"endpoint":        "http://result-s3.example.com:9000",
		"accessKeyId":     "AKIA-CANARY-RESULT-2b6c88",
		"secretAccessKey": "CANARY-RESULT-SECRET-77fe40",
	})
	newSecret(t, ns, "canary-evalhub", map[string]string{
		"url":   "https://canary-evalhub.example.com",
		"token": "CANARY-EVALHUB-TOKEN-b71a9f44",
	})
	newSecret(t, ns, "canary-hf", map[string]string{
		"token": "CANARY-HF-TOKEN-44d1c02a",
	})

	newModelRequest(t, ns, "mr-1", "profile-1", func(mr *modelopsv1alpha1.ModelRequest) {
		mr.Spec.ScanS3SecretName = "canary-scan-s3"
		mr.Spec.ResultS3SecretName = "canary-result-s3"
		mr.Spec.EvalHubSecretName = "canary-evalhub"
		mr.Spec.HuggingFaceSecretName = "canary-hf"
	})
	setupSucceededCapacityPlan(t, ns, "mr-1")

	// --- Sandbox PipelineRun ---
	mr, _, err := reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "sandboxRunning", mr.Status.Phase)

	var sandboxPR tektonv1.PipelineRun
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "mr-1-sandbox"), &sandboxPR))

	assertNoCredentialValuesInParams(t, "mr-1-sandbox", sandboxPR.Spec.Params)

	scanSecretName, ok := findParam(sandboxPR.Spec.Params, "scan-s3-secret-name")
	require.True(t, ok, "scan-s3-secret-name must be present")
	require.Equal(t, "canary-scan-s3", scanSecretName)

	resultSecretName, ok := findParam(sandboxPR.Spec.Params, "result-s3-secret-name")
	require.True(t, ok, "result-s3-secret-name must be present")
	require.Equal(t, "canary-result-s3", resultSecretName)

	evalhubSecretName, ok := findParam(sandboxPR.Spec.Params, "evalhub-secret-name")
	require.True(t, ok, "evalhub-secret-name must be present")
	require.Equal(t, "canary-evalhub", evalhubSecretName)

	evalhubURL, ok := findParam(sandboxPR.Spec.Params, "evalhub-url")
	require.True(t, ok)
	require.Equal(t, "https://canary-evalhub.example.com", evalhubURL, "the Secret's own url must win over PlatformConfig's default")

	hfSecretName, ok := findParam(sandboxPR.Spec.Params, "huggingface-secret-name")
	require.True(t, ok, "huggingface-secret-name must be present")
	require.Equal(t, "canary-hf", hfSecretName)

	// --- Promotion PipelineRun ---
	setPipelineRunCondition(t, ns, "mr-1-sandbox", corev1.ConditionTrue, "All Tasks Completed")
	mr, _, err = reconcileModelRequest(t, ns, "mr-1")
	require.NoError(t, err)
	require.Equal(t, "promotionRunning", mr.Status.Phase)

	var promoPR tektonv1.PipelineRun
	require.NoError(t, k8sClient.Get(context.Background(), nsName(ns, "mr-1-promotion-staging"), &promoPR))

	assertNoCredentialValuesInParams(t, "mr-1-promotion-staging", promoPR.Spec.Params)

	promoResultSecretName, ok := findParam(promoPR.Spec.Params, "result-s3-secret-name")
	require.True(t, ok, "result-s3-secret-name must be present")
	require.Equal(t, "canary-result-s3", promoResultSecretName)

	promoEvalhubSecretName, ok := findParam(promoPR.Spec.Params, "evalhub-secret-name")
	require.True(t, ok, "evalhub-secret-name must be present")
	require.Equal(t, "canary-evalhub", promoEvalhubSecretName)

	promoHFSecretName, ok := findParam(promoPR.Spec.Params, "huggingface-secret-name")
	require.True(t, ok, "huggingface-secret-name must be present")
	require.Equal(t, "canary-hf", promoHFSecretName)

	// Promotion never touches scan-s3 at all (sandbox-only param).
	_, hasScanS3SecretName := findParam(promoPR.Spec.Params, "scan-s3-secret-name")
	require.False(t, hasScanS3SecretName)
}
