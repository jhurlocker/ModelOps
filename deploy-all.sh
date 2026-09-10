#!/usr/bin/env bash
# Deploy the full ModelOps stack onto the currently logged-in OpenShift cluster.
#
# Phases (same order as skills/SKILL.md):
#   1. S3 / MinIO
#   2. EvalHub
#   3. Model Registry
#   4. MaaS platform          (--skip-maas to omit)
#   5. Results viewer
#   6. Model Intake UI        (--skip-build to reuse an existing image)
#   7. Tekton pipeline
#
# Usage:
#   ./deploy-all.sh
#   ./deploy-all.sh --skip-maas
#   ./deploy-all.sh --skip-build --skip-maas
#   ./deploy-all.sh --help
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PIPELINE_NS="${PIPELINE_NS:-vllm}"
STAGING_NS="${STAGING_NS:-vllm-staging}"
S3_NS="${S3_NS:-s3-storage}"
REGISTRY_NS="${REGISTRY_NS:-rhoai-model-registries}"
RHOAI_NS="${RHOAI_NS:-redhat-ods-applications}"
GPU_OP_NS="${GPU_OP_NS:-nvidia-gpu-operator}"

SKIP_MAAS=0
SKIP_BUILD=0

log()  { printf '\n==> %s\n' "$*"; }
ok()   { printf '    %s\n' "$*"; }
warn() { printf '    WARN: %s\n' "$*" >&2; }
die()  { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
  sed -n '2,20p' "$0" | sed 's/^# \?//'
  exit 0
}

need_oc() {
  command -v oc >/dev/null 2>&1 || die "oc CLI not found"
  oc whoami >/dev/null 2>&1 || die "not logged in; run oc login first"
  ok "logged in as $(oc whoami) @ $(oc whoami --show-server)"
}

ensure_ns() {
  local ns="$1"
  oc get ns "$ns" >/dev/null 2>&1 || oc create ns "$ns"
}

apply() {
  oc apply -f "$1"
}

wait_ready_label() {
  local ns="$1" selector="$2" timeout="${3:-180s}"
  oc wait -n "$ns" --for=condition=Ready pod -l "$selector" --timeout="$timeout"
}

wait_available() {
  local ns="$1" deploy="$2" timeout="${3:-180s}"
  oc wait -n "$ns" --for=condition=Available "deployment/$deploy" --timeout="$timeout"
}

dsc_name() {
  oc get dsc -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}

# ---------------------------------------------------------------------------
# Phase 1: MinIO + S3 browser + buckets
# ---------------------------------------------------------------------------
phase_s3() {
  log "Phase 1: S3 storage"
  ensure_ns "$S3_NS"
  apply "$ROOT/model_onboarding_pipeline/storage/minio-backend.yaml"
  apply "$ROOT/model_onboarding_pipeline/storage/s3ui-deployment.yaml"
  wait_ready_label "$S3_NS" app=minio 180s
  wait_ready_label "$S3_NS" app=s3-ui 180s

  oc delete job -n "$S3_NS" minio-init-buckets --ignore-not-found
  oc create -n "$S3_NS" -f - <<'EOF'
apiVersion: batch/v1
kind: Job
metadata:
  name: minio-init-buckets
spec:
  ttlSecondsAfterFinished: 300
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: init
          image: registry.access.redhat.com/ubi9/python-311:latest
          command: ["python3", "-c"]
          args:
            - |
              import subprocess, sys
              subprocess.check_call([sys.executable, "-m", "pip", "install", "-q", "boto3"])
              import boto3
              from botocore.client import Config
              client = boto3.client(
                  "s3",
                  endpoint_url="http://minio-service.s3-storage.svc.cluster.local:9000",
                  aws_access_key_id="minio",
                  aws_secret_access_key="minio123",
                  config=Config(signature_version="s3v4"),
                  region_name="us-east-1",
              )
              for bucket in ("benchmark-results", "compliance-artifact-results", "security-scan-results"):
                  try:
                      client.create_bucket(Bucket=bucket)
                      print("Created bucket:", bucket)
                  except Exception as e:
                      print("Bucket", bucket, ":", e)
EOF
  oc wait -n "$S3_NS" --for=condition=complete job/minio-init-buckets --timeout=180s
  ok "S3 UI: https://$(oc get route -n "$S3_NS" s3-ui-route -o jsonpath='{.spec.host}')"
}

