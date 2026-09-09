# Model Registry Troubleshooting

## REST Connection Fails

The pipeline talks to the in-cluster REST service, NOT MySQL directly and NOT via an external route. No auth required.

### Check MySQL Backend

```bash
oc run -n vllm mysql-test --image=mysql:8 --rm -i --restart=Never -- \
  mysql -h mysql.rhoai-model-registries.svc.cluster.local -u admin -pmysql-admin
```

### Check Registry Instance

```bash
oc get modelregistry.modelregistry.opendatahub.io -n rhoai-model-registries
oc get deploy,svc -n rhoai-model-registries | grep modelops-registry
```

### Check REST Endpoint

```bash
oc run -n vllm mr-check --image=registry.access.redhat.com/ubi9/ubi-minimal --rm -i --restart=Never -- \
  curl -s -o /dev/null -w "%{http_code}\n" \
  http://modelops-registry.rhoai-model-registries.svc.cluster.local:8080/api/model_registry/v1alpha3/registered_models
```

Expected: `200`.

## Instance Won't Provision

- Verify MySQL pod is running: `oc get pods -n rhoai-model-registries | grep mysql`
- Check MySQL service: `oc get svc mysql -n rhoai-model-registries`
- Check the ModelRegistry CR events: `oc describe modelregistry modelops-registry -n rhoai-model-registries`

## Registry Writes 404 on `/api/model_registry/v1`

Unpinned `pip install model-registry` clients probe `/api/model_registry/v1/registered_models` on connect. This RHOAI REST server only serves **v1alpha3**, so the client gets `404 page not found` and the pipeline logs `Skipping registry update` (writes are best-effort, so the TaskRun still succeeds).

Confirm the live API:

```bash
curl -sS http://modelops-registry.rhoai-model-registries.svc.cluster.local:8080/api/model_registry/v1alpha3/registered_models
# 200

curl -sS -o /dev/null -w "%{http_code}\n" \
  http://modelops-registry.rhoai-model-registries.svc.cluster.local:8080/api/model_registry/v1/registered_models
# 404
```

The pipeline tasks talk to `v1alpha3` with stdlib `urllib` (no Python SDK).

## Registry Writes are Best-Effort

A registry outage logs a WARNING and never fails the pipeline. The scan gates still enforce pass/fail independently.
