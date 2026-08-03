package main

import (
	"flag"
	"os"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/controller"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"
	"github.com/jhurlocker/modelops-operator/internal/stages/capacityplanning"
	"github.com/jhurlocker/modelops-operator/internal/stages/promotion"
	"github.com/jhurlocker/modelops-operator/internal/stages/sandbox"
	tektonstage "github.com/jhurlocker/modelops-operator/internal/stages/tekton"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(modelopsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(tektonv1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                ctrl.Options{}.Metrics,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "modelrequest-controller.modelops.example.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controller.ModelRequestReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		// StageHandlers/StageRunners: the Phase 6 dual registry the
		// generic stage walker dispatches through (see
		// internal/stagewalk.Walk and docs/REFACTOR_PLAN.md Phase 6).
		// Keyed to match defaultStages' "capacity"/"sandbox"/
		// "promotion" names and "CapacityPlan"/"PipelineRun" kinds --
		// a profile setting its own Spec.Stages must reference these
		// same names/kinds (or register additional ones here) to be
		// dispatchable.
		StageHandlers: map[string]stagecommon.StageHandler{
			"capacity":  capacityplanning.Handler{},
			"sandbox":   sandbox.Handler{},
			"promotion": promotion.Handler{},
		},
		StageRunners: map[string]stagecommon.StageRunner{
			"CapacityPlan": &capacityplanning.StageRunner{
				Client: mgr.GetClient(),
				Scheme: mgr.GetScheme(),
			},
			"PipelineRun": &tektonstage.StageRunner{
				Client: mgr.GetClient(),
				Scheme: mgr.GetScheme(),
			},
		},
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ModelRequest")
		os.Exit(1)
	}

	if err = (&controller.CapacityPlanReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "CapacityPlan")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
