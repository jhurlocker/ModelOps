package tekton

import (
	"context"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	"github.com/stretchr/testify/require"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, modelopsv1alpha1.AddToScheme(s))
	require.NoError(t, tektonv1.AddToScheme(s))
	return s
}

func findParam(params tektonv1.Params, name string) (string, bool) {
	for _, p := range params {
		if p.Name == name {
			return p.Value.StringVal, true
		}
	}
	return "", false
}

// --- New in this phase: map[string]string -> tektonv1.Params conversion ---
// (TDD: written before toTektonParams existed.)

func TestToTektonParams_OmitsEmptyValues_SameGuardAsAddParam(t *testing.T) {
	got := toTektonParams(map[string]string{
		"model-id":      "quay.io/models/foo:v1",
		"empty-omitted": "",
		"gpu-count":     "3",
	})

	require.Len(t, got, 2, "empty values must be omitted, same as stagecommon.AddParam's guard")
	v, ok := findParam(got, "model-id")
	require.True(t, ok)
	require.Equal(t, "quay.io/models/foo:v1", v)
	v, ok = findParam(got, "gpu-count")
	require.True(t, ok)
	require.Equal(t, "3", v)
	_, ok = findParam(got, "empty-omitted")
	require.False(t, ok)
}

func TestToTektonParams_EachParamIsStringTyped(t *testing.T) {
	got := toTektonParams(map[string]string{"x": "y"})
	require.Len(t, got, 1)
	require.Equal(t, tektonv1.ParamTypeString, got[0].Value.Type)
	require.Equal(t, "y", got[0].Value.StringVal)
}

func TestToTektonParams_EmptyMap_ProducesEmptyParams(t *testing.T) {
	got := toTektonParams(map[string]string{})
	require.Empty(t, got)
}

// --- New in this phase: workspace-binding shape. Nothing in the
// pre-Phase-4 characterization suite asserted on this directly (only
// PipelineRef/OwnerReferences/Params), so this closes a real gap while
// relocating buildPipelineRun, rather than just re-testing what was
// already covered. ---

func TestBuildPipelineRun_WorkspaceBindings_MatchTodaysHardcodedShape(t *testing.T) {
	mr := &modelopsv1alpha1.ModelRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-1", Namespace: "ns-1", UID: "abc-123"},
	}
	pr := buildPipelineRun("mr-1-sandbox", "ns-1", "model-intake-sandbox", tektonv1.Params{}, mr, testScheme(t))

	require.Len(t, pr.Spec.Workspaces, 3)
	byName := map[string]tektonv1.WorkspaceBinding{}
	for _, w := range pr.Spec.Workspaces {
		byName[w.Name] = w
	}

	shared, ok := byName["shared-workspace"]
	require.True(t, ok)
	require.NotNil(t, shared.PersistentVolumeClaim)
	require.Equal(t, "guidellm-output-pvc", shared.PersistentVolumeClaim.ClaimName)

	manifests, ok := byName["manifests"]
	require.True(t, ok)
	require.NotNil(t, manifests.ConfigMap)
	require.Equal(t, "mmlu-manifest", manifests.ConfigMap.Name)

	customMMLU, ok := byName["custom-mmlu"]
	require.True(t, ok)
	require.NotNil(t, customMMLU.ConfigMap)
	require.Equal(t, "custom-mmlu", customMMLU.ConfigMap.Name)

	require.Equal(t, "pipeline", pr.Spec.TaskRunTemplate.ServiceAccountName)
	require.Equal(t, "model-intake-sandbox", pr.Spec.PipelineRef.Name)
	require.Len(t, pr.OwnerReferences, 1, "PipelineRun must be owned by the ModelRequest")
	require.Equal(t, "mr-1", pr.OwnerReferences[0].Name)
}

// --- EnsureRun: relocation of the condition-reading logic that used to
// live inline in ModelRequestReconciler.Reconcile. These are the direct
// unit tests for the mapping table proposed in the Phase 4 design
// review (nothing previously isolated this mapping from the rest of
// Reconcile). ---

func newFakeClient(t *testing.T) client.Client {
	t.Helper()
	s := testScheme(t)
	return fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&tektonv1.PipelineRun{}).
		Build()
}

func newOwnerModelRequest(name, ns string) *modelopsv1alpha1.ModelRequest {
	return &modelopsv1alpha1.ModelRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: "test-uid"},
	}
}

func setCondition(t *testing.T, c client.Client, ns, name string, status corev1.ConditionStatus, reason, message string) {
	t.Helper()
	var pr tektonv1.PipelineRun
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: ns}, &pr))
	pr.Status = tektonv1.PipelineRunStatus{
		Status: duckv1.Status{
			Conditions: duckv1.Conditions{
				{Type: apis.ConditionSucceeded, Status: status, Reason: reason, Message: message},
			},
		},
	}
	require.NoError(t, c.Status().Update(context.Background(), &pr))
}

