package tekton

// Static safety net for cluster-specific hostnames (the "MinIO endpoint
// fix" / hardcoded-hostname audit): no committed manifest under gitops/ or
// operator/config/samples/ may carry a literal cluster-specific domain
// (*.opentlc.com or any OpenShift wildcard "*.apps.<cluster-domain>"
// hostname). The point is structural, not cosmetic -- the concrete failure
// mode this guards against is "a new component is added and an existing
// cluster hostname is copied in as a starting point", which silently breaks
// the moment the manifests are deployed to a differently-named cluster.
//
// There is exactly one sanctioned exception: gitops/components/maas/
// cluster-config.yaml, the single ConfigMap from which the MaaS Gateway
// listener hostnames and Route host are derived via kustomize replacement
// (see that file and gitops/components/maas/kustomization.yaml). Every other
// manifest must be cluster-portable.
//
// Generic placeholders used in sample/reference comments (e.g. *.example.com,
// *.your-cluster.com) are tolerated -- they describe the naming pattern, not
// a concrete cluster.

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// repoRoot locates the repository root relative to this test file's own
// source location (stable regardless of the working directory `go test` is
// invoked from), the same trick as pipelineYAMLDir/zotGitopsDir.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller(0) must resolve this test file's own path")
	dir, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile),
		"..", "..", "..", "..", // internal/stages/tekton -> repo root
	))
	require.NoError(t, err)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("repository root not found at %s: %v", dir, err)
	}
	return dir
}

// scannedManifestRoots are the directories (relative to the repository root)
// whose committed manifests must be free of cluster-specific hostnames.
var scannedManifestRoots = []string{"gitops", filepath.Join("operator", "config", "samples")}

// clusterConfigAllowlist is the single sanctioned repository-relative path
// permitted to hold a concrete cluster-specific hostname. It is the source
// consumed by the MaaS kustomize replacements; everything else is held to the
// "no cluster-specific literal" rule.
var clusterConfigAllowlist = []string{
	filepath.Join("gitops", "components", "maas", "cluster-config.yaml"),
}

// placeholderDomainSuffixes are generic domains used only to describe a
// naming PATTERN in sample/reference comments -- not concrete cluster state.
var placeholderDomainSuffixes = []string{
	".example.com",
	".example.org",
	".example.net",
	".your-cluster.com",
	".cluster.local",
}

// clusterWildcardDomain matches an OpenShift wildcard-ingress hostname of the
// form <anything>.apps.<at-least-two-labels>, e.g.
// minio-console-modelops-storage.apps.ocp.mb9bl.sandbox1545.opentlc.com.
var clusterWildcardDomain = regexp.MustCompile(`\.apps\.[a-z0-9][a-z0-9-]*(\.[a-z0-9-]+)+`)

// collectManifestFiles walks the scanned roots and returns every *.yaml /
// *.yml file as a repository-relative slash-separated path.
func collectManifestFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	for _, rel := range scannedManifestRoots {
		base := filepath.Join(root, rel)
		require.DirExists(t, base, "scanned manifest root must exist: %s", base)
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".yaml" && ext != ".yml" {
				return nil
			}
			relPath, err := filepath.Rel(root, path)
			require.NoError(t, err)
			files = append(files, filepath.ToSlash(relPath))
			return nil
		})
		require.NoError(t, err)
	}
	return files
}

func isAllowlisted(path string) bool {
	for _, allowed := range clusterConfigAllowlist {
		if path == allowed {
			return true
		}
	}
	return false
}

func isPlaceholderDomain(domain string) bool {
	domain = strings.ToLower(domain)
	for _, suffix := range placeholderDomainSuffixes {
		if strings.HasSuffix(domain, suffix) {
			return true
		}
	}
	return false
}

// TestClusterDomain_NoCommittedManifestHardcodesClusterSpecificHostname is the
// decisive check: outside the single allowlisted source, no committed manifest
// may contain a concrete *.opentlc.com literal (the exact "copied an existing
// hostname" regression) or a generic *.apps.<cluster-domain> wildcard literal.
func TestClusterDomain_NoCommittedManifestHardcodesClusterSpecificHostname(t *testing.T) {
	root := repoRoot(t)
	for _, path := range collectManifestFiles(t, root) {
		if isAllowlisted(path) {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		require.NoError(t, err)
		lower := strings.ToLower(string(contents))

		require.NotContains(t, lower, ".opentlc.com",
			"%s hardcodes a cluster-specific hostname (contains .opentlc.com); move it to gitops/components/maas/cluster-config.yaml and consume it via a kustomize replacement, or use cluster-internal DNS", path)

		for _, match := range clusterWildcardDomain.FindAllString(lower, -1) {
			// Keep the leading dot so domain suffix comparison is
			// unambiguous (".your-cluster.com" vs ".your-cluster.com").
			domain := "." + strings.TrimPrefix(match, ".apps.")
			if isPlaceholderDomain(domain) {
				continue
			}
			t.Errorf("%s hardcodes a cluster-specific wildcard hostname (%q); only generic placeholders are allowed outside cluster-config.yaml", path, match)
		}
	}
}
