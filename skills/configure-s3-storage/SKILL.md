---
name: configure-s3-storage
description: Deploys MinIO S3-compatible object storage and an S3 browser UI for the ModelOps pipeline. Creates the buckets needed by pipeline scan and benchmark tasks. Use when setting up storage infrastructure for model onboarding.
compatibility: Requires oc CLI and an OpenShift cluster.
---

# Configure S3 Storage

Deploys MinIO for pipeline scan reports and benchmark results, plus a web-based S3 browser. The pipeline tasks read/write compliance/security scan reports and benchmark results to these buckets.

## Deployment

### 1. Deploy MinIO Backend + S3 UI

```bash
oc new-project s3-storage || echo "s3-storage exists"
oc apply -n s3-storage -f model_onboarding_pipeline/storage/minio-backend.yaml
oc apply -n s3-storage -f model_onboarding_pipeline/storage/s3ui-deployment.yaml
```

Wait for pods:

```bash
oc wait -n s3-storage --for=condition=Ready pod -l app=minio --timeout=120s
oc wait -n s3-storage --for=condition=Ready pod -l app=s3-ui --timeout=120s
```

### 2. Verify Access

```bash
S3_ROUTE=$(oc get route -n s3-storage s3-ui-route -o jsonpath='{.spec.host}')
echo "S3 UI: https://$S3_ROUTE"
```

### 3. Create Buckets

The `mc` CLI image may have filesystem permission issues. Use the Python boto3 approach instead:

```bash
oc run -n s3-storage minio-client --image=registry.access.redhat.com/ubi9/python-311:latest --rm -i --restart=Never -- sh -c '
pip install -q boto3 && python3 -c "
import boto3
from botocore.client import Config
client = boto3.client(\"s3\",
    endpoint_url=\"http://minio-service.s3-storage.svc.cluster.local:9000\",
    aws_access_key_id=\"minio\",
    aws_secret_access_key=\"minio123\",
    config=Config(signature_version=\"s3v4\"),
    region_name=\"us-east-1\")
for bucket in [\"benchmark-results\", \"compliance-artifact-results\", \"security-scan-results\"]:
    try:
        client.create_bucket(Bucket=bucket)
        print(f\"Created bucket: {bucket}\")
    except Exception as e:
        print(f\"Bucket {bucket}: {e}\")
"'
```

The scan buckets (`compliance-artifact-results`, `security-scan-results`) are auto-created by the pipeline tasks. The `benchmark-results` bucket must be created manually.

## Default Credentials

- Endpoint (in-cluster): `http://minio-service.s3-storage.svc.cluster.local:9000`
- Access key: `minio`
- Secret key: `minio123`

These are the pipeline defaults for `scan-s3-endpoint`, `scan-s3-access-key-id`, `scan-s3-secret-access-key`.

## Gotchas

- MinIO uses a 20Gi PVC. If the cluster lacks a default StorageClass that supports RWO, the PVC will stay Pending.
- The `minio-client` pod image is large; first pull may take a minute.
