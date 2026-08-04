package controller

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
	"github.com/jhurlocker/modelops-operator/internal/stagecommon"
	"github.com/jhurlocker/modelops-operator/internal/stages/promotion"
	"github.com/jhurlocker/modelops-operator/internal/stagewalk"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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

// providerConfigLookupRequeueDelay backs off retries specifically for a
// stagecommon.ProviderConfigError (see failRequestWithRequeue and the
// "ProviderConfigLookupFailed" status reason below) -- deliberately
// longer than transientErrorRequeueDelay, since "an IntakeProviderConfig
// hasn't been created yet by a separate GitOps sync" is a slower-moving
// condition than "wait for a PipelineRun to progress." See
// docs/REFACTOR_PLAN.md Phase 7.
const providerConfigLookupRequeueDelay = 30 * time.Second

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

// RBAC (Phase 7 of REFACTOR_PLAN.md): split by concern. modelrequests/
// modellifecycleprofiles/platformconfigs/capacityplans (read-only best-
// effort lookup, see capacityPlanRunName)/events/secrets/namespaces/
// serviceaccounts/rolebindings/clusterrolebindings all stay here --
// core lifecycle CRUD, secret/namespace-provisioning glue driven by
// ProfileStageSpec.NamespaceSetup data (see ensurePromotionNamespaceRBAC/
// ensureNamespaceLabels below), not by any specific execution engine.
// tekton.dev/pipelineruns and intakeproviderconfigs now live solely on
// internal/stages/tekton.StageRunner's own marker (this file no longer
// imports tektonv1 at all -- see SetupWithManager's generalized .Owns()
// below); capacityplans' create/update/patch/delete verbs and the
// capacityplans/finalizers marker were dropped as part of the same pass
// (nothing ever creates a child object owned by a CapacityPlan, so
// capacityplans/finalizers has no live purpose -- confirmed by removing
// it and verifying against the sandbox cluster).
//
// modelrequests/finalizers is NOT dead, despite no finalizer literally
// being registered on ModelRequest in this codebase -- a real,
// live-cluster-only regression caught only by cluster verification, not
// envtest (whose admin-equivalent client bypasses this check, the same
// shape of gap Phase 1's RBAC-escalation incident already demonstrated):
// both tekton.StageRunner.buildPipelineRun and
// capacityplanning.StageRunner.EnsureRun call
// controllerutil.SetControllerReference(modelRequest, child, scheme),
// which sets OwnerReference.BlockOwnerDeletion=true by default. The API
// server's admission control requires `update` permission on
// modelrequests/finalizers to set blockOwnerDeletion:true on ANY owner
// reference pointing at a ModelRequest, regardless of whether the
// ModelRequest controller itself ever uses a finalizer -- this is a
// generic Kubernetes owner-reference safety check, not tied to this
// codebase's own finalizer usage. Removing this marker broke every
// CapacityPlan/PipelineRun creation on the sandbox cluster with
// "cannot set blockOwnerDeletion if an ownerReference refers to a
// resource you can't set finalizers on"; restored after being caught by
// live verification. See docs/PHASE_LOG.md's Phase 7 entry.
// +kubebuilder:rbac:groups=modelops.example.io,resources=modelrequests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=modelops.example.io,resources=modelrequests/finalizers,verbs=update
// +kubebuilder:rbac:groups=modelops.example.io,resources=modelrequests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=modelops.example.io,resources=modellifecycleprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups=modelops.example.io,resources=platformconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=modelops.example.io,resources=capacityplans,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// secrets create;update;patch (Phase 8, docs/PHASE_LOG.md) is narrowly
// for ensureEvalHubTokenSecret's upsert of a single, owned, ephemeral,
// per-ModelRequest Secret (name "<ModelRequest>-evalhub-token") caching
// a generated EvalHub bearer token -- NOT a general secret-writing
// capability. The reconciler never creates/updates any other Secret;
// every other *SecretName field (ScanS3/ResultS3/HuggingFace/EvalHub-
// when-explicitly-configured) is read-only (get;list;watch).
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch
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

	// Stages must be declared explicitly as of Phase 7 of
	// REFACTOR_PLAN.md -- the implicit synthesized 3-stage default
	// (defaultStages, Phase 6) was removed once every profile in this
	// repo migrated to declaring Spec.Stages itself (see
	// gitops/components/runtime-config/lifecycleprofile.yaml). A
	// profile with no Stages configured is a real, visible
	// configuration error now, not a silent no-op walk of zero stages.
	stages := profile.Spec.Stages
	if len(stages) == 0 {
		return r.failRequest(ctx, &modelRequest, "NoStagesConfigured",
			fmt.Sprintf("ModelLifecycleProfile %q has no Spec.Stages configured", profile.Name))
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
		if setup.AllowedNamespaceSelector != nil {
			if err := r.checkNamespaceApproved(ctx, ns, setup.AllowedNamespaceSelector); err != nil {
				return err
			}
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
		// ProviderConfigError (Phase 7): a stagecommon.StageSpec.
		// ProviderConfigRef resolution failure (missing/malformed
		// reference, unsupported Kind, unsupported providerType --
		// see internal/stages/tekton/providerconfig.go). Gets its own
		// visible status reason and a longer, dedicated bounded
		// requeue instead of falling into the generic silent-retry
		// path below -- long enough to tolerate the referenced
		// IntakeProviderConfig being created moments later by a
		// separate GitOps sync, without masking the failure as
		// permanent the way a plain failRequest (no requeue at all)
		// would. See docs/REFACTOR_PLAN.md Phase 7.
		var pcErr *stagecommon.ProviderConfigError
		if errors.As(walkErr, &pcErr) {
			return r.failRequestWithRequeue(ctx, &modelRequest, "ProviderConfigLookupFailed", pcErr.Error(), providerConfigLookupRequeueDelay)
		}
		// NamespaceApprovalError (Phase 9): StageNamespaceSetup.
		// AllowedNamespaceSelector check failure -- the candidate
		// namespace either doesn't exist or its labels don't match
		// the selector. Unlike ProviderConfigLookupFailed (a missing-
		// dependency race), this uses failRequest with no bounded
		// requeue: namespaces are long-lived cluster infrastructure,
		// not a dynamically-provisioned CRD; the operator has no
		// reason to expect the namespace (or its labels) to
		// spontaneously change without human intervention. See
		// docs/REFACTOR_PLAN.md Phase 9 and stagecommon/errors.go's
		// NamespaceApprovalError doc comment for the full rationale.
		var naErr *stagecommon.NamespaceApprovalError
		if errors.As(walkErr, &naErr) {
			return r.failRequest(ctx, &modelRequest, "NamespaceNotApproved", naErr.Error())
		}
		logger.Error(walkErr, "stage walk failed")
		return ctrl.Result{RequeueAfter: transientErrorRequeueDelay}, walkErr
	}

	return r.persistWalkResult(ctx, &modelRequest, result)
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
// generic (Phase 6). Phase/Message are now ALSO always fully generic
// ("<CurrentStage>Running"/"Succeeded"/"Failed", result.Message passed
// through verbatim) -- the Phase 6 compatibility shim that used to
// reproduce the pre-Phase-6 special-cased strings ("CapacityPlanning"/
// "SandboxRunning"/"PromotionRunning") for the synthesized default stage
// list was removed in Phase 7 (REFACTOR_PLAN.md), once every profile in
// this repo migrated to declaring Spec.Stages explicitly (defaultStages
// no longer exists -- see docs/PHASE_LOG.md's Phase 7 entry). This is a
// real, deliberate, visible behavior change for any ModelRequest using
// what used to be the default stage list: Phase values are now
// lowercase-stage-name-prefixed ("capacityRunning"/"sandboxRunning"/
// "promotionRunning") instead of the old bespoke strings, and
// Sandbox/Promotion-Failed messages no longer get the old "Sandbox
// pipeline failed: "/"Promotion to <ns> failed: " prose prefix (just
// result.Message directly).
func computeWalkStatus(mr *modelopsv1alpha1.ModelRequest, result stagewalk.Result) walkStatus {
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

	// sandboxStageName/promotionStageName below only match a stage
	// literally named "sandbox"/"promotion" -- a known, narrower
	// limitation of these three legacy singular RunName fields
	// (PipelineRunName/SandboxPipelineRunName/PromotionPipelineRunName
	// predate Phase 6 entirely) that a custom-named Spec.Stages profile
	// won't populate. Out of scope for Phase 7 (only the Phase/Message
	// shim was asked to be deprecated this phase, not these three
	// fields) -- Status.Stages[]/CurrentStage remain fully generic and
	// correct regardless of stage naming.
	if sandboxProgress, ok := lastProgressNamed(result.Progress, sandboxStageName); ok && sandboxProgress.RunRef != "" {
		ws.sandboxPipelineRunName = sandboxProgress.RunRef
		ws.pipelineRunName = sandboxProgress.RunRef
	}
	if promoProgress, ok := lastPromotionProgress(result.Progress); ok && promoProgress.RunRef != "" {
		ws.promotionPipelineRunName = promoProgress.RunRef
		ws.pipelineRunName = promoProgress.RunRef
	}

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

// persistWalkResult computes the new status (against mr.Status as
// currently persisted) and writes it only if something relevant
// actually changed -- widened, on purpose, beyond the older
// Phase+Message-only comparison ModelRequestReconciler.updateStatus
// still uses for its other (non-walker) call sites: Phase 6 adds
// CurrentStage/Stages[], which can change even while Phase/Message
// repeat (e.g. one promotion namespace's individual outcome changing
// while the overall phase is still "PromotionRunning"), so comparing
// only Phase+Message would silently let that go unpersisted.
func (r *ModelRequestReconciler) persistWalkResult(ctx context.Context, mr *modelopsv1alpha1.ModelRequest, result stagewalk.Result) (ctrl.Result, error) {
	ws := computeWalkStatus(mr, result)

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

// lastPromotionProgress finds the last progress entry for the
// promotion stage -- stagewalk.Progress.Name is the ProfileStageSpec's
// own Name (e.g. "promotion"), with the target namespace recorded
// separately in Progress.Namespace (see internal/stagewalk.Walk), so
// this is just lastProgressNamed under the hood; kept as its own
// function so the call site at computeWalkStatus doesn't need to know
// the conventional promotion stage's literal name.
func lastPromotionProgress(progress []stagewalk.Progress) (stagewalk.Progress, bool) {
	return lastProgressNamed(progress, promotionStageName)
}

// sandboxStageName/promotionStageName identify the conventional stage
// names used only for populating the legacy singular RunName status
// fields (see computeWalkStatus's doc comment above) -- NOT for any
// stage-sequencing/defaulting purpose (that mechanism, defaultStages,
// was removed in Phase 7 of REFACTOR_PLAN.md; every profile must
// declare Spec.Stages explicitly now). The live
// standard-generative-onboarding profile (see
// gitops/components/runtime-config/lifecycleprofile.yaml) still uses
// these exact names, matching the pre-Phase-7 default shape, so this
// legacy-field population keeps working for it unchanged.
const (
	sandboxStageName   = "sandbox"
	promotionStageName = "promotion"
)

// SetupWithManager registers watches for ModelRequest and its core
// lifecycle child, CapacityPlan (a core lifecycle CRD, not
// provider-specific -- owned explicitly here, unconditionally). Any
// execution-engine-specific child type (e.g. tektonv1.PipelineRun) is
// instead declared generically by whichever registered StageRunner
// creates it, via the optional stagecommon.OwnedTypesProvider
// interface -- this is what lets this file avoid importing tektonv1 (or
// any future non-Tekton engine's package) purely for manager-wiring
// purposes. See stagecommon.OwnedTypesProvider's doc comment and
// docs/REFACTOR_PLAN.md/docs/PHASE_LOG.md Phase 7 (this closes the
// residual import Phase 4 flagged as "a natural candidate for Phase
// 5/7").
func (r *ModelRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	bldr := ctrl.NewControllerManagedBy(mgr).
		For(&modelopsv1alpha1.ModelRequest{}).
		Owns(&modelopsv1alpha1.CapacityPlan{})

	for _, name := range sortedStageRunnerKeys(r.StageRunners) {
		if owner, ok := r.StageRunners[name].(stagecommon.OwnedTypesProvider); ok {
			for _, t := range owner.OwnedTypes() {
				bldr = bldr.Owns(t)
			}
		}
	}

	return bldr.Complete(r)
}

// sortedStageRunnerKeys returns r's keys in deterministic sorted order,
// so SetupWithManager's .Owns() registration order (and any log output
// derived from iterating the registry) doesn't vary from run to run --
// map iteration order in Go is deliberately randomized.
func sortedStageRunnerKeys(m map[string]stagecommon.StageRunner) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

// resolvedSecrets holds the outcome of resolveSecrets: Secret NAME
// references and non-secret endpoint strings only, never credential
// VALUES. This is a Phase 8 (docs/PHASE_LOG.md) change: resolveSecrets
// still Gets/inspects the underlying Secret's data (to validate it
// exists, has the expected keys, and to decide the EvalHub-token
// fallback), but the raw accessKeyId/secretAccessKey/token values never
// escape resolveSecrets itself -- see toStagecommonSecrets and
// stagecommon.Secrets' doc comment for where these names end up
// (Tekton params consumed via a Task's own env.valueFrom.secretKeyRef,
// never PipelineRun.spec.params).
type resolvedSecrets struct {
	evalhubURL            string
	evalhubSecretName     string
	huggingfaceSecretName string
	scanS3Endpoint        string
	scanS3SecretName      string
	resultS3Endpoint      string
	resultS3SecretName    string
}

// toStagecommonSecrets converts the reconciler-private resolvedSecrets
// into the stagecommon.Secrets shape stage handlers (sandbox/promotion)
// consume via StageContext.Secrets.
func toStagecommonSecrets(s *resolvedSecrets) stagecommon.Secrets {
	if s == nil {
		return stagecommon.Secrets{}
	}
	return stagecommon.Secrets{
		EvalHubURL:            s.evalhubURL,
		EvalHubSecretName:     s.evalhubSecretName,
		HuggingFaceSecretName: s.huggingfaceSecretName,
		ResultS3Endpoint:      s.resultS3Endpoint,
		ResultS3SecretName:    s.resultS3SecretName,
		ScanS3Endpoint:        s.scanS3Endpoint,
		ScanS3SecretName:      s.scanS3SecretName,
	}
}

// evalhubTokenSecretName is the deterministic name of the ephemeral,
// owned Secret resolveSecrets upserts to hold a generated
// ServiceAccount token, when no operator-configured EvalHub Secret (or
// none with a "token" key) is available. Named/owned the same way every
// other per-ModelRequest child object in this file is (see
// TestModelRequest_FirstReconcile_CreatesCapacityPlan's
// "<name>-capacity" convention).
func evalhubTokenSecretName(mr *modelopsv1alpha1.ModelRequest) string {
	return mr.Name + "-evalhub-token"
}

func (r *ModelRequestReconciler) resolveSecrets(ctx context.Context, mr *modelopsv1alpha1.ModelRequest) (*resolvedSecrets, error) {
	s := &resolvedSecrets{}

	// EvalHub: read "url" (non-secret -- Phase 8 fix: this used to land
	// in scanS3Endpoint, an unrelated field, entirely by mistake) and
	// "token" (a credential -- Phase 8 fix: this key was never read at
	// all; evalhubToken was unconditionally overwritten by a freshly
	// generated token below, silently discarding any token an operator
	// actually configured) from the Secret's own data, but only ever
	// propagate the Secret's NAME onward, never the token value itself.
	if mr.Spec.EvalHubSecretName != "" {
		secret, err := r.readSecret(ctx, mr.Namespace, mr.Spec.EvalHubSecretName)
		if err != nil {
			return nil, err
		}
		if v, ok := secret.Data["url"]; ok {
			s.evalhubURL = string(v)
		}
		if v, ok := secret.Data["token"]; ok && len(v) > 0 {
			// An explicit token was configured -- reuse the operator's
			// own Secret by name; never overwrite it with a generated
			// one.
			s.evalhubSecretName = mr.Spec.EvalHubSecretName
		}
	}
	if s.evalhubSecretName == "" {
		// No explicit token was configured (or no EvalHubSecretName at
		// all). Generate a short-lived ServiceAccount token for the
		// pipeline's own identity and persist it in an owned, ephemeral
		// Secret, so it too flows to Tekton by Secret reference -- never
		// as a plaintext value in resolvedSecrets/StageSpec.Params.
		token, err := r.generateServiceAccountToken(ctx, mr.Namespace, "pipeline")
		if err != nil {
			logger := log.FromContext(ctx)
			logger.Error(err, "failed to generate EvalHub token for pipeline SA, falling back to empty")
		} else {
			name, err := r.ensureEvalHubTokenSecret(ctx, mr, token)
			if err != nil {
				return nil, fmt.Errorf("failed to persist generated EvalHub token secret: %w", err)
			}
			s.evalhubSecretName = name
		}
	}

	// HuggingFace: optional. Still Get the Secret to fail loudly on a
	// typo'd/missing reference (matching readSecret's existing
	// behavior), but only the Secret's own name is ever propagated --
	// its "token" value is never read into a returned field.
	if mr.Spec.HuggingFaceSecretName != "" {
		if _, err := r.readSecret(ctx, mr.Namespace, mr.Spec.HuggingFaceSecretName); err != nil {
			return nil, err
		}
		s.huggingfaceSecretName = mr.Spec.HuggingFaceSecretName
	}

	// Endpoint is not a credential -- a hardcoded default cluster-local
	// service address is fine to fall back to. accessKeyId/
	// secretAccessKey are credentials: resolveSecrets still validates
	// their presence/non-emptiness (fail loudly per AGENTS.md/Phase 1,
	// "no hardcoded credential fallback"), but only the Secret's own
	// name -- never the key values -- is propagated onward (Phase 8).
	var scanS3CredentialsPresent bool
	if mr.Spec.ScanS3SecretName != "" {
		secret, err := r.readSecret(ctx, mr.Namespace, mr.Spec.ScanS3SecretName)
		if err != nil {
			return nil, err
		}
		s.scanS3Endpoint = fromMap(string(secret.Data["endpoint"]), s.scanS3Endpoint)
		if len(secret.Data["accessKeyId"]) > 0 && len(secret.Data["secretAccessKey"]) > 0 {
			s.scanS3SecretName = mr.Spec.ScanS3SecretName
			scanS3CredentialsPresent = true
		}
	}
	if s.scanS3Endpoint == "" {
		s.scanS3Endpoint = "http://minio.modelops-storage.svc.cluster.local:9000"
	}
	if !scanS3CredentialsPresent {
		return nil, fmt.Errorf("no scan storage credentials configured: set spec.scanS3SecretName to a Secret with accessKeyId/secretAccessKey keys")
	}

	var resultS3CredentialsPresent bool
	if mr.Spec.ResultS3SecretName != "" {
		secret, err := r.readSecret(ctx, mr.Namespace, mr.Spec.ResultS3SecretName)
		if err != nil {
			return nil, err
		}
		s.resultS3Endpoint = fromMap(string(secret.Data["endpoint"]), s.resultS3Endpoint)
		if len(secret.Data["accessKeyId"]) > 0 && len(secret.Data["secretAccessKey"]) > 0 {
			s.resultS3SecretName = mr.Spec.ResultS3SecretName
			resultS3CredentialsPresent = true
		}
	}

	if s.resultS3Endpoint == "" {
		s.resultS3Endpoint = "http://minio.modelops-storage.svc.cluster.local:9000"
	}
	if mr.Spec.ResultS3Endpoint != "" {
		s.resultS3Endpoint = mr.Spec.ResultS3Endpoint
	}
	if !resultS3CredentialsPresent {
		return nil, fmt.Errorf("no result storage credentials configured: set spec.resultS3SecretName to a Secret with accessKeyId/secretAccessKey keys")
	}

	return s, nil
}

// ensureEvalHubTokenSecret upserts an owned, ephemeral Secret holding a
// freshly generated ServiceAccount token, so a generated EvalHub token
// flows to Tekton by Secret reference -- like every other credential
// (Phase 8, docs/PHASE_LOG.md) -- rather than as a plaintext
// resolvedSecrets/StageSpec.Params value. Idempotent: creates the
// Secret if absent (mirroring the createIgnoringAlreadyExists pattern
// used elsewhere in this file for other owned child objects), or
// refreshes its token value in place if it already exists (the
// underlying ServiceAccount token has a 24h TTL and is regenerated on
// every resolveSecrets call). Owner-referenced to mr for garbage
// collection, consistent with every other child object this reconciler
// creates.
func (r *ModelRequestReconciler) ensureEvalHubTokenSecret(ctx context.Context, mr *modelopsv1alpha1.ModelRequest, token string) (string, error) {
	name := evalhubTokenSecretName(mr)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: mr.Namespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"token": []byte(token)},
	}
	if err := controllerutil.SetControllerReference(mr, secret, r.Scheme); err != nil {
		return "", fmt.Errorf("failed to set owner reference on EvalHub token secret: %w", err)
	}

	created, err := createIgnoringAlreadyExists(ctx, r.Client, secret)
	if err != nil {
		return "", err
	}
	if created {
		return name, nil
	}

	// Already existed: refresh its token value in place rather than
	// leaving a stale/near-expiry token cached indefinitely.
	var existing corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: mr.Namespace}, &existing); err != nil {
		return "", err
	}
	if existing.Data == nil {
		existing.Data = map[string][]byte{}
	}
	existing.Data["token"] = []byte(token)
	if err := r.Update(ctx, &existing); err != nil {
		return "", fmt.Errorf("failed to refresh EvalHub token secret: %w", err)
	}
	return name, nil
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
// controlled by ProfileStageSpec.NamespaceSetup.Labels (declared
// directly in each ModelLifecycleProfile's Spec.Stages as of Phase 7 --
// see gitops/components/runtime-config/lifecycleprofile.yaml), not by a
// Go function hardcoded to "the sandbox stage gets evalhub, the
// promotion stage gets MaaS."
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