func TestEnsureRun_FreshlyCreated_ReturnsRunning_AndCreatesPipelineRunWithGivenParams(t *testing.T) {
	c := newFakeClient(t)
	r := &StageRunner{Client: c, Scheme: testScheme(t)}
	mr := newOwnerModelRequest("mr-1", "ns-1")

	status, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name:        "sandbox",
		RunName:     "mr-1-sandbox",
		WorkflowRef: "model-intake-sandbox",
		Params:      map[string]string{"model-id": "some/model"},
	})
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageRunning, status.Phase)
	require.Equal(t, "mr-1-sandbox", status.RunRef)

	var pr tektonv1.PipelineRun
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "mr-1-sandbox", Namespace: "ns-1"}, &pr))
	require.Equal(t, "model-intake-sandbox", pr.Spec.PipelineRef.Name)
	v, ok := findParam(pr.Spec.Params, "model-id")
	require.True(t, ok)
	require.Equal(t, "some/model", v)
}

func TestEnsureRun_SecondCall_DoesNotRecreate(t *testing.T) {
	c := newFakeClient(t)
	r := &StageRunner{Client: c, Scheme: testScheme(t)}
	mr := newOwnerModelRequest("mr-1", "ns-1")
	spec := stagecommon.StageSpec{Name: "sandbox", RunName: "mr-1-sandbox", WorkflowRef: "model-intake-sandbox"}

	_, err := r.EnsureRun(context.Background(), mr, spec)
	require.NoError(t, err)
	var before tektonv1.PipelineRun
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "mr-1-sandbox", Namespace: "ns-1"}, &before))

	status, err := r.EnsureRun(context.Background(), mr, spec)
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageRunning, status.Phase, "no condition yet -> still Running")

	var after tektonv1.PipelineRun
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "mr-1-sandbox", Namespace: "ns-1"}, &after))
	require.Equal(t, before.ResourceVersion, after.ResourceVersion, "must not recreate/modify an already-existing PipelineRun")
}

func TestEnsureRun_ExistingRun_ConditionUnknown_ReturnsRunning(t *testing.T) {
	c := newFakeClient(t)
	r := &StageRunner{Client: c, Scheme: testScheme(t)}
	mr := newOwnerModelRequest("mr-1", "ns-1")
	spec := stagecommon.StageSpec{Name: "sandbox", RunName: "mr-1-sandbox", WorkflowRef: "model-intake-sandbox"}
	_, err := r.EnsureRun(context.Background(), mr, spec)
	require.NoError(t, err)

	setCondition(t, c, "ns-1", "mr-1-sandbox", corev1.ConditionUnknown, "Running", "Not all Tasks in the Pipeline have finished executing")

	status, err := r.EnsureRun(context.Background(), mr, spec)
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageRunning, status.Phase)
	require.Equal(t, "Running", status.Reason)
	require.Equal(t, "Not all Tasks in the Pipeline have finished executing", status.Message)
}

func TestEnsureRun_ExistingRun_ConditionTrue_ReturnsSucceeded(t *testing.T) {
	c := newFakeClient(t)
	r := &StageRunner{Client: c, Scheme: testScheme(t)}
	mr := newOwnerModelRequest("mr-1", "ns-1")
	spec := stagecommon.StageSpec{Name: "sandbox", RunName: "mr-1-sandbox", WorkflowRef: "model-intake-sandbox"}
	_, err := r.EnsureRun(context.Background(), mr, spec)
	require.NoError(t, err)

	setCondition(t, c, "ns-1", "mr-1-sandbox", corev1.ConditionTrue, "Succeeded", "Tasks Completed: 5 (Failed: 0, Cancelled 0), Skipped: 0")

	status, err := r.EnsureRun(context.Background(), mr, spec)
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageSucceeded, status.Phase)
	require.Equal(t, "Tasks Completed: 5 (Failed: 0, Cancelled 0), Skipped: 0", status.Message)
}

func TestEnsureRun_ExistingRun_ConditionFalse_ReturnsFailed(t *testing.T) {
	c := newFakeClient(t)
	r := &StageRunner{Client: c, Scheme: testScheme(t)}
	mr := newOwnerModelRequest("mr-1", "ns-1")
	spec := stagecommon.StageSpec{Name: "sandbox", RunName: "mr-1-sandbox", WorkflowRef: "model-intake-sandbox"}
	_, err := r.EnsureRun(context.Background(), mr, spec)
	require.NoError(t, err)

	setCondition(t, c, "ns-1", "mr-1-sandbox", corev1.ConditionFalse, "Failed", "compliance scan failed")

	status, err := r.EnsureRun(context.Background(), mr, spec)
	require.NoError(t, err)
	require.Equal(t, stagecommon.StageFailed, status.Phase)
	require.Equal(t, "compliance scan failed", status.Message)
}
