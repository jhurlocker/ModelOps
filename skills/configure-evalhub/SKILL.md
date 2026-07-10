---
name: configure-evalhub
description: Deploys EvalHub (TrustyAI evaluation orchestration) for the ModelOps pipeline and runs smoke tests against garak and GuideLLM providers. Use when setting up the evaluation infrastructure for model security and performance benchmarking.
compatibility: Requires oc CLI, OpenShift cluster with RHOAI trustyai component Managed, and eval sub-component configured for online access.
---

# Configure EvalHub

EvalHub provides standardized LLM evaluation benchmarks (garak security, GuideLLM performance, lm-evaluation-harness) as managed Kubernetes Jobs. The CR must be deployed in `redhat-ods-applications` for dashboard discovery.

## Prerequisites

1. RHOAI **trustyai component** must be `Managed` in the DataScienceCluster.
2. The **eval sub-component** must permit online access and code execution.

Check and configure:

```bash
# Verify trustyai is Managed
oc get datasciencecluster -o jsonpath='{.items[0].spec.components.trustyai.managementState}{"\n"}'

# Enable trustyai + eval sub-component
oc patch datasciencecluster default-dsc --type merge \
  -p '{"spec":{"components":{"trustyai":{"managementState":"Managed","eval":{"lmeval":{"permitCodeExecution":"allow","permitOnline":"allow"}}}}}}'

# Restart TrustyAI operator
oc rollout restart deployment trustyai-service-operator-controller-manager -n redhat-ods-applications
oc wait -n redhat-ods-applications --for=condition=Ready pod -l app.kubernetes.io/part-of=trustyai --timeout=120s
```

## Deployment

### 1. Deploy EvalHub Instance

```bash
oc apply -f model_onboarding_pipeline/evalhub/evalhub-cr.yaml
oc wait -n redhat-ods-applications --for=condition=Ready evalhub.trustyai.opendatahub.io/evalhub --timeout=120s
```

### 2. Verify

```bash
EVALHUB_URL=$(oc get route evalhub -n redhat-ods-applications -o jsonpath='{.spec.host}')
TOKEN=$(oc whoami -t)
curl -k -s -H "Authorization: Bearer $TOKEN" "https://$EVALHUB_URL/api/v1/health"
curl -k -s -H "Authorization: Bearer $TOKEN" "https://$EVALHUB_URL/api/v1/evaluations/providers"
```

### 3. Restart Dashboard

```bash
oc rollout restart deployment rhods-dashboard -n redhat-ods-applications
oc wait -n redhat-ods-applications --for=condition=Available deployment/rhods-dashboard --timeout=180s
```

The **Develop & train → Evaluations** page in the OpenShift AI web UI should now show as active.

### 4. Set Up Tenant Namespace

Evaluations require a tenant namespace with the EvalHub label:

```bash
TENANT_NS="<your-model-namespace>"
oc label namespace "$TENANT_NS" evalhub.trustyai.opendatahub.io/tenant= --overwrite
sleep 5
oc get sa "evalhub-redhat-ods-applications-job" -n "$TENANT_NS"
oc get rolebindings -n "$TENANT_NS" | grep evalhub
```

Without the tenant label, evaluation jobs stay `pending` forever — EvalHub cannot create Jobs in the target namespace.

## Smoke Tests

Run these after deployment to validate end-to-end.

### Garak Security Smoke Test (~15s)

```bash
EVALHUB_URL=$(oc get route evalhub -n redhat-ods-applications -o jsonpath='{.spec.host}')
TOKEN=$(oc whoami -t)
MODEL_URL="<your-inference-endpoint>"
MODEL_NAME="<model-name>"

JOB_RESPONSE=$(curl -k -s -X POST "https://$EVALHUB_URL/api/v1/evaluations/jobs" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Tenant: $TENANT_NS" \
  -d '{"name":"garak-smoke","model":{"url":"'"$MODEL_URL"'","name":"'"${MODEL_NAME:-test-model}"'"},"benchmarks":[{"id":"quick","provider_id":"garak"}]}')
JOB_ID=$(echo "$JOB_RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin)['resource']['id'])")

for i in $(seq 1 30); do
  STATE=$(curl -k -s -H "Authorization: Bearer $TOKEN" -H "X-Tenant: $TENANT_NS" \
    "https://$EVALHUB_URL/api/v1/evaluations/jobs/$JOB_ID" \
    | python3 -c "import sys,json; print(json.load(sys.stdin).get('status',{}).get('state','unknown'))")
  case "$STATE" in completed|failed|cancelled) break ;; esac
  sleep 10
done
```

Expected: `state: completed` with `attack_success_rate` populated.

### GuideLLM Performance Smoke Test (~30s)

Uses a `constant` profile with short duration:

```bash
JOB_RESPONSE=$(curl -k -s -X POST "https://$EVALHUB_URL/api/v1/evaluations/jobs" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Tenant: $TENANT_NS" \
  -d '{"name":"guidellm-smoke","model":{"url":"'"$MODEL_URL"'","name":"'"${MODEL_NAME:-test-model}"'"},"benchmarks":[{"id":"constant","provider_id":"guidellm","parameters":{"profile":"constant","rate":1,"max_seconds":30,"max_requests":5,"warmup":"0"}}]}')
```

Expected: `state: completed` with throughput metrics.

## Gotchas

- **Garak fails with unrecognized arguments**: The garak CLI changed between v0.3.x and v0.15.x. Use `--target_type` (not `--model`), `--generator_options` (not `--model_args`), `--report_prefix` (not `--output_json_path`), `--skip_unknown` to skip probes that don't exist.
- **Probes not found in garak 0.15**: Old probe names like `availability`, `off_topic_safety_cases`, `leaky_completion` don't exist. Use: `apikey.GetKey,atkgen.Tox,dan.AutoDANCached,dan.DanInTheWild,encoding.InjectBase64,leakreplay.GuardianCloze`. Pass `--skip_unknown` to skip unknown ones.
- **EvalHub uses namespace multi-tenancy**: The `X-Tenant` header controls the target namespace. Set it to the namespace where the InferenceService runs.
- **Lighteval smoke test**: Currently commented out in the original SKILL.md — Lighteval's litellm adapter only supports generative benchmarks; loglikelihood tasks raise `NotImplementedError`.

## References

- [Garak troubleshooting details](references/troubleshooting-garak.md)
