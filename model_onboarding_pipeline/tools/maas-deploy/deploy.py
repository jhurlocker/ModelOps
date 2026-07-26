"""
MaaS deploy tool - creates MaaS CRDs for production deployment.

Environment variables:
  MODEL_NAME         deployment name
  MODEL_ID           HuggingFace model ID
  MODELCAR_IMAGE     optional OCI modelcar image
  MAAS_SERVING_NS    namespace for LLMInferenceService
  MAAS_POLICY_NS     namespace for AuthPolicy + Subscription
  GPU_COUNT          number of GPUs
  RUNTIME_IMAGE      vLLM runtime image
  AUTHORIZED_GROUP   OpenShift group for access
"""

import json
import os
import sys
import time
import subprocess


def run_oc(args, input_data=None):
    result = subprocess.run(
        ["oc"] + args,
        capture_output=True, text=True, input=input_data,
    )
    if result.returncode != 0:
        print(f"oc {' '.join(args)} failed: {result.stderr}", file=sys.stderr)
        return None
    return result.stdout


def ensure_namespace(name):
    existing = run_oc(["get", "namespace", name, "-o", "name"])
    if not existing or name not in existing:
        print(f"Creating namespace {name}...")
        run_oc(["create", "namespace", name])
        run_oc(["label", "namespace", name,
                "modelmesh-service=enabled",
                "opendatahub.io/dashboard=true",
                "pod-security.kubernetes.io/enforce=privileged",
                "pod-security.kubernetes.io/audit=privileged",
                "pod-security.kubernetes.io/warn=privileged",
                "security.openshift.io/scc.podSecurityLabelSync=false",
                "--overwrite"])


def create_llm_inference_service():
    model_name = os.environ["MODEL_NAME"]
    serving_ns = os.environ["MAAS_SERVING_NS"]
    model_id = os.environ["MODEL_ID"]
    modelcar_image = os.environ.get("MODELCAR_IMAGE", "")
    gpu_count = os.environ.get("GPU_COUNT", "1")
    runtime_image = os.environ.get("RUNTIME_IMAGE", "registry.redhat.io/rhaiis/vllm-cuda-rhel9:3.3.0")

    ensure_namespace(serving_ns)

    cr = {
        "apiVersion": "serving.kserve.io/v1alpha1",
        "kind": "LLMInferenceService",
        "metadata": {
            "name": model_name,
            "namespace": serving_ns,
        },
        "spec": {
            "backend": "vllm",
            "runtimes": [{
                "name": "vllm",
                "image": runtime_image,
                "args": {
                    "model": model_id,
                    "gpu": int(gpu_count),
                },
            }],
        },
    }

    if modelcar_image:
        cr["spec"]["storage"] = {
            "modelSource": {
                "modelCar": {
                    "image": modelcar_image,
                }
            }
        }

    print(f"Creating LLMInferenceService {model_name} in {serving_ns}...")
    result = run_oc(["apply", "-f", "-", "-n", serving_ns],
                    input_data=json.dumps(cr))
    print(result)


def create_maas_resources():
    model_name = os.environ["MODEL_NAME"]
    serving_ns = os.environ["MAAS_SERVING_NS"]
    policy_ns = os.environ["MAAS_POLICY_NS"]
    authorized_group = os.environ.get("AUTHORIZED_GROUP", "system:authenticated")

    ensure_namespace(policy_ns)

    # MaaSModelRef
    ref = {
        "apiVersion": "maas.opendatahub.io/v1alpha1",
        "kind": "MaaSModelRef",
        "metadata": {
            "name": model_name,
            "namespace": serving_ns,
        },
        "spec": {
            "modelName": model_name,
            "inferenceServiceName": f"{model_name}-predictor",
            "namespace": serving_ns,
        },
    }
    print(f"Creating MaaSModelRef {model_name} in {serving_ns}...")
    run_oc(["apply", "-f", "-", "-n", serving_ns], input_data=json.dumps(ref))

    # MaaSAuthPolicy
    auth = {
        "apiVersion": "maas.opendatahub.io/v1alpha1",
        "kind": "MaaSAuthPolicy",
        "metadata": {
            "name": model_name,
            "namespace": policy_ns,
        },
        "spec": {
            "modelRef": {"name": model_name, "namespace": serving_ns},
            "authType": "apiKey",
            "authorizedGroups": [authorized_group],
        },
    }
    print(f"Creating MaaSAuthPolicy {model_name} in {policy_ns}...")
    run_oc(["apply", "-f", "-", "-n", policy_ns], input_data=json.dumps(auth))

    # MaaSSubscription (free tier: 100 tokens/min)
    sub_free = {
        "apiVersion": "maas.opendatahub.io/v1alpha1",
        "kind": "MaaSSubscription",
        "metadata": {
            "name": f"{model_name}-free",
            "namespace": policy_ns,
        },
        "spec": {
            "modelRef": {"name": model_name, "namespace": serving_ns},
            "rateLimit": {"tokensPerMinute": 100},
            "owners": [authorized_group],
        },
    }
    print(f"Creating MaaSSubscription {model_name}-free in {policy_ns}...")
    run_oc(["apply", "-f", "-", "-n", policy_ns], input_data=json.dumps(sub_free))

    # MaaSSubscription (premium tier: 100000 tokens/min)
    sub_premium = {
        "apiVersion": "maas.opendatahub.io/v1alpha1",
        "kind": "MaaSSubscription",
        "metadata": {
            "name": f"{model_name}-premium",
            "namespace": policy_ns,
        },
        "spec": {
            "modelRef": {"name": model_name, "namespace": serving_ns},
            "rateLimit": {"tokensPerMinute": 100000},
            "owners": [authorized_group],
        },
    }
    run_oc(["apply", "-f", "-", "-n", policy_ns], input_data=json.dumps(sub_premium))


def wait_for_readiness(max_wait=1200):
    model_name = os.environ["MODEL_NAME"]
    serving_ns = os.environ["MAAS_SERVING_NS"]

    print(f"Waiting for LLMInferenceService {model_name} to be ready (up to {max_wait}s)...")
    start = time.time()
    while time.time() - start < max_wait:
        result = run_oc([
            "get", "llminferenceservice", model_name,
            "-n", serving_ns, "-o", "json",
        ])
        if result:
            try:
                status = json.loads(result)
                conditions = status.get("status", {}).get("conditions", [])
                ready = any(
                    c.get("type") == "Ready" and c.get("status") == "True"
                    for c in conditions
                )
                if ready:
                    print(f"LLMInferenceService {model_name} is ready!")
                    return
            except json.JSONDecodeError:
                pass
        time.sleep(30)

    print(f"WARNING: LLMInferenceService {model_name} did not become ready within {max_wait}s")


def main():
    create_llm_inference_service()
    create_maas_resources()
    wait_for_readiness()

    deploy_info = {
        "model_name": os.environ["MODEL_NAME"],
        "serving_namespace": os.environ["MAAS_SERVING_NS"],
        "policy_namespace": os.environ["MAAS_POLICY_NS"],
        "inference_service": os.environ["MODEL_NAME"],
        "subscriptions": [
            f"{os.environ['MODEL_NAME']}-free (100 tok/min)",
            f"{os.environ['MODEL_NAME']}-premium (100000 tok/min)",
        ],
    }
    with open("maas-deployment-info.json", "w") as f:
        json.dump(deploy_info, f, indent=2)
    print("MaaS deployment complete.")
    print(json.dumps(deploy_info, indent=2))


if __name__ == "__main__":
    main()