// checkNamespaceApproved evaluates sel against a target namespace's
// labels, returning a stagecommon.NamespaceApprovalError if the namespace
// doesn't exist or doesn't match. A nil or empty selector is treated as
// "permits everything" (the caller -- setupNamespace -- already guards
// against nil, so this function only receives a non-nil selector).
func (r *ModelRequestReconciler) checkNamespaceApproved(ctx context.Context, ns string, sel *metav1.LabelSelector) error {
	var namespace corev1.Namespace
	if err := r.Get(ctx, types.NamespacedName{Name: ns}, &namespace); apierrors.IsNotFound(err) {
		return &stagecommon.NamespaceApprovalError{
			Err: fmt.Errorf("namespace %q does not exist", ns),
		}
	} else if err != nil {
		return fmt.Errorf("failed to get namespace %q for approval check: %w", ns, err)
	}

	selector, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return &stagecommon.NamespaceApprovalError{
			Err: fmt.Errorf("invalid allowedNamespaceSelector: %w", err),
		}
	}

	if !selector.Matches(labels.Set(namespace.Labels)) {
		return &stagecommon.NamespaceApprovalError{
			Err: fmt.Errorf("namespace %q labels do not match allowedNamespaceSelector", ns),
		}
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

// failRequestWithRequeue is failRequest's counterpart for status
// reasons that are plausibly transient (e.g. "ProviderConfigLookupFailed":
// the referenced object may simply not exist yet, created moments later
// by a separate GitOps sync -- see docs/REFACTOR_PLAN.md Phase 7).
// Unlike failRequest, this ALWAYS returns a bounded RequeueAfter, even
// on the "Phase/Message already match, nothing to persist" no-op branch
// -- failRequest's other reasons (ProfileLookupFailed,
// PlatformConfigLookupFailed, ...) rely solely on the ModelRequest's own
// resync or a later unrelated watch event to ever re-check a fixed
// dependency, which risks looking permanently stuck for a genuinely
// transient condition. Adding the same active-requeue treatment to
// those older reasons (or, better, a Watches()-based immediate
// re-trigger for all of them) is deliberately deferred -- see the
// backlog note added to REFACTOR_PLAN.md's Phase 7 section.
func (r *ModelRequestReconciler) failRequestWithRequeue(ctx context.Context, mr *modelopsv1alpha1.ModelRequest, phase, message string, requeueAfter time.Duration) (ctrl.Result, error) {
	if mr.Status.Phase == phase && mr.Status.Message == message {
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	mr.Status.Phase = phase
	mr.Status.Message = message
	if err := r.Status().Update(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
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
