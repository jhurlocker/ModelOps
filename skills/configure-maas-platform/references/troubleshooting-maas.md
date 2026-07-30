# MaaS Troubleshooting

## LLMInferenceService Never Becomes Ready

Check pod status:

```bash
oc get pods -n llm -l serving.kserve.io/llminferenceservice=<model-name>
```

Common causes:

- **AuthPolicy CRD cached by controller**: `llmisvc-controller-manager` caches CRD availability on startup. If `authpolicies.kuadrant.io` was installed after the controller started, restart it:
  ```bash
  oc rollout restart deployment llmisvc-controller-manager -n redhat-ods-applications
  oc wait -n redhat-ods-applications --for=condition=Available deployment/llmisvc-controller-manager --timeout=120s
  ```
  The LLMInferenceService should reconcile within ~30s.
- **Image pull failure**: Runtime image requires a pull secret. Check `oc describe pod`.
- **No GPU available**: Pod stays Pending if no GPU node exists or all GPUs are occupied.
- **HuggingFace Xet download hang**: If using `hf://` URIs, the KServe initializer may hang. Patch:
  ```bash
  oc patch deployment <model-name>-kserve -n llm --type=json \
    -p '[{"op":"add","path":"/spec/template/spec/initContainers/0/env/-","value":{"name":"HF_HUB_DISABLE_XET","value":"1"}}]'
  ```
- **Gateway OOMKill**: Wasm extensions push Istio gateway past 1Gi. See SKILL.md Gateway OOMKill Prevention section.

## MaaS API Returns 401/403/429

| Code | Meaning | Fix |
|------|---------|-----|
| 401 | Invalid/expired/revoked API key | Create new key |
| 403 | MaaSAuthPolicy denies access or model not in subscription | `oc get maasauthpolicy -n models-as-a-service -o yaml` |
| 429 | Rate limit hit | Wait or upgrade subscription tier |

## MaaS UI Shows "Some models may be unavailable" Warning

The RHOAI dashboard warning `"Models as a Service could not be loaded"` means the `maas-ui` sidecar cannot reach the MaaS API. Check the chain:

```bash
# 1. Check maas-ui logs for the error cause
oc logs -n redhat-ods-applications deploy/rhods-dashboard -c maas-ui --tail=20 | grep -i "error\|maas"

# 2. Try reaching the MaaS API directly
CLUSTER_DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')
curl -sk "https://maas.${CLUSTER_DOMAIN}/maas-api/health"
```

Common status codes from maas-ui logs:

| Status | Meaning | Fix |
|--------|---------|-----|
| 503 | No route to MaaS API (DNS/gateway down) | See [auth-proxy.md](auth-proxy.md) fallback |
| 404 | Route exists but path mapping is wrong | Check Route `haproxy.router.openshift.io/rewrite-target` annotation |
| 500 `"Exception thrown while generating token"` | Auth headers missing, AuthPolicy not enforced | Check AuthPolicy enforcement or deploy auth proxy |
| 200 with `"data":null` | API is working correctly | May need to wait for UI polling cycle |

## AuthPolicy Not Enforced

The MaaS controller creates an `AuthPolicy` in `redhat-ods-applications` named `maas-api-auth-policy`. This policy performs `kubernetesTokenReview` on bearer tokens and injects `X-MaaS-Username`/`X-MaaS-Group` headers. If enforcement fails, the MaaS API rejects all authenticated requests.

Check enforcement status:

```bash
oc get authpolicy -n redhat-ods-applications maas-api-auth-policy \
  -o jsonpath='{range .status.conditions[*]}{.type}={.status}: {.reason} - {.message}{"\n"}{end}'
```

`Enforced=False` with reason `MissingResource` means the `Kuadrant` CR is missing or the kuadrant controller is not running:

```bash
# Check if Kuadrant CR exists
oc get kuadrant -A

# If missing, create it
cat <<EOF | oc apply -f -
apiVersion: kuadrant.io/v1beta1
kind: Kuadrant
metadata:
  name: kuadrant
spec: {}
EOF

# Confirm enforcement
oc get authpolicy -n redhat-ods-applications maas-api-auth-policy \
  -o jsonpath='{.status.conditions[?(@.type=="Enforced")].status}'
# Expected: True
```

## Kuadrant WASM Plugin Fails to Load

The kuadrant WASM plugin (`kuadrant-wasm-shim`) runs inside the Gateway's Envoy proxy. If it can't load or can't reach Authorino, all AuthPolicy-enforced traffic will fail.

Check Gateway logs:

```bash
oc logs -n openshift-ingress deploy/maas-default-gateway-data-science-gateway-class --tail=50 | grep -i "wasm"
```

Common WASM errors:

| Error | Cause | Fix |
|-------|-------|-----|
| `failed to load (in progress) from ...plugin.wasm` | WASM binary can't download | Check `kuadrant-operator-wasm` service in `openshift-operators` is running |
| `Plugin kuadrant-wasm-shim failed to load` / `Plugin configured to fail closed` | WASM loaded but can't initialize | Restart the Gateway to force reload: `oc rollout restart deployment maas-default-gateway-data-science-gateway-class -n openshift-ingress` |
| `Failed to dispatch gRPC call to kuadrant-auth-service` | WASM can't reach Authorino gRPC | Authorino deployment or service is missing. The WASM creates a gRPC cluster named `kuadrant-auth-service` that must resolve to Authorino. Deploy the auth proxy fallback. |
| `Failed to dispatch gRPC call to kuadrant-ratelimit-service` | WASM can't reach Limitador gRPC | Rate limiting will fail open (allowed), not a blocker for auth |
| `gRPC status code is not OK` | Auth check reaches Authorino but returns non-OK | Check Authorino logs. May indicate `gateway-default-auth` deny rule matching. Deploy the passthrough Route fallback below. |
| `Plugin kuadrant-wasm-shim failed to load` on Gateway restart | WASM binary corrupt in Envoy cache | The WASM VM runtime caches a bad binary. Delete the Gateway pod to force fresh download, or use the passthrough Route fallback. |

If WASM failures persist after restarting the Gateway and verifying Authorino/Limitador are running, use the [passthrough Route fallback](#passthrough-route-for-inference) for inference or the [auth proxy fallback](auth-proxy.md) for the MaaS API.

## Passthrough Route for Inference

When the kuadrant WASM plugin blocks Gateway inference traffic, create a passthrough TLS Route that bypasses the Gateway entirely. vLLM serves HTTPS via KServe-provisioned certs, so the Route must use `passthrough` termination.

```bash
CLUSTER_DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')
model=granite-2b
namespace=staging

cat <<EOF | oc apply -f -
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: ${model}-inference
  namespace: ${namespace}
spec:
  host: ${model}-${namespace}.\${CLUSTER_DOMAIN}
  port:
    targetPort: 8000
  tls:
    termination: passthrough
  to:
    kind: Service
    name: ${model}-kserve-workload-svc
EOF

curl -sk -X POST "https://${model}-${namespace}.${CLUSTER_DOMAIN}/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -d '{"model":"'"${model}"'","messages":[{"role":"user","content":"Hello"}],"max_tokens":20}'
```

The LLMInferenceService `spec.router.route: {}` should auto-create this Route. If it doesn't, create it manually.

**Important:** vLLM serves HTTPS whenever `--ssl-certfile` and `--ssl-keyfile` are provided. Do NOT include `--enable-ssl-refresh` — KServe rotates certs via delete-then-recreate, which triggers the SSL refresher on every intermediate file event (including "deleted"). This causes brief SSL context invalidation and can fail probes. vLLM's static SSL context loaded at startup is sufficient for KServe's long-lived self-signed certs.

## MaaS Model Not Appearing in Dashboard

```bash
oc label namespace llm opendatahub.io/dashboard=true --overwrite
oc get llminferenceservice <model-name> -n llm -o jsonpath='{.metadata.labels.opendatahub\.io/dashboard}'
# Expect: true
```

## Deploy-maas Task Fails with "forbidden"

Pipeline SA lacks RBAC in target namespaces. Use `--role-namespace` flag for cross-namespace bindings:

```bash
oc policy add-role-to-user admin -z pipeline -n vllm --role-namespace=llm
oc policy add-role-to-user admin -z pipeline -n vllm --role-namespace=models-as-a-service
```

## ModelsAsServiceReady: False (PrerequisitesNotMet)

One of the five prerequisites is missing. The full error lists each item. Common checks:

```bash
oc get secret maas-db-config -n redhat-ods-applications
oc get authorino -n kuadrant-system -o jsonpath='{.items[0].spec.listener.tls}'
oc get cm cluster-monitoring-config -n openshift-monitoring
oc get gateway maas-default-gateway -n openshift-ingress
```

If not ready to deploy all prerequisites, set MaaS to `Removed` temporarily to keep the DSC healthy:

```bash
oc patch dsc default-dsc --type json -p '[
  {"op": "replace", "path": "/spec/components/kserve/modelsAsService/managementState", "value": "Removed"}
]'
```

## Disabling Token Authentication for Testing

Annotate the LLMInferenceService to allow unauthenticated access:

```bash
oc annotate llminferenceservice <model-name> -n llm \
  security.opendatahub.io/enable-auth=false --overwrite
```

Alternatively, the LLMInferenceService creates a direct passthrough Route (bypassing Gateway auth) at:
`https://<model-name>-llm.apps.<cluster-domain>/v1/chat/completions`
