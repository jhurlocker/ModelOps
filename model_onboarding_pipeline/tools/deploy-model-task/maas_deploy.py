"""
MaaS deployment module - creates LLMInferenceService + MaaS CRDs.

Called from deploy_model.py when DEPLOY_MAAS=true.
"""

import json
import os
import subprocess
import sys
import time
import yaml


def _oc(args, input_data=None, check=True):
    result = subprocess.run(
        ["oc"] + list(args),
        capture_output=True, text=True, input=input_data,
    )
    if result.stdout:
        for line in result.stdout.strip().split("\n"):
            if line.strip():
                print("  " + line)
    if result.stderr:
        print(result.stderr, file=sys.stderr)
    if check and result.returncode != 0:
        sys.exit(result.returncode)
    return result


def _apply_yaml(obj, namespace):
    yaml_str = yaml.dump(obj, default_flow_style=False, sort_keys=False)
    name = obj["metadata"]["name"]
    kind = obj["kind"]
    print(f"Applying {kind} [{name}] in namespace [{namespace}]")
    _oc(["apply", "-n", namespace, "-f", "-"], input_data=yaml_str)


def _wait_for_llm_ready(name, namespace, max_wait=1200):
    print(f"Waiting for LLMInferenceService [{name}] to be Ready (up to {max_wait}s)...")
    start = time.time()
    while time.time() - start < max_wait:
        result = subprocess.run(
            ["oc", "get", "llminferenceservice", name,
             "-n", namespace,
             "-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}"],
            capture_output=True, text=True,
        )
        if result.returncode == 0 and result.stdout.strip() == "True":
            print(f"LLMInferenceService [{name}] is Ready.")
            return True
        print("  Not Ready yet; waiting 15s...")
        time.sleep(15)

    print(f"WARNING: LLMInferenceService [{name}] did not become Ready within {max_wait}s.", file=sys.stderr)
    _oc(["get", "pods", "-n", namespace, "-l", f"serving.kserve.io/llminferenceservice={name}"], check=False)
    _oc(["describe", "llminferenceservice", name, "-n", namespace], check=False)
    return False


