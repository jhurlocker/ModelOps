---
name: configure-maas-platform
description: Deploys the full OpenShift AI Models-as-a-Service (MaaS) platform infrastructure including Connectivity Link operator, Authorino TLS, PostgreSQL, monitoring, Gateway, DataScienceCluster enablement, namespaces, RBAC, and routing fixes. Use when setting up MaaS for the first time on a cluster or when any MaaS prerequisite is missing.
compatibility: Requires oc CLI, OpenShift cluster with cluster-admin, Red Hat Connectivity Link operator subscription access, and OpenShift AI.
---

# Configure MaaS Platform

Deploys the complete Models-as-a-Service platform on OpenShift. MaaS adds API-key authentication, Authorino authorization, and Limitador rate limiting behind a dedicated Gateway. Required before enabling the `deploy-maas` pipeline param.

**WARNING:** The `maas-default-gateway` can hijack `*.apps` wildcard DNS, breaking the OpenShift console, dashboard, and OAuth. Follow the DNS hijacking prevention steps carefully.

## Prerequisites

All five must be in place before enabling `modelsAsService` in the DataScienceCluster:

1. Red Hat Connectivity Link (rhcl-operator)
2. Authorino TLS enabled
3. PostgreSQL for MaaS API key storage
4. User Workload Monitoring enabled
5. `maas-default-gateway` Gateway in `openshift-ingress`

## Steps

### Step 1: Install Red Hat Connectivity Link

```bash
oc apply -f - <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: rhcl-operator
  namespace: openshift-operators
spec:
  channel: stable
  installPlanApproval: Manual
  name: rhcl-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
EOF

# Wait for install plan, then approve
until oc get installplan -n openshift-operators --no-headers | grep rhcl; do sleep 10; done
INSTALLPLAN=$(oc get installplan -n openshift-operators --no-headers | grep rhcl | grep -v true | awk '{print $1}')
oc patch installplan $INSTALLPLAN -n openshift-operators --type merge -p '{"spec":{"approved":true}}'
oc wait --for=jsonpath='{.status.phase}'=Succeeded csv/rhcl-operator.v1.4.1 -n openshift-operators --timeout=300s
```

If the install plan never appears, check for broken subscriptions blocking the sync loop:

```bash
for sub in $(oc get sub -n openshift-operators -o jsonpath='{.items[?(@.status.state=="")].metadata.name}'); do
  oc delete sub $sub -n openshift-operators
done
oc rollout restart deployment catalog-operator -n openshift-operator-lifecycle-manager
```

Create the Kuadrant CR:

```bash
oc new-project kuadrant-system || true
oc apply -f - <<EOF
apiVersion: kuadrant.io/v1beta1
kind: Kuadrant
metadata:
  name: kuadrant
  namespace: kuadrant-system
spec: {}
EOF
```

Verify CRDs exist:

```bash
oc get crd authpolicies.kuadrant.io
oc get crd ratelimitpolicies.kuadrant.io
```

If LLMInferenceServices are already deployed and stuck in `Pending`, the `llmisvc-controller-manager` may have cached the CRD absence. Restart it:

```bash
oc rollout restart deployment llmisvc-controller-manager -n redhat-ods-applications
oc wait -n redhat-ods-applications --for=condition=Available deployment/llmisvc-controller-manager --timeout=120s
```

### Step 2: Configure Authorino TLS

The Wasm plugin trusts ONLY the OpenShift service CA. Do NOT use self-signed certificates.

```bash
oc annotate svc authorino-authorino-authorization -n kuadrant-system \
  service.beta.openshift.io/serving-cert-secret-name=authorino-service-ca-tls --overwrite
sleep 10
oc get secret authorino-service-ca-tls -n kuadrant-system

AUTH_NAME=$(oc get authorino -n kuadrant-system -o jsonpath='{.items[0].metadata.name}')
oc patch authorino $AUTH_NAME -n kuadrant-system --type json -p "[
  {\"op\": \"add\", \"path\": \"/spec/listener/tls\", \"value\": {
    \"enabled\": true,
    \"certSecretRef\": {\"name\": \"authorino-service-ca-tls\"}
  }}
]"
oc wait -n kuadrant-system --for=condition=Available deployment/authorino --timeout=60s

# Verify gRPC started with TLS:
oc logs -n kuadrant-system deployment/authorino | grep "grpc auth service"
```

Restart the gateway:

```bash
oc rollout restart deployment maas-default-gateway-data-science-gateway-class -n openshift-ingress
oc wait -n openshift-ingress --for=condition=Available deployment/maas-default-gateway-data-science-gateway-class --timeout=120s
```

