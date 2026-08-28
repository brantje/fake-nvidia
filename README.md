# fake-nvidia

`fake-nvidia` is a CPU-only NVIDIA GPU environment simulator for integration and system testing.

It is intended for software that normally discovers and monitors NVIDIA GPUs through NVML, `nvidia-smi`, CUDA device APIs, container runtime injection, and GPU process telemetry. The primary initial consumer is LlamaCPP-Manager, but the project must remain generic and must not require consumer applications to contain a special "fake GPU" code path.

> **Status:** planning/bootstrap. The implementation does not exist yet. Track progress in the repository issues.

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

The project will build on NVIDIA's open-source [`k8s-test-infra`](https://github.com/NVIDIA/k8s-test-infra) Mock NVML implementation rather than reimplementing hundreds of NVML symbols.

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

## Upstream foundation

NVIDIA Mock NVML already provides much of the low-level behavior this project needs, including configurable GPU profiles, a real `nvidia-smi` binary backed by mock NVML, runtime state overrides, `nvml-mock-ctl`, process records, dynamic metrics, failure injection, Docker examples, CDI/Kubernetes integration, and fake NVIDIA device surfaces.

`fake-nvidia` should wrap and extend those capabilities instead of maintaining a competing NVML implementation.

Known compatibility work needed for the initial LlamaCPP-Manager target includes `nvidia-smi pmon`, which upstream currently does not expose through its mock despite supporting public NVML per-process utilization APIs.

## LlamaCPP-Manager compatibility target

The simulator must support the normal interfaces LlamaCPP-Manager already consumes, without source changes to the manager.

Hardware discovery:

```bash
nvidia-smi \
  --query-gpu=index,uuid,name,memory.total,memory.used,utilization.gpu \
  --format=csv,noheader,nounits
```

GPU process accounting:

```bash
nvidia-smi \
  --query-compute-apps=pid,gpu_uuid,used_memory,process_name \
  --format=csv,noheader,nounits
```

Process utilization:

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

A project license has not yet been selected. Any reuse or distribution of upstream NVIDIA Mock NVML/Mock CUDA code or artifacts must preserve the applicable upstream license and notices. NVIDIA `k8s-test-infra` is currently Apache-2.0 licensed.
