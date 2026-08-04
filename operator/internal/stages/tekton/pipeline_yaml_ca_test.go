package tekton

// Phase 9 (SSL trust fix for EvalHub-tenant endpoints) static safety
// net: security-scan-task.yaml and guidellm-benchmark-task.yaml both
// talk to the EvalHub API over HTTPS.  The TrustyAI Operator provisions
// a service CA ConfigMap (default-evalhub-service-ca, key
// service-ca.crt) in any namespace labeled
// evalhub.trustyai.opendatahub.io/tenant.  Before this phase, both
// Tasks set REQUESTS_CA_BUNDLE to the wrong path
// (/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt,
// never populated by anything), causing [SSL: CERTIFICATE_VERIFY_FAILED]
// on every EvalHub API call.  The fix mounts the real ConfigMap as a
// volume at /etc/evalhub-ca and points REQUESTS_CA_BUNDLE at
// /etc/evalhub-ca/service-ca.crt.
//
// These tests read the actual committed YAML (not a copy) to guard
// against one of the Task files drifting back to a per-Task guessed
// CA path or an arbitrary non-standard path that disagrees with the
// operator-provisioned ConfigMap.  Also asserts that Tasks which do
// NOT talk to EvalHub (gpu-advisor, which calls an external endpoint
// with public CAs) do not accidentally acquire the EvalHub CA mount
// or the old broken REQUESTS_CA_BUNDLE path.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	evalHubCAVolumeName    = "evalhub-service-ca"
	evalHubCAMountPath     = "/etc/evalhub-ca"
	evalHubCAConfigMapName = "default-evalhub-service-ca"
	evalHubCABundleEnvVal  = "/etc/evalhub-ca/service-ca.crt"
	oldBrokenCABundlePath  = "/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt"
)

// evalHubTaskFiles are the Task YAML files that talk to an
// EvalHub-tenant-scoped endpoint over HTTPS and therefore must mount
// the operator-provisioned default-evalhub-service-ca ConfigMap.
var evalHubTaskFiles = []string{
	"security-scan-task.yaml",
	"guidellm-benchmark-task.yaml",
	"promote-and-benchmark-task.yaml",
}

func TestPipelineYAML_EvalHubTasks_MountCAConfigMapVolume(t *testing.T) {
	dir := pipelineYAMLDir(t)
	for _, filename := range evalHubTaskFiles {
		contents, err := os.ReadFile(filepath.Join(dir, filename))
		require.NoError(t, err)
		text := string(contents)

		require.Contains(t, text, "name: "+evalHubCAVolumeName,
			"%s must declare a volume named %q", filename, evalHubCAVolumeName)
		require.Contains(t, text, "configMap",
			"%s volume %q must be a configMap", filename, evalHubCAVolumeName)
		require.Contains(t, text, "name: "+evalHubCAConfigMapName,
			"%s volume %q must reference ConfigMap %q",
			filename, evalHubCAVolumeName, evalHubCAConfigMapName)
	}
}

func TestPipelineYAML_EvalHubTasks_MountVolumeAtExpectedPath(t *testing.T) {
	dir := pipelineYAMLDir(t)
	for _, filename := range evalHubTaskFiles {
		contents, err := os.ReadFile(filepath.Join(dir, filename))
		require.NoError(t, err)
		text := string(contents)

		require.Contains(t, text, "mountPath: "+evalHubCAMountPath,
			"%s step must mount %q at %q",
			filename, evalHubCAVolumeName, evalHubCAMountPath)
	}
}

func TestPipelineYAML_EvalHubTasks_UseCorrectCABundle(t *testing.T) {
	dir := pipelineYAMLDir(t)
	for _, filename := range evalHubTaskFiles {
		contents, err := os.ReadFile(filepath.Join(dir, filename))
		require.NoError(t, err)
		text := string(contents)

		require.Contains(t, text, evalHubCABundleEnvVal,
			"%s must set a CA bundle env var to %q (REQUESTS_CA_BUNDLE for requests-based scripts, SSL_CERT_FILE for urllib-based scripts)",
			filename, evalHubCABundleEnvVal)
		require.NotContains(t, text, oldBrokenCABundlePath,
			"%s must NOT use the old broken CA bundle path %q",
			filename, oldBrokenCABundlePath)
	}
}

// nonEvalHubTaskFiles are Tasks that do NOT talk to EvalHub and
// therefore must NOT have the old broken REQUESTS_CA_BUNDLE path.
var nonEvalHubTaskFiles = []string{
	"gpu-advisor-task.yaml",
}

func TestPipelineYAML_NonEvalHubTasks_NoBrokenCABundlePath(t *testing.T) {
	dir := pipelineYAMLDir(t)
	for _, filename := range nonEvalHubTaskFiles {
		contents, err := os.ReadFile(filepath.Join(dir, filename))
		require.NoError(t, err)
		text := string(contents)

		require.NotContains(t, text, oldBrokenCABundlePath,
			"%s does not talk to EvalHub; must not carry the old broken REQUESTS_CA_BUNDLE value %q",
			filename, oldBrokenCABundlePath)
	}
}

func TestPipelineYAML_AllEvaluHubCallingFilesListed(t *testing.T) {
	dir := pipelineYAMLDir(t)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), "-task.yaml") {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, err)
		text := string(contents)

		if !strings.Contains(text, "evalhub-url") {
			continue
		}

		found := false
		for _, listed := range evalHubTaskFiles {
			if entry.Name() == listed {
				found = true
				break
			}
		}
		require.True(t, found,
			"%s has an evalhub-url param (talks to EvalHub over HTTPS) but is NOT listed in evalHubTaskFiles -- add it to the list so the CA ConfigMap consistency assertions cover it",
			entry.Name())
	}
}
