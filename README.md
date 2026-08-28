# fake-nvidia

`fake-nvidia` is a CPU-only NVIDIA GPU environment simulator for integration and system testing.

It is intended for software that normally discovers and monitors NVIDIA GPUs through NVML, `nvidia-smi`, CUDA device APIs, container runtime injection, and GPU process telemetry. The primary initial consumer is LlamaCPP-Manager, but the project remains generic and does not require consumer applications to contain a special "fake GPU" code path.

> **Status:** Phase 1 profile/state integration is merged. Phase 2 adds a reproducible pinned Mock NVML + real `nvidia-smi` runtime bundle and CPU-only command-level discovery tests.

## Why

Testing GPU-aware schedulers and managers normally requires access to many physical GPU topologies: different device counts, VRAM capacities, mixed cards, external VRAM pressure, process utilization, OOM conditions, device failures, and multi-GPU placement.

`fake-nvidia` aims to make those scenarios reproducible on ordinary CPU-only Linux hosts and CI runners.

Typical scenarios include:

- 1x 8/16 GiB GPU
- 2x 16 GiB GPUs
- 4x 24 GiB GPUs
- Mixed GPU models and VRAM sizes
- External processes consuming VRAM
- GPU utilization changes
- GPU disappearance/failure
- CUDA allocation failures / OOM
- Scheduler eviction before model load
- Per-process GPU telemetry

## What this project is

`fake-nvidia` is a **GPU environment simulator**, not a cycle-accurate GPU emulator.

The project builds on NVIDIA's open-source [`k8s-test-infra`](https://github.com/NVIDIA/k8s-test-infra) Mock NVML implementation rather than reimplementing hundreds of NVML symbols.

The planned stack is:

```text
                        fake-nvidia
                 profiles / scenarios / CLI
                              |
               upstream Mock NVML runtime state
                    + targeted extensions
                              |
          +-------------------+-------------------+
          |                   |                   |
   libnvidia-ml.so        nvidia-smi       limited libcuda.so
          |                   |                   |
          +-------------------+-------------------+
                              |
                    unmodified consumer
                              |
                 e.g. LlamaCPP-Manager
```

An optional fake `llama-server` will provide a real child process, health/inference endpoints, and simulated VRAM/process registration for full scheduler and eviction tests without performing real GGUF inference.

## What this project is not

`fake-nvidia` is **not** intended to:

- Execute CUDA kernels on the CPU.
- Interpret PTX or SASS.
- Produce meaningful GPU performance numbers.
- Benchmark CUDA or LLM inference performance.
- Make an unmodified CUDA build of llama.cpp perform real inference without a GPU.
- Silently return success for unsupported CUDA operations when doing so would invalidate a test.

Simulated utilization, memory pressure, timing, and throughput are test inputs—not predictions of real hardware performance.

## Implementation language

`fake-nvidia` is a **Go project**. All CLIs, orchestration, profile/config handling, scenario execution, integration helpers, test harnesses, and the optional fake `llama-server` are implemented in Go.

C/CGo is reserved for the narrow NVIDIA-compatible native ABI surface where necessary, such as Mock NVML/Mock CUDA shared-library integration. Core behavior must not be split into Python, Rust, Node.js, or other language-specific services.

## Phase 1 profile/configuration tool

The CLI renders upstream-compatible Mock NVML YAML without requiring a GPU:

```bash
go run ./cmd/fake-nvidia profiles
go run ./cmd/fake-nvidia topologies

go run ./cmd/fake-nvidia render \
  --profile rtx4060ti-16gb \
  --count 2 \
  --vram-mib 16384

go run ./cmd/fake-nvidia render \
  --device rtx4060ti-16gb@16384 \
  --device rtx4090-24gb@24576

go run ./cmd/fake-nvidia render --topology mixed-gpu
```

For full per-device state, including used VRAM, utilization, process records, temperature/power, and failure injection, use a strict JSON spec:

```json
{
  "system": {
    "driver_version": "580.173.02",
    "cuda_version": "13.0"
  },
  "devices": [
    {
      "profile": "rtx4090-24gb",
      "vram_mib": 24576,
      "used_mib": 4096,
      "gpu_util": 72,
      "memory_util": 31,
      "processes": [
        {
          "pid": 1234,
          "type": "C",
          "name": "llama-server",
          "used_memory_mib": 2048,
          "sm_util": 60
        }
      ],
      "failure": {
        "mode": "lost",
        "after_calls": 7,
        "xid": 79
      }
    }
  ]
}
```

Render it with:

```bash
go run ./cmd/fake-nvidia render --spec ./config.json --output ./mock-nvml.yaml
```

Built-in cards currently include RTX 4060 Ti 16 GB, RTX 4090 24 GB, T4 16 GB, L40S 48 GB, A100 40 GB, and H100 80 GB. T4/L40S/A100/H100 are tied to the pinned upstream Mock NVML profile sources; see [`UPSTREAM.md`](UPSTREAM.md).

Runtime mutation is intentionally delegated to upstream `nvml-mock-ctl`, including its locking, atomic override writes, merge precedence, and cross-process reload behavior. `fake-nvidia` does not maintain a second runtime-state database or daemon.

## Phase 2 Mock NVML runtime

Build the pinned NVIDIA-facing userspace bundle with:

```bash
make runtime
```

This produces ignored local artifacts under `.runtime/`:

```text
.runtime/bin/nvidia-smi
.runtime/bin/nvml-mock-ctl
.runtime/lib/libnvidia-ml.so
.runtime/lib/libnvidia-ml.so.1
.runtime/lib/libnvidia-ml.so.<version>
```

The build compiles Mock NVML from the exact `NVIDIA/k8s-test-infra` revision in `runtime/pins.env` and extracts NVIDIA's real `nvidia-smi` from the pinned official `nvidia-utils-580` package. It does not implement a fake `nvidia-smi` parser or copy host NVIDIA drivers.

Example:

```bash
go run ./cmd/fake-nvidia render \
  --topology mixed-gpu \
  --output .runtime/config/config.yaml

export PATH="$PWD/.runtime/bin:$PATH"
export LD_LIBRARY_PATH="$PWD/.runtime/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export MOCK_NVML_CONFIG="$PWD/.runtime/config/config.yaml"
export MOCK_NVML_OVERRIDES="$PWD/.runtime/config/overrides.yaml"

nvidia-smi -L
nvidia-smi --query-gpu=index,uuid,name,memory.total,memory.used,utilization.gpu \
  --format=csv,noheader,nounits
```

Run the full Phase 2 native contract suite with:

```bash
make phase2
```

The suite runs without physical NVIDIA hardware and exercises single, multiple, and mixed GPUs, `nvidia-smi`, `-L`, `-q`, LlamaCPP-Manager discovery, unsupported field rendering, and live memory/utilization mutations observed by a separate process. See [`runtime/README.md`](runtime/README.md) for the runtime layout and safety boundary.

## Upstream foundation

NVIDIA Mock NVML already provides much of the low-level behavior this project needs, including configurable GPU profiles, a real `nvidia-smi` binary backed by mock NVML, runtime state overrides, `nvml-mock-ctl`, process records, dynamic metrics, failure injection, Docker examples, CDI/Kubernetes integration, and fake NVIDIA device surfaces.

`fake-nvidia` wraps and extends those capabilities instead of maintaining a competing NVML implementation.

Known compatibility work needed after Phase 2 includes `nvidia-smi pmon`, which upstream currently does not expose through its mock despite supporting public NVML per-process utilization APIs.

## LlamaCPP-Manager compatibility target

The simulator supports the normal interfaces LlamaCPP-Manager consumes, without source changes to the manager.

