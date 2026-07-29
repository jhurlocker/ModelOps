"""
Deploy-model entry point. Handles two deployment paths:

  DEPLOY_MAAS=false (default)  -> Helm-based vLLM KServe deployment
  DEPLOY_MAAS=true             -> LLMInferenceService + MaaS CRDs
"""

import json
import os
import subprocess
import sys
import time

import requests
import yaml

# ---------------------------------------------------------------------------
# Environment variables (set by Tekton Task params)
# ---------------------------------------------------------------------------
DEPLOY_MAAS = os.environ.get("DEPLOY_MAAS", "false").lower() == "true"

RELEASE_NAME      = os.environ["RELEASE_NAME"]
NAMESPACE         = os.environ["NAMESPACE"]
MODEL_ID          = os.environ["MODEL_ID"]
MODELCAR_IMAGE    = os.environ.get("MODELCAR_IMAGE", "").strip()
HF_TOKEN          = os.environ.get("HF_TOKEN", "")
VALUES_CONTENT    = os.environ.get("VALUES_CONTENT", "")
GPU_COUNT_OVERRIDE = os.environ.get("GPU_COUNT_OVERRIDE", "").strip()
HW_PROFILE_NAME   = os.environ.get("HW_PROFILE_NAME", "")
HW_PROFILE_NS     = os.environ.get("HW_PROFILE_NAMESPACE", "")
CHART_URL         = os.environ.get("CHART_URL", "https://redhat-ai-services.github.io/helm-charts/")
CHART_VERSION     = os.environ.get("CHART_VERSION", "0.7.1")
SR_VLLM_NAME       = os.environ.get("SERVING_RUNTIME_NAME", "").strip() or RELEASE_NAME
SR_VLLM_IMAGE      = os.environ.get("SERVING_RUNTIME_IMAGE", "registry.redhat.io/rhaiis/vllm-cuda-rhel9:3.2.4")

# MaaS params (used when DEPLOY_MAAS=true)
MAAS_SERVING_NS    = os.environ.get("MAAS_SERVING_NS", "llm")
MAAS_POLICY_NS     = os.environ.get("MAAS_POLICY_NS", "models-as-a-service")
MAAS_GPU_COUNT     = os.environ.get("MAAS_GPU_COUNT", "1")
MAAS_RUNTIME_IMAGE = os.environ.get("MAAS_RUNTIME_IMAGE", "registry.redhat.io/rhaiis/vllm-cuda-rhel9:3.3.0")
MAAS_GPU_MEM       = os.environ.get("MAAS_GPU_MEM_UTIL", "0.90")
MAAS_AUTH_GROUP    = os.environ.get("MAAS_AUTHORIZED_GROUP", "system:authenticated")


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
def _oc(args, input_data=None, check=True):
    quoted = " ".join(f'"{a}"' if " " in a else a for a in args)
    print(f"+ oc {quoted}")
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


def _helm(args, check=True):
    quoted = " ".join(f'"{a}"' if " " in a else a for a in args)
    print(f"+ helm {quoted}")
    result = subprocess.run(["helm"] + list(args), capture_output=True, text=True)
    if result.stdout:
        for line in result.stdout.strip().split("\n"):
            if line.strip():
                print("  " + line)
    if result.stderr:
        print(result.stderr, file=sys.stderr)
    if check and result.returncode != 0:
        sys.exit(result.returncode)
    return result


def _tag_exists(tag):
    resp = requests.get(
        "https://quay.io/api/v1/repository/redhat-ai-services/modelcar-catalog/tag/",
        params={"specificTag": tag}, timeout=30,
    )
    return resp.status_code == 200 and bool(resp.json().get("tags"))


def _resolve_modelcar_uri():
    if MODELCAR_IMAGE:
        bare = MODELCAR_IMAGE
        for scheme in ("oci://", "docker://"):
            if bare.startswith(scheme):
                bare = bare[len(scheme):]
                break
        return "oci://" + bare

    short_tag = MODEL_ID.split("/")[-1].lower()
    org_tag = MODEL_ID.lower().replace("/", "--")
    repo = "redhat-ai-services/modelcar-catalog"

    for candidate in (short_tag, org_tag):
        print(f"  Checking modelcar catalog for tag '{candidate}'...")
        if _tag_exists(candidate):
            return f"oci://quay.io/{repo}:{candidate}"

    print(f"FATAL: no modelcar image found for '{MODEL_ID}'", file=sys.stderr)
    sys.exit(1)


