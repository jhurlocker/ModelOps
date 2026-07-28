package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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
// +kubebuilder:rbac:groups=modelops.example.io,resources=modellifecycleprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups=modelops.example.io,resources=platformconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=modelops.example.io,resources=capacityplans,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=modelops.example.io,resources=capacityplans/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=tekton.dev,resources=pipelineruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=get;list;watch;create;update;patch

func (r *ModelRequestReconciler) Reconcile(
	ctx context.Context,
	req ctrl.Request,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var modelRequest modelopsv1alpha1.ModelRequest
	if err := r.Get(ctx, req.NamespacedName, &modelRequest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	profile, err := r.lookupProfile(ctx, &modelRequest)
	if err != nil {
		return r.failRequest(ctx, &modelRequest, "ProfileLookupFailed", err.Error())
	}

	platformConfig, err := r.lookupPlatformConfig(ctx, &modelRequest)
	if err != nil {
		return r.failRequest(ctx, &modelRequest, "PlatformConfigLookupFailed", err.Error())
	}

	capacityPlanName := fmt.Sprintf("%s-capacity", modelRequest.Name)
	sandboxRunName := fmt.Sprintf("%s-sandbox", modelRequest.Name)

	var capacityPlan modelopsv1alpha1.CapacityPlan
	if err := r.Get(ctx, types.NamespacedName{
		Name: capacityPlanName, Namespace: modelRequest.Namespace,
	}, &capacityPlan); apierrors.IsNotFound(err) {
		capacityPlan = r.buildCapacityPlan(&modelRequest, capacityPlanName, profile, platformConfig)
		if err := controllerutil.SetControllerReference(&modelRequest, &capacityPlan, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, &capacityPlan); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("created CapacityPlan", "name", capacityPlan.Name)
		modelRequest.Status.Phase = "CapacityPlanning"
		modelRequest.Status.Message = "Capacity plan created, waiting for GPU advisor"
		if err := r.Status().Update(ctx, &modelRequest); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	if capacityPlan.Status.Phase != "Succeeded" {
		modelRequest.Status.Phase = "CapacityPlanning"
		modelRequest.Status.Message = fmt.Sprintf("Waiting for capacity plan: %s", capacityPlan.Status.Phase)
		if err := r.Status().Update(ctx, &modelRequest); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	secrets, secretErr := r.resolveSecrets(ctx, &modelRequest)
	if secretErr != nil {
		return r.failRequest(ctx, &modelRequest, "SecretLookupFailed", secretErr.Error())
	}

	// PHASE 1: Sandbox pipeline
	sandboxRun := tektonv1.PipelineRun{}
	sandboxKey := types.NamespacedName{Name: sandboxRunName, Namespace: modelRequest.Namespace}
	err = r.Get(ctx, sandboxKey, &sandboxRun)

	if apierrors.IsNotFound(err) {
		if rbacErr := r.ensurePromotionNamespaceRBAC(ctx, modelRequest.Namespace); rbacErr != nil {
			return r.failRequest(ctx, &modelRequest, "RBACSetupFailed", rbacErr.Error())
		}
		sandboxRun = buildPipelineRun(sandboxRunName, modelRequest.Namespace,
			r.sandboxPipelineNameOrDefault(profile, &modelRequest),
			r.buildSandboxPipelineParams(&modelRequest, profile, platformConfig, &capacityPlan, secrets),
			&modelRequest, r.Scheme)
		if err := r.Create(ctx, &sandboxRun); err != nil {
			return ctrl.Result{}, err
		}
		modelRequest.Status.Phase = "SandboxRunning"
		modelRequest.Status.SandboxPipelineRunName = sandboxRun.Name
		modelRequest.Status.PipelineRunName = sandboxRun.Name
		modelRequest.Status.Message = "Sandbox governance pipeline started"
		if err := r.Status().Update(ctx, &modelRequest); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("created sandbox PipelineRun", "pipelineRun", sandboxRun.Name)
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	sandboxCond := sandboxRun.Status.GetCondition("Succeeded")
	if sandboxCond == nil || sandboxCond.Status == corev1.ConditionUnknown {
		return r.updateStatus(ctx, &modelRequest, "SandboxRunning", "Sandbox pipeline is running")
	}
	if sandboxCond.Status == corev1.ConditionFalse {
		return r.updateStatus(ctx, &modelRequest, "Failed", "Sandbox pipeline failed: "+sandboxCond.Message)
	}

	// PHASE 2: Promotion pipelines (one per namespace)
	promoNamespaces := r.getPromotionNamespaces(&modelRequest)
	planID := fmt.Sprintf("%s-promotion", modelRequest.Name)
	pipelineName := r.promotionPipelineNameOrDefault(profile, &modelRequest)

	allSucceeded := true
	anyRunning := false

	for i, ns := range promoNamespaces {
		if err := r.ensurePromotionNamespaceRBAC(ctx, ns); err != nil {
			return r.failRequest(ctx, &modelRequest, "RBACSetupFailed", err.Error())
		}

		prName := fmt.Sprintf("%s-promotion-%s", modelRequest.Name, ns)
		promotionRun := tektonv1.PipelineRun{}
		promotionKey := types.NamespacedName{Name: prName, Namespace: modelRequest.Namespace}
		err = r.Get(ctx, promotionKey, &promotionRun)

		if apierrors.IsNotFound(err) {
			isFirst := i == 0
			isLast := i == len(promoNamespaces)-1
			params := r.buildPromotionPipelineParams(&modelRequest, profile, platformConfig, &capacityPlan, secrets, ns, planID, isFirst, isLast)
			promotionRun = buildPipelineRun(prName, modelRequest.Namespace, pipelineName, params, &modelRequest, r.Scheme)
			if err := r.Create(ctx, &promotionRun); err != nil {
				return ctrl.Result{}, err
			}
			logger.Info("created promotion PipelineRun", "pipelineRun", promotionRun.Name, "namespace", ns)
			allSucceeded = false
			anyRunning = true
			modelRequest.Status.PromotionPipelineRunName = prName
			modelRequest.Status.PipelineRunName = prName
			continue
		}
		if err != nil {
			return ctrl.Result{}, err
		}

		cond := promotionRun.Status.GetCondition("Succeeded")
		if cond == nil || cond.Status == corev1.ConditionUnknown {
			anyRunning = true
			allSucceeded = false
		} else if cond.Status == corev1.ConditionFalse {
			return r.updateStatus(ctx, &modelRequest, "Failed", fmt.Sprintf("Promotion to %s failed: %s", ns, cond.Message))
		} else if cond.Status == corev1.ConditionTrue {
			// this one succeeded, continue checking others
		}
	}

	if anyRunning {
		return r.updateStatus(ctx, &modelRequest, "PromotionRunning", "Promotion pipeline(s) running")
	}

	if allSucceeded {
		return r.updateStatus(ctx, &modelRequest, "Succeeded", "Model onboarding completed successfully")
	}

	return r.updateStatus(ctx, &modelRequest, "PromotionRunning", "Promotion pipeline(s) initiated")
}

func (r *ModelRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&modelopsv1alpha1.ModelRequest{}).
		Owns(&tektonv1.PipelineRun{}).
		Owns(&modelopsv1alpha1.CapacityPlan{}).
		Complete(r)
}

func (r *ModelRequestReconciler) lookupProfile(ctx context.Context, mr *modelopsv1alpha1.ModelRequest) (*modelopsv1alpha1.ModelLifecycleProfile, error) {
	profileName := mr.Spec.LifecycleProfile
	if profileName == "" {
		profileName = "standard-generative-onboarding"
	}

	var profile modelopsv1alpha1.ModelLifecycleProfile
	key := types.NamespacedName{Name: profileName, Namespace: mr.Namespace}
	if err := r.Get(ctx, key, &profile); err != nil {
		return nil, fmt.Errorf("ModelLifecycleProfile %q not found: %w", profileName, err)
	}
	return &profile, nil
}

func (r *ModelRequestReconciler) lookupPlatformConfig(ctx context.Context, mr *modelopsv1alpha1.ModelRequest) (*modelopsv1alpha1.PlatformConfig, error) {
	profile, err := r.lookupProfile(ctx, mr)
	if err != nil {
		return nil, err
	}

	cfgRef := profile.Spec.PlatformConfigRef
	if cfgRef == "" {
		cfgRef = "default-modelops-platform"
	}

	var cfg modelopsv1alpha1.PlatformConfig
	key := types.NamespacedName{Name: cfgRef, Namespace: mr.Namespace}
	if err := r.Get(ctx, key, &cfg); err != nil {
		return nil, fmt.Errorf("PlatformConfig %q not found: %w", cfgRef, err)
	}
	return &cfg, nil
}

func (r *ModelRequestReconciler) sandboxPipelineNameOrDefault(profile *modelopsv1alpha1.ModelLifecycleProfile, mr *modelopsv1alpha1.ModelRequest) string {
	if mr.Spec.PipelineRef != "" {
		return mr.Spec.PipelineRef
	}
	if profile != nil && profile.Spec.Workflow.PipelineRef != "" {
		return profile.Spec.Workflow.PipelineRef
	}
	return "model-intake-sandbox"
}

func (r *ModelRequestReconciler) promotionPipelineNameOrDefault(profile *modelopsv1alpha1.ModelLifecycleProfile, mr *modelopsv1alpha1.ModelRequest) string {
	return "model-intake-promotion"
}

func buildPipelineRun(name, namespace, pipelineName string, params tektonv1.Params, modelReq *modelopsv1alpha1.ModelRequest, scheme *runtime.Scheme) tektonv1.PipelineRun {
	pr := tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"modelops.example.io/model-request": modelReq.Name,
			},
		},
		Spec: tektonv1.PipelineRunSpec{
			PipelineRef: &tektonv1.PipelineRef{
				Name: pipelineName,
			},
			Params: params,
			TaskRunTemplate: tektonv1.PipelineTaskRunTemplate{
				ServiceAccountName: "pipeline",
			},
			Timeouts: &tektonv1.TimeoutFields{
				Pipeline: &metav1.Duration{Duration: 0},
			},
			Workspaces: []tektonv1.WorkspaceBinding{
				{
					Name: "shared-workspace",
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
	controllerutil.SetControllerReference(modelReq, &pr, scheme)
	return pr
}

type resolvedSecrets struct {
	evalhubToken      string
	huggingfaceToken  string
	scanS3Endpoint    string
	scanS3AccessKey   string
	scanS3SecretKey   string
	resultS3Endpoint  string
	resultS3AccessKey string
	resultS3SecretKey string
	advisorAPIKey     string
}

func (r *ModelRequestReconciler) resolveSecrets(ctx context.Context, mr *modelopsv1alpha1.ModelRequest) (*resolvedSecrets, error) {
	s := &resolvedSecrets{}

	if mr.Spec.EvalHubSecretName != "" {
		secret, err := r.readSecret(ctx, mr.Namespace, mr.Spec.EvalHubSecretName)
		if err != nil {
			return nil, err
		}
		s.evalhubToken = string(secret.Data["token"])
		if v, ok := secret.Data["url"]; ok {
			s.scanS3Endpoint = string(v)
		}
	}

	if mr.Spec.HuggingFaceSecretName != "" {
		secret, err := r.readSecret(ctx, mr.Namespace, mr.Spec.HuggingFaceSecretName)
		if err != nil {
			return nil, err
		}
		s.huggingfaceToken = string(secret.Data["token"])
	}

	if mr.Spec.ScanS3SecretName != "" {
		secret, err := r.readSecret(ctx, mr.Namespace, mr.Spec.ScanS3SecretName)
		if err != nil {
			return nil, err
		}
		s.scanS3Endpoint = fromMap(string(secret.Data["endpoint"]), s.scanS3Endpoint)
		s.scanS3AccessKey = string(secret.Data["accessKeyId"])
		s.scanS3SecretKey = string(secret.Data["secretAccessKey"])
	}

	if mr.Spec.ResultS3SecretName != "" {
		secret, err := r.readSecret(ctx, mr.Namespace, mr.Spec.ResultS3SecretName)
		if err != nil {
			return nil, err
		}
		s.resultS3Endpoint = fromMap(string(secret.Data["endpoint"]), s.resultS3Endpoint)
		s.resultS3AccessKey = string(secret.Data["accessKeyId"])
		s.resultS3SecretKey = string(secret.Data["secretAccessKey"])
	}

	if s.resultS3Endpoint == "" {
		s.resultS3Endpoint = "http://minio-service.s3-storage.svc.cluster.local:9000"
	}
	if s.resultS3AccessKey == "" {
		s.resultS3AccessKey = "minio"
	}
	if s.resultS3SecretKey == "" {
		s.resultS3SecretKey = "minio123"
	}

	return s, nil
}

func (r *ModelRequestReconciler) readSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	var secret corev1.Secret
	key := types.NamespacedName{Name: name, Namespace: namespace}
	if err := r.Get(ctx, key, &secret); err != nil {
		return nil, fmt.Errorf("Secret %q not found: %w", name, err)
	}
	return &secret, nil
}

func fromMap(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
}

func (r *ModelRequestReconciler) buildCapacityPlan(
	mr *modelopsv1alpha1.ModelRequest,
	planName string,
	profile *modelopsv1alpha1.ModelLifecycleProfile,
	cfg *modelopsv1alpha1.PlatformConfig,
) modelopsv1alpha1.CapacityPlan {
	spec := mr.Spec
	reqs := spec.Requirements
	if reqs == nil {
		reqs = &modelopsv1alpha1.ModelRequirements{}
	}

	plan := modelopsv1alpha1.CapacityPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      planName,
			Namespace: mr.Namespace,
			Labels: map[string]string{
				"modelops.example.io/model-request": mr.Name,
			},
		},
		Spec: modelopsv1alpha1.CapacityPlanSpec{
			ModelRef: modelopsv1alpha1.CapacityPlanModelRef{
				ModelRequestName: mr.Name,
			},
			ContextLength:         intOrDefault(reqs.ContextLength, 32768),
			Concurrency:           intOrDefault(reqs.ExpectedConcurrency, 4),
			AllowTimeSlicing:      boolOrDefault(reqs.AllowTimeSlicing, true),
			AllowMIG:              boolOrDefault(reqs.AllowMIG, false),
			IsolationPolicy:       strOrDefault(reqs.GPUIsolationPolicy, "dedicated"),
			AdvisorEndpoint:       reqs.AdvisorEndpoint,
			AdvisorSecretName:     cfg.Spec.AdvisorSecretName,
			AdvisorTimeoutSeconds: cfg.Spec.AdvisorTimeoutSeconds,
			GPUOperatorNamespace:  cfg.Spec.GPUOperatorNamespace,
			ClusterPolicyName:     cfg.Spec.ClusterPolicyName,
			TimeSlicingConfigMap:  cfg.Spec.TimeSlicingConfigMap,
			MaxTimeSlices:         intOrDefault(cfg.Spec.MaxTimeSlices, 8),
		},
	}

	return plan
}

func (r *ModelRequestReconciler) buildSandboxPipelineParams(
	mr *modelopsv1alpha1.ModelRequest,
	profile *modelopsv1alpha1.ModelLifecycleProfile,
	cfg *modelopsv1alpha1.PlatformConfig,
	plan *modelopsv1alpha1.CapacityPlan,
	secrets *resolvedSecrets,
) tektonv1.Params {
	p := tektonv1.Params{}
	spec := mr.Spec
	reqs := spec.Requirements
	if reqs == nil {
		reqs = &modelopsv1alpha1.ModelRequirements{}
	}

	addParam(&p, "model-id", spec.Model.URI)
	addParam(&p, "model-name", strOrDefault(spec.Model.Name, "unknown"))
	addParam(&p, "model-version", strOrDefault(spec.Model.Version, "v1"))
	addParam(&p, "requested-by", spec.RequestedBy)

	addParam(&p, "target-namespace", strOrDefault(reqs.SandboxNamespace, "sandbox"))

	addParam(&p, "modelcar-repo", strOrDefault(cfg.Spec.ModelCarRepo, "redhat-ai-services/modelcar-catalog"))
	addParam(&p, "modelcar-image", "")

	addParam(&p, "artifact-scan-image", strOrDefault(cfg.Spec.ComplianceScanImage, "registry.access.redhat.com/ubi9/python-311:latest"))
	addParam(&p, "artifact-cve-threshold", strOrDefault(reqs.CVEThreshold, "critical"))
	addParam(&p, "ignore-unfixed", strOrDefault(cfg.Spec.ComplianceIgnoreUnfixed, "true"))
	addParam(&p, "allowed-architectures", strings.Join(cfg.Spec.ComplianceAllowedArch, ","))

	if plan != nil && plan.Status.GPUsNeeded > 0 {
		addParam(&p, "gpu-count-override", strconv.Itoa(plan.Status.GPUsNeeded))
	}
	addParam(&p, "context-length", strconv.Itoa(intOrDefault(reqs.ContextLength, 32768)))
	addParam(&p, "concurrency", strconv.Itoa(intOrDefault(reqs.ExpectedConcurrency, 4)))
	addParam(&p, "allow-time-slicing", strconv.FormatBool(boolOrDefault(reqs.AllowTimeSlicing, true)))
	addParam(&p, "allow-mig", strconv.FormatBool(boolOrDefault(reqs.AllowMIG, false)))
	addParam(&p, "gpu-isolation-policy", strOrDefault(reqs.GPUIsolationPolicy, "dedicated"))
	addParam(&p, "gpu-operator-namespace", strOrDefault(cfg.Spec.GPUOperatorNamespace, "nvidia-gpu-operator"))
	addParam(&p, "clusterpolicy-name", strOrDefault(cfg.Spec.ClusterPolicyName, "gpu-cluster-policy"))
	addParam(&p, "time-slicing-configmap", strOrDefault(cfg.Spec.TimeSlicingConfigMap, "modelops-time-slicing"))
	addParam(&p, "max-time-slices", strconv.Itoa(intOrDefault(cfg.Spec.MaxTimeSlices, 8)))
	addParam(&p, "advisor-endpoint", reqs.AdvisorEndpoint)
	addParam(&p, "advisor-secret-name", strOrDefault(cfg.Spec.AdvisorSecretName, "gpu-advisor-credentials"))
	addParam(&p, "advisor-timeout-seconds", strconv.Itoa(intOrDefault(cfg.Spec.AdvisorTimeoutSeconds, 300)))

	addParam(&p, "release-name", strOrDefault(spec.Model.Name, "unknown"))
	addParam(&p, "chart-url", strOrDefault(cfg.Spec.ChartURL, "https://redhat-ai-services.github.io/helm-charts/"))
	addParam(&p, "chart-version", strOrDefault(cfg.Spec.ChartVersion, "0.7.1"))
	addParam(&p, "values-content", reqs.ValuesContent)
	addParam(&p, "gpu-count-override", reqs.GPUCountOverride)
	addParam(&p, "hardware-profile-name", strOrDefault(cfg.Spec.HardwareProfileName, "gpu-profile"))
	addParam(&p, "hardware-profile-namespace", strOrDefault(cfg.Spec.HardwareProfileNamespace, "redhat-ods-applications"))

	addParam(&p, "severity-threshold", strOrDefault(reqs.SecurityThreshold, "block"))
	addParam(&p, "evalhub-url", cfg.Spec.EvalHubURL)
	addParam(&p, "evalhub-token", secrets.evalhubToken)
	addParam(&p, "tenant-ns", strOrDefault(reqs.SandboxNamespace, "vllm"))
	addParam(&p, "openshift-console-domain", reqs.OpenShiftConsoleDomain)

	addParam(&p, "huggingface-token", secrets.huggingfaceToken)

	addParam(&p, "scan-s3-endpoint", secrets.scanS3Endpoint)
	addParam(&p, "scan-s3-access-key-id", secrets.scanS3AccessKey)
	addParam(&p, "scan-s3-secret-access-key", secrets.scanS3SecretKey)
	addParam(&p, "compliance-s3-bucket", strOrDefault(cfg.Spec.ComplianceS3Bucket, "compliance-artifact-results"))
	addParam(&p, "security-s3-bucket", strOrDefault(cfg.Spec.SecurityS3Bucket, "security-scan-results"))
	addParam(&p, "s3-ui-route", "")

	addParam(&p, "s3-api-endpoint", secrets.resultS3Endpoint)
	addParam(&p, "s3-access-key-id", secrets.resultS3AccessKey)
	addParam(&p, "s3-secret-access-key", secrets.resultS3SecretKey)

	addParam(&p, "mr-server", strOrDefault(cfg.Spec.RegistryServer, "http://modelops-registry.rhoai-model-registries.svc.cluster.local"))
	addParam(&p, "mr-port", strOrDefault(cfg.Spec.RegistryPort, "8080"))
	addParam(&p, "model-reg-author", strOrDefault(cfg.Spec.RegistryAuthor, "ModelOps Platform Team"))

	return p
}

func (r *ModelRequestReconciler) ensurePromotionNamespaceRBAC(ctx context.Context, namespace string) error {
	logger := log.FromContext(ctx)

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pipeline",
			Namespace: namespace,
		},
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(sa), &corev1.ServiceAccount{}); apierrors.IsNotFound(err) {
		if err := r.Create(ctx, sa); err != nil {
			return fmt.Errorf("failed to create pipeline SA in %s: %w", namespace, err)
		}
		logger.Info("created pipeline ServiceAccount", "namespace", namespace)
	}

	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pipeline-edit",
			Namespace: namespace,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "edit",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "pipeline",
				Namespace: namespace,
			},
		},
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(rb), &rbacv1.RoleBinding{}); apierrors.IsNotFound(err) {
		if err := r.Create(ctx, rb); err != nil {
			return fmt.Errorf("failed to create pipeline-edit RoleBinding in %s: %w", namespace, err)
		}
		logger.Info("created pipeline-edit RoleBinding", "namespace", namespace)
	}

	evalhubCrb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-pipeline-evalhub", namespace),
			Labels: map[string]string{
				"app.kubernetes.io/part-of":     "modelops",
				"app.kubernetes.io/managed-by":  "modelops-operator",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "pipeline-evalhub-submitter",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "pipeline",
				Namespace: namespace,
			},
		},
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(evalhubCrb), &rbacv1.ClusterRoleBinding{}); apierrors.IsNotFound(err) {
		if err := r.Create(ctx, evalhubCrb); err != nil {
			return fmt.Errorf("failed to create evalhub ClusterRoleBinding for %s: %w", namespace, err)
		}
		logger.Info("created evalhub ClusterRoleBinding", "namespace", namespace)
	}

	return nil
}

