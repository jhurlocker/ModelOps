package tekton

// Static safety net for the Zot TLS transition (see docs/PHASE_LOG.md's Zot
// TLS phase / the EvalHub CA fix). This change moves the in-cluster
// build-modelcar push and compliance-artifact-scan inspect OFF insecure
// --tls-verify=false and onto verified HTTPS against Zot's serving cert. These
// tests read the committed YAML (not a copy) to pin:
//
//   - build-modelcar-task.yaml has ZERO --tls-verify=false occurrences (the
//     explicit "no insecure-registry exceptions" target);
//   - both build-modelcar and compliance-artifact-scan install the service-CA
//     (preferring the pod serviceaccount bundle, falling back to the
//     zot-service-ca inject-cabundle ConfigMap) via update-ca-trust;
//   - the Zot Service requests a serving cert, Zot's config serves TLS using
//     it, and its probes stay TLS-agnostic (tcpSocket).

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func zotGitopsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	dir, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile),
		"..", "..", "..", "..", // internal/stages/tekton -> repo root
		"gitops", "components", "zot"))
	require.NoError(t, err)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("zot gitops directory not found at %s: %v", dir, err)
	}
	return dir
}

func readZotGitopsFile(t *testing.T, filename string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(zotGitopsDir(t), filename))
	require.NoError(t, err)
	return string(contents)
}

func TestPipelineYAML_BuildModelcar_NoInsecureTLSVerifyFlags(t *testing.T) {
	text := readPipelineYAMLFile(t, buildModelcarTaskFile)
	require.NotContains(t, text, "--tls-verify=false",
		"build-modelcar must no longer disable TLS verification anywhere; the push must verify Zot's serving cert")
}

func TestPipelineYAML_BuildModelcar_InstallsServiceCA(t *testing.T) {
	text := readPipelineYAMLFile(t, buildModelcarTaskFile)
	require.Contains(t, text, "update-ca-trust",
		"build-modelcar must install the service CA into the trust store")
	require.Contains(t, text, "/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt",
		"primary trust source must be the pod's own serviceaccount CA bundle")
	require.Contains(t, text, "/etc/zot-ca/service-ca.crt",
		"fallback trust source must be the inject-cabundle ConfigMap path")
	require.Contains(t, text, "zot-service-ca",
		"build-modelcar must mount the zot-service-ca fallback ConfigMap")
}

func TestPipelineYAML_ComplianceScan_InstallsServiceCA(t *testing.T) {
	text := readPipelineYAMLFile(t, "compliance-artifact-scan-task.yaml")
	require.Contains(t, text, "update-ca-trust",
		"compliance-artifact-scan must install the service CA before its skopeo inspect")
	require.Contains(t, text, "/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt",
		"primary trust source must be the pod's own serviceaccount CA bundle")
	require.Contains(t, text, "/etc/zot-ca/service-ca.crt",
		"fallback trust source must be the inject-cabundle ConfigMap path")
}

func TestZotGitops_ServiceRequestsServingCert(t *testing.T) {
	text := readZotGitopsFile(t, "service.yaml")
	require.Contains(t, text, "service.beta.openshift.io/serving-cert-secret-name: zot-serving-cert",
		"the Zot Service must request a service-serving certificate")
}

func TestZotGitops_ConfigServesTLS(t *testing.T) {
	text := readZotGitopsFile(t, "configmap.yaml")
	require.Contains(t, text, `"tls": {`,
		"Zot's config.json must enable TLS under http")
	require.Contains(t, text, "/etc/zot/certs/tls.crt",
		"Zot's TLS cert must come from the mounted serving-cert Secret")
	require.Contains(t, text, "/etc/zot/certs/tls.key",
		"Zot's TLS key must come from the mounted serving-cert Secret")
}

func TestZotGitops_DeploymentMountsCertAndUsesTCPProbes(t *testing.T) {
	text := readZotGitopsFile(t, "deployment.yaml")
	require.Contains(t, text, "zot-serving-cert",
		"the Zot pod must mount the zot-serving-cert Secret")
	require.Contains(t, text, "tcpSocket",
		"Zot probes must be tcpSocket (TLS-agnostic), not httpGet over a now-HTTPS port")
	require.NotContains(t, text, "httpGet",
		"Zot probes must not use httpGet against the now-HTTPS port")
}
