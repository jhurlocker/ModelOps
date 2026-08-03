package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
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

// transientErrorRequeueDelay backs off retries for Create calls that fail
// with something other than AlreadyExists (e.g. a momentarily unavailable
// API server), so the controller doesn't hot-loop as fast as the
// workqueue allows against a persistent problem.
const transientErrorRequeueDelay = 5 * time.Second

// createIgnoringAlreadyExists creates obj, treating AlreadyExists as a
// harmless no-op (created=false, err=nil) instead of a reconcile-failing
// error. This makes Create calls for child objects idempotent against
// races where a prior Get saw the object as missing but it was created
// by a concurrent/earlier reconcile before this Create landed. Any other
// error is returned as-is for the caller to handle (typically with a
// backoff requeue).
func createIgnoringAlreadyExists(ctx context.Context, c client.Client, obj client.Object) (created bool, err error) {
	if err := c.Create(ctx, obj); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

type ModelRequestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// StageRunner drives sandbox and promotion stage execution
	// (Tekton PipelineRuns today via internal/stages/tekton.StageRunner,
	// injected at manager setup; a fake implementation in tests -- see
	// internal/stagecommon.StageRunner/REFACTOR_PLAN.md Phase 4). The
	// reconciler never constructs a PipelineRun or reads a Tekton
	// condition directly.
	StageRunner stagecommon.StageRunner
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
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create
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
		if created, err := createIgnoringAlreadyExists(ctx, r.Client, &capacityPlan); err != nil {
			return ctrl.Result{RequeueAfter: transientErrorRequeueDelay}, err
		} else if created {
			logger.Info("created CapacityPlan", "name", capacityPlan.Name)
		}
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

	if labelErr := r.ensureEvalHubTenantLabel(ctx, modelRequest.Namespace); labelErr != nil {
		logger.Error(labelErr, "failed to label namespace for EvalHub")
	}

	secrets, secretErr := r.resolveSecrets(ctx, &modelRequest)
	if secretErr != nil {
		return r.failRequest(ctx, &modelRequest, "SecretLookupFailed", secretErr.Error())
	}

	// PHASE 1: Sandbox stage
	if rbacErr := r.ensurePromotionNamespaceRBAC(ctx, modelRequest.Namespace, modelRequest.Namespace); rbacErr != nil {
		return r.failRequest(ctx, &modelRequest, "RBACSetupFailed", rbacErr.Error())
	}
	sandboxStatus, err := r.StageRunner.EnsureRun(ctx, &modelRequest, stagecommon.StageSpec{
		Name:        "sandbox",
		RunName:     sandboxRunName,
		WorkflowRef: r.sandboxPipelineNameOrDefault(profile, &modelRequest),
		Params:      r.buildSandboxPipelineParams(&modelRequest, profile, platformConfig, &capacityPlan, secrets),
	})
	if err != nil {
		return ctrl.Result{RequeueAfter: transientErrorRequeueDelay}, err
	}
	modelRequest.Status.SandboxPipelineRunName = sandboxRunName
	modelRequest.Status.PipelineRunName = sandboxRunName

	switch sandboxStatus.Phase {
	case stagecommon.StageRunning:
		logger.Info("sandbox stage running", "runName", sandboxRunName)
		return r.updateStatus(ctx, &modelRequest, "SandboxRunning", sandboxStatus.Message)
	case stagecommon.StageFailed:
		return r.updateStatus(ctx, &modelRequest, "Failed", "Sandbox pipeline failed: "+sandboxStatus.Message)
	}
	// stagecommon.StageSucceeded: fall through to promotion.

	// PHASE 2: Promotion stages (one per namespace)
	promoNamespaces := r.getPromotionNamespaces(&modelRequest)
	planID := fmt.Sprintf("%s-promotion", modelRequest.Name)
	pipelineName := r.promotionPipelineNameOrDefault(profile, &modelRequest)

	allSucceeded := true
	anyRunning := false

	for i, ns := range promoNamespaces {
		if err := r.ensurePromotionNamespaceRBAC(ctx, ns, modelRequest.Namespace); err != nil {
			return r.failRequest(ctx, &modelRequest, "RBACSetupFailed", err.Error())
		}
		if err := r.ensureMaaSNamespaceLabels(ctx, ns); err != nil {
			return r.failRequest(ctx, &modelRequest, "NamespaceSetupFailed", err.Error())
		}

		prName := fmt.Sprintf("%s-promotion-%s", modelRequest.Name, ns)
		isFirst := i == 0
		isLast := i == len(promoNamespaces)-1
		params := r.buildPromotionPipelineParams(&modelRequest, profile, platformConfig, &capacityPlan, secrets, ns, planID, isFirst, isLast)

		promoStatus, err := r.StageRunner.EnsureRun(ctx, &modelRequest, stagecommon.StageSpec{
			Name:        fmt.Sprintf("promotion-%s", ns),
			RunName:     prName,
			WorkflowRef: pipelineName,
			Params:      params,
		})
		if err != nil {
			return ctrl.Result{RequeueAfter: transientErrorRequeueDelay}, err
		}
		modelRequest.Status.PromotionPipelineRunName = prName
		modelRequest.Status.PipelineRunName = prName

		switch promoStatus.Phase {
		case stagecommon.StageFailed:
			return r.updateStatus(ctx, &modelRequest, "Failed", fmt.Sprintf("Promotion to %s failed: %s", ns, promoStatus.Message))
		case stagecommon.StageRunning:
			anyRunning = true
			allSucceeded = false
		case stagecommon.StageSucceeded:
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
	if profile != nil && profile.Spec.Workflow.PromotionPipelineRef != "" {
		return profile.Spec.Workflow.PromotionPipelineRef
	}
	return "model-intake-promotion"
}

// buildPipelineRun, and the PipelineRun construction/condition-reading
// it used to be paired with inline in Reconcile, moved to
// internal/stages/tekton.StageRunner in Phase 4 of REFACTOR_PLAN.md.
// ModelRequestReconciler now drives both the sandbox and promotion
// stages through r.StageRunner (stagecommon.StageRunner) instead.

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
		if v, ok := secret.Data["url"]; ok {
			s.scanS3Endpoint = string(v)
		}
	}
	if s.evalhubToken == "" {
		token, err := r.generateServiceAccountToken(ctx, mr.Namespace, "pipeline")
		if err != nil {
			logger := log.FromContext(ctx)
			logger.Error(err, "failed to generate EvalHub token for pipeline SA, falling back to empty")
		} else {
			s.evalhubToken = token
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

	// Endpoint is not a credential -- a hardcoded default cluster-local
	// service address is fine to fall back to. Access/secret keys are
	// credentials and must come from a real Secret; no hardcoded
	// credential fallback is allowed (see AGENTS.md: "Never store
	// plaintext credentials..."). If no *SecretName was configured (or
	// the referenced Secret didn't populate these keys), fail loudly
	// instead of silently defaulting to a known credential pair.
	if s.scanS3Endpoint == "" {
		s.scanS3Endpoint = "http://minio.modelops-storage.svc.cluster.local:9000"
	}
	if s.scanS3AccessKey == "" || s.scanS3SecretKey == "" {
		return nil, fmt.Errorf("no scan storage credentials configured: set spec.scanS3SecretName to a Secret with accessKeyId/secretAccessKey keys")
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
		s.resultS3Endpoint = "http://minio.modelops-storage.svc.cluster.local:9000"
	}
	if mr.Spec.ResultS3Endpoint != "" {
		s.resultS3Endpoint = mr.Spec.ResultS3Endpoint
	}
	if s.resultS3AccessKey == "" || s.resultS3SecretKey == "" {
		return nil, fmt.Errorf("no result storage credentials configured: set spec.resultS3SecretName to a Secret with accessKeyId/secretAccessKey keys")
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

func (r *ModelRequestReconciler) generateServiceAccountToken(ctx context.Context, namespace, saName string) (string, error) {
	tr := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: ptrInt64(86400),
		},
	}
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: namespace,
		},
	}
	if err := r.SubResource("token").Create(ctx, sa, tr); err != nil {
		return "", fmt.Errorf("failed to create token for %s/%s: %w", namespace, saName, err)
	}
	return tr.Status.Token, nil
}

