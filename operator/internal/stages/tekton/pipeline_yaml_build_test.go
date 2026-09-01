package tekton

// Phase B (model-intake vertical slice, see docs/PHASE_LOG.md) static
// safety net for the new build-modelcar Task. This Task is the first
// genuine in-cluster image build in the repo: it downloads a model from
// Hugging Face, packages it as a ModelCar OCI image (two-stage build,
// /models layout per Red Hat's "Build and deploy a ModelCar container"
// article), and pushes it to the in-cluster Zot registry. Because it has
// real security surface (a Hugging Face token for gated models, and
// plaintext registry push credentials), these tests read the committed
// YAML (not a copy) to pin the exact shape that was design-reviewed:
//
//   - build-modelcar is Task #1 in sandbox-pipeline.yaml, and
//     compliance-artifact-scan (the old first task) now runs after it;
//   - it is guarded by `when: in ["huggingface"]` so oci/s3 sources skip
//     the build entirely (fail-safe-by-default: any future source type
//     also skips rather than surprising-us with a build);
//   - it declares an `image-ref` result (the full Zot reference
//     <registry-url>/<model-name>:<model-version> that Phase C consumes);
//   - both credentials (HF token, Zot push username/password) come from
//     secretKeyRef, never from a param default or a literal value;
//   - the builder image is pinned (never `:latest`), and the registry is
//     addressed by its internal Service DNS (HTTP), not the Route.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

const buildModelcarTaskFile = "build-modelcar-task.yaml"

// pipelineTaskDoc is just enough of a Tekton v1 Pipeline to read the ordered
// task list without pulling in the full tekton API types.
type pipelineTaskDoc struct {
	Spec struct {
		Tasks []struct {
			Name     string `json:"name"`
			RunAfter []string `json:"runAfter"`
		} `json:"tasks"`
	} `json:"spec"`
}

func readPipelineYAMLFile(t *testing.T, filename string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(pipelineYAMLDir(t), filename))
	require.NoError(t, err)
	return string(contents)
}

func TestPipelineYAML_BuildModelcar_IsFirstTaskInSandboxPipeline(t *testing.T) {
	var doc pipelineTaskDoc
	require.NoError(t, yaml.Unmarshal([]byte(readPipelineYAMLFile(t, "sandbox-pipeline.yaml")), &doc))
	require.NotEmpty(t, doc.Spec.Tasks, "sandbox-pipeline.yaml must declare tasks")

	require.Equal(t, "build-modelcar", doc.Spec.Tasks[0].Name,
		"build-modelcar must be the FIRST task in sandbox-pipeline.yaml (running before compliance-artifact-scan)")

	var sawCompliance bool
	for _, task := range doc.Spec.Tasks {
		if task.Name == "compliance-artifact-scan" {
			sawCompliance = true
			require.Contains(t, task.RunAfter, "build-modelcar",
				"compliance-artifact-scan must runAfter build-modelcar now that a build precedes it")
		}
	}
	require.True(t, sawCompliance, "compliance-artifact-scan must still be present in sandbox-pipeline.yaml")
}

func TestPipelineYAML_BuildModelcar_WhenGuard_RunsOnlyForHuggingface(t *testing.T) {
	text := readPipelineYAMLFile(t, "sandbox-pipeline.yaml")
	require.Contains(t, text, "input: $(params.model-source-type)",
		"the when guard must key off the existing model-source-type param")
	require.Contains(t, text, "operator: in",
		"the when guard must use a positive 'in' match (fail-safe-by-default)")
	require.Contains(t, text, `values: ["huggingface"]`,
		"the when guard must match exactly [\"huggingface\"]; oci/s3 (and any future type) skip the build")
	require.NotContains(t, text, `operator: notin`,
		"the guard must be a positive 'in' match, not 'notin'")
}

func TestPipelineYAML_BuildModelcarTask_EmitsImageRefResult(t *testing.T) {
	text := readPipelineYAMLFile(t, buildModelcarTaskFile)
	require.Contains(t, text, "name: image-ref",
		"build-modelcar must declare an image-ref result")
	require.Contains(t, text, "$(results.image-ref.path)",
		"the build-and-push step must write the full image reference to the image-ref result")
	require.Contains(t, text, "results:",
		"build-modelcar must declare a results: block")
}

func TestPipelineYAML_BuildModelcarTask_ConsumesCredentialsViaSecretKeyRef_NeverLiteralValues(t *testing.T) {
	text := readPipelineYAMLFile(t, buildModelcarTaskFile)

	require.Contains(t, text, "secretKeyRef",
		"build-modelcar must source credentials via secretKeyRef, not literal values")
	require.Contains(t, text, "huggingface-secret-name",
		"the HF token must be referenced by Secret name (huggingface-secret-name)")
	require.Contains(t, text, "optional: true",
		"the HF token secretKeyRef must be optional (ungated models have no token)")
	require.Contains(t, text, "registry-auth-secret-name",
		"Zot push credentials must be referenced by Secret name (registry-auth-secret-name)")
	require.Contains(t, text, "key: username",
		"the Zot push credential Secret must supply a username key")
	require.Contains(t, text, "key: password",
		"the Zot push credential Secret must supply a password key")

	// The credential Value must never be baked into the Task as a literal
	// env value or a param default. zotadmin is the sandbox credential; if it
	// appears as a *value* (not a doc-comment mention of the identity name)
	// it has leaked out of the Secret and into GitOps YAML. The word may
	// legitimately appear in a comment explaining the htpasswd rotation
	// coupling (see gitops/components/runtime-config/secrets.yaml), so these
	// assertions target the value-carrying forms, not the word itself.
	require.NotContains(t, text, "value: zotadmin",
		"build-modelcar-task.yaml must never set a credential as a literal env value")
	require.NotContains(t, text, `default: "zotadmin"`,
		"build-modelcar-task.yaml must never carry a credential as a param default")
	require.NotContains(t, text, "huggingface-token:",
		"build-modelcar-task.yaml must reference the HF token by Secret name, not a huggingface-token value param")
}

func TestPipelineYAML_BuildModelcarTask_PinnedBuildahImage_NeverLatest(t *testing.T) {
	text := readPipelineYAMLFile(t, buildModelcarTaskFile)
	require.Contains(t, text, "quay.io/buildah/stable:v1.43.2",
		"build-modelcar must pin a specific buildah image version")
	require.NotContains(t, text, "buildah/stable:latest",
		"build-modelcar must not use the floating 'latest' buildah tag")
	require.Contains(t, text, "--storage-driver vfs",
		"build-modelcar must use the vfs storage driver (rootless-safe on OpenShift)")
	require.Contains(t, text, "SETFCAP",
		"build-modelcar's buildah step must request the SETFCAP capability (required for userns creation on OCP 4.11+/kernel 5.12+; Red Hat KB #6993746)")
}

func TestPipelineYAML_BuildModelcar_RegistryUrlIsInternalServiceDNS(t *testing.T) {
	text := readPipelineYAMLFile(t, buildModelcarTaskFile)
	require.Contains(t, text, "zot.modelops-zot.svc.cluster.local:5000",
		"build-modelcar must push to Zot's internal Service DNS (HTTP), never the external Route")
}
