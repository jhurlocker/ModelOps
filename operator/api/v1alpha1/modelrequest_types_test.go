package v1alpha1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// modelRequirementsGoldenJSON pins the on-the-wire JSON shape of
// ModelRequirements as it existed before Phase 2 of docs/REFACTOR_PLAN.md
// grouped its ~20 flat fields into GPUConfig/BenchmarkTargets/
// SecurityConfig/DeploymentConfig sub-structs. All 20 fields are flat,
// direct siblings here -- no nested wrapper objects. This exact string
// must unmarshal into ModelRequirements and marshal back out to the same
// shape both before and after the Phase 2 struct split, proving the
// on-the-wire format (and therefore any existing ModelRequest CR in a
// live cluster) is unaffected by the internal Go reorganization.
const modelRequirementsGoldenJSON = `{
  "contextLength": 8192,
  "expectedConcurrency": 4,
  "gpuIsolationPolicy": "dedicated",
  "allowTimeSlicing": true,
  "allowMIG": false,
  "cveThreshold": "high",
  "securityThreshold": "block",
  "targetEnvironment": "prod",
  "sandboxNamespace": "sandbox",
  "stagingNamespace": "staging",
  "promotionNamespaces": ["staging", "preprod"],
  "advisorEndpoint": "http://advisor.example.com",
  "gpuCountOverride": "3",
  "valuesContent": "replicaCount: 1",
  "customBenchmarkData": true,
  "customBenchmarkFile": "custom.json",
  "openshiftConsoleDomain": "apps.example.com",
  "requestRate": "4.0",
  "targetTTFT": "500ms",
  "targetThroughput": "100"
}`

// modelRequestGoldenYAML pins the on-the-wire YAML shape of a full
// ModelRequest CR (spec only, as a cluster's etcd/API server would store
// it), covering a nested `requirements:` block alongside the CR's other
// top-level spec fields. Used the same way as modelRequirementsGoldenJSON
// above, but at the whole-CR level rather than just the sub-struct.
const modelRequestGoldenYAML = `
model:
  sourceType: huggingface
  uri: ibm-granite/granite-3.0-2b-instruct
  name: granite-3-2b
  version: v1
displayName: Granite 3 2B
businessJustification: chatbot pilot
lifecycleProfile: standard
pipelineRef: model-intake-sandbox
requestedBy: jane@example.com
evalhubSecretName: evalhub-credentials
huggingfaceSecretName: hf-credentials
scanS3SecretName: scan-s3-credentials
resultS3SecretName: result-s3-credentials
resultS3Endpoint: http://minio.example.com
resultS3Bucket: model-results
requirements:
  contextLength: 8192
  expectedConcurrency: 4
  gpuIsolationPolicy: dedicated
  allowTimeSlicing: true
  allowMIG: false
  cveThreshold: high
  securityThreshold: block
  targetEnvironment: prod
  sandboxNamespace: sandbox
  stagingNamespace: staging
  promotionNamespaces:
    - staging
    - preprod
  advisorEndpoint: http://advisor.example.com
  gpuCountOverride: "3"
  valuesContent: "replicaCount: 1"
  customBenchmarkData: true
  customBenchmarkFile: custom.json
  openshiftConsoleDomain: apps.example.com
  requestRate: "4.0"
  targetTTFT: 500ms
  targetThroughput: "100"
access:
  authorizedViewers: team-a
  accessRole: viewer
maas:
  enabled: true
  gpuCount: "1"
  runtimeImage: quay.io/example/runtime:latest
  authorizedGroup: team-a
`

// TestModelRequirements_WireFormatUnchanged_RoundTrip proves that
// unmarshaling the pre-Phase-2 flat JSON shape and marshaling it back out
// produces byte-for-byte-equivalent JSON (compared as decoded maps, so
// key order doesn't matter). This is the core regression test for Phase
// 2's sub-struct grouping: it must pass unmodified both before and after
// that change.
func TestModelRequirements_WireFormatUnchanged_RoundTrip(t *testing.T) {
	var reqs ModelRequirements
	require.NoError(t, json.Unmarshal([]byte(modelRequirementsGoldenJSON), &reqs))

	out, err := json.Marshal(&reqs)
	require.NoError(t, err)

	var gotMap, wantMap map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &gotMap))
	require.NoError(t, json.Unmarshal([]byte(modelRequirementsGoldenJSON), &wantMap))

	require.Equal(t, wantMap, gotMap,
		"ModelRequirements JSON shape changed -- sub-struct grouping must use "+
			"anonymous embedding with `json:\",inline\"` (or equivalent) so fields "+
			"stay flat siblings, not nested under a wrapper key")
}

// TestModelRequirements_WireFormatUnchanged_NoWrapperKeys is a more
// pointed check than the round-trip test: it asserts no field ends up
// nested under a named sub-struct key (e.g. "gpuConfig") in the marshaled
// output, which is exactly the mistake a non-anonymous embed (`GPUConfig
// GPUConfig \`json:"gpuConfig,omitempty"\`` instead of an anonymous
// `GPUConfig \`json:",inline"\``) would produce.
func TestModelRequirements_WireFormatUnchanged_NoWrapperKeys(t *testing.T) {
	var reqs ModelRequirements
	require.NoError(t, json.Unmarshal([]byte(modelRequirementsGoldenJSON), &reqs))

	out, err := json.Marshal(&reqs)
	require.NoError(t, err)

	var gotMap map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &gotMap))

	for _, wrapper := range []string{
		"gpuConfig", "benchmarkTargets", "securityConfig", "deploymentConfig",
		"GPUConfig", "BenchmarkTargets", "SecurityConfig", "DeploymentConfig",
	} {
		_, exists := gotMap[wrapper]
		require.False(t, exists,
			"found unexpected wrapper key %q in marshaled ModelRequirements -- "+
				"sub-struct fields must be inlined, not nested under a named object", wrapper)
	}
}

// TestModelRequest_WireFormatUnchanged_FullCRRoundTrip covers the same
// property one level up, at the whole ModelRequest CR spec level (as
// requested for Phase 2): an existing CR's YAML, including a nested
// `requirements:` block, must unmarshal and re-marshal to the same shape
// regardless of how ModelRequirements is organized internally.
func TestModelRequest_WireFormatUnchanged_FullCRRoundTrip(t *testing.T) {
	var spec ModelRequestSpec
	require.NoError(t, yaml.Unmarshal([]byte(modelRequestGoldenYAML), &spec))

	require.NotNil(t, spec.Requirements)
	// Spot-check a field from each intended sub-struct group is reachable
	// via the promoted (embedded) field name, proving field access at
	// call sites (`reqs.GPUCountOverride`, `reqs.ContextLength`, etc.)
	// keeps working unqualified even after the sub-structs exist.
	require.Equal(t, "3", spec.Requirements.GPUCountOverride)
	require.Equal(t, 8192, spec.Requirements.ContextLength)
	require.Equal(t, "high", spec.Requirements.CVEThreshold)
	require.Equal(t, "replicaCount: 1", spec.Requirements.ValuesContent)

	out, err := yaml.Marshal(&spec)
	require.NoError(t, err)

	var gotMap, wantMap map[string]interface{}
	require.NoError(t, yaml.Unmarshal(out, &gotMap))
	require.NoError(t, yaml.Unmarshal([]byte(modelRequestGoldenYAML), &wantMap))

	require.Equal(t, wantMap, gotMap,
		"ModelRequest spec YAML shape changed on round-trip -- an existing "+
			"CR's on-cluster YAML must be unaffected by the ModelRequirements "+
			"sub-struct refactor")
}
