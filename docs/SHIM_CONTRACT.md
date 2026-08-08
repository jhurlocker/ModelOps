# Shim Contract

The canonical HTTP contract for a webhook shim — a standalone translation
service that sits between the ModelOps operator's generic
`WebhookProviderConfig`-backed `StageRunner` and one specific external
platform (SageMaker, Azure AI, Vertex AI, Databricks, etc.).

A conformant shim is a single HTTP server that implements this contract.
The translation from platform-native vocabulary into the canonical
vocabulary happens **inside the shim**, so every
`WebhookProviderConfig` instance pointing at a conformant shim uses
identical `phaseJsonPath`, `phaseValueMap`, `submitJobIDJsonPath`,
`messageTemplate`, and `detailsUrlTemplate` values — only the shim's
service hostname and its `authSecretRef` differ.

This contract is the stable, versioned integration surface other teams
build against. It does not change lightly.

## Endpoints

### `GET /health`

Returns `200 OK` with an empty body. No authentication required.

Every conformant shim must implement this endpoint. It is what the UI
consumes for live reachability checks and what an operator uses for
readiness probes. A shim that does not respond to `/health` is not
conformant.

### `POST /jobs`

Submits a new job to the external platform.

- **Request body:** The rendered output of the `WebhookProviderConfig`'s
  `requestTemplate`. The shim must accept `application/json` and parse
  the body without imposing any schema requirements beyond valid JSON
  — the schema is defined by the template, not by the shim.
- **Authentication:** Required. The shim's auth middleware checks the
  `Authorization: Bearer <token>` header before the request reaches any
  platform logic (see Authentication below).
- **Success response:** `201 Created`
  ```json
  {"jobId": "<opaque-platform-identifier>"}
  ```
- **Error responses:** `401` if the bearer token is missing or invalid
  (handled by auth middleware). `500` with an empty body if the platform
  submission fails. `502` with an empty body if the platform API call
  succeeds but the response does not contain a parseable job identifier.

The `jobId` returned here becomes the `{{.JobID}}` value injected into
the operator's `statusEndpoint` template on every subsequent poll. It
must be stable and round-trippable: `GET /jobs/{jobId}` must locate the
same job the shim submitted in this call.

### `GET /jobs/{jobId}`

Returns the current status of a previously submitted job.

- **Authentication:** Required (same bearer token check as `POST /jobs`).
- **Success response:** `200 OK`
  ```json
  {
    "phase": "Running" | "Succeeded" | "Failed",
    "message": "<human-readable status detail>",
    "detailsUrl": "<link to the platform's own console or job page>"
  }
  ```
- **Error responses:** `401` if the bearer token is missing or invalid.
  `404` with an empty body if the `jobId` does not correspond to any
  known job. `500` with an empty body if the platform status check fails.

`phase` must be exactly one of the three strings `"Running"`,
`"Succeeded"`, or `"Failed"` — the shim translates the platform's own
vocabulary (SageMaker `"Executing"`, Azure `"Completed"`, etc.) into
this canonical set before responding. The operator's
`phaseValueMap: {Running: Running, Succeeded: Succeeded, Failed: Failed}`
identity mapping relies on this: if a shim emits any other value,
the operator classifies it as `StageRunning` with an
`"unrecognized provider phase"` message.

`message` is free-form text. For a `Failed` phase, it should include
the platform's error or failure reason. It may be an empty string.

`detailsUrl` is a human-facing link out of the operator's UI to the
platform's own console, logs, or job page. It may be an empty string
if the platform does not have a web console or the shim cannot
construct a deep link.

## Authentication

Every endpoint except `/health` requires a bearer token, checked by
the shim before any platform logic executes.

```
Authorization: Bearer <shared-token>
```

The token is a single opaque string stored in a Kubernetes Secret and
referenced from the `WebhookProviderConfig` via `authSecretRef`:

```yaml
authSecretRef:
  name: sagemaker-shim-token
  key: token
```

This reuses the existing `SecretKeyRef`-only pattern from Phase 8
(no inline credential values in CRDs). The operator reads the Secret
and sends the value as a bearer token on every submit and poll HTTP
call. The shim extracts it from the `Authorization` header and
compares it against the token it received through its own environment
(e.g. the `SHIM_AUTH_TOKEN` environment variable).

Zero auth between operator and shim undermines the evidence-chain
premise: an attacker with cluster network access who can send an
unauthenticated `POST /jobs` to a shim can cause arbitrary platform
workloads to be launched and attributed to the operator. The bearer
token closes this gap without inventing a new auth mechanism — it
reuses the existing `SecretKeyRef` → `Authorization` header pattern
the `webhook.StageRunner` already implements for every HTTP call.

The token is a shared secret between one operator instance and its
shims, not a per-tenant or per-ModelRequest credential. Rotating it
means updating the Secret and restarting the shim.

### Recommended additional layer: NetworkPolicy

Shim-level bearer-token auth is mandatory. Kubernetes `NetworkPolicy`
restricting ingress to the shim's pods from only the operator's
namespace is a recommended additional layer — defense in depth, not a
substitute for the shim checking its own `Authorization` header.

## `WebhookProviderConfig` mapping

Every conformant shim is addressed by a `WebhookProviderConfig` instance
whose `statusMapping` values are **identical across all shims**:

| Field | Value (identical for all conformant shims) |
|---|---|
| `submitJobIDJsonPath` | `{.jobId}` |
| `statusMapping.phaseJsonPath` | `{.phase}` |
| `statusMapping.phaseValueMap` | `Running: Running`<br>`Succeeded: Succeeded`<br>`Failed: Failed` |
| `statusMapping.messageTemplate` | `{{.Response.message}}` |
| `statusMapping.detailsUrlTemplate` | `{{.Response.detailsUrl}}` |

