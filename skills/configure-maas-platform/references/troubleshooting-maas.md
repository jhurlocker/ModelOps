# MaaS Troubleshooting

## LLMInferenceService Never Becomes Ready

Check pod status:

```bash
oc get pods -n llm -l serving.kserve.io/llminferenceservice=<model-name>
```

Common causes:

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