# ---------------------------------------------------------------------------
# Phase 2: EvalHub
# ---------------------------------------------------------------------------
phase_evalhub() {
  log "Phase 2: EvalHub"
  local dsc
  dsc="$(dsc_name)"
  if [[ -z "$dsc" ]]; then
    warn "no DataScienceCluster found; skipping trustyai patch"
  else
    oc patch datasciencecluster "$dsc" --type merge -p \
      '{"spec":{"components":{"trustyai":{"managementState":"Managed","eval":{"lmeval":{"permitCodeExecution":"allow","permitOnline":"allow"}}}}}}' \
      >/dev/null
    oc rollout restart deployment trustyai-service-operator-controller-manager -n "$RHOAI_NS" 2>/dev/null || true
  fi

  apply "$ROOT/model_onboarding_pipeline/evalhub/evalhub-cr.yaml"
  oc wait -n "$RHOAI_NS" --for=condition=Ready evalhub.trustyai.opendatahub.io/evalhub --timeout=180s || \
    warn "EvalHub CR not Ready yet; continuing"
}

# ---------------------------------------------------------------------------
# Phase 3: Model Registry
# ---------------------------------------------------------------------------
phase_registry() {
  log "Phase 3: Model Registry"
  local dsc
  dsc="$(dsc_name)"
  if [[ -n "$dsc" ]]; then
    oc patch datasciencecluster "$dsc" --type merge -p \
      "{\"spec\":{\"components\":{\"modelregistry\":{\"managementState\":\"Managed\",\"registriesNamespace\":\"$REGISTRY_NS\"}}}}" \
      >/dev/null
  fi
  ensure_ns "$REGISTRY_NS"
  apply "$ROOT/model_onboarding_pipeline/model-registry/mysql-secret.yaml"
  apply "$ROOT/model_onboarding_pipeline/model-registry/mysql-pvc.yaml"
  apply "$ROOT/model_onboarding_pipeline/model-registry/mysql-service.yaml"
  apply "$ROOT/model_onboarding_pipeline/model-registry/mysql-deployment.yaml"
  wait_ready_label "$REGISTRY_NS" name=mysql 180s
  apply "$ROOT/model_onboarding_pipeline/model-registry/modelregistry-instance.yaml"
  oc wait -n "$REGISTRY_NS" --for=condition=Available \
    modelregistry.modelregistry.opendatahub.io/modelops-registry --timeout=300s || \
    warn "ModelRegistry CR not Available yet; continuing"
  apply "$ROOT/model_onboarding_pipeline/model-registry/networkpolicy.yaml"
}

