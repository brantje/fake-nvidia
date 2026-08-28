#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
out_dir=${1:-"${repo_root}/.runtime"}

# shellcheck disable=SC1091
source "${repo_root}/runtime/pins.env"

rm -rf "${out_dir}"
mkdir -p "${out_dir}"

docker buildx build \
  --file "${repo_root}/runtime/Dockerfile" \
  --target export \
  --build-arg "UPSTREAM_REVISION=${UPSTREAM_REVISION}" \
  --build-arg "UPSTREAM_GO_VERSION=${UPSTREAM_GO_VERSION}" \
  --build-arg "NVIDIA_UTILS_VERSION=${NVIDIA_UTILS_VERSION}" \
  --output "type=local,dest=${out_dir}" \
  "${repo_root}"

mkdir -p "${out_dir}/config"

test -x "${out_dir}/bin/nvidia-smi"
test -x "${out_dir}/bin/nvml-mock-ctl"
test -e "${out_dir}/lib/libnvidia-ml.so.1"

echo "fake-nvidia runtime bundle: ${out_dir}"
echo "upstream: NVIDIA/k8s-test-infra@${UPSTREAM_REVISION}"
echo "nvidia-smi package: nvidia-utils-580=${NVIDIA_UTILS_VERSION}"
