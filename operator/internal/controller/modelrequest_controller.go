package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"
	"github.com/jhurlocker/modelops-operator/internal/stages/promotion"
	"github.com/jhurlocker/modelops-operator/internal/stagewalk"

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
	// StageHandlers/StageRunners are the dual registry the Phase 6
	// generic stage walker (internal/stagewalk) dispatches through:
	// StageHandlers is keyed by ProfileStageSpec.Name (builds *what* to
	// run), StageRunners is keyed by ProfileStageSpec.Kind (builds/
	// tracks *how* it runs -- Tekton PipelineRuns via
	// internal/stages/tekton.StageRunner, CapacityPlan objects via
	// internal/stages/capacityplanning.StageRunner, a fake of either in
	// tests). See internal/stagewalk.Walk and docs/REFACTOR_PLAN.md
	// Phase 6 for the design. The reconciler itself never constructs a
	// PipelineRun/CapacityPlan or reads a Tekton condition directly.
	StageHandlers map[string]stagecommon.StageHandler
	StageRunners  map[string]stagecommon.StageRunner
}

// namespaceSetupError distinguishes an RBAC-provisioning failure from a
// namespace-labeling failure inside the generic SetupNamespace callback
// below, so Reconcile can still surface the exact pre-Phase-6 status
// reasons ("RBACSetupFailed"/"NamespaceSetupFailed") without
// internal/stagewalk needing to know either concept exists.
type namespaceSetupError struct {
	reason string
	err    error
}

func (e *namespaceSetupError) Error() string { return e.err.Error() }
func (e *namespaceSetupError) Unwrap() error { return e.err }

// secretLookupError marks a resolveSecrets failure surfaced through the
// walker's BuildContext callback, so Reconcile can map it to the
// pre-Phase-6 "SecretLookupFailed" status reason.
type secretLookupError struct {
	err error
}

func (e *secretLookupError) Error() string { return e.err.Error() }
func (e *secretLookupError) Unwrap() error { return e.err }

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

	stages := profile.Spec.Stages
	usingDefaultStages := len(stages) == 0
	if usingDefaultStages {
		stages = defaultStages(profile)
	}

	// Best-effort capacity plan lookup: find the declared CapacityPlan-
	// kind stage (if any) and fetch its deterministic child object, so
	// later stages' handlers (sandbox/promotion) can read its
	// Status.GPUsNeeded. Nil (not found, or no CapacityPlan-kind stage
	// declared at all) is tolerated -- a stage handler that wants it
	// must handle a nil StageContext.CapacityPlan.
	var capacityPlan *modelopsv1alpha1.CapacityPlan
	if name, ok := capacityPlanRunName(&modelRequest, stages); ok {
		var plan modelopsv1alpha1.CapacityPlan
		if getErr := r.Get(ctx, types.NamespacedName{Name: name, Namespace: modelRequest.Namespace}, &plan); getErr == nil {
			capacityPlan = &plan
		}
	}

	// Secrets are resolved at most once per Reconcile call, lazily --
	// only once a stage's context actually needs them (see
	// buildContext below). This preserves the pre-Phase-6 ordering
	// guarantee (resolveSecrets is never even attempted while capacity
	// planning hasn't succeeded yet), since the CapacityPlan-kind
	// stage's own handler never reads StageContext.Secrets.
	var (
		secretsTried bool
		secretsVal   *resolvedSecrets
		secretsErr   error
	)
	resolveSecretsOnce := func() (*resolvedSecrets, error) {
		if !secretsTried {
			secretsTried = true
			secretsVal, secretsErr = r.resolveSecrets(ctx, &modelRequest)
		}
		return secretsVal, secretsErr
	}

	buildContext := func(stage modelopsv1alpha1.ProfileStageSpec, ns string, idx, count int) (stagecommon.StageContext, error) {
		sc := stagecommon.StageContext{
			ModelRequest:   &modelRequest,
			Profile:        profile,
			PlatformConfig: platformConfig,
			CapacityPlan:   capacityPlan,
			Stage:          stage,
			Namespace:      ns,
			NamespaceIndex: idx,
			NamespaceCount: count,
		}
		if stage.Kind != "CapacityPlan" {
			secrets, err := resolveSecretsOnce()
			if err != nil {
				return stagecommon.StageContext{}, &secretLookupError{err: err}
			}
			sc.Secrets = toStagecommonSecrets(secrets)
		}
		return sc, nil
	}

	namespaces := func(stage modelopsv1alpha1.ProfileStageSpec) []string {
		if !stage.PerNamespace {
			return []string{modelRequest.Namespace}
		}
		return promotion.GetNamespaces(&modelRequest)
	}

	setupNamespace := func(ctx context.Context, ns string, stage modelopsv1alpha1.ProfileStageSpec) error {
		setup := stage.NamespaceSetup
		if setup == nil {
			return nil
		}
		if setup.EnsureRBAC {
			if err := r.ensurePromotionNamespaceRBAC(ctx, ns, modelRequest.Namespace); err != nil {
				return &namespaceSetupError{reason: "RBACSetupFailed", err: err}
			}
		}
		if len(setup.Labels) > 0 {
			if err := r.ensureNamespaceLabels(ctx, ns, setup.Labels); err != nil {
				return &namespaceSetupError{reason: "NamespaceSetupFailed", err: err}
			}
		}
		return nil
	}

	result, walkErr := stagewalk.Walk(ctx, &modelRequest, stagewalk.Input{
		Stages:         stages,
		Handlers:       r.StageHandlers,
		Runners:        r.StageRunners,
		Namespaces:     namespaces,
		SetupNamespace: setupNamespace,
		BuildContext:   buildContext,
	})
	if walkErr != nil {
		var nsErr *namespaceSetupError
		if errors.As(walkErr, &nsErr) {
			return r.failRequest(ctx, &modelRequest, nsErr.reason, nsErr.err.Error())
		}
		var secErr *secretLookupError
		if errors.As(walkErr, &secErr) {
			return r.failRequest(ctx, &modelRequest, "SecretLookupFailed", secErr.err.Error())
		}
		logger.Error(walkErr, "stage walk failed")
		return ctrl.Result{RequeueAfter: transientErrorRequeueDelay}, walkErr
	}

	return r.persistWalkResult(ctx, &modelRequest, usingDefaultStages, result)
}

