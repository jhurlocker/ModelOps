#!/usr/bin/env bash
set -euo pipefail

# Build and push all ModelOps tool container images.
#
# Usage:
#   ./build-tools.sh [REGISTRY] [TAG]
#
# Defaults:
#   REGISTRY = quay.io/jhurlocker
#   TAG      = v0.1.0
#
# Requirements: podman (or docker)

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TOOLS_DIR="${SCRIPT_DIR}"

REGISTRY="${1:-quay.io/jhurlocker}"
TAG="${2:-v0.1.0}"

# Tools with Containerfiles (directory name == image name)
TOOLS=(
  #approval-gate
  #compliance-scanner
  #gpu-advisor
  #guidellm-benchmark
  maas-deploy
  model-registry-helper
  s3-upload
  security-scanner
)

# Choose container runtime
if command -v podman &>/dev/null; then
  RUNTIME="podman"
elif command -v docker &>/dev/null; then
  RUNTIME="docker"
else
  echo "ERROR: neither podman nor docker found in PATH" >&2
  exit 1
fi

echo "Using runtime: ${RUNTIME}"
echo "Registry:      ${REGISTRY}"
echo "Tag:           ${TAG}"
echo "Tools:         ${#TOOLS[@]}"
echo ""

build_and_push() {
  local name="$1"
  local dir="${TOOLS_DIR}/${name}"
  local image="${REGISTRY}/${name}:${TAG}"

  if [[ ! -f "${dir}/Containerfile" ]]; then
    echo "[${name}] SKIP: no Containerfile found"
    return 0
  fi

  echo "[${name}] Building ${image} ..."
  ${RUNTIME} build \
    -t "${image}" \
    -f "${dir}/Containerfile" \
    "${dir}"

  echo "[${name}] Pushing ${image} ..."
  ${RUNTIME} push "${image}"

  echo "[${name}] DONE"
  echo ""
}

for tool in "${TOOLS[@]}"; do
  build_and_push "${tool}"
done

echo "All ${#TOOLS[@]} tools built and pushed to ${REGISTRY}/tool-name:${TAG}"