The only fields that differ between shim provider configs are
`submitEndpoint`, `statusEndpoint`, and `authSecretRef` — the
shim's service hostname and its bearer-token Secret reference.

### Worked example: SageMaker shim

```yaml
apiVersion: modelops.example.io/v1alpha1
kind: WebhookProviderConfig
metadata:
  name: sagemaker-provider
spec:
  providerType: webhook
  submitEndpoint: http://sagemaker-shim.sandbox.svc.cluster.local:8080/jobs
  method: POST
  authSecretRef:
    name: sagemaker-shim-token
    key: token
  requestTemplate: |
    {
      "pipelineName": "model-validation",
      "pipelineParameters": {
        "modelId": "{{.ModelRequest.Spec.Model.URI}}"
      },
      "pipelineExecutionDisplayName": "{{.ModelRequest.Name}}"
    }
  submitJobIDJsonPath: "{.jobId}"
  statusEndpoint: "http://sagemaker-shim.sandbox.svc.cluster.local:8080/jobs/{{.JobID}}"
  statusMapping:
    phaseJsonPath: "{.phase}"
    phaseValueMap:
      Running: Running
      Succeeded: Succeeded
      Failed: Failed
    messageTemplate: "{{.Response.message}}"
    detailsUrlTemplate: "{{.Response.detailsUrl}}"
```

### Worked example: Azure AI shim

```yaml
apiVersion: modelops.example.io/v1alpha1
kind: WebhookProviderConfig
metadata:
  name: azure-ai-provider
spec:
  providerType: webhook
  submitEndpoint: http://azure-ai-shim.sandbox.svc.cluster.local:8080/jobs
  method: POST
  authSecretRef:
    name: azure-ai-shim-token
    key: token
  requestTemplate: |
    {
      "experimentName": "model-validation",
      "displayName": "{{.ModelRequest.Name}}",
      "pipelineDefinitionId": "model-intake-pipeline",
      "parameters": {
        "modelId": "{{.ModelRequest.Spec.Model.URI}}"
      }
    }
  submitJobIDJsonPath: "{.jobId}"
  statusEndpoint: "http://azure-ai-shim.sandbox.svc.cluster.local:8080/jobs/{{.JobID}}"
  statusMapping:
    phaseJsonPath: "{.phase}"
    phaseValueMap:
      Running: Running
      Succeeded: Succeeded
      Failed: Failed
    messageTemplate: "{{.Response.message}}"
    detailsUrlTemplate: "{{.Response.detailsUrl}}"
```

## Reference shims

Two reference implementations prove the contract against the two
genuinely different auth patterns identified in this project:
AWS SigV4 request signing (no static token exists) and OAuth2
client-credentials (a refreshable bearer token). Both live under
`tools/webhook-shims/` alongside the reusable `shim-template` skeleton.

### `shim-template/`

A minimal, runnable Flask server implementing the full contract shape.
The shim author fills in exactly two functions:

- `submit_to_platform(payload: dict) -> str` — submit to the real
  platform, return the platform's job ID.
- `check_status_on_platform(job_id: str) -> tuple[str, str, str]` —
  return `(phase, message, detailsUrl)`.

Everything else — HTTP routing, the canonical response JSON shape, the
`/health` endpoint, and the bearer-token auth middleware — is
boilerplate the author never touches.

### `sagemaker/`

Uses boto3 (`sagemaker.start_pipeline_execution`,
`sagemaker.describe_pipeline_execution`). SigV4 signing is handled by
boto3's credential chain — the shim never constructs a signature
manually and the operator never sees AWS credentials.

SageMaker `PipelineExecutionStatus` mapping:

| SageMaker | Canonical |
|---|---|
| `Executing` | `Running` |
| `Stopping` | `Running` |
| `Succeeded` | `Succeeded` |
| `Failed` | `Failed` |
| `Stopped` | `Failed` |

`PipelineExecutionFailureReason` → `message`.

### `azure-ai/`

Uses `azure-identity` (`DefaultAzureCredential`) and `azure-ai-ml`
(`MLClient`). Token acquisition and refresh are internal to the shim
— the operator never touches Azure AD.

Azure ML job status mapping:

| Azure ML | Canonical |
|---|---|
| `NotStarted`, `Queued`, `Preparing`, `Running`, `Finalizing` | `Running` |
| `Completed` | `Succeeded` |
| `Failed`, `Canceled` | `Failed` |

Error details from the job's error object → `message`.

## Testing

Each reference shim ships with pytest tests:

1. **Contract conformance test:** Starts the Flask app via
   `app.test_client()`, calls `POST /jobs` and `GET /jobs/<id>` with
   a valid bearer token, and asserts the response JSON has exactly the
   canonical shape — no extra keys, no missing keys.
2. **Auth middleware test:** Asserts that an unauthenticated request
   (missing or invalid `Authorization` header) to any protected endpoint
   is rejected with `401` before it reaches the platform fill-in
   functions.
3. **Unit tests with mocked platform SDK:** Tests for
   `submit_to_platform` and `check_status_on_platform` that mock
   boto3's `sagemaker` client / Azure's `MLClient` using
   `unittest.mock.patch`. Proves the status-mapping translation logic
   is correct without real cloud credentials.

No live-account integration test is required — it isn't reproducible
in CI. End-to-end validation against a real cluster uses a deployable
mock service (the same pattern the Phase A sandbox-cluster verification
used: a Python `http.server` mock in a `Deployment`+`Service`).

The shim-template itself has no fill-in functions and therefore no
platform-unit tests — only the auth middleware and contract shape tests
apply.