// capacityPlanRunName finds the declared CapacityPlan-kind stage (if
// any) in stages and returns the deterministic RunName its StageRunner
// uses (see internal/stages/capacityplanning.Handler.BuildSpec:
// "<ModelRequest.Name>-<stage.Name>"), without needing the reconciler
// to actually invoke that stage's handler first. This is the one place
// Reconcile knows anything about a specific Kind -- reconciler-level
// glue for a genuine cross-stage data dependency (later stages' params
// want the CapacityPlan's derived GPU count), not part of the walker's
// own advance/stop/skip decision logic (see internal/stagewalk.Walk,
// which never inspects Kind beyond registry dispatch).
func capacityPlanRunName(mr *modelopsv1alpha1.ModelRequest, stages []modelopsv1alpha1.ProfileStageSpec) (string, bool) {
	for _, s := range stages {
		if s.Kind == "CapacityPlan" {
			return fmt.Sprintf("%s-%s", mr.Name, s.Name), true
		}
	}
	return "", false
}

// walkStatus is the pure computed result of translating a
// stagewalk.Result into ModelRequest.Status field values -- kept
// separate from persistWalkResult so "what should the new status be"
// (this function) and "is a write actually needed, compared against
// what's currently persisted" (persistWalkResult) can't accidentally be
// compared against each other after mutation, the way a single
// mutate-then-compare-the-same-object bug would.
type walkStatus struct {
	phase                    string
	message                  string
	currentStage             string
	stages                   []modelopsv1alpha1.StageProgress
	pipelineRunName          string
	sandboxPipelineRunName   string
	promotionPipelineRunName string
}

