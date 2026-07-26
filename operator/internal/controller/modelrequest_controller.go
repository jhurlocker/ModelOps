package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ModelRequestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=modelops.example.io,resources=modelrequests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=modelops.example.io,resources=modelrequests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=modelops.example.io,resources=modelrequests/finalizers,verbs=update
// +kubebuilder:rbac:groups=tekton.dev,resources=pipelineruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *ModelRequestReconciler) Reconcile(
	ctx context.Context,
	req ctrl.Request,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var modelRequest modelopsv1alpha1.ModelRequest
	if err := r.Get(ctx, req.NamespacedName, &modelRequest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	pipelineRunName := fmt.Sprintf("%s-onboarding", modelRequest.Name)

	var pipelineRun tektonv1.PipelineRun
	pipelineRunKey := types.NamespacedName{
		Name:      pipelineRunName,
		Namespace: modelRequest.Namespace,
	}

	err := r.Get(ctx, pipelineRunKey, &pipelineRun)

	if apierrors.IsNotFound(err) {
		pipelineRun = tektonv1.PipelineRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pipelineRunName,
				Namespace: modelRequest.Namespace,
				Labels: map[string]string{
					"modelops.example.io/model-request": modelRequest.Name,
				},
			},
			Spec: tektonv1.PipelineRunSpec{
				PipelineRef: &tektonv1.PipelineRef{
					Name: pipelineNameOrDefault(modelRequest.Spec.PipelineRef),
				},
				Params: buildPipelineParams(&modelRequest),
				TaskRunTemplate: tektonv1.PipelineTaskRunTemplate{
					ServiceAccountName: "pipeline",
				},
				Timeouts: &tektonv1.TimeoutFields{
					Pipeline: &metav1.Duration{Duration: 0},
				},
				Workspaces: []tektonv1.WorkspaceBinding{
					{
						Name:                "shared-workspace",
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: "guidellm-output-pvc",
						},
					},
					{
						Name: "manifests",
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "mmlu-manifest",
							},
						},
					},
					{
						Name: "custom-mmlu",
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "custom-mmlu",
							},
						},
					},
				},
			},
		}

		if err := controllerutil.SetControllerReference(
			&modelRequest,
			&pipelineRun,
			r.Scheme,
		); err != nil {
			return ctrl.Result{}, err
		}

		if err := r.Create(ctx, &pipelineRun); err != nil {
			return ctrl.Result{}, err
		}

		modelRequest.Status.Phase = "PipelineRunning"
		modelRequest.Status.PipelineRunName = pipelineRun.Name
		modelRequest.Status.Message = "Model onboarding pipeline started"

		if err := r.Status().Update(ctx, &modelRequest); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info(
			"created onboarding PipelineRun",
			"pipelineRun", pipelineRun.Name,
		)

		return ctrl.Result{}, nil
	}

	if err != nil {
		return ctrl.Result{}, err
	}

	condition := pipelineRun.Status.GetCondition(tektonv1.PipelineRunConditionSucceeded)
	if condition == nil {
		return ctrl.Result{}, nil
	}

	switch condition.Status {
	case corev1.ConditionTrue:
		return r.updateStatus(
			ctx,
			&modelRequest,
			"Succeeded",
			"Model onboarding pipeline completed successfully",
		)

	case corev1.ConditionFalse:
		return r.updateStatus(
			ctx,
			&modelRequest,
			"Failed",
			condition.Message,
		)

	default:
		return r.updateStatus(
			ctx,
			&modelRequest,
			"PipelineRunning",
			condition.Message,
		)
	}
}

func (r *ModelRequestReconciler) updateStatus(
	ctx context.Context,
	request *modelopsv1alpha1.ModelRequest,
	phase string,
	message string,
) (ctrl.Result, error) {
	if request.Status.Phase == phase &&
		request.Status.Message == message {
		return ctrl.Result{}, nil
	}

	request.Status.Phase = phase
	request.Status.Message = message

	if err := r.Status().Update(ctx, request); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ModelRequestReconciler) SetupWithManager(
	mgr ctrl.Manager,
) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&modelopsv1alpha1.ModelRequest{}).
		Owns(&tektonv1.PipelineRun{}).
		Complete(r)
}