# ---------------------------------------------------------------------------
# Phase 4: MaaS (optional)
# ---------------------------------------------------------------------------
phase_maas() {
  log "Phase 4: MaaS platform"
  if oc get sub rhcl-operator -n openshift-operators >/dev/null 2>&1; then
    ok "rhcl-operator subscription already exists"
  else
    oc apply -f - <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: rhcl-operator
  namespace: openshift-operators
spec:
  channel: stable
  installPlanApproval: Automatic
  name: rhcl-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
EOF
  fi

  local waited=0
  until oc get crd authpolicies.kuadrant.io >/dev/null 2>&1; do
    if (( waited >= 300 )); then
      warn "Kuadrant CRDs not installed after 5m; skipping remaining MaaS steps"
      return 0
    fi
    sleep 10
    waited=$((waited + 10))
  done

  ensure_ns kuadrant-system
  oc apply -f - <<EOF
apiVersion: kuadrant.io/v1beta1
kind: Kuadrant
metadata:
  name: kuadrant
  namespace: kuadrant-system
spec: {}
EOF

  oc annotate svc authorino-authorino-authorization -n kuadrant-system \
    service.beta.openshift.io/serving-cert-secret-name=authorino-service-ca-tls --overwrite >/dev/null || true
  sleep 8
  local auth_name
  auth_name="$(oc get authorino -n kuadrant-system -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
  if [[ -n "$auth_name" ]]; then
    oc patch authorino "$auth_name" -n kuadrant-system --type merge -p \
      '{"spec":{"listener":{"tls":{"enabled":true,"certSecretRef":{"name":"authorino-service-ca-tls"}}}}}' \
      >/dev/null || true
  fi

  apply "$ROOT/model_onboarding_pipeline/maas/maas-db.yaml"
  wait_available "$RHOAI_NS" maas-db 180s || warn "maas-db not Available yet"
  oc create secret generic maas-db-config -n "$RHOAI_NS" \
    --from-literal=DB_CONNECTION_URL="postgresql://maas:maas-demo-password@maas-db.${RHOAI_NS}.svc.cluster.local:5432/maas" \
    --dry-run=client -o yaml | oc apply -f -

  oc apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: cluster-monitoring-config
  namespace: openshift-monitoring
data:
  config.yaml: |
    enableUserWorkload: true
EOF

  oc apply -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: maas-default-gateway
  namespace: openshift-ingress
spec:
  gatewayClassName: data-science-gateway-class
  listeners:
    - name: http
      port: 80
      protocol: HTTP
EOF
  oc delete dnsrecord -n openshift-ingress -l gateway.networking.k8s.io/gateway-name=maas-default-gateway --ignore-not-found >/dev/null 2>&1 || true
  oc get dnsrecord -n openshift-ingress --no-headers 2>/dev/null | awk '/wildcard/ {print $1}' | \
    xargs -r -I{} oc delete dnsrecord {} -n openshift-ingress --ignore-not-found || true

  local dsc
  dsc="$(dsc_name)"
  if [[ -n "$dsc" ]]; then
    oc patch dsc "$dsc" --type json -p \
      '[{"op":"replace","path":"/spec/components/kserve/modelsAsService/managementState","value":"Managed"}]' \
      >/dev/null || warn "could not enable modelsAsService on $dsc"
  fi

  ensure_ns llm
  oc label namespace llm opendatahub.io/generated-namespace=true --overwrite >/dev/null
  oc label namespace llm maas.opendatahub.io/gateway-access=true --overwrite >/dev/null
  oc label namespace llm opendatahub.io/dashboard=true --overwrite >/dev/null
  ensure_ns models-as-a-service

  oc policy add-role-to-user admin -z pipeline -n llm >/dev/null 2>&1 || true
  oc policy add-role-to-user admin -z pipeline -n models-as-a-service >/dev/null 2>&1 || true

  local domain
  domain="$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')"
  oc apply -f - <<EOF
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: maas-gateway
  namespace: openshift-ingress
spec:
  host: maas.${domain}
  to:
    kind: Service
    name: maas-default-gateway-data-science-gateway-class
  port:
    targetPort: "http"
  tls:
    termination: edge
    insecureEdgeTerminationPolicy: Redirect
EOF
  oc patch httproute maas-api-route -n "$RHOAI_NS" --type json -p '[{"op":"remove","path":"/spec/hostnames"}]' >/dev/null 2>&1 || true
  oc patch configmap -n openshift-ingress gateway-resources \
    --type merge -p '{"data":{"memory-limits":"2Gi"}}' >/dev/null 2>&1 || true
  ok "MaaS route: https://maas.${domain}/maas-api/health"
}

# ---------------------------------------------------------------------------
# Phase 5: Results viewer
# ---------------------------------------------------------------------------
phase_results_ui() {
  log "Phase 5: Results viewer"
  ensure_ns "$PIPELINE_NS"
  local ui_dir="$ROOT/model_onboarding_pipeline/results-ui"

  if [[ "$SKIP_BUILD" -eq 0 ]]; then
    oc get is benchmark-viewer -n "$PIPELINE_NS" >/dev/null 2>&1 || \
      oc create imagestream benchmark-viewer -n "$PIPELINE_NS"
    if ! oc get bc benchmark-viewer -n "$PIPELINE_NS" >/dev/null 2>&1; then
      oc new-build --binary --strategy=docker --name=benchmark-viewer \
        -n "$PIPELINE_NS" --to=benchmark-viewer:latest
    fi
    oc start-build benchmark-viewer -n "$PIPELINE_NS" --from-dir="$ui_dir" --follow
  else
    ok "skipping image build (--skip-build)"
  fi

  oc apply -n "$PIPELINE_NS" -f "$ui_dir/deployment.yaml"
  if [[ "$SKIP_BUILD" -eq 0 ]]; then
    oc rollout restart deployment/benchmark-viewer -n "$PIPELINE_NS" >/dev/null 2>&1 || true
  fi
  wait_ready_label "$PIPELINE_NS" app=benchmark-viewer 180s || warn "benchmark-viewer not Ready yet"
  ok "Results UI: https://$(oc get route -n "$PIPELINE_NS" benchmark-viewer -o jsonpath='{.spec.host}' 2>/dev/null || echo '(route pending)')"
}

