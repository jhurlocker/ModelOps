# Auth Proxy Fallback

## When to Use

Deploy the auth proxy when kuadrant AuthPolicy WASM enforcement is broken and the MaaS API is unreachable through the Gateway. Symptoms:

- `maas-ui` logs show `request to maas-api failed statusCode=500 error="Exception thrown while generating token"`
- MaaS API logs show `Missing or empty username header header="X-MaaS-Username"`
- RHOAI dashboard shows warning: `"Models as a Service could not be loaded"`
- API Keys section fails to load
- `oc get authpolicy maas-api-auth-policy -n redhat-ods-applications -o jsonpath='{.status.conditions[?(@.type=="Enforced")].status}'` returns `False` with reason `MissingResource`

## How It Works

The MaaS API requires `X-MaaS-Username` and `X-MaaS-Group` headers, which are normally injected by the kuadrant AuthPolicy after performing a Kubernetes TokenReview on the bearer token.

When the kuadrant Gateway WASM plugin fails (e.g., `kuadrant-wasm-shim` can't load, or can't reach Authorino gRPC), the AuthPolicy is never enforced. The auth proxy bridges this gap:

```
Client (maas-ui) → OpenShift Route (edge TLS, path rewrite)
               → Auth Proxy (TokenReview + header injection)
               → MaaS API
```

The proxy:
1. Receives plain HTTP requests on port 8443
2. Extracts the `Authorization: Bearer <token>` header
3. Performs a `TokenReview` against `https://kubernetes.default.svc`
4. Extracts the authenticated username and groups
5. Adds `X-MaaS-Username` and `X-MaaS-Group` (JSON array string) headers
6. Forwards to `https://maas-api.redhat-ods-applications.svc.cluster.local:8443`

## Deployment

```bash
CLUSTER_DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')

# 1. Apply the auth proxy (RBAC, ConfigMap, Deployment, Service)
oc apply -f skills/configure-maas-platform/assets/maas-auth-proxy.yaml

# 2. Wait for the proxy to become ready
oc wait -n redhat-ods-applications --for=condition=Available deployment/maas-auth-proxy --timeout=120s

# 3. Replace the existing MaaS Route to point at the proxy
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
  host: maas.${CLUSTER_DOMAIN}
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

# 4. Restart the dashboard to pick up the working API
oc rollout restart deployment rhods-dashboard -n redhat-ods-applications
oc wait -n redhat-ods-applications --for=condition=Available deployment/rhods-dashboard --timeout=120s
```

## Verification

```bash
CLUSTER_DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')
USER_TOKEN=$(echo "sha256~$(oc whoami -t)" | cut -c1-100) # truncated for display

# Health check
curl -sk "https://maas.${CLUSTER_DOMAIN}/maas-api/health"
# Expected: {"status":"healthy"}

# Authenticated request
curl -sk -H "Authorization: Bearer $(oc whoami -t)" \
  -X POST -H "Content-Type: application/json" -d '{}' \
  "https://maas.${CLUSTER_DOMAIN}/maas-api/v1/api-keys/search"
# Expected: {"object":"list","data":null,"has_more":false}

# Check proxy logs
oc logs -n redhat-ods-applications deploy/maas-auth-proxy --tail=5
# Expected: "OK: admin groups=[\"system:authenticated\",\"system:masters\"]"
```

## Tear Down (Restore Gateway-AuthPath)

When kuadrant AuthPolicy enforcement is fixed, remove the proxy and restore the Gateway Route:

```bash
oc delete route maas-api -n redhat-ods-applications --ignore-not-found
oc delete -f skills/configure-maas-platform/assets/maas-auth-proxy.yaml --ignore-not-found
```

Then ensure the HTTPRoute `maas-api-route` in `redhat-ods-applications` is present and the Gateway Route in `openshift-ingress` routes traffic through the Gateway (as configured in Step 9 of the main SKILL.md).
