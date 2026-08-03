package controller

import (
	modelopsv1alpha1 "github.com/jhurlocker/modelops-operator/api/v1alpha1"
)

// defaultCapacityStageName/defaultSandboxStageName/defaultPromotionStageName
// are the exact stage names defaultStages uses -- deliberately matching
// the pre-Phase-6 RunName conventions ("<mr>-capacity", "<mr>-sandbox",
// "<mr>-promotion-<ns>") byte-for-byte, rather than the more descriptive
// names REFACTOR_PLAN.md's illustrative YAML uses ("capacity-planning",
// "sandbox-intake") -- same judgment call Phase 0 already made choosing
// "sandbox" over the plan's "intake" (see docs/PHASE_LOG.md). Using the
// plan's names here would silently rename every child object on
// upgrade; that's a real behavior/migration change, not a cosmetic one.
const (
	defaultCapacityStageName  = "capacity"
	defaultSandboxStageName   = "sandbox"
	defaultPromotionStageName = "promotion"
)

// defaultStages synthesizes the exact pre-Phase-6 3-stage sequence
// (capacity-planning -> sandbox -> promotion) as data, used whenever
// profile.Spec.Stages is empty. This is what makes Phase 6 additive,
// not breaking: every ModelLifecycleProfile from Phase 0-5 needs zero
// changes to keep reconciling identically -- see
// docs/REFACTOR_PLAN.md Phase 6.
func defaultStages(profile *modelopsv1alpha1.ModelLifecycleProfile) []modelopsv1alpha1.ProfileStageSpec {
	var providerConfigRef *modelopsv1alpha1.ProviderConfigRef
	if profile != nil {
		providerConfigRef = profile.Spec.ProviderConfigRef
	}

	return []modelopsv1alpha1.ProfileStageSpec{
		{
			Name: defaultCapacityStageName,
			Kind: "CapacityPlan",
		},
		{
			Name:              defaultSandboxStageName,
			Kind:              "PipelineRun",
			ProviderConfigRef: providerConfigRef,
			NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{
				EnsureRBAC: true,
				Labels: map[string]string{
					// Was ensureEvalHubTenantLabel, previously hardcoded
					// inline in Reconcile, unconditionally run once
					// capacity planning succeeded, on the ModelRequest's
					// own namespace.
					"evalhub.trustyai.opendatahub.io/tenant": "",
				},
			},
		},
		{
			Name:              defaultPromotionStageName,
			Kind:              "PipelineRun",
			ProviderConfigRef: providerConfigRef,
			PerNamespace:      true,
			NamespaceSetup: &modelopsv1alpha1.StageNamespaceSetup{
				EnsureRBAC: true,
				Labels: map[string]string{
					// Was ensureMaaSNamespaceLabels, previously hardcoded
					// to only run in the promotion loop.
					"opendatahub.io/generated-namespace": "true",
					"maas.opendatahub.io/gateway-access":  "true",
					"opendatahub.io/dashboard":            "true",
				},
			},
		},
	}
}
