package noop

import (
	"context"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEnsureRun_AlwaysSucceedsImmediately_NoChildObjectCreated(t *testing.T) {
	r := StageRunner{}
	mr := &modelopsv1alpha1.ModelRequest{ObjectMeta: metav1.ObjectMeta{Name: "mr-1", Namespace: "ns-1"}}

	status, err := r.EnsureRun(context.Background(), mr, stagecommon.StageSpec{
		Name:    "sandbox",
		RunName: "mr-1-sandbox",
	})

	require.NoError(t, err)
	require.Equal(t, stagecommon.StageSucceeded, status.Phase)
	require.Equal(t, "mr-1-sandbox", status.RunRef)
	require.NotEmpty(t, status.Message)
}

func TestEnsureRun_RepeatedCalls_KeepReturningSucceeded(t *testing.T) {
	r := StageRunner{}
	mr := &modelopsv1alpha1.ModelRequest{ObjectMeta: metav1.ObjectMeta{Name: "mr-1", Namespace: "ns-1"}}
	spec := stagecommon.StageSpec{Name: "promotion-staging", RunName: "mr-1-promotion-staging"}

	for i := 0; i < 3; i++ {
		status, err := r.EnsureRun(context.Background(), mr, spec)
		require.NoError(t, err)
		require.Equal(t, stagecommon.StageSucceeded, status.Phase, "call %d", i)
	}
}