// computeWalkStatus translates a stagewalk.Result into the new
// ModelRequest.Status field values. CurrentStage/Stages are always
// generic (Phase 6). Phase/Message/*PipelineRunName additionally
// reproduce the exact pre-Phase-6 values when usingDefaultStages is
// true -- a deliberate, isolated compatibility shim (see
// docs/REFACTOR_PLAN.md Phase 6 design review) so every Phase 0-5
// characterization test keeps passing unmodified. A profile with
// explicit Spec.Stages gets fully generic Phase values instead, since
// there's no pre-existing behavior to preserve for it. Deprecating this
// shim once profiles migrate off the default list is tracked in
// REFACTOR_PLAN.md Phase 7.
func computeWalkStatus(mr *modelopsv1alpha1.ModelRequest, usingDefaultStages bool, result stagewalk.Result) walkStatus {
	ws := walkStatus{
		currentStage: result.CurrentStage,
		stages:       toStageProgressList(result.Progress),
		// Carry forward existing RunName fields by default; only
		// overwritten below when a fresher one was actually recorded
		// this pass.
		pipelineRunName:          mr.Status.PipelineRunName,
		sandboxPipelineRunName:   mr.Status.SandboxPipelineRunName,
		promotionPipelineRunName: mr.Status.PromotionPipelineRunName,
	}

	if sandboxProgress, ok := lastProgressNamed(result.Progress, defaultSandboxStageName); ok && sandboxProgress.RunRef != "" {
		ws.sandboxPipelineRunName = sandboxProgress.RunRef
		ws.pipelineRunName = sandboxProgress.RunRef
	}
	if promoProgress, ok := lastPromotionProgress(result.Progress); ok && promoProgress.RunRef != "" {
		ws.promotionPipelineRunName = promoProgress.RunRef
		ws.pipelineRunName = promoProgress.RunRef
	}

	if !usingDefaultStages {
		switch result.Outcome {
		case stagewalk.OutcomeSucceeded:
			ws.phase = "Succeeded"
			ws.message = "Model onboarding completed successfully"
		case stagewalk.OutcomeFailed:
			ws.phase = "Failed"
			ws.message = result.Message
		case stagewalk.OutcomeRunning:
			ws.phase = fmt.Sprintf("%sRunning", result.CurrentStage)
			ws.message = result.Message
		}
		return ws
	}

	switch result.CurrentStage {
	case defaultCapacityStageName:
		ws.phase = "CapacityPlanning"
		ws.message = result.Message
	case defaultSandboxStageName:
		if result.Outcome == stagewalk.OutcomeFailed {
			ws.phase = "Failed"
			ws.message = "Sandbox pipeline failed: " + result.Message
		} else {
			ws.phase = "SandboxRunning"
			ws.message = result.Message
		}
	case defaultPromotionStageName:
		if result.Outcome == stagewalk.OutcomeFailed {
			ns := ""
			if len(result.Progress) > 0 {
				ns = result.Progress[len(result.Progress)-1].Namespace
			}
			ws.phase = "Failed"
			ws.message = fmt.Sprintf("Promotion to %s failed: %s", ns, result.Message)
		} else {
			ws.phase = "PromotionRunning"
			ws.message = "Promotion pipeline(s) running"
		}
	default:
		// Outcome == Succeeded: CurrentStage is empty (see
		// internal/stagewalk.Walk's final return).
		ws.phase = "Succeeded"
		ws.message = "Model onboarding completed successfully"
	}
	return ws
}

