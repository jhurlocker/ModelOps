# GPU Advisor Troubleshooting

## Discover GPU Inventory

The `gpu-advisor` task uses `image-registry.openshift-image-registry.svc:5000/openshift/cli:latest`. Verify it exists:

```bash
oc get imagestreamtag cli:latest -n openshift
```

The `pipeline` SA must have cluster-scoped `nodes` read (the `cluster-reader` ClusterRole). A namespaced `view` RoleBinding is NOT sufficient.

## Advisor Reports BLOCKED

Expected when the cluster lacks enough free GPU memory. Check `gpu-advisor-summary.txt` in the shared workspace or TaskRun logs for the specific shortfall.

Options:
- Add GPUs
- Lower `context-length` / `concurrency`
- Enable `allow-time-slicing` to pack models on shared GPU
- Point `advisor-endpoint` at a remote agent that can recommend quantization

## Remote Endpoint (advisor-endpoint)

The task calls `POST {advisor-endpoint}` with JSON containing `gpu_inventory` + model/sizing params. Expects JSON with `options` (or `deployment_options`).

On any error (timeout, non-2xx, bad JSON, empty options), the task **falls back to the local heuristic** — check logs for `ERROR calling advisor endpoint:`.

If the remote agent should always be used, a fallback is an alert. Common causes:
- Network policy blocking egress
- DNS resolution failure
- Missing/incorrect `api-key` in the `gpu-advisor-credentials` Secret
- `advisor-timeout-seconds` too low for agents doing live web research

## Logs

```bash
TASKRUN=$(oc get taskrun -n vllm -o name --sort-by=.metadata.creationTimestamp | grep gpu-advisor | tail -1)
oc logs $TASKRUN -c step-run-advisor -n vllm
```