def deploy(model_name, model_id, modelcar_image, serving_ns, policy_ns,
           gpu_count, runtime_image, gpu_mem, authorized_group):
    safe_name = model_name.lower().replace("_", "-").replace(".", "-").replace(" ", "-")

    # Normalise modelcar URI
    modelcar_uri = modelcar_image
    for scheme in ("oci://", "docker://"):
        if modelcar_uri.startswith(scheme):
            modelcar_uri = modelcar_uri[len(scheme):]
            break
    if not modelcar_uri.startswith("oci://"):
        modelcar_uri = "oci://" + modelcar_uri

    display_name = model_id

    # 1. LLMInferenceService (Distributed inference)
    print("\n=== Step 1: LLMInferenceService ===")
    llm_service = {
        "apiVersion": "serving.kserve.io/v1alpha1",
        "kind": "LLMInferenceService",
        "metadata": {
            "name": safe_name,
            "namespace": serving_ns,
            "annotations": {
                "openshift.io/display-name": display_name,
                "opendatahub.io/model-type": "generative",
                "security.opendatahub.io/enable-auth": "true",
            },
            "labels": {
                "opendatahub.io/dashboard": "true",
                "opendatahub.io/genai-asset": "true",
            },
        },
        "spec": {
            "model": {
                "name": model_id.split("/")[-1],
                "uri": modelcar_uri,
            },
            "replicas": 1,
            "router": {
                "route": {},
                "gateway": {
                    "refs": [{
                        "name": "maas-default-gateway",
                        "namespace": "openshift-ingress",
                    }],
                },
            },
            "template": {
                "tolerations": [{
                    "effect": "NoSchedule",
                    "key": "nvidia.com/gpu",
                    "operator": "Exists",
                }],
                "containers": [{
                    "name": "main",
                    "image": runtime_image,
                    "command": ["python", "-m", "vllm.entrypoints.openai.api_server"],
                    "args": [
                        "--served-model-name={{.Name}}",
                        "--model=/mnt/models",
                        "--ssl-certfile=/var/run/kserve/tls/tls.crt",
                        "--ssl-keyfile=/var/run/kserve/tls/tls.key",
                        "--enable-force-include-usage",
                        f"--gpu-memory-utilization={gpu_mem}",
                    ],
                    "ports": [{
                        "containerPort": 8000,
                        "name": "http",
                        "protocol": "TCP",
                    }],
                    "livenessProbe": {
                        "httpGet": {"path": "/health", "port": 8000, "scheme": "HTTPS"},
                        "initialDelaySeconds": 300,
                        "periodSeconds": 30,
                        "timeoutSeconds": 30,
                        "failureThreshold": 5,
                    },
                    "readinessProbe": {
                        "httpGet": {"path": "/health", "port": 8000, "scheme": "HTTPS"},
                        "initialDelaySeconds": 300,
                        "periodSeconds": 15,
                        "timeoutSeconds": 15,
                        "failureThreshold": 30,
                    },
                    "resources": {
                        "requests": {
                            "cpu": "2",
                            "memory": "8Gi",
                            "nvidia.com/gpu": gpu_count,
                        },
                        "limits": {
                            "cpu": "4",
                            "memory": "24Gi",
                            "nvidia.com/gpu": gpu_count,
                        },
                    },
                    "volumeMounts": [{
                        "mountPath": "/dev/shm",
                        "name": "shm",
                    }],
                }],
                "volumes": [{
                    "name": "shm",
                    "emptyDir": {
                        "medium": "Memory",
                        "sizeLimit": "2Gi",
                    },
                }],
            },
        },
    }
    _apply_yaml(llm_service, serving_ns)

    # 2. MaaSModelRef
    print("\n=== Step 2: MaaSModelRef ===")
    maas_ref = {
        "apiVersion": "maas.opendatahub.io/v1alpha1",
        "kind": "MaaSModelRef",
        "metadata": {
            "name": safe_name,
            "namespace": serving_ns,
            "annotations": {
                "openshift.io/display-name": display_name,
                "openshift.io/description": f"{model_id} deployed via MaaS",
            },
        },
        "spec": {
            "modelRef": {
                "kind": "LLMInferenceService",
                "name": safe_name,
            },
        },
    }
    _apply_yaml(maas_ref, serving_ns)

    # 3. MaaSAuthPolicy
    print("\n=== Step 3: MaaSAuthPolicy ===")
    auth_policy_name = f"{safe_name}-access"
    auth_policy = {
        "apiVersion": "maas.opendatahub.io/v1alpha1",
        "kind": "MaaSAuthPolicy",
        "metadata": {
            "name": auth_policy_name,
            "namespace": policy_ns,
            "annotations": {
                "openshift.io/display-name": f"{display_name} Access",
                "openshift.io/description": f"Grants group {authorized_group} access to {display_name}",
            },
        },
        "spec": {
            "modelRefs": [{
                "name": safe_name,
                "namespace": serving_ns,
            }],
            "subjects": {
                "groups": [{"name": authorized_group}],
                "users": [],
            },
        },
    }
    _apply_yaml(auth_policy, policy_ns)

    # 5. MaaSSubscriptions
    print("\n=== Step 4: MaaSSubscriptions ===")
    sub_free_name = f"{safe_name}-free"
    sub_free = {
        "apiVersion": "maas.opendatahub.io/v1alpha1",
        "kind": "MaaSSubscription",
        "metadata": {
            "name": sub_free_name,
            "namespace": policy_ns,
            "annotations": {
                "openshift.io/display-name": f"{display_name} Free Tier",
                "openshift.io/description": f"Free tier: 100 tokens/min for group {authorized_group}",
            },
        },
        "spec": {
            "owner": {
                "groups": [{"name": authorized_group}],
                "users": [],
            },
            "modelRefs": [{
                "name": safe_name,
                "namespace": serving_ns,
                "tokenRateLimits": [{"limit": 100, "window": "1m"}],
            }],
            "priority": 10,
        },
    }
    _apply_yaml(sub_free, policy_ns)

    sub_premium_name = f"{safe_name}-premium"
    sub_premium = {
        "apiVersion": "maas.opendatahub.io/v1alpha1",
        "kind": "MaaSSubscription",
        "metadata": {
            "name": sub_premium_name,
            "namespace": policy_ns,
            "annotations": {
                "openshift.io/display-name": f"{display_name} Premium Tier",
                "openshift.io/description": f"Premium tier: 100000 tokens/min for group {authorized_group}",
            },
        },
        "spec": {
            "owner": {
                "groups": [{"name": authorized_group}],
                "users": [],
            },
            "modelRefs": [{
                "name": safe_name,
                "namespace": serving_ns,
                "tokenRateLimits": [{"limit": 100000, "window": "1m"}],
            }],
            "priority": 20,
        },
    }
    _apply_yaml(sub_premium, policy_ns)

    # 6. Wait for readiness
    print("\n=== Step 5: Waiting for LLMInferenceService readiness ===")
    ready = _wait_for_llm_ready(safe_name, serving_ns)

    # 7. Write deployment info
    print("\n=== Step 6: Writing MaaS deployment info ===")
    cluster_domain = subprocess.run(
        ["oc", "get", "ingresses.config/cluster", "-o", "jsonpath={.spec.domain}"],
        capture_output=True, text=True,
    ).stdout.strip() or "apps.unknown"

    maas_info = {
        "model_name": model_name,
        "model_id": model_id,
        "maas_api_url": f"https://maas.{cluster_domain}",
        "llm_inference_service": safe_name,
        "serving_namespace": serving_ns,
        "policy_namespace": policy_ns,
        "subscriptions": {
            "free": sub_free_name,
            "premium": sub_premium_name,
        },
    }
    with open("maas-deployment-info.json", "w") as f:
        json.dump(maas_info, f, indent=2)

    print("\n--- MaaS Deployment Complete ---")
    print(f"Model registered as: {safe_name}")
    print(f"MaaS API URL:       https://maas.{cluster_domain}")
    print(f"Free tier sub:      {sub_free_name}")
    print(f"Premium tier sub:   {sub_premium_name}")

    return True
