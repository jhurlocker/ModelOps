# GPU Time-Slicing Details

Time-slicing shares GPU memory and compute (no hardware isolation). Good for packing small models/demos, not for strict latency SLOs. Use MIG or dedicated GPUs for production workloads.

## How It Works

1. `gpu-advisor` detects models already on the GPU. When fully allocated but the incoming model fits alongside existing ones, it recommends time-slicing.
2. It generates `time-slicing-config.yaml` (a device-plugin ConfigMap with `replicas: N`) and `clusterpolicy-patch.json`.
3. It computes a fair per-model `--gpu-memory-utilization` split (e.g., two 2B models on one L4 → `0.45` each).
4. `apply-gpu-sharing` applies the ConfigMap, patches the ClusterPolicy, waits for the node to advertise `N` `nvidia.com/gpu` replicas, and redeploys co-resident models at the lower split.

## Demo Workflow

Run the pipeline twice to see it:
- **Run 1**: Model A onboards to an empty GPU → dedicated deployment.
- **Run 2**: Model B onboards. A occupies the GPU → 2-way time-slicing applied, A redeployed at lower `--gpu-memory-utilization`.

## Revert to Dedicated GPUs

```bash
oc patch clusterpolicy gpu-cluster-policy --type merge \
  -p '{"spec":{"devicePlugin":{"config":{"name":"","default":""}}}}'
oc delete configmap modelops-time-slicing -n nvidia-gpu-operator --ignore-not-found
# Redeploy models at higher --gpu-memory-utilization as needed.
```

## Notes

- Time-slicing is **cluster-wide and persists** after the pipeline completes.
- `gpu-isolation-policy: dedicated` avoids sharing unless `allow-time-slicing`/`allow-mig` is set and nothing else fits.
- `max-time-slices` (default: 8) is the upper bound on replicas per GPU.
