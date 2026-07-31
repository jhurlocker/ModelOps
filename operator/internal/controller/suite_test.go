package controller

// Phase 0 test scaffolding: envtest bootstrap shared by every test in this
// package. This starts a real kube-apiserver + etcd (via
// sigs.k8s.io/controller-runtime/pkg/envtest) so reconciler logic can be
// exercised against a real API server without a full cluster and without
// Tekton actually installed.
//
// KUBEBUILDER_ASSETS must point at the envtest binaries (etcd,
// kube-apiserver). `make test` wires this automatically; see the Makefile.

import (
	"fmt"
	"os"
	"testing"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	testEnv   *envtest.Environment
	testCfg   *rest.Config
	k8sClient client.Client
)

// TestMain starts one shared envtest environment for the whole package and
// tears it down afterwards, rather than paying the ~1s kube-apiserver
// bootstrap cost per test. Individual tests must not assume an empty
// cluster; they should create their own uniquely-named Namespace and
// objects and clean up after themselves (see newTestNamespace in
// testutil_test.go).
func TestMain(m *testing.M) {
	logf.SetLogger(zap.New(zap.UseDevMode(true)))

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			"../../config/crd/bases",   // our own CRDs (ModelRequest, CapacityPlan, ...)
			"../../config/crd/testdata", // vendored third-party CRDs (Tekton PipelineRun)
		},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to start envtest environment:", err)
		os.Exit(1)
	}
	testCfg = cfg

	if err := modelopsv1alpha1.AddToScheme(scheme.Scheme); err != nil {
		fmt.Fprintln(os.Stderr, "failed to register modelopsv1alpha1 scheme:", err)
		_ = testEnv.Stop()
		os.Exit(1)
	}
	if err := tektonv1.AddToScheme(scheme.Scheme); err != nil {
		fmt.Fprintln(os.Stderr, "failed to register tektonv1 scheme:", err)
		_ = testEnv.Stop()
		os.Exit(1)
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to create envtest client:", err)
		_ = testEnv.Stop()
		os.Exit(1)
	}

	code := m.Run()

	if err := testEnv.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, "failed to stop envtest environment:", err)
	}

	os.Exit(code)
}
