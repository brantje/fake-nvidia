# fake-nvidia

`fake-nvidia` is a CPU-only NVIDIA GPU environment simulator for integration and system testing.

It is intended for software that normally discovers and monitors NVIDIA GPUs through NVML, `nvidia-smi`, CUDA device APIs, container runtime injection, and GPU process telemetry. The primary initial consumer is LlamaCPP-Manager, but the project remains generic and does not require consumer applications to contain a special "fake GPU" code path.

> **Status:** Phases 1 and 2 provide profile/state composition plus a reproducible pinned Mock NVML runtime. Phase 3 adds process accounting and parser-compatible one-shot `nvidia-smi pmon` telemetry for LlamaCPP-Manager.

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

## Profile/configuration tool

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
          "sm_util": 60,
          "mem_util": 12
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

## Mock NVML runtime

Build the pinned NVIDIA-facing userspace bundle with:

```bash
make runtime
```

This produces ignored local artifacts under `.runtime/`:

```text
.runtime/bin/nvidia-smi
.runtime/bin/nvidia-smi.real
.runtime/bin/nvml-mock-ctl
.runtime/lib/libnvidia-ml.so
.runtime/lib/libnvidia-ml.so.1
.runtime/lib/libnvidia-ml.so.<version>
```

The build compiles Mock NVML from the exact `NVIDIA/k8s-test-infra` revision in `runtime/pins.env` and extracts NVIDIA's real `nvidia-smi` from the pinned official `nvidia-utils-580` package. The untouched NVIDIA utility is stored as `nvidia-smi.real`.

The `nvidia-smi` command is a narrow Go dispatcher. For ordinary discovery, CSV queries, `-L`, `-q`, compute-process enumeration, and all other non-supported-`pmon` forms, it directly delegates to `nvidia-smi.real`. It does not reimplement the general NVIDIA-SMI parser or renderer.

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

## Phase 3 process telemetry

The pinned upstream Mock NVML already exposes configured/runtime-mutated process rows through the real NVIDIA utility, so fake-nvidia preserves that path for LlamaCPP-Manager's exact process query:

```bash
nvidia-smi \
  --query-compute-apps=pid,gpu_uuid,used_memory,process_name \
  --format=csv,noheader,nounits
```

Upstream also implements the public `nvmlDeviceGetProcessUtilization` API, but the pinned upstream changelog documents that real `nvidia-smi pmon` reaches a separate private/internal NVIDIA entry point that is not mapped by the mock. Rather than guessing that private ABI, the dispatcher intercepts only the two one-shot forms LlamaCPP-Manager currently tries:

```bash
nvidia-smi pmon -c 1 -s u
nvidia-smi pmon -c 1
```

The compatibility rows are populated from the public Mock NVML per-process utilization surface. GPU index, PID, SM utilization, memory utilization, encoder utilization, decoder utilization, and process name are represented; one PID may appear on multiple GPUs. All unrelated `pmon` modes are delegated to the real NVIDIA binary.

The low-level `SetProcesses` control method preserves upstream process-override semantics. For fake-nvidia-managed scenarios that also own process VRAM, `SetProcessesReconciled` writes the process list plus device used/free memory in one upstream override transaction. Explicit non-process/system usage is retained, and removing a fake process releases its process-owned VRAM.

Run the native discovery + process compatibility suite with:

```bash
make phase3
```

`make phase2` remains as a backward-compatible alias. The suite runs without physical NVIDIA hardware and covers discovery, baseline NVIDIA-SMI forms, the exact compute-app query, both LlamaCPP-Manager `pmon` forms, multiple processes, a PID spanning multiple GPUs, live process overrides, empty process state, delegation to `nvidia-smi.real`, and process-memory reconciliation. See [`runtime/README.md`](runtime/README.md) for the runtime layout and safety boundary.

## Upstream foundation

NVIDIA Mock NVML already provides much of the low-level behavior this project needs, including configurable GPU profiles, runtime state overrides, `nvml-mock-ctl`, process records, public process-utilization APIs, dynamic metrics, failure injection, Docker examples, CDI/Kubernetes integration, and fake NVIDIA device surfaces.

`fake-nvidia` wraps and extends those capabilities instead of maintaining a competing NVML implementation. The Phase 3 `pmon` compatibility path is intentionally narrow and removable if upstream later maps NVIDIA-SMI's private process-monitoring entry point. See [`UPSTREAM.md`](UPSTREAM.md) for the evidence and boundary.

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

The compatibility suite tests both the exact six-field parser contract and the current enriched/fallback flow. Clock values are not invented when a profile does not model them; upstream `NOT_SUPPORTED` is allowed to render as `N/A`.

GPU process accounting is tested through:

```bash
nvidia-smi \
  --query-compute-apps=pid,gpu_uuid,used_memory,process_name \
  --format=csv,noheader,nounits
```

Process utilization is tested through the manager's fallback sequence:

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

A project license has not yet been selected. Any reuse or distribution of upstream NVIDIA Mock NVML/Mock CUDA code or artifacts must preserve the applicable upstream license and notices. NVIDIA `k8s-test-infra` is currently Apache-2.0 licensed. See [`runtime/THIRD_PARTY_NOTICES.md`](runtime/THIRD_PARTY_NOTICES.md) for native dependency notes.
