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

## Registry Writes are Best-Effort

A registry outage logs a WARNING and never fails the pipeline. The scan gates still enforce pass/fail independently.