func (r *ModelRequestReconciler) getPromotionNamespaces(mr *modelopsv1alpha1.ModelRequest) []string {
	reqs := mr.Spec.Requirements
	if reqs == nil {
		return []string{"staging"}
	}
	if len(reqs.PromotionNamespaces) > 0 {
		return reqs.PromotionNamespaces
	}
	if reqs.StagingNamespace != "" {
		return []string{reqs.StagingNamespace}
	}
	return []string{"staging"}
}

func (r *ModelRequestReconciler) buildPromotionPipelineParams(
	mr *modelopsv1alpha1.ModelRequest,
	profile *modelopsv1alpha1.ModelLifecycleProfile,
	cfg *modelopsv1alpha1.PlatformConfig,
	plan *modelopsv1alpha1.CapacityPlan,
	secrets *resolvedSecrets,
	targetNamespace string,
	planID string,
	isFirst bool,
	isLast bool,
) tektonv1.Params {
	p := tektonv1.Params{}
	spec := mr.Spec
	reqs := spec.Requirements
	if reqs == nil {
		reqs = &modelopsv1alpha1.ModelRequirements{}
	}

	addParam(&p, "model-id", spec.Model.URI)
	addParam(&p, "model-name", strOrDefault(spec.Model.Name, "unknown"))
	addParam(&p, "model-version", strOrDefault(spec.Model.Version, "v1"))
	addParam(&p, "requested-by", spec.RequestedBy)

	addParam(&p, "target-namespace", targetNamespace)
	addParam(&p, "plan-id", planID)

	addParam(&p, "modelcar-repo", strOrDefault(cfg.Spec.ModelCarRepo, "redhat-ai-services/modelcar-catalog"))
	addParam(&p, "modelcar-image", "")

	if plan != nil && plan.Status.GPUsNeeded > 0 {
		addParam(&p, "gpu-count-override", strconv.Itoa(plan.Status.GPUsNeeded))
	}
	addParam(&p, "context-length", strconv.Itoa(intOrDefault(reqs.ContextLength, 32768)))
	addParam(&p, "concurrency", strconv.Itoa(intOrDefault(reqs.ExpectedConcurrency, 4)))
	addParam(&p, "allow-time-slicing", strconv.FormatBool(boolOrDefault(reqs.AllowTimeSlicing, true)))
	addParam(&p, "allow-mig", strconv.FormatBool(boolOrDefault(reqs.AllowMIG, false)))
	addParam(&p, "gpu-isolation-policy", strOrDefault(reqs.GPUIsolationPolicy, "dedicated"))
	addParam(&p, "gpu-operator-namespace", strOrDefault(cfg.Spec.GPUOperatorNamespace, "nvidia-gpu-operator"))
	addParam(&p, "clusterpolicy-name", strOrDefault(cfg.Spec.ClusterPolicyName, "gpu-cluster-policy"))
	addParam(&p, "time-slicing-configmap", strOrDefault(cfg.Spec.TimeSlicingConfigMap, "modelops-time-slicing"))
	addParam(&p, "max-time-slices", strconv.Itoa(intOrDefault(cfg.Spec.MaxTimeSlices, 8)))
	addParam(&p, "advisor-endpoint", reqs.AdvisorEndpoint)
	addParam(&p, "advisor-secret-name", strOrDefault(cfg.Spec.AdvisorSecretName, "gpu-advisor-credentials"))
	addParam(&p, "advisor-timeout-seconds", strconv.Itoa(intOrDefault(cfg.Spec.AdvisorTimeoutSeconds, 300)))

	addParam(&p, "release-name", strOrDefault(spec.Model.Name, "unknown"))
	addParam(&p, "chart-url", strOrDefault(cfg.Spec.ChartURL, "https://redhat-ai-services.github.io/helm-charts/"))
	addParam(&p, "chart-version", strOrDefault(cfg.Spec.ChartVersion, "0.7.1"))
	addParam(&p, "values-content", reqs.ValuesContent)
	addParam(&p, "hardware-profile-name", strOrDefault(cfg.Spec.HardwareProfileName, "gpu-profile"))
	addParam(&p, "hardware-profile-namespace", strOrDefault(cfg.Spec.HardwareProfileNamespace, "redhat-ods-applications"))

	approvalURL := strOrDefault(cfg.Spec.ApprovalApiUrl, "")
	if !isFirst {
		approvalURL = ""
	}
	addParam(&p, "approval-api-url", approvalURL)
	addParam(&p, "approval-poll-interval-seconds", strconv.Itoa(intOrDefault(cfg.Spec.ApprovalPollIntervalSeconds, 15)))
	addParam(&p, "approval-timeout-seconds", strconv.Itoa(intOrDefault(cfg.Spec.ApprovalTimeoutSeconds, 3600)))

	addParam(&p, "evalhub-url", cfg.Spec.EvalHubURL)
	addParam(&p, "evalhub-token", secrets.evalhubToken)
	addParam(&p, "openshift-console-domain", reqs.OpenShiftConsoleDomain)

	addParam(&p, "guidellm-profile", strOrDefault(cfg.Spec.BenchmarkProfile, "constant"))
	addParam(&p, "guidellm-rate", fmt.Sprintf("%.1f", floatOrDefault(cfg.Spec.BenchmarkRate, 4.0)))
	addParam(&p, "guidellm-max-seconds", strconv.Itoa(intOrDefault(cfg.Spec.BenchmarkMaxSeconds, 15)))
	addParam(&p, "guidellm-max-requests", strconv.Itoa(intOrDefault(cfg.Spec.BenchmarkMaxRequests, 2)))
	addParam(&p, "custom-data", strconv.FormatBool(reqs.CustomBenchmarkData))
	addParam(&p, "custom-filename", strOrDefault(reqs.CustomBenchmarkFile, "no-file"))
	addParam(&p, "huggingface-token", secrets.huggingfaceToken)

	addParam(&p, "s3-api-endpoint", secrets.resultS3Endpoint)
	addParam(&p, "s3-access-key-id", secrets.resultS3AccessKey)
	addParam(&p, "s3-secret-access-key", secrets.resultS3SecretKey)

	addParam(&p, "mr-server", strOrDefault(cfg.Spec.RegistryServer, "http://modelops-registry.rhoai-model-registries.svc.cluster.local"))
	addParam(&p, "mr-port", strOrDefault(cfg.Spec.RegistryPort, "8080"))
	addParam(&p, "model-reg-author", strOrDefault(cfg.Spec.RegistryAuthor, "ModelOps Platform Team"))

	if spec.Access != nil {
		addParam(&p, "authorized-viewers", spec.Access.AuthorizedViewers)
		addParam(&p, "access-role", strOrDefault(spec.Access.AccessRole, "view"))
	}

	maasGPU := strOrDefault(cfg.Spec.MaaSGPUCount, "1")
	if spec.MaaS != nil {
		addParam(&p, "deploy-maas", strconv.FormatBool(spec.MaaS.Enabled))
		maasGPU = strOrDefault(spec.MaaS.GPUCount, maasGPU)
	} else {
		addParam(&p, "deploy-maas", "false")
	}
	addParam(&p, "maas-serving-ns", strOrDefault(cfg.Spec.MaaSServingNS, "llm"))
	addParam(&p, "maas-policy-ns", strOrDefault(cfg.Spec.MaaSPolicyNS, "models-as-a-service"))
	addParam(&p, "maas-gpu-count", maasGPU)
	addParam(&p, "maas-runtime-image", strOrDefault(cfg.Spec.MaaSRuntimeImage, "registry.redhat.io/rhaiis/vllm-cuda-rhel9:3.3.0"))
	addParam(&p, "maas-authorized-group", strOrDefault(cfg.Spec.MaaSAuthorizedGroup, "system:authenticated"))

	addParam(&p, "run-register", strconv.FormatBool(isLast))

	return p
}

func (r *ModelRequestReconciler) failRequest(ctx context.Context, mr *modelopsv1alpha1.ModelRequest, phase, message string) (ctrl.Result, error) {
	if mr.Status.Phase == phase && mr.Status.Message == message {
		return ctrl.Result{}, nil
	}
	mr.Status.Phase = phase
	mr.Status.Message = message
	if err := r.Status().Update(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *ModelRequestReconciler) updateStatus(ctx context.Context, request *modelopsv1alpha1.ModelRequest, phase string, message string) (ctrl.Result, error) {
	if request.Status.Phase == phase && request.Status.Message == message {
		return ctrl.Result{}, nil
	}
	request.Status.Phase = phase
	request.Status.Message = message
	if err := r.Status().Update(ctx, request); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
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

func strOrDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}

func intOrDefault(val, def int) int {
	if val == 0 {
		return def
	}
	return val
}

func floatOrDefault(val, def float64) float64 {
	if val == 0.0 {
		return def
	}
	return val
}

func boolOrDefault(val *bool, def bool) bool {
	if val == nil {
		return def
	}
	return *val
}
