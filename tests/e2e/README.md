# Phase 8 — LlamaCPP-Manager E2E

This directory contains CPU-only full-stack compatibility scenarios for an **unmodified published LlamaCPP-Manager image**.

## Compatibility target

Phase 8 is pinned to:

- Repository: `brantje/llamacpp-manager`
- Revision: `0c26e8e19635c5047d06babc7ba3b0173570e6ce`
- Revision tag: `ghcr.io/brantje/llamacpp-manager:main-0c26e8e`
- Immutable image: `ghcr.io/brantje/llamacpp-manager@sha256:1cbd6bf1d31893cdcdf6126e1e4239d39f1a903e4837251d0cd528d7a7a70586`

The Phase 8 GitHub Actions workflow pulls the immutable image digest and records it in the job summary. `LLAMACPP_MANAGER_IMAGE` can be overridden deliberately for compatibility checks, but advancing the default pin must update the revision, image digest, workflow metadata, and this document together.

## Boundary

The manager is not modified for fake-nvidia. The test container receives normal NVIDIA-facing userspace and a llama-server replacement through runtime injection:

- Mock NVML + the fake-nvidia `nvidia-smi` dispatcher
- `nvml-mock-ctl` backed live state overrides
- the limited CUDA shim from the fake runtime
- `fake-llama-server` through the manager's existing `LCM_LLAMA_SERVER` setting

The harness bootstraps and authenticates through the real management API, creates sparse GGUF test files, creates real manager Models/Instances, and drives the normal lifecycle endpoints.

## Scenario matrix

The current suite covers:

| Area | Scenarios |
| --- | --- |
| Discovery | 1x 8 GiB, 1x 16 GiB, 2x 16 GiB, 4x 24 GiB, mixed 16/24/16/48 GiB, no NVIDIA GPU, GPU lost/offline and recovery |
| Capacity / placement | single-GPU placement, aggregate multi-GPU split placement, external VRAM pressure, insufficient capacity, deterministic resource change between manager planning and worker reservation |
| Lifecycle / eviction | worker start/stop accounting, eligible victim eviction, eviction-disabled worker protection, multiple simultaneous workers pinned to distinct fake GPUs |
| Telemetry | manager-visible process PID/device/VRAM, GPU used-memory deltas, live utilization mutation, UI-facing runtime WebSocket per-instance VRAM and GPU utilization |
| Failure paths | injected CUDA OOM, startup timeout, NVIDIA query failure/recovery, GPU lost/offline, post-ready fake llama crash, simulator process/VRAM cleanup |

Each lifecycle scenario asserts both manager-observable state and fake-nvidia runtime state so a manager decision cannot pass merely because the fake server defaulted to GPU 0.

The plan-versus-launch scenario uses `FAKE_LLAMA_REGISTER_GATE` to hold the fake worker after LlamaCPP-Manager has planned and launched it but before the worker registers fake NVML process/VRAM state. The test changes available VRAM and then releases the gate, making the race deterministic rather than timing-based.

## Running

The full-stack suite is intentionally separate from ordinary Go tests:

```bash
make phase8-e2e
```

Requirements:

- Linux
- Docker daemon
- network access to pull the pinned LlamaCPP-Manager GHCR image
- enough disk space to build the pinned fake-nvidia runtime

`make phase8-e2e` builds the fake runtime and then runs:

```bash
FAKE_NVIDIA_RUNTIME_DIR="$PWD/.runtime" \
  go test -tags=e2e -count=1 -v ./tests/e2e
```

The model files are sparse files, so logical model sizes such as 20 GiB do not consume the corresponding amount of host disk space.

For interactive reproduction, use `examples/llamacpp-manager/compose.phase8.yaml` after preparing an injection root with `fake-nvidia up`.