# ---------------------------------------------------------------------------
# Phase 6: Model Intake UI (build from this repo, then apply)
# ---------------------------------------------------------------------------
phase_intake_ui() {
  log "Phase 6: Model Intake UI"
  ensure_ns "$PIPELINE_NS"
  local ui_dir="$ROOT/model_onboarding_pipeline/model-intake-ui"

  if [[ "$SKIP_BUILD" -eq 0 ]]; then
    oc get is model-intake-ui -n "$PIPELINE_NS" >/dev/null 2>&1 || \
      oc create imagestream model-intake-ui -n "$PIPELINE_NS"
    if ! oc get bc model-intake-ui -n "$PIPELINE_NS" >/dev/null 2>&1; then
      oc new-build --binary --strategy=docker --name=model-intake-ui \
        -n "$PIPELINE_NS" --to=model-intake-ui:latest
    fi
    oc start-build model-intake-ui -n "$PIPELINE_NS" --from-dir="$ui_dir" --follow
  else
    ok "skipping image build (--skip-build)"
  fi

  oc apply -n "$PIPELINE_NS" -f "$ui_dir/deployment.yaml"
  if [[ "$SKIP_BUILD" -eq 0 ]]; then
    oc rollout restart deployment/model-intake -n "$PIPELINE_NS" >/dev/null 2>&1 || true
  fi
  wait_ready_label "$PIPELINE_NS" app=model-intake 180s
  ok "Intake UI: https://$(oc get route -n "$PIPELINE_NS" model-intake -o jsonpath='{.spec.host}')"
}

# ---------------------------------------------------------------------------
# Phase 7: Tekton pipeline + RBAC
# ---------------------------------------------------------------------------
ensure_gpu_hardware_profile() {
  # deploy-model references HardwareProfile "gpu" in the RHOAI applications
  # namespace. The ODH webhook denies InferenceServices that name a missing
  # profile (this cluster's dashboard profile is "gpu", not "gpu-profile").
  if ! oc get crd hardwareprofiles.infrastructure.opendatahub.io >/dev/null 2>&1; then
    warn "HardwareProfile CRD not installed; skipping gpu profile (OpenShift AI required for model deploy)"
    return 0
  fi
  oc apply -n "$RHOAI_NS" -f "$ROOT/model_onboarding_pipeline/model-intake-pipeline/pipeline/gpu-hardware-profile.yaml"
  ok "HardwareProfile gpu in $RHOAI_NS"
}

