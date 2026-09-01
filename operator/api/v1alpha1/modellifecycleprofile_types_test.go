package v1alpha1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProfileStageSpec_NoCheckTypes_SerializesIdenticallyToPrePhaseB(t *testing.T) {
	stage := &ProfileStageSpec{
		Name:              "sandbox",
		Kind:              "PipelineRun",
		ProviderConfigRef: &ProviderConfigRef{Name: "test-provider"},
	}

	out, err := json.Marshal(stage)
	require.NoError(t, err)

	var gotMap map[string]any
	require.NoError(t, json.Unmarshal(out, &gotMap))

	_, hasCheckTypes := gotMap["checkTypes"]
	require.False(t, hasCheckTypes,
		"an empty/nil checkTypes must not appear in JSON output -- "+
			"proves backward compatibility with every existing profile")

	require.Equal(t, "sandbox", gotMap["name"])
	require.Equal(t, "PipelineRun", gotMap["kind"])
}

func TestProfileStageSpec_CheckTypes_Set_SerializesCorrectly(t *testing.T) {
	stage := &ProfileStageSpec{
		Name:       "sandbox",
		Kind:       "PipelineRun",
		CheckTypes: []CheckType{CheckTypeSecurityScan, CheckTypeComplianceScan},
	}

	out, err := json.Marshal(stage)
	require.NoError(t, err)

	var gotMap map[string]any
	require.NoError(t, json.Unmarshal(out, &gotMap))

	checkTypes, ok := gotMap["checkTypes"].([]any)
	require.True(t, ok, "checkTypes must be present and be an array")
	require.Len(t, checkTypes, 2)
	require.Equal(t, "SecurityScan", checkTypes[0])
	require.Equal(t, "ComplianceScan", checkTypes[1])
}

func TestStageProgress_NoCheckResults_SerializesIdenticallyToPrePhaseB(t *testing.T) {
	sp := &StageProgress{
		Name:      "sandbox",
		Namespace: "test-ns",
		Phase:     "Succeeded",
		RunRef:    "mr-1-sandbox",
	}

	out, err := json.Marshal(sp)
	require.NoError(t, err)

	var gotMap map[string]any
	require.NoError(t, json.Unmarshal(out, &gotMap))

	_, hasCheckResults := gotMap["checkResults"]
	require.False(t, hasCheckResults,
		"an empty/nil checkResults must not appear in JSON output")
}

func TestStageProgress_WithCheckResults_SerializesCorrectly(t *testing.T) {
	sp := &StageProgress{
		Name:      "sandbox",
		Namespace: "test-ns",
		Phase:     "Succeeded",
		RunRef:    "mr-1-sandbox",
		CheckResults: []CheckResult{
			{Type: CheckTypeSecurityScan, Passed: true, Reason: "no-cves"},
			{Type: CheckTypeComplianceScan, Passed: false, Reason: "policy-violation"},
		},
	}

	out, err := json.Marshal(sp)
	require.NoError(t, err)

	var gotMap map[string]any
	require.NoError(t, json.Unmarshal(out, &gotMap))

	results, ok := gotMap["checkResults"].([]any)
	require.True(t, ok)
	require.Len(t, results, 2)

	r0 := results[0].(map[string]any)
	require.Equal(t, "SecurityScan", r0["type"])
	require.Equal(t, true, r0["passed"])
	require.Equal(t, "no-cves", r0["reason"])

	r1 := results[1].(map[string]any)
	require.Equal(t, "ComplianceScan", r1["type"])
	require.Equal(t, false, r1["passed"])
	require.Equal(t, "policy-violation", r1["reason"])
}

func TestStageProgress_NoResults_SerializesIdenticallyToPrePhaseC(t *testing.T) {
	sp := &StageProgress{
		Name:      "sandbox",
		Namespace: "test-ns",
		Phase:     "Succeeded",
		RunRef:    "mr-1-sandbox",
	}

	out, err := json.Marshal(sp)
	require.NoError(t, err)

	var gotMap map[string]any
	require.NoError(t, json.Unmarshal(out, &gotMap))

	_, hasResults := gotMap["results"]
	require.False(t, hasResults,
		"an empty/nil results must not appear in JSON output -- "+
			"proves backward compatibility with every existing persisted status")
}

func TestStageProgress_WithResults_SerializesCorrectly(t *testing.T) {
	sp := &StageProgress{
		Name:      "sandbox",
		Namespace: "test-ns",
		Phase:     "Succeeded",
		RunRef:    "mr-1-sandbox",
		Results: []StageResult{
			{Name: "image-ref", Value: "zot.modelops-zot.svc.cluster.local:5000/smollm2-135m-instruct:v1"},
		},
	}

	out, err := json.Marshal(sp)
	require.NoError(t, err)

	var gotMap map[string]any
	require.NoError(t, json.Unmarshal(out, &gotMap))

	results, ok := gotMap["results"].([]any)
	require.True(t, ok, "results must be present and be an array")
	require.Len(t, results, 1)

	r0 := results[0].(map[string]any)
	require.Equal(t, "image-ref", r0["name"])
	require.Equal(t, "zot.modelops-zot.svc.cluster.local:5000/smollm2-135m-instruct:v1", r0["value"])
}