// persistWalkResult computes the new status (against mr.Status as
// currently persisted) and writes it only if something relevant
// actually changed -- widened, on purpose, beyond the older
// Phase+Message-only comparison ModelRequestReconciler.updateStatus
// still uses for its other (non-walker) call sites: Phase 6 adds
// CurrentStage/Stages[], which can change even while Phase/Message
// repeat (e.g. one promotion namespace's individual outcome changing
// while the overall phase is still "PromotionRunning"), so comparing
// only Phase+Message would silently let that go unpersisted.
func (r *ModelRequestReconciler) persistWalkResult(ctx context.Context, mr *modelopsv1alpha1.ModelRequest, usingDefaultStages bool, result stagewalk.Result) (ctrl.Result, error) {
	ws := computeWalkStatus(mr, usingDefaultStages, result)

	if mr.Status.Phase == ws.phase &&
		mr.Status.Message == ws.message &&
		mr.Status.CurrentStage == ws.currentStage &&
		mr.Status.PipelineRunName == ws.pipelineRunName &&
		mr.Status.SandboxPipelineRunName == ws.sandboxPipelineRunName &&
		mr.Status.PromotionPipelineRunName == ws.promotionPipelineRunName &&
		stageProgressEqual(mr.Status.Stages, ws.stages) {
		return ctrl.Result{}, nil
	}

	mr.Status.Phase = ws.phase
	mr.Status.Message = ws.message
	mr.Status.CurrentStage = ws.currentStage
	mr.Status.Stages = ws.stages
	mr.Status.PipelineRunName = ws.pipelineRunName
	mr.Status.SandboxPipelineRunName = ws.sandboxPipelineRunName
	mr.Status.PromotionPipelineRunName = ws.promotionPipelineRunName

	if err := r.Status().Update(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func stageProgressEqual(a, b []modelopsv1alpha1.StageProgress) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func toStageProgressList(progress []stagewalk.Progress) []modelopsv1alpha1.StageProgress {
	out := make([]modelopsv1alpha1.StageProgress, 0, len(progress))
	for _, p := range progress {
		out = append(out, modelopsv1alpha1.StageProgress{
			Name:      p.Name,
			Namespace: p.Namespace,
			Phase:     string(p.Phase),
			RunRef:    p.RunRef,
			Message:   p.Message,
		})
	}
	return out
}

func lastProgressNamed(progress []stagewalk.Progress, name string) (stagewalk.Progress, bool) {
	var found stagewalk.Progress
	ok := false
	for _, p := range progress {
		if p.Name == name {
			found = p
			ok = true
		}
	}
	return found, ok
}

// lastPromotionProgress finds the last progress entry whose Name is
// "<promotion-stage-name>-<namespace>" -- i.e. any PerNamespace
// promotion-kind invocation, without hardcoding the exact default name.
func lastPromotionProgress(progress []stagewalk.Progress) (stagewalk.Progress, bool) {
	var found stagewalk.Progress
	ok := false
	prefix := defaultPromotionStageName + "-"
	for _, p := range progress {
		if len(p.Name) > len(prefix) && p.Name[:len(prefix)] == prefix {
			found = p
			ok = true
		}
	}
	return found, ok
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

type resolvedSecrets struct {
	evalhubToken      string
	huggingfaceToken  string
	scanS3Endpoint    string
	scanS3AccessKey   string
	scanS3SecretKey   string
	resultS3Endpoint  string
	resultS3AccessKey string
	resultS3SecretKey string
}

// toStagecommonSecrets converts the reconciler-private resolvedSecrets
// into the stagecommon.Secrets shape stage handlers (sandbox/promotion)
// consume via StageContext.Secrets.
func toStagecommonSecrets(s *resolvedSecrets) stagecommon.Secrets {
	if s == nil {
		return stagecommon.Secrets{}
	}
	return stagecommon.Secrets{
		EvalHubToken:      s.evalhubToken,
		HuggingFaceToken:  s.huggingfaceToken,
		ResultS3Endpoint:  s.resultS3Endpoint,
		ResultS3AccessKey: s.resultS3AccessKey,
		ResultS3SecretKey: s.resultS3SecretKey,
		ScanS3Endpoint:    s.scanS3Endpoint,
		ScanS3AccessKey:   s.scanS3AccessKey,
		ScanS3SecretKey:   s.scanS3SecretKey,
	}
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

// ensureNamespaceLabels applies labels to namespace idempotently: a key
// is only added/updated if it's currently absent or set to a different
// value. Generalizes the pre-Phase-6 ensureEvalHubTenantLabel (a single
// presence-only-checked label) and ensureMaaSNamespaceLabels (three
// value-checked labels) into one data-driven helper -- which labels get
// applied to which namespace, for which stage, is now entirely
// controlled by ProfileStageSpec.NamespaceSetup.Labels (see
// defaultStages), not by a Go function hardcoded to "the sandbox stage
// gets evalhub, the promotion stage gets MaaS."
func (r *ModelRequestReconciler) ensureNamespaceLabels(ctx context.Context, namespace string, labels map[string]string) error {
	var ns corev1.Namespace
	if err := r.Get(ctx, types.NamespacedName{Name: namespace}, &ns); err != nil {
		return fmt.Errorf("failed to get namespace %s: %w", namespace, err)
	}
	if ns.Labels == nil {
		ns.Labels = map[string]string{}
	}
	needsUpdate := false
	for k, v := range labels {
		if existing, ok := ns.Labels[k]; !ok || existing != v {
			ns.Labels[k] = v
			needsUpdate = true
		}
	}
	if !needsUpdate {
		return nil
	}
	if err := r.Update(ctx, &ns); err != nil {
		return fmt.Errorf("failed to label namespace %s: %w", namespace, err)
	}
	log.FromContext(ctx).Info("applied namespace labels", "namespace", namespace, "labels", labels)
	return nil
}

func fromMap(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
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
