# DNS Hijacking Recovery

The `maas-default-gateway` Gateway controller automatically creates a DNSRecord (`maas-default-gateway-*-wildcard`) in `openshift-ingress` targeting `*.apps.<cluster-domain>`. This overwrites Route53 with the MaaS gateway's NLB IPs instead of the OpenShift router's CLB IPs, causing ALL HTTPS traffic (console, dashboard, OAuth, Routes) to time out because the gateway only listens on port 80.

## Symptoms

- All `*.apps.<cluster-domain>` URLs timeout on port 443
- Console, dashboard, all Routes return HTTP 000
- IngressController shows `Degraded=True` with `CanaryChecksRepetitiveFailures`
- `dig *.apps.<cluster-domain>` returns different IPs than the router CLB

## Recovery Procedure

```bash
CLUSTER_DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')

# 1. Delete the offending DNS record
oc delete dnsrecord maas-default-gateway-*-wildcard -n openshift-ingress

# 2. Force ingress operator to re-ensure the wildcard DNS
oc annotate dnsrecord default-wildcard -n openshift-ingress-operator \
  force-reconcile="$(date +%s)" --overwrite

# 3. Trigger reconciliation by toggling TTL
oc patch dnsrecord default-wildcard -n openshift-ingress-operator --type json -p '[
  {"op": "replace", "path": "/spec/recordTTL", "value": 60}
]'
oc patch dnsrecord default-wildcard -n openshift-ingress-operator --type json -p '[
  {"op": "replace", "path": "/spec/recordTTL", "value": 30}
]'

# 4. Wait for DNS propagation (~60s), then verify
sleep 60
curl -sk --connect-timeout 5 "https://console-openshift-console.apps.${CLUSTER_DOMAIN}" -o /dev/null -w "HTTP %{http_code}\n"
# Expect: HTTP 200
```

## If DNS Record Keeps Recreating

The Gateway controller has a cached reference. Delete and recreate the Gateway:

```bash
oc delete gateway maas-default-gateway -n openshift-ingress
oc delete dnsrecord maas-default-gateway-*-wildcard -n openshift-ingress
# Then recreate the Gateway from the MaaS setup skill (Step 5)
```

## Prevention

When creating the Gateway, omit the `hostname` field from the listener spec (as the MaaS setup skill instructs).