### Step 3: Deploy PostgreSQL for MaaS

Deploy in `redhat-ods-applications`:

```bash
oc apply -f - <<'EOF'
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: maas-db-pvc
  namespace: redhat-ods-applications
spec:
  accessModes: [ReadWriteOnce]
  resources: {requests: {storage: 10Gi}}
---
apiVersion: v1
kind: Secret
metadata:
  name: maas-db-credentials
  namespace: redhat-ods-applications
stringData:
  POSTGRESQL_USER: maas
  POSTGRESQL_PASSWORD: maas-demo-password
  POSTGRESQL_DATABASE: maas
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: maas-db
  namespace: redhat-ods-applications
  labels: {app: maas-db}
spec:
  replicas: 1
  selector: {matchLabels: {app: maas-db}}
  template:
    metadata: {labels: {app: maas-db}}
    spec:
      containers:
      - name: postgresql
        image: registry.redhat.io/rhel9/postgresql-16:latest
        ports: [{containerPort: 5432}]
        envFrom: [{secretRef: {name: maas-db-credentials}}]
        volumeMounts: [{name: data, mountPath: /var/lib/pgsql/data}]
      volumes: [{name: data, persistentVolumeClaim: {claimName: maas-db-pvc}}]
---
apiVersion: v1
kind: Service
metadata:
  name: maas-db
  namespace: redhat-ods-applications
  labels: {app: maas-db}
spec:
  ports: [{port: 5432, targetPort: 5432}]
  selector: {app: maas-db}
EOF

oc wait -n redhat-ods-applications --for=condition=Available deployment/maas-db --timeout=120s

oc create secret generic maas-db-config -n redhat-ods-applications \
  --from-literal=DB_CONNECTION_URL="postgresql://maas:maas-demo-password@maas-db.redhat-ods-applications.svc.cluster.local:5432/maas"
```

### Step 4: Enable User Workload Monitoring

```bash
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
```

### Step 5: Create MaaS Gateway (WITHOUT hostname)

Omit `hostname` in the listener spec to prevent DNS hijacking:

```bash
cat <<EOF | oc apply -f -
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

oc wait --for=condition=Programmed gateway/maas-default-gateway -n openshift-ingress --timeout=120s

# IMMEDIATELY check for DNS hijacking:
oc delete dnsrecord maas-default-gateway-*-wildcard -n openshift-ingress --ignore-not-found

# Verify console still works:
CLUSTER_DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')
curl -sk --connect-timeout 5 "https://console-openshift-console.apps.${CLUSTER_DOMAIN}" -o /dev/null -w "HTTP %{http_code}\n"
```

### Step 6: Enable MaaS in DataScienceCluster

```bash
oc patch dsc default-dsc --type json -p '[
  {"op": "replace", "path": "/spec/components/kserve/modelsAsService/managementState", "value": "Managed"}
]'
oc wait --for=jsonpath='{.status.conditions[?(@.type=="ModelsAsServiceReady")].status}'=True dsc/default-dsc --timeout=600s

# Verify:
oc get dsc -o jsonpath='{range .items[0].status.conditions[*]}{.type}={.status}{"\n"}{end}' | grep -E "Ready|ModelsAsService|Dashboard"
oc get pods -n redhat-ods-applications -l app.kubernetes.io/name=maas-api
oc get tenant default-tenant -n models-as-a-service
```

### Step 7: Create and Label MaaS Namespaces

```bash
oc new-project llm || echo "llm exists"
oc label namespace llm opendatahub.io/generated-namespace=true --overwrite
oc label namespace llm maas.opendatahub.io/gateway-access=true --overwrite
oc label namespace llm opendatahub.io/dashboard=true --overwrite

oc new-project models-as-a-service || echo "models-as-a-service exists"
```

Add `llm` to the `openshift-ai-inference` Gateway's allowed namespaces:

```bash
oc patch gateway openshift-ai-inference -n openshift-ingress --type json -p '[
  {"op": "add", "path": "/spec/listeners/0/allowedRoutes/namespaces/selector/matchExpressions/0/values/-", "value": "llm"}
]'

oc create secret tls default-gateway-tls -n openshift-ingress \
  --cert=<(oc get secret data-science-gateway-service-tls -n openshift-ingress -o jsonpath='{.data.tls\.crt}' | base64 -d) \
  --key=<(oc get secret data-science-gateway-service-tls -n openshift-ingress -o jsonpath='{.data.tls\.key}' | base64 -d)
```

### Step 8: Grant MaaS RBAC to Pipeline SA