# ---------------------------------------------------------------------------
# Helm vLLM deployment path
# ---------------------------------------------------------------------------
def _deploy_helm_vllm():
    print("\n=== Helm vLLM deployment ===")

    modelcar_uri = _resolve_modelcar_uri()
    print(f"--- Using modelcar image: {modelcar_uri} ---")

    # --- Build deploy-values.yaml ---
    if VALUES_CONTENT.strip():
        print("--- Using caller-supplied valuesContent ---")
        with open("deploy-values.yaml", "w") as f:
            f.write(VALUES_CONTENT)
    else:
        gpu_count = 1
        tp_size = 1
        vllm_flags = []
        source = "default (1 GPU)"

        if os.path.isfile("deployment-options.json"):
            try:
                with open("deployment-options.json") as f:
                    data = json.load(f)
                options = data.get("options", [])
                recommended = options[0] if options else None
                if recommended and recommended.get("status") == "blocked":
                    print("FATAL: gpu-advisor's top plan is BLOCKED", file=sys.stderr)
                    sys.exit(1)
                if recommended:
                    gpu_count = int(recommended.get("gpu_per_replica", 1)) or 1
                    tp_size = int(recommended.get("topology", {}).get("tp", gpu_count)) or gpu_count
                    vllm_flags = recommended.get("vllm_flags", []) or []
                    source = f"gpu-advisor ({recommended.get('name', 'unknown')})"
            except Exception as e:
                print(f"WARNING: could not parse deployment-options.json: {e}")
        elif GPU_COUNT_OVERRIDE:
            gpu_count = int(GPU_COUNT_OVERRIDE)
            tp_size = gpu_count
            source = "gpu-count-override param"

        print(f"GPU count per replica: {gpu_count} (source: {source})")
        print(f"Tensor-parallel size: {tp_size}")

        base_args = {"--gpu-memory-utilization": "0.90"}
        plan_args = {}
        for flag in vllm_flags:
            if flag.startswith("--") and "=" in flag:
                k, v = flag.split("=", 1)
                plan_args[k] = v
        base_args.update(plan_args)
        if tp_size > 1:
            base_args["--tensor-parallel-size"] = str(tp_size)

        model_args = [f"{k}={v}" for k, v in base_args.items()]

        values = {
            "deploymentMode": "RawDeployment",
            "servingTopology": "singleNode",
            "hardwareProfile": {
                "name": HW_PROFILE_NAME,
                "namespace": HW_PROFILE_NS,
            },
            "inferenceService": {"name": RELEASE_NAME},
            "model": {
                "mode": "uri",
                "uri": modelcar_uri,
                "args": model_args,
            },
            "scaling": {
                "minReplicas": 1,
                "maxReplicas": 1,
                "rawDeployment": {"deploymentStrategy": {"type": "Recreate"}},
            },
        }

        if SR_VLLM_NAME:
            values["servingRuntime"] = {"useExisting": SR_VLLM_NAME}

        with open("deploy-values.yaml", "w") as f:
            yaml.dump(values, f, default_flow_style=False, sort_keys=False)

        with open("gpu-count-required.txt", "w") as f:
            f.write(str(gpu_count))

        print("--- Generated deploy-values.yaml ---")
        with open("deploy-values.yaml") as f:
            print(f.read())

    # --- Apply ServingRuntime ---
    print(f"\nApplying ServingRuntime [{SR_VLLM_NAME}] (image: {SR_VLLM_IMAGE}) to namespace [{NAMESPACE}]...")
    sr_yaml = f"""
apiVersion: serving.kserve.io/v1alpha1
kind: ServingRuntime
metadata:
  name: {SR_VLLM_NAME}
  annotations:
    opendatahub.io/apiProtocol: REST
    opendatahub.io/recommended-accelerators: '["nvidia.com/gpu"]'
    opendatahub.io/runtime-version: v0.9.1.0
    opendatahub.io/serving-runtime-scope: global
    opendatahub.io/template-display-name: vLLM NVIDIA GPU ServingRuntime for KServe
    opendatahub.io/template-name: vllm-cuda-runtime-template
    openshift.io/display-name: vLLM NVIDIA GPU ServingRuntime for KServe
  labels:
    opendatahub.io/dashboard: 'true'
spec:
  annotations:
    prometheus.io/path: /metrics
    prometheus.io/port: '8080'
  containers:
    - args:
        - '--port=8080'
        - '--model=/mnt/models'
        - '--served-model-name={{{{.Name}}}}'
      command:
        - python
        - '-m'
        - vllm.entrypoints.openai.api_server
      env:
        - name: HF_HOME
          value: /tmp/hf_home
      image: '{SR_VLLM_IMAGE}'
      name: kserve-container
      ports:
        - containerPort: 8080
          protocol: TCP
  multiModel: false
  supportedModelFormats:
    - autoSelect: true
      name: vLLM
"""
    _oc(["apply", "-n", NAMESPACE, "-f", "-"], input_data=sr_yaml)

    # --- GPU cross-check ---
    required_gpus = 1
    if os.path.isfile("gpu-count-required.txt"):
        with open("gpu-count-required.txt") as f:
            required_gpus = int(f.read().strip() or 1)

    if HW_PROFILE_NAME:
        r = subprocess.run(
            ["oc", "get", "hardwareprofile", HW_PROFILE_NAME,
             "-n", HW_PROFILE_NS,
             "-o", "jsonpath={.spec.identifiers[?(@.identifier==\"nvidia.com/gpu\")].defaultCount}"],
            capture_output=True, text=True,
        )
        profile_gpu = r.stdout.strip()
        if profile_gpu:
            print(f"HardwareProfile [{HW_PROFILE_NAME}] default GPU count: {profile_gpu} (plan requires: {required_gpus})")
            if profile_gpu != str(required_gpus):
                print(f"WARNING: GPU count mismatch (profile={profile_gpu}, plan={required_gpus})")

    # --- Helm install ---
    print("\n--- Helm values to be applied ---")
    with open("deploy-values.yaml") as f:
        print(f.read())
    print("----------------------------------")

    print("Adding Helm repo...")
    _helm(["repo", "add", "redhat-ai-services", CHART_URL])
    _helm(["repo", "update"])

    print(f"Deploying release [{RELEASE_NAME}] to namespace [{NAMESPACE}]")
    _oc(["label", "ns", NAMESPACE, "evalhub.trustyai.opendatahub.io/tenant=true", "--overwrite"], check=False)

    _helm([
        "upgrade", "--install",
        RELEASE_NAME,
        "redhat-ai-services/vllm-kserve",
        "--version", CHART_VERSION,
        "--namespace", NAMESPACE,
        "-f", "deploy-values.yaml",
    ])

    # --- Delete orphan ServingRuntime ---
    if SR_VLLM_NAME:
        orphan = f"{RELEASE_NAME}-vllm-kserve"
        if orphan != SR_VLLM_NAME:
            r = _oc(["get", "servingruntime", orphan, "-n", NAMESPACE], check=False)
            if r.returncode == 0:
                print(f"Deleting chart-created orphan ServingRuntime [{orphan}]...")
                _oc(["delete", "servingruntime", orphan, "-n", NAMESPACE, "--ignore-not-found"], check=False)

    # --- Disable auth ---
    print("Disabling enforced predictor auth...")
    _oc(["annotate", "inferenceservice", RELEASE_NAME,
         "--namespace", NAMESPACE,
         "security.opendatahub.io/enable-auth=false", "--overwrite"], check=False)

    # --- Wait for readiness ---
    def has_auth_sidecar():
        r = subprocess.run(
            ["oc", "get", "deployment", f"{RELEASE_NAME}-predictor",
             "-n", NAMESPACE,
             "-o", "jsonpath={.spec.template.spec.containers[*].name}"],
            capture_output=True, text=True,
        )
        return "kube-rbac-proxy" in r.stdout

    def is_ready():
        r = subprocess.run(
            ["oc", "get", "inferenceservice", RELEASE_NAME,
             "-n", NAMESPACE,
             "-o", "jsonpath={.status.conditions[?(@.type==\"Ready\")].status}"],
            capture_output=True, text=True,
        )
        return r.stdout.strip() == "True"

    print(f"Waiting for InferenceService [{RELEASE_NAME}] to be Ready without auth sidecar...")
    deadline = time.time() + 1500
    last_annotate = 0
    while time.time() < deadline:
        if is_ready() and not has_auth_sidecar():
            print(f"InferenceService [{RELEASE_NAME}] is Ready with no auth sidecar.")
            break

        if has_auth_sidecar() and (time.time() - last_annotate) >= 20:
            print("  Auth sidecar present - (re)asserting enable-auth=false...")
            _oc(["annotate", "inferenceservice", RELEASE_NAME,
                 "--namespace", NAMESPACE,
                 "security.opendatahub.io/enable-auth=false", "--overwrite"], check=False)
            last_annotate = time.time()

        # Scale old ReplicaSets to 0 to break GPU deadlock
        r = subprocess.run(
            ["oc", "get", "replicaset", "-n", NAMESPACE,
             "-l", f"serving.kserve.io/inferenceservice={RELEASE_NAME},component=predictor",
             "--sort-by=.metadata.creationTimestamp",
             "-o", "jsonpath={.items[-1:].metadata.name}"],
            capture_output=True, text=True,
        )
        newest_rs = r.stdout.strip()
        r2 = subprocess.run(
            ["oc", "get", "replicaset", "-n", NAMESPACE,
             "-l", f"serving.kserve.io/inferenceservice={RELEASE_NAME},component=predictor",
             "-o", "jsonpath={.items[*].metadata.name}"],
            capture_output=True, text=True,
        )
        for rs in r2.stdout.split():
            if newest_rs and rs != newest_rs:
                r3 = subprocess.run(
                    ["oc", "get", "replicaset", rs, "-n", NAMESPACE,
                     "-o", "jsonpath={.spec.replicas}"],
                    capture_output=True, text=True,
                )
                if r3.stdout.strip() not in ("", "0"):
                    print(f"  Scaling down superseded ReplicaSet [{rs}] to unblock single-GPU rollout...")
                    _oc(["scale", "replicaset", rs, "-n", NAMESPACE, "--replicas=0"], check=False)

        time.sleep(10)

    if not is_ready():
        print(f"ERROR: InferenceService [{RELEASE_NAME}] did not become Ready", file=sys.stderr)
        _oc(["get", "inferenceservice", RELEASE_NAME, "-n", NAMESPACE, "-o", "yaml"], check=False)
        _oc(["get", "pods", "-n", NAMESPACE, "-l", f"serving.kserve.io/inferenceservice={RELEASE_NAME}"], check=False)
        sys.exit(1)

    if has_auth_sidecar():
        print("WARNING: could not remove auth sidecar automatically")
    else:
        print("Predictor is Ready and serving without an auth sidecar - no bearer token required.")


