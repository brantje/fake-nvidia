# LlamaCPP-Manager example

This directory contains a standalone Docker Compose example for running an unmodified LlamaCPP-Manager container against fake NVIDIA GPUs on a CPU-only Linux host.

## Start it

From the `fake-nvidia` repository root:

```bash
make runtime

go run ./cmd/fake-nvidia up \
  --profile rtx4060ti-16gb \
  --gpus 2 \
  --runtime-dir "$PWD/.runtime" \
  --root "$PWD/.fake-nvidia/llamacpp-manager"

mkdir -p examples/llamacpp-manager/data/{config,models}

export FAKE_NVIDIA_ROOT="$PWD/.fake-nvidia/llamacpp-manager"
export PUID="$(id -u)"
export PGID="$(id -g)"

docker compose -f examples/llamacpp-manager/docker-compose.yaml up -d
```

Open `http://localhost:8080`.

The example persists LlamaCPP-Manager configuration and model files under `examples/llamacpp-manager/data/` by default.

## Stop it

```bash
docker compose -f examples/llamacpp-manager/docker-compose.yaml down
```

## Change the fake GPUs

Recreate the fake-nvidia root with another profile/count, for example four 24 GiB RTX 4090s:

```bash
go run ./cmd/fake-nvidia up \
  --profile rtx4090-24gb \
  --gpus 4 \
  --runtime-dir "$PWD/.runtime" \
  --root "$PWD/.fake-nvidia/llamacpp-manager"
```

You can also use a built-in mixed topology:

```bash
go run ./cmd/fake-nvidia up \
  --topology mixed-gpu \
  --runtime-dir "$PWD/.runtime" \
  --root "$PWD/.fake-nvidia/llamacpp-manager"
```

Restart the manager after replacing the fake runtime/state:

```bash
docker compose -f examples/llamacpp-manager/docker-compose.yaml restart
```

## Useful overrides

The Compose file supports these environment variables:

- `LCM_PORT` — host port, default `8080`.
- `LCM_CONFIG_HOST_DIR` — host directory mounted at `/config`.
- `LCM_MODELS_HOST_DIR` — host directory mounted at `/models`.
- `PUID` / `PGID` — container user IDs, default `1000:1000`.
- `LLAMACPP_MANAGER_IMAGE` — manager image override. The default is the immutable image validated by Phase 8.

## How it works

No NVIDIA Container Toolkit and no physical GPU are required. The Compose file mounts the generated fake-nvidia runtime into LlamaCPP-Manager and configures:

- Mock NVML and the compatible `nvidia-smi` path for discovery and telemetry.
- The limited fake CUDA userspace used by the test environment.
- `fake-llama-server` through LlamaCPP-Manager's normal `LCM_LLAMA_SERVER` setting.
- `/proc` through `LCM_HOST_PROC` so manager process accounting follows its normal container deployment path.

LlamaCPP-Manager itself is not modified and does not contain a fake-NVIDIA-specific code path.

`compose.phase8.yaml` remains the pinned CI/full-stack test harness. `compose.fake-nvidia.yaml` is the smaller overlay for injecting only NVIDIA discovery/telemetry into the upstream LlamaCPP-Manager Compose stack.
