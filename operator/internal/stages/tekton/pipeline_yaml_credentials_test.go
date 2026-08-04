package tekton

// Phase 8 (docs/PHASE_LOG.md / docs/REVIEW_RESPONSE_PLAN.md) static
// safety net: the Go-side fix (Secrets/StageSpec carrying Secret NAMES,
// never values) is only half of this phase's actual change -- the
// underlying Tekton Task/Pipeline YAML had to change in the same
// commit, because the Task/Pipeline params being removed carried
// hardcoded credential-shaped defaults (discovered during this phase's
// investigation, see docs/PHASE_LOG.md) that would otherwise silently
// take over the moment Go stopped supplying a value. These tests read
// the actual committed YAML (not a copy) to guard against either
// regressing back to a value-carrying param or reintroducing a
// hardcoded credential default, in either of these files, ever again.
//
// Scoped deliberately to the exact 8 files this phase's Task/Pipeline
// changes touch -- not a blanket scan of every YAML file in the
// directory. model-intake-pipeline.yaml/model-intake-pipelinerun.yaml
// (a dead/orphaned Pipeline and a static sample manifest, neither
// referenced by any live WorkflowRef/ProviderConfig -- see
// docs/PHASE_LOG.md Phase 8) still contain similar patterns but are out
// of this phase's scope, same as the already-identified dead app.py
// file in docs/REVIEW_RESPONSE_PLAN.md Phase 10 -- flagged as a known
// follow-up, not silently ignored.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// pipelineYAMLDir locates model_onboarding_pipeline/model-intake-pipeline/pipeline
// relative to this test file's own source location (stable regardless
// of the working directory `go test` is invoked from).
func pipelineYAMLDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller(0) must resolve this test file's own path")
	dir, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile),
		"..", "..", "..", "..", // internal/stages/tekton -> internal -> operator -> repo root
		"model_onboarding_pipeline", "model-intake-pipeline", "pipeline"))
	require.NoError(t, err)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("pipeline YAML directory not found at %s: %v", dir, err)
	}
	return dir
}

// affectedPipelineAndTaskFiles are the 2 Pipelines + 6 Tasks this
// phase's YAML changes touch.
var affectedPipelineAndTaskFiles = []string{
	"sandbox-pipeline.yaml",
	"promotion-pipeline.yaml",
	"compliance-artifact-scan-task.yaml",
	"security-scan-task.yaml",
	"deploy-model-task.yaml",
	"promote-and-benchmark-task.yaml",
	"upload-guidellm-results-task.yaml",
	"upload-lm-eval-results-task.yaml",
}

// knownBadCredentialDefaults are literal default values discovered
// during this phase's investigation, baked directly into Task/Pipeline
// param declarations -- the same "never hardcode a real credential"
// defect Phase 1 already fixed once in Go, found again at the
// Tekton-YAML layer.
var knownBadCredentialDefaults = []string{
	`default: "minioadmin"`,
	`default: "minio"`,
}

func TestPipelineYAML_AffectedFiles_NoHardcodedCredentialDefaults(t *testing.T) {
	dir := pipelineYAMLDir(t)
	for _, filename := range affectedPipelineAndTaskFiles {
		contents, err := os.ReadFile(filepath.Join(dir, filename))
		require.NoError(t, err)
		for _, bad := range knownBadCredentialDefaults {
			require.NotContains(t, string(contents), bad,
				"%s contains a hardcoded credential default (%q) -- Secret values must never be baked into a Task/Pipeline param default", filename, bad)
		}
	}
}

// removedCredentialValueParamDeclarations are the old value-carrying
// param names this phase's Task/Pipeline changes replace with
// *-secret-name references consumed via secretKeyRef. Their
// declaration (`name: <param>`) must be fully absent from every
// affected file after this phase -- not just unused.
var removedCredentialValueParamDeclarations = []string{
	"name: s3-access-key-id",
	"name: s3-secret-access-key",
	"name: scan-s3-access-key-id",
	"name: scan-s3-secret-access-key",
	"name: evalhub-token",
	"name: huggingface-token",
}

func TestPipelineYAML_AffectedFiles_CredentialValueParamsFullyRemoved(t *testing.T) {
	dir := pipelineYAMLDir(t)
	for _, filename := range affectedPipelineAndTaskFiles {
		contents, err := os.ReadFile(filepath.Join(dir, filename))
		require.NoError(t, err)
		for _, decl := range removedCredentialValueParamDeclarations {
			require.NotContains(t, string(contents), decl,
				"%s still declares %q -- credentials must be referenced by Secret name (secretKeyRef), not carried as a param value", filename, decl)
		}
	}
}

// taskFilesExpectingSecretKeyRef is the subset of affectedPipelineAndTaskFiles
// that actually consume a credential directly in a step (the two
// Pipeline files only forward secret-name params down to Tasks -- they
// never mount an env var themselves).
var taskFilesExpectingSecretKeyRef = []string{
	"compliance-artifact-scan-task.yaml",
	"security-scan-task.yaml",
	"deploy-model-task.yaml",
	"promote-and-benchmark-task.yaml",
	"upload-guidellm-results-task.yaml",
	"upload-lm-eval-results-task.yaml",
}

func TestPipelineYAML_AffectedTaskFiles_ConsumeCredentialsViaSecretKeyRef(t *testing.T) {
	dir := pipelineYAMLDir(t)
	for _, filename := range taskFilesExpectingSecretKeyRef {
		contents, err := os.ReadFile(filepath.Join(dir, filename))
		require.NoError(t, err)
		require.Contains(t, string(contents), "secretKeyRef",
			"%s must consume its credential(s) via secretKeyRef, not a plain param value", filename)
	}
}

// TestPipelineYAML_AffectedFiles_SecretNameParamsPresent is a light
// structural check that the replacement *-secret-name params actually
// exist somewhere in the affected files (paired with the "removed"
// check above so a future edit can't just delete the old param without
// adding its replacement).
func TestPipelineYAML_AffectedFiles_SecretNameParamsPresent(t *testing.T) {
	dir := pipelineYAMLDir(t)
	allContents := ""
	for _, filename := range affectedPipelineAndTaskFiles {
		contents, err := os.ReadFile(filepath.Join(dir, filename))
		require.NoError(t, err)
		allContents += string(contents)
	}
	for _, expected := range []string{"s3-secret-name", "scan-s3-secret-name", "result-s3-secret-name", "evalhub-secret-name", "huggingface-secret-name"} {
		require.True(t, strings.Contains(allContents, expected),
			"expected to find %q declared/referenced somewhere across the affected Task/Pipeline files", expected)
	}
}
