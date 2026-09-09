"""Helpers for rendering ModelRequest.Status in a human-readable way.

Since the operator's Phase 6/7 stage-walker refactor
(docs/REFACTOR_PLAN.md), ModelRequest.Status.Phase is fully generic:
"<CurrentStage>Running" while a stage is in progress (e.g.
"sandboxRunning", "promotionRunning"), or a bare "Succeeded"/"Failed"
once the walk finishes, or one of several *LookupFailed/*SetupFailed/
NoStagesConfigured reasons for a config/lookup error. The pre-refactor
UI rendered this string directly, which used to look fine
("SandboxRunning") but now renders as an un-styled, all-lowercase blob
("sandboxRunning"). See docs/PHASE_LOG.md's Phase 7 entry for the exact
before/after Phase values.

Status.CurrentStage (the ProfileStageSpec.Name the walker is/was on) is
a better primary label for the *running* case, since it's just the
stage name and can be humanized independently of Phase's casing.
CurrentStage is NOT a good label for a terminal outcome, though:
- On success, the operator's walker clears CurrentStage entirely
  (internal/stagewalk.Walk returns a zero-value CurrentStage on
  OutcomeSucceeded) -- so this case is naturally handled by falling
  back to Phase ("Succeeded") when CurrentStage is empty.
- On failure, CurrentStage is NOT cleared -- it stays set to whichever
  stage actually failed (confirmed against a live ModelRequest on the
  sandbox cluster: currentStage="sandbox", phase="Failed"). Showing the
  stage name alone here would hide the fact that it failed, so Phase's
  two genuine terminal values ("Succeeded"/"Failed") are always shown
  verbatim, even when CurrentStage is non-empty.
"""

import re

_TERMINAL_PHASES = {"Succeeded", "Failed"}


def humanize_stage_name(name):
    """Turn a stage identifier (e.g. "sandbox", "promotion-staging")
    into a readable, title-cased label ("Sandbox", "Promotion Staging").
    """
    if not name:
        return ""
    words = re.split(r"[-_\s]+", str(name))
    return " ".join(w[:1].upper() + w[1:] for w in words if w)


def status_label(status):
    """Primary human-readable status label for a ModelRequest.

    Prefers a humanized Status.CurrentStage over the raw Status.Phase
    string while a stage is running. Falls back to Phase verbatim for:
    - the two genuine terminal outcomes ("Succeeded"/"Failed"), so a
      completed/failed request doesn't just show a stage name, and
    - any ModelRequest with no CurrentStage set at all (a lookup/setup
      failure that never reached the stage walker, or an object
      reconciled by a pre-Phase-6 operator version that never wrote
      this field).
    """
    status = status or {}
    phase = status.get("phase") or "Unknown"
    if phase in _TERMINAL_PHASES:
        return phase
    current_stage = status.get("currentStage")
    if current_stage:
        return humanize_stage_name(current_stage)
    return phase


def status_badge_class(status):
    """CSS badge class for a ModelRequest's status, keyed off the raw
    Phase (CurrentStage carries no success/failure information of its
    own)."""
    status = status or {}
    phase = status.get("phase") or ""
    if phase == "Succeeded":
        return "badge-success"
    if phase == "Failed":
        return "badge-danger"
    if phase.endswith("Running"):
        return "badge-info"
    if not phase or phase == "Unknown":
        return "badge-neutral"
    # Any other value is one of the *LookupFailed/*SetupFailed/
    # NoStagesConfigured reasons -- a blocked/needs-attention state,
    # some of which retry on a bounded requeue rather than being
    # permanent, so "warning" rather than "danger".
    return "badge-warning"


def stage_progress_badge_class(phase):
    """CSS badge class for one Status.Stages[] entry's own Phase
    (stagecommon.StagePhase's string values: "Running"/"Succeeded"/
    "Failed")."""
    if phase == "Succeeded":
        return "badge-success"
    if phase == "Failed":
        return "badge-danger"
    if phase == "Running":
        return "badge-info"
    return "badge-neutral"


def resolve_stage_providers(profile):
    """For each stage declared on a ModelLifecycleProfile, report which
    IntakeProviderConfig it resolves to (spec.stages[].providerConfigRef,
    Phase 5-7 of docs/REFACTOR_PLAN.md), or the legacy inline pipeline
    ref it falls back to when that's unset.

    Display-only: this never fetches the referenced IntakeProviderConfig
    object itself (the UI's ServiceAccount has no RBAC for that kind,
    and this is meant to make the pluggable-provider seam visible when
    demoing, not to let users pick/switch providers from the UI) -- it
    only reports what the profile's own spec says it would resolve to,
    mirroring internal/stages/tekton/providerconfig.go's
    resolveProviderDetails.
    """
    if not profile:
        return []
    spec = profile.get("spec", {}) or {}
    stages = spec.get("stages", []) or []
    workflow = spec.get("workflow", {}) or {}
    rows = []
    for stage in stages:
        name = stage.get("name", "")
        kind = stage.get("kind", "")
        ref = stage.get("providerConfigRef")
        if ref:
            provider = "{} ({})".format(ref.get("name", "—"), ref.get("kind") or "IntakeProviderConfig")
        elif kind == "PipelineRun":
            # DEPRECATED fallback, only resolvable here for the two
            # built-in stage names that have a dedicated Handler
            # reading workflow.pipelineRef/promotionPipelineRef
            # (internal/stages/sandbox/promotion's PipelineNameOrDefault)
            # -- a custom-named PipelineRun stage's fallback depends on
            # which Go package main.go registered for that name, which
            # isn't visible from the profile object alone.
            if name == "sandbox":
                provider = "legacy pipelineRef: {} (deprecated)".format(
                    workflow.get("pipelineRef") or "model-intake-sandbox")
            elif name == "promotion":
                provider = "legacy promotionPipelineRef: {} (deprecated)".format(
                    workflow.get("promotionPipelineRef") or "model-intake-promotion")
            else:
                provider = "not set (deprecated inline pipelineRef fallback)"
        else:
            # e.g. the CapacityPlan kind, which never resolves a
            # provider config at all.
            provider = "—"
        rows.append({
            "name": name,
            "kind": kind,
            "required": stage.get("required", True),
            "per_namespace": stage.get("perNamespace", False),
            "provider": provider,
        })
    return rows