Current hardware discovery first attempts max-memory-clock enrichment:

```bash
nvidia-smi \
  --query-gpu=index,uuid,name,memory.total,memory.used,utilization.gpu,clocks.max.memory \
  --format=csv,noheader,nounits
```

and falls back to the required six-field discovery contract if the driver rejects that enrichment:

```bash
nvidia-smi \
  --query-gpu=index,uuid,name,memory.total,memory.used,utilization.gpu \
  --format=csv,noheader,nounits
```

Phase 2 tests both the exact six-field parser contract and the current enriched/fallback flow. Clock values are not invented when a profile does not model them; upstream `NOT_SUPPORTED` is allowed to render as `N/A`.

GPU process accounting is a Phase 3 compatibility surface:

```bash
nvidia-smi \
  --query-compute-apps=pid,gpu_uuid,used_memory,process_name \
  --format=csv,noheader,nounits
```

Process utilization is also completed in Phase 3:

```bash
nvidia-smi pmon -c 1 -s u
nvidia-smi pmon -c 1
```

Deployment-specific injection is allowed. Consumer source-code changes are not required or desired.

## Planned components

- GPU profile/configuration layer
- Upstream Mock NVML integration
- Mock-NVML-backed `nvidia-smi`
- Runtime mutation/control UX
- Declarative scenario runner
- `nvidia-smi pmon` compatibility extension
- Docker/Compose runtime injection
- Limited CUDA device/memory shim
- Optional fake `llama-server`
- LlamaCPP-Manager E2E scenario suite
- Kubernetes/CDI integration
- CPU-only CI and release packaging

See [`SPEC.md`](SPEC.md) for the detailed design.

## Roadmap

1. [Phase 0 — Bootstrap repository and project documentation](https://github.com/brantje/fake-nvidia/issues/1)
2. [Phase 1 — Build GPU profiles and runtime-state integration on Mock NVML](https://github.com/brantje/fake-nvidia/issues/2)
3. [Phase 2 — Integrate Mock NVML and provide nvidia-smi GPU discovery compatibility](https://github.com/brantje/fake-nvidia/issues/3)
4. [Phase 3 — Emulate GPU process accounting and nvidia-smi pmon](https://github.com/brantje/fake-nvidia/issues/4)
5. [Phase 4 — Build fake-nvidia control/scenario UX on upstream runtime overrides](https://github.com/brantje/fake-nvidia/issues/5)
6. [Phase 5 — Package Docker/runtime injection for transparent consumer use](https://github.com/brantje/fake-nvidia/issues/6)
7. [Phase 6 — Add limited CUDA driver/runtime shim for device and memory simulation](https://github.com/brantje/fake-nvidia/issues/7)
8. [Phase 7 — Implement fake llama-server for full scheduler/eviction integration tests](https://github.com/brantje/fake-nvidia/issues/8)
9. [Phase 8 — Build LlamaCPP-Manager end-to-end compatibility scenarios](https://github.com/brantje/fake-nvidia/issues/9)
10. [Phase 9 — Add Kubernetes/CDI integration without changing simulator semantics](https://github.com/brantje/fake-nvidia/issues/10)
11. [Phase 10 — Add CI matrix, release packaging, compatibility tracking, and profiles](https://github.com/brantje/fake-nvidia/issues/11)

## Development workflow

**Never push directly to `main`.**

All work must be performed on a dedicated branch and merged through a pull request after tests/checks pass. See [`AGENTS.md`](AGENTS.md) for contributor/agent rules.

## License

A project license has not yet been selected. Any reuse or distribution of upstream NVIDIA Mock NVML/Mock CUDA code or artifacts must preserve the applicable upstream license and notices. NVIDIA `k8s-test-infra` is currently Apache-2.0 licensed. See [`runtime/THIRD_PARTY_NOTICES.md`](runtime/THIRD_PARTY_NOTICES.md) for Phase 2 native dependency notes.