# ---------------------------------------------------------------------------
# MaaS deployment path
# ---------------------------------------------------------------------------
def _deploy_maas():
    print("\n=== MaaS (LLMInferenceService) deployment ===")
    from maas_deploy import deploy as maas_deploy_func
    modelcar_uri = _resolve_modelcar_uri()
    print(f"--- Using modelcar image: {modelcar_uri} ---")
    maas_deploy_func(
        model_name=RELEASE_NAME,
        model_id=MODEL_ID,
        modelcar_image=modelcar_uri,
        serving_ns=MAAS_SERVING_NS,
        policy_ns=MAAS_POLICY_NS,
        gpu_count=MAAS_GPU_COUNT,
        runtime_image=MAAS_RUNTIME_IMAGE,
        gpu_mem=MAAS_GPU_MEM,
        authorized_group=MAAS_AUTH_GROUP,
    )


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
if __name__ == "__main__":
    print(f"ModelOps deploy-model | MaaS={'enabled' if DEPLOY_MAAS else 'disabled'}")
    print(f"  Release: {RELEASE_NAME} | Namespace: {NAMESPACE} | Model: {MODEL_ID}")

    if DEPLOY_MAAS:
        _deploy_maas()
    else:
        _deploy_helm_vllm()

    print("\nDeployment complete.")