phase_pipeline() {
  log "Phase 7: Tekton pipeline"
  apply "$ROOT/model_onboarding_pipeline/model-intake-pipeline/pipeline/sandbox-namespace.yaml"
  apply "$ROOT/model_onboarding_pipeline/model-intake-pipeline/pipeline/staging-namespace.yaml"
  oc label namespace "$PIPELINE_NS" evalhub.trustyai.opendatahub.io/tenant= --overwrite >/dev/null || true
  oc label namespace "$STAGING_NS" evalhub.trustyai.opendatahub.io/tenant= --overwrite >/dev/null || true

  ensure_gpu_hardware_profile

  apply "$ROOT/model_onboarding_pipeline/model-intake-pipeline/pipeline/gpu-sharing-rbac.yaml"
  apply "$ROOT/model_onboarding_pipeline/model-intake-pipeline/pipeline/evalhub-rbac.yaml"
  apply "$ROOT/model_onboarding_pipeline/model-intake-pipeline/pipeline/inferenceservice-rbac.yaml"
  apply "$ROOT/model_onboarding_pipeline/model-intake-pipeline/pipeline/pvc.yaml"

  oc create configmap mmlu-manifest -n "$PIPELINE_NS" \
    --from-file="$ROOT/model_onboarding_pipeline/model-intake-pipeline/pipeline/mmlu.yaml" \
    --dry-run=client -o yaml | oc apply -f -
  oc create configmap custom-mmlu -n "$PIPELINE_NS" \
    --from-file=custom-mmlu.yaml="$ROOT/model_onboarding_pipeline/model-intake-pipeline/custom-lm-eval/custom-mmlu.yaml" \
    --dry-run=client -o yaml | oc apply -f -

  oc create sa pipeline -n "$PIPELINE_NS" --dry-run=client -o yaml | oc apply -f -
  oc policy add-role-to-user edit -z pipeline -n "$PIPELINE_NS" >/dev/null
  oc adm policy add-cluster-role-to-user cluster-reader -z pipeline -n "$PIPELINE_NS" >/dev/null
  oc adm policy add-scc-to-user anyuid -z pipeline -n "$PIPELINE_NS" >/dev/null
  oc adm policy add-scc-to-user anyuid -z default -n "$PIPELINE_NS" >/dev/null || true
  oc adm policy add-scc-to-user anyuid -z default -n "$STAGING_NS" >/dev/null || true

  local pipe="$ROOT/model_onboarding_pipeline/model-intake-pipeline/pipeline"
  local task
  for task in \
    compliance-artifact-scan-task.yaml \
    gpu-advisor-task.yaml \
    approval-gate-task.yaml \
    apply-gpu-sharing-task.yaml \
    deploy-model-task.yaml \
    security-scan-task.yaml \
    teardown-model-task.yaml \
    grant-model-access-task.yaml \
    guidellm-benchmark-task.yaml \
    upload-guidellm-results-task.yaml \
    model-registry-task.yaml \
    deploy-maas-task.yaml \
    model-intake-pipeline.yaml
  do
    oc apply -n "$PIPELINE_NS" -f "$pipe/$task"
  done

  # The sample Pipeline/PipelineRun YAML may still carry a previous cluster's
  # EvalHub host. Point the live Pipeline defaults at this cluster's route.
  local evalhub domain
  evalhub="$(oc get route evalhub -n "$RHOAI_NS" -o jsonpath='{.spec.host}' 2>/dev/null || true)"
  domain="$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}' 2>/dev/null || true)"
  if [[ -n "$evalhub" || -n "$domain" ]]; then
    EVALHUB_HOST="$evalhub" CLUSTER_DOMAIN="$domain" \
      oc get pipeline.tekton.dev model-intake-pipeline -n "$PIPELINE_NS" -o json | python3 -c '
import json, os, sys
pipe = json.load(sys.stdin)
evalhub = os.environ.get("EVALHUB_HOST") or ""
domain = os.environ.get("CLUSTER_DOMAIN") or ""
for param in pipe.get("spec", {}).get("params", []):
    name = param.get("name")
    if evalhub and name == "evalhub-url":
        param["default"] = evalhub
    if domain and name == "openshift-console-domain":
        param["default"] = domain
json.dump(pipe, sys.stdout)
' | oc apply -n "$PIPELINE_NS" -f - >/dev/null
    [[ -n "$evalhub" ]] && ok "EvalHub host: $evalhub"
  fi

  ok "Tasks: $(oc get tasks.tekton.dev -n "$PIPELINE_NS" --no-headers 2>/dev/null | wc -l | tr -d ' ')"
  ok "Pipeline: $(oc get pipeline.tekton.dev -n "$PIPELINE_NS" --no-headers 2>/dev/null | awk '{print $1}')"
  ok "Garak profiles: quality,avid_security,cwe (override with garak-benchmarks)"
}

print_summary() {
  local domain intake results s3
  domain="$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}' 2>/dev/null || true)"
  intake="$(oc get route -n "$PIPELINE_NS" model-intake -o jsonpath='{.spec.host}' 2>/dev/null || true)"
  results="$(oc get route -n "$PIPELINE_NS" benchmark-viewer -o jsonpath='{.spec.host}' 2>/dev/null || true)"
  s3="$(oc get route -n "$S3_NS" s3-ui-route -o jsonpath='{.spec.host}' 2>/dev/null || true)"
  log "Deploy complete"
  [[ -n "$intake"  ]] && ok "Model Intake UI : https://$intake"
  [[ -n "$results" ]] && ok "Results viewer  : https://$results"
  [[ -n "$s3"      ]] && ok "S3 browser      : https://$s3"
  [[ -n "$domain" && "$SKIP_MAAS" -eq 0 ]] && ok "MaaS API        : https://maas.${domain}/maas-api/health"
  ok "Submit a run from the Intake UI, or: oc create -n $PIPELINE_NS -f $ROOT/model_onboarding_pipeline/model-intake-pipeline/pipeline/model-intake-pipelinerun.yaml"
}

# ---------------------------------------------------------------------------
main() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --skip-maas)  SKIP_MAAS=1 ;;
      --skip-build) SKIP_BUILD=1 ;;
      -h|--help)    usage ;;
      *)            die "unknown argument: $1 (try --help)" ;;
    esac
    shift
  done

  need_oc
  phase_s3
  phase_evalhub
  phase_registry
  if [[ "$SKIP_MAAS" -eq 0 ]]; then
    phase_maas
  else
    log "Phase 4: MaaS skipped (--skip-maas)"
  fi
  phase_results_ui
  phase_intake_ui
  phase_pipeline
  print_summary
}

main "$@"