func ptrInt64(i int64) *int64 {
	return &i
}

func (r *ModelRequestReconciler) ensureEvalHubTenantLabel(ctx context.Context, namespace string) error {
	var ns corev1.Namespace
	if err := r.Get(ctx, types.NamespacedName{Name: namespace}, &ns); err != nil {
		return fmt.Errorf("failed to get namespace %s: %w", namespace, err)
	}
	if ns.Labels == nil {
		ns.Labels = map[string]string{}
	}
	if _, ok := ns.Labels["evalhub.trustyai.opendatahub.io/tenant"]; ok {
		return nil
	}
	ns.Labels["evalhub.trustyai.opendatahub.io/tenant"] = ""
	if err := r.Update(ctx, &ns); err != nil {
		return fmt.Errorf("failed to label namespace %s: %w", namespace, err)
	}
	log.FromContext(ctx).Info("added evalhub tenant label to namespace", "namespace", namespace)
	return nil
}

func (r *ModelRequestReconciler) ensureMaaSNamespaceLabels(ctx context.Context, namespace string) error {
	var ns corev1.Namespace
	if err := r.Get(ctx, types.NamespacedName{Name: namespace}, &ns); err != nil {
		return fmt.Errorf("failed to get namespace %s: %w", namespace, err)
	}
	if ns.Labels == nil {
		ns.Labels = map[string]string{}
	}
	needsUpdate := false
	for _, l := range []struct{ k, v string }{
		{"opendatahub.io/generated-namespace", "true"},
		{"maas.opendatahub.io/gateway-access", "true"},
		{"opendatahub.io/dashboard", "true"},
	} {
		if ns.Labels[l.k] != l.v {
			ns.Labels[l.k] = l.v
			needsUpdate = true
		}
	}
	if !needsUpdate {
		return nil
	}
	if err := r.Update(ctx, &ns); err != nil {
		return fmt.Errorf("failed to label namespace %s for MaaS: %w", namespace, err)
	}
	log.FromContext(ctx).Info("added MaaS labels to namespace", "namespace", namespace)
	return nil
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
			ContextLength:         stagecommon.IntOrDefault(reqs.BenchmarkTargets.ContextLength, 32768),
			Concurrency:           stagecommon.IntOrDefault(reqs.BenchmarkTargets.ExpectedConcurrency, 4),
			AllowTimeSlicing:      stagecommon.BoolOrDefault(reqs.GPUConfig.AllowTimeSlicing, true),
			AllowMIG:              stagecommon.BoolOrDefault(reqs.GPUConfig.AllowMIG, false),
			IsolationPolicy:       stagecommon.StrOrDefault(reqs.GPUConfig.GPUIsolationPolicy, "dedicated"),
			AdvisorEndpoint:       reqs.AdvisorEndpoint,
			AdvisorSecretName:     cfg.Spec.AdvisorSecretName,
			AdvisorTimeoutSeconds: cfg.Spec.AdvisorTimeoutSeconds,
			GPUOperatorNamespace:  cfg.Spec.GPUOperatorNamespace,
			ClusterPolicyName:     cfg.Spec.ClusterPolicyName,
			TimeSlicingConfigMap:  cfg.Spec.TimeSlicingConfigMap,
			MaxTimeSlices:         stagecommon.IntOrDefault(cfg.Spec.MaxTimeSlices, 8),
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
) map[string]string {
	spec := mr.Spec
	reqs := spec.Requirements
	if reqs == nil {
		reqs = &modelopsv1alpha1.ModelRequirements{}
	}

	p := stagecommon.BuildCommonModelParams(spec, reqs, cfg, stagecommon.Secrets{
		EvalHubToken:      secrets.evalhubToken,
		HuggingFaceToken:  secrets.huggingfaceToken,
		ResultS3Endpoint:  secrets.resultS3Endpoint,
		ResultS3AccessKey: secrets.resultS3AccessKey,
		ResultS3SecretKey: secrets.resultS3SecretKey,
	})

	stagecommon.AddParam(p, "target-namespace", stagecommon.StrOrDefault(reqs.SandboxNamespace, "sandbox"))

	stagecommon.AddParam(p, "artifact-scan-image", stagecommon.StrOrDefault(cfg.Spec.ComplianceScanImage, "registry.access.redhat.com/ubi9/python-311:latest"))
	stagecommon.AddParam(p, "artifact-cve-threshold", stagecommon.StrOrDefault(reqs.SecurityConfig.CVEThreshold, "critical"))
	stagecommon.AddParam(p, "ignore-unfixed", stagecommon.StrOrDefault(cfg.Spec.ComplianceIgnoreUnfixed, "true"))
	stagecommon.AddParam(p, "allowed-architectures", strings.Join(cfg.Spec.ComplianceAllowedArch, ","))

	// Exactly one gpu-count-override param: an explicit
	// reqs.GPUConfig.GPUCountOverride always wins over the
	// CapacityPlan-derived value; the plan-derived value is only used as
	// a fallback when no override was set. This stays here (not in
	// stagecommon.BuildCommonModelParams) because buildPromotionPipelineParams
	// computes this param differently -- see stagecommon/params.go's doc
	// comment for why folding it into the shared helper isn't safe.
	if reqs.GPUConfig.GPUCountOverride != "" {
		stagecommon.AddParam(p, "gpu-count-override", reqs.GPUConfig.GPUCountOverride)
	} else if plan != nil && plan.Status.GPUsNeeded > 0 {
		stagecommon.AddParam(p, "gpu-count-override", strconv.Itoa(plan.Status.GPUsNeeded))
	}

	stagecommon.AddParam(p, "severity-threshold", stagecommon.StrOrDefault(reqs.SecurityConfig.SecurityThreshold, "block"))
	stagecommon.AddParam(p, "tenant-ns", stagecommon.StrOrDefault(reqs.SandboxNamespace, "vllm"))

	stagecommon.AddParam(p, "scan-s3-endpoint", secrets.scanS3Endpoint)
	stagecommon.AddParam(p, "scan-s3-access-key-id", secrets.scanS3AccessKey)
	stagecommon.AddParam(p, "scan-s3-secret-access-key", secrets.scanS3SecretKey)
	compBucket := stagecommon.StrOrDefault(spec.ResultS3Bucket, stagecommon.StrOrDefault(cfg.Spec.ComplianceS3Bucket, "compliance-artifact-results"))
	secBucket := stagecommon.StrOrDefault(spec.ResultS3Bucket, stagecommon.StrOrDefault(cfg.Spec.SecurityS3Bucket, "security-scan-results"))
	stagecommon.AddParam(p, "compliance-s3-bucket", compBucket)
	stagecommon.AddParam(p, "security-s3-bucket", secBucket)
	stagecommon.AddParam(p, "s3-ui-route", "")

	return p
}

