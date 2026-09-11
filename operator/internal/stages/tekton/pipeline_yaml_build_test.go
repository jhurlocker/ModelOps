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
	"strings"
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
			Name     string   `json:"name"`
			RunAfter []string `json:"runAfter"`
		} `json:"tasks"`
		Results []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"results"`
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
		"build-modelcar must PUSH to Zot's internal Service DNS (the Route hostname is only used for the emitted node-level pull reference)")
}

// TestPipelineYAML_BuildModelcar_EmitsNodeResolvablePullReference pins the
// node-level pull fix: build-modelcar must emit BOTH an internal `image-ref`
// (Zot's internal Service DNS, for in-cluster consumers) and a node-resolvable
// `pull-ref` (Zot's Route hostname, resolved at runtime from the live Route),
// because kubelet/CRI-O pulls the ModelCar image in the node's host network
// namespace where .svc.cluster.local does not resolve.
func TestPipelineYAML_BuildModelcar_EmitsNodeResolvablePullReference(t *testing.T) {
	text := readPipelineYAMLFile(t, buildModelcarTaskFile)

	require.Contains(t, text, "name: route-host",
		"build-modelcar must declare a route-host result for the resolved Zot Route hostname")
	require.Contains(t, text, "name: pull-ref",
		"build-modelcar must declare a pull-ref result for the node-resolvable reference")
	require.Contains(t, text, "resolve-registry-host",
		"build-modelcar must resolve the Zot Route hostname in a dedicated resolve-registry-host step")
	require.Contains(t, text, "oc get route zot -n modelops-zot",
		"resolve-registry-host must read the live Zot Route hostname (no committed host)")

	// pull-ref is composed in the resolve-registry-host step (not in
	// build-and-push), because Tekton v1 does NOT interpolate
	// $(steps.X.results.Y) environment values -- the env arrives as the literal
	// text, while $(params.X) interpolation works. pull-ref therefore uses the
	// resolved shell HOST + MODEL_NAME/MODEL_VERSION sourced from params.
	require.Contains(t, text, `printf '%s' "${HOST}/${MODEL_NAME}:${MODEL_VERSION}" > "$(results.pull-ref.path)"`,
		"pull-ref must be composed in resolve-registry-host from the resolved host + model name/version")
	require.Contains(t, text, `"${REGISTRY_HOST}/${MODEL_NAME}:${MODEL_VERSION}" > "$(results.image-ref.path)"`,
		"image-ref must use the internal Service DNS registry host")
	require.NotContains(t, text, "name: ROUTE_HOST",
		"build-and-push must not declare a ROUTE_HOST env var sourced from $(steps.resolve-registry-host.results.route-host) (not interpolated by Tekton)")
}

// TestPipelineYAML_SandboxSurfacesModelcarResultsAtPipelineLevel pins the
// piece that actually carries build-modelcar's output out of Tekton and into
// the operator: the Pipeline-level results block. Task-level results alone
// never reach PipelineRun.status.results (what tekton.StageRunner reads), so
// without these two mappings the operator's promotion handler has no
// image-ref/pull-ref to consume in a real deployment. image-ref (internal
// Service DNS) and pull-ref (node-resolvable Route hostname) must BOTH be
// declared here, in this order, referencing the build-modelcar task results.
func TestPipelineYAML_SandboxSurfacesModelcarResultsAtPipelineLevel(t *testing.T) {
	var doc pipelineTaskDoc
	require.NoError(t, yaml.Unmarshal([]byte(readPipelineYAMLFile(t, "sandbox-pipeline.yaml")), &doc))
	require.Len(t, doc.Spec.Results, 2, "sandbox-pipeline.yaml must surface exactly two Pipeline-level results")

	require.Equal(t, "image-ref", doc.Spec.Results[0].Name)
	require.Equal(t, "$(tasks.build-modelcar.results.image-ref)", doc.Spec.Results[0].Value,
		"image-ref must map up from the build-modelcar task result (internal Service DNS)")

	require.Equal(t, "pull-ref", doc.Spec.Results[1].Name)
	require.Equal(t, "$(tasks.build-modelcar.results.pull-ref)", doc.Spec.Results[1].Value,
		"pull-ref must map up from the build-modelcar task result (node-resolvable Route hostname)")
}

// TestPipelineYAML_SandboxConsumesImageRef_ComplianceAndDeploy pins the
// Phase C sandbox-pipeline companion wiring: compliance-artifact-scan must
// consume the internal image-ref (in-cluster skopeo inspect), while
// deploy-model must consume the node-resolvable pull-ref (its KServe pods
// pull at the node level). Neither re-derives a tag from
// quay.io/redhat-ai-services/modelcar-catalog. Reads the committed YAML.
func TestPipelineYAML_SandboxConsumesImageRef_ComplianceAndDeploy(t *testing.T) {
	text := readPipelineYAMLFile(t, "sandbox-pipeline.yaml")

	const internalRef = "value: $(tasks.build-modelcar.results.image-ref)"
	const pullRef = "value: $(tasks.build-modelcar.results.pull-ref)"
	// Each reference appears exactly twice: once as a task param
	// (compliance-artifact-scan for image-ref, deploy-model for pull-ref) and
	// once in the Pipeline-level results block that surfaces it into
	// PipelineRun.status.results for the operator to read.
	require.Equal(t, 2, strings.Count(text, internalRef),
		"image-ref must be consumed by exactly one task (compliance-artifact-scan) AND surfaced once at the Pipeline level")
	require.Equal(t, 2, strings.Count(text, pullRef),
		"pull-ref must be consumed by exactly one task (deploy-model) AND surfaced once at the Pipeline level")

	// The old param-forwarding must be gone from the sandbox pipeline --
	// modelcar-image now comes from the build result, never from a
	// (always-empty) modelcar-image param.
	require.NotContains(t, text, "value: $(params.modelcar-image)",
		"sandbox-pipeline.yaml must no longer forward $(params.modelcar-image) to its tasks")
}
