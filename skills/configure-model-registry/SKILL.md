---
name: configure-model-registry
description: Deploys the OpenShift AI Model Registry with a MySQL backend and verifies the in-cluster REST service. Use when setting up the model registry for the ModelOps pipeline or troubleshooting registry connectivity.
compatibility: Requires oc CLI, OpenShift cluster with the RHOAI modelregistry component set to Managed in the DataScienceCluster.
---

# Configure Model Registry

Deploys a MySQL-backed OpenShift AI Model Registry instance in the `rhoai-model-registries` namespace. The pipeline talks to this registry's in-cluster REST service to register/update models at every stage.

## Prerequisites

Verify the RHOAI modelregistry component is Managed:

```bash
oc get datasciencecluster -o jsonpath='{.items[0].spec.components.modelregistry.managementState}{"\n"}'
```

Expected: `Managed`. If `Removed`/`Unmanaged`:

```bash
oc patch datasciencecluster default-dsc --type merge \
  -p '{"spec":{"components":{"modelregistry":{"managementState":"Managed","registriesNamespace":"rhoai-model-registries"}}}}'
```

## Deployment

### 1. Deploy MySQL Backend

```bash
oc apply -f model_onboarding_pipeline/model-registry/mysql-secret.yaml
oc apply -f model_onboarding_pipeline/model-registry/mysql-pvc.yaml
oc apply -f model_onboarding_pipeline/model-registry/mysql-service.yaml
oc apply -f model_onboarding_pipeline/model-registry/mysql-deployment.yaml
oc wait -n rhoai-model-registries --for=condition=Ready pod -l name=mysql --timeout=180s
```

### 2. Create Registry Instance

```bash
oc apply -f model_onboarding_pipeline/model-registry/modelregistry-instance.yaml
oc wait -n rhoai-model-registries --for=condition=Available \
  modelregistry.modelregistry.opendatahub.io/modelops-registry --timeout=300s
```

### 3. Verify REST Service

The pipeline uses the in-cluster REST service (no auth required):

```bash
oc run -n vllm mr-check --image=registry.access.redhat.com/ubi9/ubi-minimal --rm -i --restart=Never -- \
  curl -s -o /dev/null -w "%{http_code}\n" \
  http://modelops-registry.rhoai-model-registries.svc.cluster.local:8080/api/model_registry/v1alpha3/registered_models
```

Expected: `200`.

The pipeline's `mr-server` / `mr-port` params default to `http://modelops-registry.rhoai-model-registries.svc.cluster.local` : `8080`. If you name the instance differently, update those params.

## Gotchas

- **Registry writes are best-effort**: A registry outage logs a warning and never fails the pipeline by itself. The scan gates still enforce pass/fail.
- **Instance won't provision**: Check MySQL is healthy: `oc get pods -n rhoai-model-registries | grep mysql`
- **401/403 on REST**: The in-cluster REST service has no auth. External access (via Route) requires OAuth.

## References

- [Troubleshooting registry connectivity](references/troubleshooting-registry.md)