func pipelineNameOrDefault(ref string) string {
	if ref == "" {
		return "model-intake-pipeline"
	}
	return ref
}

func buildPipelineParams(req *modelopsv1alpha1.ModelRequest) tektonv1.Params {
	p := tektonv1.Params{}
	s := req.Spec

	// Model identification
	addParam(&p, "model-id", s.ModelURI)
	addParam(&p, "model-name", s.ModelName)
	addParam(&p, "model-version", withDefault(s.ModelVersion, "v1"))
	addParam(&p, "requested-by", s.RequestedBy)

	// Namespaces
	nsc := &NamespaceConfig{}
	if s.Namespaces != nil {
		nsc = s.Namespaces
	}
	addParam(&p, "target-namespace", withDefault(nsc.Sandbox, "vllm"))
	addParam(&p, "staging-namespace", withDefault(nsc.Staging, "vllm-staging"))

	// ModelCar
	mc := &ModelCarConfig{}
	if s.ModelCar != nil {
		mc = s.ModelCar
	}
	addParam(&p, "modelcar-repo", withDefault(mc.Repo, "redhat-ai-services/modelcar-catalog"))
	addParam(&p, "modelcar-image", mc.Image)

	// Compliance / artifact scan
	cc := &ComplianceConfig{}
	if s.Compliance != nil {
		cc = s.Compliance
	}
	addParam(&p, "artifact-scan-image", withDefault(cc.ArtifactScanImage, "registry.access.redhat.com/ubi9/python-311:latest"))
	addParam(&p, "artifact-cve-threshold", withDefault(cc.CVeThreshold, "critical"))
	addParam(&p, "ignore-unfixed", withDefault(cc.IgnoreUnfixed, "true"))
	addParam(&p, "allowed-architectures", strings.Join(cc.AllowedArchitectures, ","))

	// GPU Advisor
	ga := &GPUAdvisorConfig{}
	if s.GPUAdvisor != nil {
		ga = s.GPUAdvisor
	}
	addParam(&p, "context-length", strconv.Itoa(withDefaultInt(ga.ContextLength, 32768)))
	addParam(&p, "concurrency", strconv.Itoa(withDefaultInt(ga.Concurrency, 4)))
	addParam(&p, "allow-time-slicing", strconv.FormatBool(ga.AllowTimeSlicing))
	addParam(&p, "allow-mig", strconv.FormatBool(ga.AllowMIG))
	addParam(&p, "gpu-isolation-policy", withDefault(ga.GPUIsolationPolicy, "dedicated"))
	addParam(&p, "gpu-operator-namespace", withDefault(ga.GPUOperatorNamespace, "nvidia-gpu-operator"))
	addParam(&p, "clusterpolicy-name", withDefault(ga.ClusterPolicyName, "gpu-cluster-policy"))
	addParam(&p, "time-slicing-configmap", withDefault(ga.TimeSlicingConfigMap, "modelops-time-slicing"))
	addParam(&p, "max-time-slices", strconv.Itoa(withDefaultInt(ga.MaxTimeSlices, 8)))
	addParam(&p, "advisor-endpoint", ga.AdvisorEndpoint)
	addParam(&p, "advisor-secret-name", withDefault(ga.AdvisorSecretName, "gpu-advisor-credentials"))
	addParam(&p, "advisor-timeout-seconds", strconv.Itoa(withDefaultInt(ga.AdvisorTimeoutSeconds, 300)))

	// Deploy
	dc := &DeployConfig{}
	if s.Deploy != nil {
		dc = s.Deploy
	}
	addParam(&p, "release-name", withDefault(dc.ReleaseName, s.ModelName))
	addParam(&p, "chart-url", withDefault(dc.ChartURL, "https://redhat-ai-services.github.io/helm-charts/"))
	addParam(&p, "chart-version", withDefault(dc.ChartVersion, "0.7.1"))
	addParam(&p, "values-content", dc.ValuesContent)
	addParam(&p, "gpu-count-override", dc.GPUCountOverride)
	addParam(&p, "hardware-profile-name", withDefault(dc.HardwareProfileName, "gpu-profile"))
	addParam(&p, "hardware-profile-namespace", withDefault(dc.HardwareProfileNamespace, "redhat-ods-applications"))

	// Security scan (garak via EvalHub)
	ss := &SecurityScanConfig{}
	if s.SecurityScan != nil {
		ss = s.SecurityScan
	}
	addParam(&p, "severity-threshold", withDefault(ss.SeverityThreshold, "block"))
	addParam(&p, "evalhub-url", ss.EvalHubURL)
	addParam(&p, "evalhub-token", ss.EvalHubToken)
	addParam(&p, "tenant-ns", withDefault(ss.TenantNamespace, nsc.Sandbox))
	addParam(&p, "openshift-console-domain", ss.OpenShiftConsoleDomain)

	// Approval
	ac := &ApprovalConfig{}
	if s.Approval != nil {
		ac = s.Approval
	}
	addParam(&p, "approval-api-url", ac.APIURL)
	addParam(&p, "approval-poll-interval-seconds", strconv.Itoa(withDefaultInt(ac.PollIntervalSeconds, 15)))
	addParam(&p, "approval-timeout-seconds", strconv.Itoa(withDefaultInt(ac.TimeoutSeconds, 3600)))

	// Benchmark (GuideLLM)
	bc := &BenchmarkConfig{}
	if s.Benchmark != nil {
		bc = s.Benchmark
	}
	addParam(&p, "guidellm-profile", withDefault(bc.Profile, "constant"))
	addParam(&p, "guidellm-rate", fmt.Sprintf("%.1f", withDefaultFloat(bc.Rate, 4.0)))
	addParam(&p, "guidellm-max-seconds", strconv.Itoa(withDefaultInt(bc.MaxSeconds, 15)))
	addParam(&p, "guidellm-max-requests", strconv.Itoa(withDefaultInt(bc.MaxRequests, 2)))
	addParam(&p, "custom-data", strconv.FormatBool(bc.CustomData))
	addParam(&p, "custom-filename", withDefault(bc.CustomFilename, "no-file"))
	addParam(&p, "huggingface-token", bc.HuggingFaceToken)

	// S3 Storage
	s3 := &S3Config{}
	if s.S3Storage != nil {
		s3 = s.S3Storage
	}
	addParam(&p, "s3-api-endpoint", s3.Endpoint)
	addParam(&p, "s3-access-key-id", s3.AccessKeyID)
	addParam(&p, "s3-secret-access-key", s3.SecretAccessKey)

	// Scan-result S3
	scanS3 := &S3Config{}
	if s.ScanS3Storage != nil {
		scanS3 = s.ScanS3Storage
	}
	addParam(&p, "scan-s3-endpoint", scanS3.Endpoint)
	addParam(&p, "scan-s3-access-key-id", scanS3.AccessKeyID)
	addParam(&p, "scan-s3-secret-access-key", scanS3.SecretAccessKey)
	addParam(&p, "compliance-s3-bucket", withDefault(scanS3.Bucket, "compliance-artifact-results"))
	addParam(&p, "s3-ui-route", scanS3.UIRoute)

	// Security S3 bucket (separate)
	addParam(&p, "security-s3-bucket", "security-scan-results")

	// Model Registry
	mr := &ModelRegistryConfig{}
	if s.ModelRegistry != nil {
		mr = s.ModelRegistry
	}
	addParam(&p, "mr-server", withDefault(mr.Server, "http://modelops-registry.rhoai-model-registries.svc.cluster.local"))
	addParam(&p, "mr-port", withDefault(mr.Port, "8080"))
	addParam(&p, "model-reg-author", withDefault(mr.Author, "ModelOps Platform Team"))

	// Model Access
	ma := &ModelAccessConfig{}
	if s.ModelAccess != nil {
		ma = s.ModelAccess
	}
	addParam(&p, "authorized-viewers", ma.AuthorizedViewers)
	addParam(&p, "access-role", withDefault(ma.AccessRole, "view"))

	// MaaS
	ms := &MaaSConfig{}
	if s.MaaS != nil {
		ms = s.MaaS
	}
	addParam(&p, "deploy-maas", strconv.FormatBool(ms.Enabled))
	addParam(&p, "maas-serving-ns", withDefault(ms.ServingNamespace, "llm"))
	addParam(&p, "maas-policy-ns", withDefault(ms.PolicyNamespace, "models-as-a-service"))
	addParam(&p, "maas-gpu-count", withDefault(ms.GPUCount, "1"))
	addParam(&p, "maas-runtime-image", withDefault(ms.RuntimeImage, "registry.redhat.io/rhaiis/vllm-cuda-rhel9:3.3.0"))
	addParam(&p, "maas-authorized-group", withDefault(ms.AuthorizedGroup, "system:authenticated"))

	// lm-eval
	le := &LMEvalConfig{}
	if s.LMEval != nil {
		le = s.LMEval
	}
	addParam(&p, "lm-eval-job-name", withDefault(le.JobName, "mmlu-jurisprudence-eval-job"))
	addParam(&p, "lm-eval-custom", strconv.FormatBool(le.UseCustom))

	return p
}