```bash
oc policy add-role-to-user admin -z pipeline -n vllm --role-namespace=llm
oc policy add-role-to-user admin -z pipeline -n vllm --role-namespace=models-as-a-service
```

### Step 9: Fix MaaS API Routing

**9a. Remove hostname restriction from Tenant HTTPRoute:**

```bash
oc patch httproute maas-api-route -n redhat-ods-applications --type json -p '[
  {"op": "remove", "path": "/spec/hostnames"}
]'
```

**9b. Create OpenShift Route for `maas.<cluster-domain>`:**

```bash
CLUSTER_DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')
oc apply -f - <<EOF
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: maas-gateway
  namespace: openshift-ingress
spec:
  host: maas.${CLUSTER_DOMAIN}
  to:
    kind: Service
    name: maas-default-gateway-data-science-gateway-class
  port:
    targetPort: "http"
  tls:
    termination: edge
    insecureEdgeTerminationPolicy: Redirect
EOF
```

**9c. Add dashboard hostname to HTTPRoute:**

```bash
oc patch httproute rhods-dashboard -n redhat-ods-applications --type json -p "[
  {\"op\": \"add\", \"path\": \"/spec/hostnames\", \"value\": [\"rh-ai.${CLUSTER_DOMAIN}\"]}
]"
```

Restart dashboard:

```bash
oc rollout restart deployment rhods-dashboard -n redhat-ods-applications
oc wait -n redhat-ods-applications --for=condition=Available deployment/rhods-dashboard --timeout=120s
```

### Step 10: Verify MaaS API

```bash
CLUSTER_DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')
curl -sk --connect-timeout 5 "https://maas.${CLUSTER_DOMAIN}/maas-api/health"
```

Expected: `{"status":"healthy"}`.

### Step 11: Auth Proxy Fallback (when kuadrant WASM enforcement fails)

If the kuadrant AuthPolicy WASM plugin cannot enforce authentication (Gateway logs show `Failed to dispatch gRPC call to kuadrant-auth-service`, or `AuthPolicy` status shows `Enforced: False`), deploy a lightweight auth proxy that handles TokenReview and header injection without the Gateway.

**Symptoms requiring this step:**
- `curl https://maas.<cluster>/maas-api/health` returns 200, but authenticated requests return 500 `"Exception thrown while generating token"`
- MaaS API logs show `Missing or empty username header header="X-MaaS-Username"`
- Dashboard shows warning `"Models as a Service could not be loaded"`

```bash
CLUSTER_DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')

# 1. Deploy the auth proxy
oc apply -f skills/configure-maas-platform/assets/maas-auth-proxy.yaml
oc wait -n redhat-ods-applications --for=condition=Available deployment/maas-auth-proxy --timeout=120s

# 2. Point the MaaS Route at the proxy
oc delete route maas-api -n redhat-ods-applications --ignore-not-found
cat <<EOF | oc apply -f -
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: maas-api
  namespace: redhat-ods-applications
  annotations:
    haproxy.router.openshift.io/rewrite-target: /
  labels:
    app.kubernetes.io/component: api
    app.kubernetes.io/name: maas-api
    app.kubernetes.io/part-of: models-as-a-service
spec:
  host: maas.\${CLUSTER_DOMAIN}
  path: /maas-api
  port:
    targetPort: 8443
  tls:
    termination: edge
    insecureEdgeTerminationPolicy: Redirect
  to:
    kind: Service
    name: maas-auth-proxy
EOF

# 3. Restart the dashboard
oc rollout restart deployment rhods-dashboard -n redhat-ods-applications
oc wait -n redhat-ods-applications --for=condition=Available deployment/rhods-dashboard --timeout=120s

# 4. Verify
curl -sk -H "Authorization: Bearer \$(oc whoami -t)" \
  -X POST -H "Content-Type: application/json" -d '{}' \
  "https://maas.\${CLUSTER_DOMAIN}/maas-api/v1/api-keys/search"
# Expected: {"object":"list","data":null,"has_more":false}
```

See [auth-proxy.md](references/auth-proxy.md) for detailed architecture, tear-down, and troubleshooting.

## Gateway OOMKill Prevention

RHOAI 3.5.0 introduced an "AI Gateway" architecture that serves the dashboard
and MaaS through Sail/Istio gateways (`istio-proxy` based). These gateways have
a default **1Gi** memory limit that is too small once Kuadrant WASM filters and
Authorino auth load — the pods get OOMKilled (exit 137) and restart in a loop,
taking down the dashboard (`rh-ai.apps.<domain>`) and MaaS.

There are **two** gateways in `openshift-ingress`, and both must be bumped:

| Gateway (`gateway.networking.k8s.io`) | params ConfigMap | Purpose |
|---|---|---|
| `data-science-gateway` | `data-science-gateway-config` | Dashboard + AI Gateway (operator-owned) |
| `maas-default-gateway` | `maas-gw-options` | MaaS API |

The Sail gateway controller renders the deployment from the Gateway's
`spec.infrastructure.parametersRef` ConfigMap. A `deployment` key in that
ConfigMap overrides the pod template (including `istio-proxy` resources):

```bash
# Dashboard gateway (operator-owned ConfigMap, but the operator preserves
# extra data keys — safe to merge into)
oc patch configmap data-science-gateway-config -n openshift-ingress --type merge -p '{"data":{"deployment":"spec:\n  template:\n    spec:\n      containers:\n      - name: istio-proxy\n        resources:\n          requests:\n            cpu: 100m\n            memory: 256Mi\n          limits:\n            cpu: \"2\"\n            memory: 2Gi\n"}}'

# MaaS gateway (GitOps-managed via gitops/components/maas/gateway-config.yaml)
oc patch configmap maas-gw-options -n openshift-ingress --type merge -p '{"data":{"deployment":"spec:\n  template:\n    spec:\n      containers:\n      - name: istio-proxy\n        resources:\n          requests:\n            cpu: 100m\n            memory: 256Mi\n          limits:\n            cpu: \"2\"\n            memory: 2Gi\n"}}'
```

The deployment resources are picked up automatically when the Sail gateway
controller reconciles; verify with:

```bash
oc get deploy -n openshift-ingress data-science-gateway-data-science-gateway-class \
  -o jsonpath='{.spec.template.spec.containers[0].resources.limits.memory}{"\n"}'
# Expected: 2Gi
```

**Diagnosing an OOMKilled gateway:**
```bash
oc get pods -n openshift-ingress | grep gateway
# CrashLoopBackOff with RESTARTS climbing = OOMKilled
oc get pod <pod> -n openshift-ingress -o jsonpath='{.status.containerStatuses[0].lastState.terminated.reason}'
# Expected: OOMKilled (exit code 137)
```

### RHOAI operator auto-upgrade gotcha

The `rhods-operator` Subscription is usually `installPlanApproval: Automatic` on
the `stable-3.x` channel, so RHOAI can auto-upgrade (e.g. 3.4.3 → 3.5.0) and
silently introduce the AI Gateway on a running cluster. After any RHOAI minor
upgrade, re-check the gateway memory limits and dashboard route (`rh-ai.apps.<domain>`).

## Gotchas

- **DNS hijacking**: If all `*.apps` URLs break (console, dashboard timeout on port 443), the Gateway controller created a wildcard DNS record. See `references/dns-hijacking.md` for the recovery procedure.
- **ModelsAsServiceReady: False (PrerequisitesNotMet)**: One of the five prerequisites is missing. Check: `oc get secret maas-db-config -n redhat-ods-applications`, Authorino TLS, monitoring ConfigMap, Gateway.
- **Rhcl-operator install plan never appears**: Broken subscriptions in `openshift-operators` block the catalog-operator sync loop. See Step 1 cleanup commands.
- **MaaS namespace not labeled for Gateway access**: Without `maas.opendatahub.io/gateway-access=true`, HTTPRoutes from the namespace are rejected.
- **MaaS API returns 401/403/429**: 401 = invalid API key, 403 = MaaSAuthPolicy denies access, 429 = rate limit hit.
- **LLMInferenceService stuck in Pending after kuadrant install**: `llmisvc-controller-manager` caches CRD availability. Restart it. See Step 1.
- **AuthPolicy not enforced (Enforced: False, MissingResource)**: The `Kuadrant` CR is missing or kuadrant controller isn't running. Create the Kuadrant CR — the operator needs it to wire Authorino, Limitador, and Wasm.
- **Kuadrant WASM plugin fails to load**: Authorino gRPC may be unreachable from the Gateway Envoy. Try restarting the Gateway first. If `Failed to dispatch gRPC call` persists, deploy the auth proxy fallback (Step 11).
- **maas-ui cannot reach MaaS API (503/500)**: The Gateway Route may not correctly forward traffic. Use the auth proxy fallback (Step 11) to bypass Gateway auth routing.

## References

- [DNS hijacking recovery procedure](references/dns-hijacking.md)
- [MaaS LLMInferenceService troubleshooting](references/troubleshooting-maas.md)
- [Auth proxy fallback (bypass kuadrant WASM)](references/auth-proxy.md)
