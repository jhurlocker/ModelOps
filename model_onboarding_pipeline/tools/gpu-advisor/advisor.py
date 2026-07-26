"""
GPU Advisor - capacity planning tool extracted from the gpu-advisor Tekton task.
Discovers GPU inventory, analyzes model requirements, and generates deployment plans.

Mode: LOCAL (built-in heuristic) or REMOTE (external LLM endpoint).

Environment variables (see gpu-advisor task params for full list).
"""

import json
import math
import os
import sys
import time


def product_family(product):
    p = product.replace("NVIDIA-", "").replace("NVIDIA ", "")
    p = p.replace("-SHARED", "").replace("_SHARED", "")
    return p.split("-")[0].strip()


def gpu_util_from_args(args):
    if not args:
        return None
    for i, a in enumerate(args):
        if a.startswith("--gpu-memory-utilization"):
            if "=" in a:
                try:
                    return float(a.split("=", 1)[1])
                except ValueError:
                    return None
            if i + 1 < len(args):
                try:
                    return float(args[i + 1])
                except ValueError:
                    return None
    return None


def discover_gpu_inventory(nodes_json_file, pods_json_file):
    nodes_json = json.load(open(nodes_json_file))
    pods_json = json.load(open(pods_json_file))

    gpu_mem = {
        "H100": 80, "A100": 80, "A10": 24, "A10G": 24, "L4": 23, "L40": 48,
        "L40S": 48, "RTX6000": 48, "T4": 16, "V100": 32
    }

    node_gpus = {}
    occupants = []
    total_alloc = total_used = total_free = 0

    for node in nodes_json.get("items", []):
        name = node["metadata"]["name"]
        labels = node["metadata"].get("labels", {})
        allocs = node["status"].get("allocatable", {})
        gpu_count = int(allocs.get("nvidia.com/gpu", 0))
        if gpu_count == 0:
            continue
        product = labels.get("nvidia.com/gpu.product", "unknown")
        family = product_family(product)
        mem_mib = labels.get("nvidia.com/gpu.memory")
        if mem_mib:
            try:
                phys_mem = int(mem_mib) / 1024.0
            except ValueError:
                phys_mem = gpu_mem.get(family, 40)
        else:
            phys_mem = gpu_mem.get(family, 40)
        usable_mem = phys_mem * 0.9
        try:
            replicas = int(labels.get("nvidia.com/gpu.replicas", "1"))
        except ValueError:
            replicas = 1
        time_sliced = replicas > 1 or product.endswith("SHARED")
        physical_gpus = max(1, gpu_count // replicas) if replicas else gpu_count
        schedulable = not any(t.get("effect") == "NoSchedule" for t in node["spec"].get("taints", []))
        alloc = 0
        node_occupants = []
        for pod in pods_json.get("items", []):
            if pod["spec"].get("nodeName") != name:
                continue
            pod_gpu = 0
            pod_util = None
            for c in pod["spec"].get("containers", []):
                pod_gpu += int(c["resources"].get("requests", {}).get("nvidia.com/gpu", 0))
                u = gpu_util_from_args(c.get("args"))
                if u is not None:
                    pod_util = u
            alloc += pod_gpu
            if pod_gpu > 0:
                pmeta = pod["metadata"]
                plabels = pmeta.get("labels", {})
                isvc = plabels.get("serving.kserve.io/inferenceService") or plabels.get("serving.kserve.io/inferenceservice")
                occ = {
                    "namespace": pmeta.get("namespace"),
                    "pod": pmeta.get("name"),
                    "inferenceservice": isvc,
                    "gpu_requested": pod_gpu,
                    "gpu_memory_utilization": pod_util,
                    "node": name,
                }
                node_occupants.append(occ)
                occupants.append(occ)
        free = gpu_count - alloc
        node_gpus[name] = {
            "product": product, "family": family,
            "allocatable": gpu_count, "allocated": alloc, "free": free,
            "physical_gpus": physical_gpus, "replicas_per_gpu": replicas,
            "time_sliced": time_sliced,
            "physical_memory_gb": round(phys_mem, 1),
            "memory_gb": round(usable_mem, 1),
            "schedulable": schedulable,
            "occupants": node_occupants,
        }
        total_alloc += gpu_count
        total_used += alloc
        total_free += free

    largest_free = max((n["free"] for n in node_gpus.values()), default=0)
    min_mem = min((n["memory_gb"] for n in node_gpus.values()), default=0)

    return {
        "total": total_alloc, "allocated": total_used, "free": total_free,
        "largest_single_node": largest_free, "nodes": node_gpus,
        "occupants": occupants,
    }, node_gpus, largest_free, min_mem


def local_recommendation(model_id, ctx_len, concurrency, node_gpus, min_mem, total_free):
    param_map = {
        "70b": 70e9, "33b": 33e9, "32b": 32e9, "13b": 13e9,
        "8b": 8e9, "7b": 7e9, "6.9b": 6.9e9, "3b": 3e9, "2b": 2e9, "1b": 1e9
    }
    model_lower = model_id.lower()
    param_count = 7e9
    for k, v in param_map.items():
        if k in model_lower:
            param_count = v
            break

    try:
        from transformers import AutoConfig
        cfg = AutoConfig.from_pretrained(model_id, use_fast=True, trust_remote_code=False)
        num_layers = getattr(cfg, "num_hidden_layers", 32)
        hidden_size = getattr(cfg, "hidden_size", 4096)
        num_heads = getattr(cfg, "num_attention_heads", 32)
        num_kv_heads = getattr(cfg, "num_key_value_heads", num_heads)
        head_dim = hidden_size // num_heads
        print(f"  Loaded config: {hidden_size} hidden, {num_layers} layers, {num_heads} heads, {num_kv_heads} KV heads")
    except Exception as e:
        print(f"  Could not load config ({e}). Using defaults.")
        num_layers, hidden_size, num_heads, num_kv_heads = 32, 4096, 32, 32
        head_dim = 128

    weight_gb = (param_count * 2) / 1e9
    kv_bytes_per_tok = 2 * num_layers * num_kv_heads * head_dim * 2
    kv_gb = (kv_bytes_per_tok * ctx_len * concurrency) / 1e9
    overhead_gb = weight_gb * 0.15
    total_gb = weight_gb + kv_gb + overhead_gb

    print(f"  Param estimate: {param_count / 1e9:.1f}B, Weight (BF16): {weight_gb:.2f} GB, KV cache: {kv_gb:.2f} GB")
    print(f"  Overhead: {overhead_gb:.2f} GB, Total per replica: {total_gb:.2f} GB")

    options = []
    if total_gb <= min_mem and node_gpus:
        options.append({
            "name": "Single GPU", "status": "recommended",
            "gpu_per_replica": 1, "replicas": 1, "total_gpus": 1,
            "topology": {"tp": 1, "pp": 1, "dp": 1},
            "pros": ["Simplest, lowest latency, easy ops"],
            "cons": ["Limited throughput, single point of failure"],
            "vllm_flags": [f"--max-model-len={ctx_len}",
                           f"--max-num-seqs={concurrency * 2}",
                           "--enable-prefix-caching"]
        })

    if not options:
        tp = math.ceil(total_gb / min_mem) if min_mem > 0 else 0
        if tp <= largest_free and tp >= 2:
            options.append({
                "name": f"Tensor Parallel (TP={tp})", "status": "recommended",
                "gpu_per_replica": tp, "replicas": 1, "total_gpus": tp,
                "topology": {"tp": tp, "pp": 1, "dp": 1},
                "pros": [f"Distributes across {tp} GPUs"],
                "cons": ["Requires GPUs on same node"],
                "vllm_flags": [f"--tensor-parallel-size={tp}",
                               f"--max-model-len={ctx_len}",
                               f"--max-num-seqs={concurrency * 2}",
                               "--enable-prefix-caching"]
            })

    return {
        "model_id": model_id,
        "param_count": param_count,
        "estimated_total_memory_gb": total_gb,
        "options": options,
        "sources": ["local heuristic sizing (no web research performed)"]
    }


ADVISOR_SYSTEM_PROMPT = """\
You are an expert OpenShift + vLLM GPU deployment advisor performing \
capacity planning for a model-onboarding pipeline. ...

Respond with STRICT JSON ONLY. No prose, no markdown, no code fences. \
Match this exact schema:
{
  "options": [
    {
      "name": "string",
      "status": "recommended | feasible | not_recommended | blocked",
      "gpu_per_replica": 0,
      "replicas": 1,
      "total_gpus": 0,
      "topology": {"tp": 1, "pp": 1, "dp": 1},
      "pros": ["string"],
      "cons": ["string"],
      "vllm_flags": ["--max-model-len=32768", "--max-num-seqs=8", "--enable-prefix-caching"]
    }
  ],
  "param_count": 0,
  "estimated_total_memory_gb": 0.0
}"""


class BudgetExhaustedError(ValueError):
    pass


def remote_recommendation(gpu_inventory, model_id, ctx_len, concurrency,
                          isolation, allow_ts, allow_mig, plan_id, target_ns,
                          advisor_endpoint, advisor_api_key, advisor_timeout):
    import requests

    headers = {"Content-Type": "application/json"}
    if advisor_api_key:
        headers["Authorization"] = f"Bearer {advisor_api_key}"

    # Discover model served by the endpoint
    print(f"  GET {advisor_endpoint.rstrip('/')}/models")
    models_resp = requests.get(
        f"{advisor_endpoint.rstrip('/')}/models",
        headers=headers, timeout=advisor_timeout,
    )
    models_resp.raise_for_status()
    served = models_resp.json().get("data", [])
    if not served:
        raise ValueError("Advisor endpoint /models returned no served models")
    advisor_model_id = served[0]["id"]
    print(f"  Advisor endpoint is serving: {advisor_model_id}")

    user_payload = {
        "plan_id": plan_id,
        "model_id": model_id,
        "target_namespace": target_ns,
        "expected_context_length": ctx_len,
        "expected_concurrency": concurrency,
        "gpu_isolation_policy": isolation,
        "allow_time_slicing": allow_ts,
        "allow_mig": allow_mig,
        "gpu_inventory": gpu_inventory,
    }
    chat_payload = {
        "model": advisor_model_id,
        "temperature": 0,
        "messages": [
            {"role": "system", "content": ADVISOR_SYSTEM_PROMPT},
            {"role": "user", "content": json.dumps(user_payload, indent=2)},
        ],
    }

    def _call(max_tokens):
        nonlocal chat_payload
        chat_payload["max_tokens"] = max_tokens
        print(f"  POST {advisor_endpoint.rstrip('/')}/chat/completions (model={advisor_model_id}, timeout={advisor_timeout}s, max_tokens={max_tokens})")
        resp = requests.post(
            f"{advisor_endpoint.rstrip('/')}/chat/completions",
            json=chat_payload, headers=headers, timeout=advisor_timeout,
        )
        resp.raise_for_status()
        choice = resp.json()["choices"][0]
        content = (choice["message"].get("content") or "").strip()
        if not content:
            if choice.get("finish_reason") == "length":
                raise BudgetExhaustedError(f"LLM exhausted {max_tokens}-token budget")
            raise ValueError(f"Empty content (finish_reason={choice.get('finish_reason')})")
        if content.startswith("```"):
            content = content.split("```", 2)[1]
            if content.startswith("json"):
                content = content[4:]
            content = content.strip()
        data = json.loads(content)
        options = data.get("options") or []
        if not options:
            raise ValueError("No 'options' in response")
        return {"model_id": model_id,
                "param_count": data.get("param_count", 0) or 0,
                "estimated_total_memory_gb": data.get("estimated_total_memory_gb", 0) or 0,
                "options": options,
                "sources": [f"remote agentic LLM ({advisor_model_id})"],
                "raw_response": data}

    max_tokens = 6144
    for attempt in range(1, 4):
        try:
            return _call(max_tokens)
        except BudgetExhaustedError as e:
            print(f"  Attempt {attempt}/3 failed: {e}", file=sys.stderr)
            max_tokens = min(max_tokens * 2, 24576)
            if attempt < 3:
                print(f"  Retrying with max_tokens={max_tokens}...", file=sys.stderr)
        except (ValueError, requests.RequestException) as e:
            print(f"  Attempt {attempt}/3 failed: {e}", file=sys.stderr)
            if attempt < 3:
                print("  Retrying in 5s...", file=sys.stderr)
                time.sleep(5)

    raise RuntimeError("All remote advisor attempts failed")


def plan_gpu_sharing(node_gpus, total_free, total_gb, param_count, allow_ts,
                     isolation, max_time_slices, gpu_operator_ns, clusterpolicy_name,
                     time_slicing_cm):
    plan = {
        "enabled": False,
        "reconfigure_time_slicing": False,
        "reason": "",
        "node": None,
        "gpu_product": None,
        "physical_memory_gb": None,
        "replicas": 1,
        "target_gpu_memory_utilization": None,
        "coresident_models": [],
        "configmap_name": time_slicing_cm,
        "configmap_namespace": gpu_operator_ns,
        "clusterpolicy_name": clusterpolicy_name,
    }
    if not node_gpus:
        plan["reason"] = "no GPU nodes discovered"
        return plan

    occ_nodes = [(n, d) for n, d in node_gpus.items() if d["occupants"]]
    if occ_nodes:
        node_name, node = occ_nodes[0]
    else:
        node_name, node = min(node_gpus.items(), key=lambda kv: kv[1]["free"])

    phys_mem = node["physical_memory_gb"]
    existing = node["occupants"]
    n_existing = len(existing)

    has_free_dedicated = (total_free > 0) and (not node["time_sliced"])
    if has_free_dedicated:
        plan["reason"] = f"a free dedicated GPU is available ({total_free} free); no sharing required"
        return plan
    if not allow_ts:
        plan["reason"] = "GPU is fully allocated and allow-time-slicing is false; cannot fit"
        return plan

    models_to_coreside = n_existing + 1
    replicas = max(models_to_coreside, node["replicas_per_gpu"])
    replicas = min(replicas, max_time_slices)
    target_util = round(0.9 / models_to_coreside, 2)
    share_gb = target_util * phys_mem
    weight_gb = (param_count * 2) / 1e9 if param_count else (total_gb or 0.0)
    min_footprint = weight_gb * 1.2

    if min_footprint > share_gb + 0.01:
        plan["reason"] = (f"even time-slicing {models_to_coreside}-way gives only {share_gb:.1f} GB/model, "
                          f"but the new model's weights need ~{min_footprint:.1f} GB - does not fit")
        plan["does_not_fit"] = True
        return plan

    plan["enabled"] = True
    plan["node"] = node_name
    plan["gpu_product"] = node["product"]
    plan["physical_memory_gb"] = phys_mem
    plan["replicas"] = replicas
    plan["target_gpu_memory_utilization"] = target_util
    plan["reconfigure_time_slicing"] = node["replicas_per_gpu"] < replicas
    plan["reason"] = (f"GPU {node['product']} on {node_name} is fully allocated; "
                      f"time-slice {replicas}-way at --gpu-memory-utilization={target_util}")

    for occ in existing:
        cur = occ.get("gpu_memory_utilization")
        needs = (cur is None) or (cur > target_util + 0.01)
        plan["coresident_models"].append({
            "namespace": occ["namespace"],
            "inferenceservice": occ["inferenceservice"],
            "pod": occ["pod"],
            "current_gpu_memory_utilization": cur,
            "target_gpu_memory_utilization": target_util,
            "needs_redeploy": bool(needs),
        })
    return plan


def main():
    model_id = os.environ["MODEL_ID"]
    target_ns = os.environ["TARGET_NS"]
    ctx_len = int(os.environ["CTX_LEN"])
    concurrency = int(os.environ["CONCURRENCY"])
    allow_ts = os.environ["ALLOW_TS"].lower() == "true"
    allow_mig = os.environ["ALLOW_MIG"].lower() == "true"
    isolation = os.environ["ISOLATION"]
    plan_id = os.environ["PLAN_ID"]
    gpu_operator_ns = os.environ.get("GPU_OPERATOR_NS", "nvidia-gpu-operator")
    clusterpolicy_name = os.environ.get("CLUSTERPOLICY_NAME", "gpu-cluster-policy")
    time_slicing_cm = os.environ.get("TIME_SLICING_CM", "modelops-time-slicing")
    max_time_slices = int(os.environ.get("MAX_TIME_SLICES", "8"))
    advisor_endpoint = os.environ.get("ADVISOR_ENDPOINT", "").strip()
    advisor_api_key = os.environ.get("ADVISOR_API_KEY", "")
    advisor_timeout = int(os.environ.get("ADVISOR_TIMEOUT", "300"))

    # Discover GPU inventory
    gpu_inventory, node_gpus, largest_free, min_mem = discover_gpu_inventory(
        "gpu_nodes.json", "all_pods.json"
    )

    # Local or remote recommendation
    if advisor_endpoint:
        try:
            result = remote_recommendation(
                gpu_inventory, model_id, ctx_len, concurrency,
                isolation, allow_ts, allow_mig, plan_id, target_ns,
                advisor_endpoint, advisor_api_key, advisor_timeout,
            )
        except Exception as e:
            print(f"ERROR calling advisor endpoint: {e}", file=sys.stderr)
            print("Falling back to local heuristic sizing.", file=sys.stderr)
            result = local_recommendation(model_id, ctx_len, concurrency, node_gpus, min_mem, largest_free)
    else:
        result = local_recommendation(model_id, ctx_len, concurrency, node_gpus, min_mem, largest_free)

    options = result["options"]
    param_count = result.get("param_count", 0) or 0
    total_gb = result.get("estimated_total_memory_gb", 0) or 0

    sharing_plan = plan_gpu_sharing(
        node_gpus, total_free, total_gb, param_count, allow_ts,
        isolation, max_time_slices, gpu_operator_ns, clusterpolicy_name, time_slicing_cm
    )

    # Add time-slicing option if sharing is needed
    if sharing_plan.get("enabled"):
        target_util = sharing_plan["target_gpu_memory_utilization"]
        replicas = sharing_plan["replicas"]
        ts_option = {
            "name": f"Time Slicing (Shared GPU, {replicas}-way)",
            "status": "recommended",
            "gpu_per_replica": 1, "replicas": 1, "total_gpus": 1,
            "time_slices": replicas,
            "topology": {"tp": 1, "pp": 1, "dp": 1, "sharing": "time_slicing"},
            "pros": [f"Fits new model onto occupied {sharing_plan['gpu_product']} alongside existing models"],
            "cons": ["Shared compute/memory; not for strict latency SLOs"],
            "vllm_flags": [
                f"--gpu-memory-utilization={target_util}",
                f"--max-model-len={ctx_len}",
                f"--max-num-seqs={concurrency}",
                "--enable-prefix-caching",
            ],
            "gpu_sharing": sharing_plan,
        }
        options.insert(0, ts_option)

    # If sharing doesn't fit, force BLOCKED
    if sharing_plan.get("does_not_fit"):
        options = [{
            "name": "BLOCKED - Does Not Fit Even Time-Sliced", "status": "blocked",
            "gpu_per_replica": 0, "replicas": 0, "total_gpus": 0, "topology": {},
            "pros": [], "cons": [sharing_plan.get("reason", "insufficient capacity")],
            "vllm_flags": [],
        }]

    blocked = (not options) or (options[0].get("status") == "blocked")

    # Write outputs
    with open("gpu-inventory.json", "w") as f:
        json.dump(gpu_inventory, f, indent=2)
    with open("deployment-options.json", "w") as f:
        json.dump({"plan_id": plan_id, "recommended": options[0]["name"] if options else "none", "options": options}, f, indent=2)
    with open("gpu-sharing-plan.json", "w") as f:
        json.dump(sharing_plan, f, indent=2)

    if sharing_plan.get("enabled") and sharing_plan.get("reconfigure_time_slicing"):
        replicas = sharing_plan["replicas"]
        cm_yaml = (f"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {sharing_plan['configmap_name']}\n"
                   f"  namespace: {sharing_plan['configmap_namespace']}\ndata:\n"
                   f"  any: |-\n    version: v1\n    flags:\n      migStrategy: none\n"
                   f"    sharing:\n      timeSlicing:\n        resources:\n"
                   f"          - name: nvidia.com/gpu\n            replicas: {replicas}\n")
        with open("time-slicing-config.yaml", "w") as f:
            f.write(cm_yaml)
        patch = {"spec": {"devicePlugin": {"config": {"name": sharing_plan["configmap_name"], "default": "any"}}}}
        with open("clusterpolicy-patch.json", "w") as f:
            json.dump(patch, f, indent=2)

    with open("plan-id.txt", "w") as f:
        f.write(plan_id)

    if blocked:
        with open("plan-status.txt", "w") as f:
            f.write("BLOCKED")
        with open("recommended-vllm-command.sh", "w") as f:
            f.write("# No feasible options\n")
        print("\n# BLOCKED - no feasible deployment option. Halting pipeline.")
        sys.exit(1)
    else:
        with open("plan-status.txt", "w") as f:
            f.write("PLAN_READY")
        with open("recommended-vllm-command.sh", "w") as f:
            f.write(f"#!/usr/bin/env bash\nvllm serve {model_id} {' '.join(options[0]['vllm_flags'])}\n")

    # Summary
    summary = [
        "# vLLM Deployment Recommendation", "",
        "## Summary",
        f"**Plan id**: {plan_id}",
        f"**Model**: {model_id}",
        f"**Source**: {', '.join(result.get('sources', []))}",
    ]
    if param_count:
        summary.append(f"**Estimated parameters**: {param_count / 1e9:.1f}B")
    if total_gb:
        summary.append(f"**Estimated memory**: {total_gb:.2f} GB")
    summary.append(f"**Cluster**: {gpu_inventory['total']} total GPUs, {gpu_inventory['free']} free")
    summary.extend(["", "## Recommendation"])
    rec = options[0]
    summary.extend([
        f"**Recommended topology**: {rec['name']}",
        f"**Status**: {rec['status'].upper()}",
    ])
    with open("gpu-advisor-summary.txt", "w") as f:
        f.write("\n".join(summary))

    print("\n".join(summary))
    print("\n# PLAN_READY")


if __name__ == "__main__":
    main()