type NamespaceConfig struct {
	Sandbox string
	Staging string
}

type ModelCarConfig struct {
	Repo  string
	Image string
}

type ComplianceConfig struct {
	ArtifactScanImage    string
	CVeThreshold         string
	IgnoreUnfixed        string
	AllowedArchitectures []string
}

type GPUAdvisorConfig struct {
	ContextLength         int
	Concurrency           int
	AllowTimeSlicing      bool
	AllowMIG              bool
	GPUIsolationPolicy    string
	GPUOperatorNamespace  string
	ClusterPolicyName     string
	TimeSlicingConfigMap  string
	MaxTimeSlices         int
	AdvisorEndpoint       string
	AdvisorSecretName     string
	AdvisorTimeoutSeconds int
}

type DeployConfig struct {
	ReleaseName              string
	ChartURL                 string
	ChartVersion             string
	ValuesContent            string
	GPUCountOverride         string
	HardwareProfileName      string
	HardwareProfileNamespace string
}

type SecurityScanConfig struct {
	SeverityThreshold       string
	EvalHubURL              string
	EvalHubToken            string
	TenantNamespace         string
	OpenShiftConsoleDomain  string
}

type ApprovalConfig struct {
	APIURL              string
	PollIntervalSeconds int
	TimeoutSeconds      int
}

type BenchmarkConfig struct {
	Profile          string
	Rate             float64
	MaxSeconds       int
	MaxRequests      int
	CustomData       bool
	CustomFilename   string
	HuggingFaceToken string
}

type S3Config struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	UIRoute         string
}

type ModelRegistryConfig struct {
	Server string
	Port   string
	Author string
}

type ModelAccessConfig struct {
	AuthorizedViewers string
	AccessRole        string
}

type MaaSConfig struct {
	Enabled          bool
	ServingNamespace string
	PolicyNamespace  string
	GPUCount         string
	RuntimeImage     string
	AuthorizedGroup  string
}

type LMEvalConfig struct {
	Enabled   bool
	JobName   string
	UseCustom bool
}

func addParam(params *tektonv1.Params, name, value string) {
	if value != "" {
		*params = append(*params, tektonv1.Param{
			Name: name,
			Value: tektonv1.ParamValue{
				Type:      tektonv1.ParamTypeString,
				StringVal: value,
			},
		})
	}
}

func withDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}

func withDefaultInt(val, def int) int {
	if val == 0 {
		return def
	}
	return val
}

func withDefaultFloat(val, def float64) float64 {
	if val == 0.0 {
		return def
	}
	return val
}
