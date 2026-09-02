package tekton

// Static safety net for plaintext credentials (the "sealed secrets" phase):
// no committed manifest may contain a plain Kubernetes `Secret` with literal,
// non-empty data (encrypted `SealedSecret` objects are the only acceptable
// way to commit credentials), and no kustomization may carry a
// `secretGenerator` with plaintext literals. The point is structural, not
// cosmetic -- the concrete failure mode this guards against is "the next
// component is added by copying an existing Secret/secretGenerator with the
// vendor-default credential (minioadmin & friends) as a starting point",
// which silently re-exposes a plaintext credential in a public, permanent
// repository.
//
// There is an explicitly documented, justified exception list (by Secret
// name) for the two committed Secrets that hold no credential value:
//   - evalhub-credentials: only a cluster-internal Service URL (a non-secret
//     endpoint the operator reads by Secret name for shape-consistency with
//     the *-secret-name pattern), not a token/password.
//   - gpu-advisor-credentials: an optional external GPU-advisor API key whose
//     committed value is the literal placeholder "REPLACE_ME", to be supplied
//     manually per cluster (documented in its own header comment).
//
// Everything else -- MinIO root, Zot htpasswd + push, scan/result S3, the UI
// prefill defaults, MySQL, and MaaS DB credentials -- must be a SealedSecret.
//
// The scan roots are the two deployment roots (gitops/ and the Tekton
// pipeline directory) plus operator/config/samples, matching the neighboring
// cluster-domain hostname safety net.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// secretScanRoots are the repository-relative directories whose committed
// manifests must be free of plaintext Secrets and secretGenerator literals.
// (operator/config/samples is scanned for the same reason as the hostname net:
// samples are the copy-paste starting point for a new deployment.)
var secretScanRoots = []string{
	"gitops",
	filepath.Join("operator", "config", "samples"),
	filepath.Join("model_onboarding_pipeline", "model-intake-pipeline", "pipeline"),
}

// plainSecretAllowlist are Secret names that ARE permitted to remain plain
// Kubernetes Secrets because they carry no credential value. Each entry's
// justification is documented in the file header comment above.
var plainSecretAllowlist = map[string]string{
	"evalhub-credentials":      "cluster-internal Service URL only, no credential value",
	"gpu-advisor-credentials":  "optional external API key committed as the REPLACE_ME placeholder, filled manually per cluster",
}

// splitDocuments splits a multi-document YAML stream on top-level `---`
// document markers and returns each non-empty document's text.
func splitDocuments(contents string) []string {
	var docs []string
	var current []string
	flush := func() {
		text := strings.TrimSpace(strings.Join(current, "\n"))
		if text != "" {
			docs = append(docs, text)
		}
		current = nil
	}
	for _, line := range strings.Split(contents, "\n") {
		if strings.TrimSpace(line) == "---" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	return docs
}

// collectSecretScanFiles walks the scan roots and returns every *.yaml/*.yml
// file as a repository-relative slash-separated path.
func collectSecretScanFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	for _, rel := range secretScanRoots {
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

// nonEmptyMapValues returns the keys whose value is a non-empty string. It is
// used generically for both `data` (base64-encoded values) and `stringData`
// (plain values): in either case, a non-empty committed literal is a leak.
func nonEmptyMapValues(m map[string]interface{}) []string {
	var out []string
	for k, v := range m {
		s, ok := v.(string)
		if ok && strings.TrimSpace(s) != "" {
			out = append(out, k)
		}
	}
	return out
}

// TestPlaintextSecrets_NoCommittedPlainSecretOutsideAllowlist scans every
// committed manifest and fails on any plain Secret whose data/stringData
// holds a literal non-empty value not covered by plainSecretAllowlist.
func TestPlaintextSecrets_NoCommittedPlainSecretOutsideAllowlist(t *testing.T) {
	root := repoRoot(t)
	for _, path := range collectSecretScanFiles(t, root) {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		require.NoError(t, err)

		for _, doc := range splitDocuments(string(contents)) {
			var obj map[string]interface{}
			if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
				continue // not a plain mapping document (e.g. a list); skip
			}
			kind, _ := obj["kind"].(string)
			if strings.ToLower(kind) != "secret" {
				continue
			}

			var violations []string
			if data, ok := obj["data"].(map[string]interface{}); ok {
				for _, k := range nonEmptyMapValues(data) {
					violations = append(violations, "data."+k)
				}
			}
			if stringData, ok := obj["stringData"].(map[string]interface{}); ok {
				for _, k := range nonEmptyMapValues(stringData) {
					violations = append(violations, "stringData."+k)
				}
			}
			if len(violations) == 0 {
				continue
			}

			name := ""
			if meta, ok := obj["metadata"].(map[string]interface{}); ok {
				name, _ = meta["name"].(string)
			}
			if _, allowed := plainSecretAllowlist[name]; allowed {
				continue
			}

			t.Errorf("%s commits plaintext Secret %q with non-empty literal key(s) %v -- convert it to a SealedSecret via gitops/README.md's \"Secrets setup\" step, or add a documented exception to plainSecretAllowlist", path, name, violations)
		}
	}
}

// TestPlaintextSecrets_NoCommittedSecretGeneratorLiterals scans every
// kustomization.yaml in the scan roots and fails on any secretGenerator whose
// literals would commit a plaintext credential (the pre-existing MinIO/Zot
// pattern this phase replaced). secretGenerator with files is tolerated only
// when it carries no `literals`; file-based generators are out of scope here.
func TestPlaintextSecrets_NoCommittedSecretGeneratorLiterals(t *testing.T) {
	root := repoRoot(t)
	for _, path := range collectSecretScanFiles(t, root) {
		if filepath.Base(path) != "kustomization.yaml" {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		require.NoError(t, err)

		var obj map[string]interface{}
		if err := yaml.Unmarshal(contents, &obj); err != nil {
			continue
		}
		gen, ok := obj["secretGenerator"].([]interface{})
		if !ok {
			continue
		}
		for i, g := range gen {
			entry, ok := g.(map[string]interface{})
			if !ok {
				continue
			}
			literals, ok := entry["literals"].([]interface{})
			if !ok || len(literals) == 0 {
				continue
			}
			t.Errorf("%s secretGenerator[%d] commits plaintext literal(s) -- replace with a SealedSecret (see gitops/README.md \"Secrets setup\")", path, i)
		}
	}
}