func (r *ModelRequestReconciler) ensurePromotionNamespaceRBAC(ctx context.Context, targetNS, sourceNS string) error {
	logger := log.FromContext(ctx)

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pipeline",
			Namespace: targetNS,
		},
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(sa), &corev1.ServiceAccount{}); apierrors.IsNotFound(err) {
		// Only attempt Create when we've confirmed the object is absent:
		// RBAC-granting objects like the RoleBindings/ClusterRoleBinding
		// below can trip the API server's privilege-escalation check on
		// *any* Create attempt (even a harmless no-op re-create of an
		// object that already exists exactly as desired) if the
		// controller's own ServiceAccount doesn't itself hold every
		// permission being granted. createIgnoringAlreadyExists still
		// guards the narrow race between this Get and the Create below.
		if created, err := createIgnoringAlreadyExists(ctx, r.Client, sa); err != nil {
			return fmt.Errorf("failed to create pipeline SA in %s: %w", targetNS, err)
		} else if created {
			logger.Info("created pipeline ServiceAccount", "namespace", targetNS)
		}
	} else if err != nil {
		return fmt.Errorf("failed to check for existing pipeline SA in %s: %w", targetNS, err)
	}

	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pipeline-edit",
			Namespace: targetNS,
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
				Namespace: sourceNS,
			},
		},
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(rb), &rbacv1.RoleBinding{}); apierrors.IsNotFound(err) {
		if created, err := createIgnoringAlreadyExists(ctx, r.Client, rb); err != nil {
			return fmt.Errorf("failed to create pipeline-edit RoleBinding in %s: %w", targetNS, err)
		} else if created {
			logger.Info("created pipeline-edit RoleBinding", "namespace", targetNS)
		}
	} else if err != nil {
		return fmt.Errorf("failed to check for existing pipeline-edit RoleBinding in %s: %w", targetNS, err)
	}

	maasRb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pipeline-maas-deployer",
			Namespace: targetNS,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "pipeline-maas-deployer",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "pipeline",
				Namespace: sourceNS,
			},
		},
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(maasRb), &rbacv1.RoleBinding{}); apierrors.IsNotFound(err) {
		if created, err := createIgnoringAlreadyExists(ctx, r.Client, maasRb); err != nil {
			return fmt.Errorf("failed to create pipeline-maas-deployer RoleBinding in %s: %w", targetNS, err)
		} else if created {
			logger.Info("created pipeline-maas-deployer RoleBinding", "namespace", targetNS)
		}
	} else if err != nil {
		return fmt.Errorf("failed to check for existing pipeline-maas-deployer RoleBinding in %s: %w", targetNS, err)
	}

	evalhubCrb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-pipeline-evalhub", sourceNS),
			Labels: map[string]string{
				"app.kubernetes.io/part-of":    "modelops",
				"app.kubernetes.io/managed-by": "modelops-operator",
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
				Namespace: sourceNS,
			},
		},
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(evalhubCrb), &rbacv1.ClusterRoleBinding{}); apierrors.IsNotFound(err) {
		if created, err := createIgnoringAlreadyExists(ctx, r.Client, evalhubCrb); err != nil {
			return fmt.Errorf("failed to create evalhub ClusterRoleBinding for %s: %w", targetNS, err)
		} else if created {
			logger.Info("created evalhub ClusterRoleBinding", "namespace", targetNS)
		}
	} else if err != nil {
		return fmt.Errorf("failed to check for existing evalhub ClusterRoleBinding for %s: %w", targetNS, err)
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
) map[string]string {
	spec := mr.Spec
	reqs := spec.Requirements
	if reqs == nil {
		reqs = &modelopsv1alpha1.ModelRequirements{}
	}

	p := stagecommon.BuildCommonModelParams(spec, reqs, cfg, stagecommon.Secrets{
		EvalHubToken:      secrets.evalhubToken,
		HuggingFaceToken:  secrets.huggingfaceToken,
		ResultS3Endpoint:  secrets.resultS3Endpoint,
		ResultS3AccessKey: secrets.resultS3AccessKey,
		ResultS3SecretKey: secrets.resultS3SecretKey,
	})

	stagecommon.AddParam(p, "target-namespace", targetNamespace)
	stagecommon.AddParam(p, "plan-id", planID)

	// KNOWN BEHAVIOR, unchanged by this refactor: unlike
	// buildSandboxPipelineParams, this never checks
	// reqs.GPUConfig.GPUCountOverride -- only the CapacityPlan-derived
	// value is ever used here. See stagecommon/params.go's doc comment
	// for why gpu-count-override is deliberately kept out of the shared
	// helper instead of being unified with sandbox's override-aware
	// logic.
	if plan != nil && plan.Status.GPUsNeeded > 0 {
		stagecommon.AddParam(p, "gpu-count-override", strconv.Itoa(plan.Status.GPUsNeeded))
	}

	approvalURL := stagecommon.StrOrDefault(cfg.Spec.ApprovalApiUrl, "")
	if !isFirst {
		approvalURL = ""
	}
	stagecommon.AddParam(p, "approval-api-url", approvalURL)
	stagecommon.AddParam(p, "approval-poll-interval-seconds", strconv.Itoa(stagecommon.IntOrDefault(cfg.Spec.ApprovalPollIntervalSeconds, 15)))
	stagecommon.AddParam(p, "approval-timeout-seconds", strconv.Itoa(stagecommon.IntOrDefault(cfg.Spec.ApprovalTimeoutSeconds, 3600)))

	stagecommon.AddParam(p, "guidellm-profile", stagecommon.StrOrDefault(cfg.Spec.BenchmarkProfile, "constant"))
	stagecommon.AddParam(p, "guidellm-rate", fmt.Sprintf("%.1f", floatOrDefault(cfg.Spec.BenchmarkRate, 4.0)))
	stagecommon.AddParam(p, "guidellm-max-seconds", strconv.Itoa(stagecommon.IntOrDefault(cfg.Spec.BenchmarkMaxSeconds, 15)))
	stagecommon.AddParam(p, "guidellm-max-requests", strconv.Itoa(stagecommon.IntOrDefault(cfg.Spec.BenchmarkMaxRequests, 2)))
	if cfg.Spec.BenchmarkTargetUrl != "" {
		stagecommon.AddParam(p, "benchmark-target-url", cfg.Spec.BenchmarkTargetUrl)
	} else if spec.MaaS != nil && spec.MaaS.Enabled {
		stagecommon.AddParam(p, "benchmark-target-url", fmt.Sprintf("https://%s-kserve-workload-svc.%s.svc.cluster.local:8000/v1", stagecommon.StrOrDefault(spec.Model.Name, "unknown"), targetNamespace))
	} else {
		stagecommon.AddParam(p, "benchmark-target-url", fmt.Sprintf("http://%s-predictor.%s.svc.cluster.local:8080/v1", stagecommon.StrOrDefault(spec.Model.Name, "unknown"), targetNamespace))
	}
	stagecommon.AddParam(p, "custom-data", strconv.FormatBool(reqs.SecurityConfig.CustomBenchmarkData))
	stagecommon.AddParam(p, "custom-filename", stagecommon.StrOrDefault(reqs.SecurityConfig.CustomBenchmarkFile, "no-file"))

	if spec.Access != nil {
		stagecommon.AddParam(p, "authorized-viewers", spec.Access.AuthorizedViewers)
		stagecommon.AddParam(p, "access-role", stagecommon.StrOrDefault(spec.Access.AccessRole, "view"))
	}

	maasGPU := stagecommon.StrOrDefault(cfg.Spec.MaaSGPUCount, "1")
	if spec.MaaS != nil {
		stagecommon.AddParam(p, "deploy-maas", strconv.FormatBool(spec.MaaS.Enabled))
		maasGPU = stagecommon.StrOrDefault(spec.MaaS.GPUCount, maasGPU)
	} else {
		stagecommon.AddParam(p, "deploy-maas", "false")
	}
	stagecommon.AddParam(p, "maas-serving-ns", stagecommon.StrOrDefault(cfg.Spec.MaaSServingNS, targetNamespace))
	stagecommon.AddParam(p, "maas-policy-ns", stagecommon.StrOrDefault(cfg.Spec.MaaSPolicyNS, targetNamespace))
	stagecommon.AddParam(p, "maas-gpu-count", maasGPU)
	stagecommon.AddParam(p, "maas-runtime-image", stagecommon.StrOrDefault(cfg.Spec.MaaSRuntimeImage, "registry.redhat.io/rhaiis/vllm-cuda-rhel9:3.3.0"))
	stagecommon.AddParam(p, "maas-authorized-group", stagecommon.StrOrDefault(cfg.Spec.MaaSAuthorizedGroup, "system:authenticated"))

	stagecommon.AddParam(p, "run-register", strconv.FormatBool(isLast))

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

// addParam, strOrDefault, intOrDefault, and boolOrDefault used to be
// defined here. Phase 3 moved them to internal/stagecommon (exported as
// AddParam/StrOrDefault/IntOrDefault/BoolOrDefault) as the single source
// of truth, since buildSandboxPipelineParams, buildPromotionPipelineParams,
// and buildCapacityPlan all need them. floatOrDefault stays here: it's
// only used by buildPromotionPipelineParams's guidellm-rate formatting,
// never part of the sandbox/promotion-shared param set.
func floatOrDefault(val, def float64) float64 {
	if val == 0.0 {
		return def
	}
	return val
}
