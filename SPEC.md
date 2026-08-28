# fake-nvidia Specification

Status: **Draft / bootstrap**  
Repository: `brantje/fake-nvidia`

## 1. Purpose

`fake-nvidia` provides a configurable NVIDIA GPU environment for integration, system, scheduler, telemetry, and failure-path testing on machines without physical NVIDIA GPUs.

The simulator must be usable by unmodified consumers that normally discover GPUs through NVIDIA-facing interfaces. Deployment/runtime configuration may differ in tests, but consumer source code must not need to know that the GPU environment is simulated.

The primary initial integration target is LlamaCPP-Manager. The implementation must remain generic enough to support other NVML/`nvidia-smi`/CUDA-aware software.

## 2. Core principles

1. **Transparent to consumers.** Prefer normal NVIDIA interfaces over fake-nvidia-specific SDKs inside consumer applications.
2. **Upstream first.** Build on NVIDIA `k8s-test-infra` Mock NVML/Mock CUDA instead of recreating NVIDIA API surfaces unnecessarily.
3. **No fake performance claims.** Simulated utilization, VRAM, timing, and throughput are test state, not hardware benchmarks.
4. **Fail unsupported behavior clearly.** Do not return success for unsupported compute operations if doing so could make a test falsely pass.
5. **CPU-only testability.** Core functionality and compatibility tests must run without physical NVIDIA hardware.
6. **Deterministic scenarios.** Resource pressure and failures should be reproducible.
7. **Go-first implementation.** All fake-nvidia application, CLI, orchestration, scenario, integration, and fake-server code is written in Go. C/CGo is limited to NVIDIA ABI/shared-library boundaries where required.
8. **No direct pushes to `main`.** All repository changes go through branches and pull requests.

## 3. Goals

### 3.1 Hardware discovery

Allow configuration of arbitrary fake NVIDIA GPU topologies, including:

- GPU count
- Model/product name
- UUID
- PCI identity
- Architecture and compute capability metadata
- Total/reserved/used/free VRAM
- GPU and memory utilization
- Temperature/power/clocks where useful
- Device visibility and supported failure state

### 3.2 Process telemetry

Represent GPU processes with:

- PID
- Process name
- GPU assignment
- Used VRAM
- SM/GPU utilization
- Memory utilization where supported

### 3.3 Dynamic runtime state

Tests must be able to change supported device/process state while consumers are running and have later NVML/`nvidia-smi` calls observe those changes.

### 3.4 Failure simulation

Support deterministic injection of relevant failure conditions, including where feasible:

- NVML query failures
- Device unavailable/offline behavior
- CUDA OOM
- Model/server startup failure
- Model/server crash
- Load timeout/hang
- External VRAM pressure

### 3.5 Container and orchestration integration

Support CPU-only:

- Linux host usage
- Docker/Compose
- Kubernetes/Kind/CDI in a later phase

### 3.6 Full manager integration

Provide an optional fake `llama-server` so a real manager can exercise process supervision, load/unload, routing, eviction, and telemetry without requiring actual LLM inference.

## 4. Non-goals

V1 does not require:

- CUDA kernel execution.
- CPU emulation of NVIDIA SMs.
- PTX/SASS execution.
- Cycle-accurate GPU simulation.
- Accurate inference speed simulation.
- Real GGUF model execution.
- Making CUDA llama.cpp run real inference on a CPU-only machine.
- Full implementation of every NVML or CUDA function before a consumer needs it.

`fake-nvidia` must never be used as evidence for real GPU performance, capacity efficiency, thermal behavior, or inference throughput.

## 5. Upstream foundation

The preferred low-level foundation is NVIDIA [`k8s-test-infra`](https://github.com/NVIDIA/k8s-test-infra).

At specification time, upstream provides:

- A CGo-based Mock NVML `libnvidia-ml.so` with a broad NVIDIA-compatible export surface.
- A real `nvidia-smi` binary patched/configured to use Mock NVML.
- YAML and environment-based GPU configuration.
- `nvml-mock-ctl` runtime state overrides.
- Cross-process runtime override reload with locking/TTL semantics.
- Runtime process records visible to `nvidia-smi --query-compute-apps`.
- Public NVML per-process utilization support.
- Dynamic metrics and failure injection.
- Docker and Kubernetes examples.
- CDI/device-node injection infrastructure.
- An early-stage Mock CUDA library.

Important current limitation: upstream `nvidia-smi pmon` does not enumerate mocked processes because the real binary uses a separate internal NVIDIA entry point that is not mapped by Mock NVML. Closing this gap is a specific fake-nvidia compatibility task.

Important current CUDA limitation: upstream Mock CUDA exposes only a small early-stage API subset and is not sufficient for real CUDA workloads such as `vectorAdd`. The fake-nvidia specification intentionally does not require real kernel execution.

### 5.1 Dependency policy

- Pin the upstream revision/release used by each fake-nvidia release.
- Record upstream version/revision in the compatibility matrix.
- Prefer contributing generally useful fixes (especially `pmon`) upstream.
- Avoid maintaining a large permanent private fork when a wrapper or upstream patch is feasible.
- Preserve upstream licensing/notices.

## 6. Architecture

### 6.1 High-level design

```text
                 +------------------------------+
                 |          fake-nvidia         |
                 | profiles / config / scenarios|
                 | integration / test UX        |
                 +---------------+--------------+
                                 |
                  base config + runtime overrides
                                 |
                 +---------------v--------------+
                 | NVIDIA Mock NVML / ctl       |
                 | + targeted compatibility     |
                 |   extensions (e.g. pmon)     |
                 +------+---------------+--------+
                        |               |
                libnvidia-ml.so      nvidia-smi
                        |               |
                        +-------+-------+
                                |
                        normal consumer

          optional later surfaces:

            limited libcuda.so      fake llama-server
                    |                       |
                    +-----------+-----------+
                                |
                       full integration tests
```

### 6.2 No duplicate state daemon by default

Upstream already supplies a runtime override control plane. fake-nvidia must use it rather than immediately creating a second daemon, database, lock protocol, or state format.

A long-running fake-nvidia API/server may be introduced only if a concrete use case cannot be satisfied by upstream runtime overrides and CLI/scenario orchestration.

### 6.3 Proposed repository layout

The exact structure may evolve, but the intended responsibilities are:

```text
fake-nvidia/
├── cmd/
│   ├── fake-nvidia/              # setup/profile/scenario UX
│   ├── fake-nvidia-ctl/          # optional dedicated control UX
│   └── fake-llama-server/        # optional Phase 7 binary
├── internal/
│   ├── config/                   # fake-nvidia config/profile composition
│   ├── upstream/                 # pinned Mock NVML/CUDA integration helpers
│   ├── control/                  # wrapper around upstream runtime overrides
│   ├── scenario/                 # deterministic scenario execution
│   └── integration/              # Docker/LCM/K8s helpers
├── profiles/
├── runtime/                       # injection/runtime assets
├── examples/
│   └── llamacpp-manager/
├── deploy/
│   └── kubernetes/
├── tests/
│   ├── compatibility/
│   └── e2e/
├── README.md
├── SPEC.md
└── AGENTS.md
```

### 6.4 Implementation language

Go is the required implementation language for fake-nvidia.

Use Go for:

- `fake-nvidia` CLI and subcommands
- profile/config generation and validation
- runtime override/control wrappers
- scenario orchestration
- Docker/Compose/Kubernetes integration helpers
- compatibility/E2E harnesses
- the optional `fake-llama-server`
- any long-running control service if one is ever justified

C or CGo may be used only at a native ABI boundary that cannot reasonably be implemented in pure Go, such as exporting NVIDIA-compatible `libnvidia-ml.so` / `libcuda.so` symbols or integrating upstream native mock components. Keep that native layer narrow and expose Go-owned policy/state/orchestration above it.

Do not introduce Python, Rust, Node.js, or another language for core project functionality without an explicit spec change and issue explaining why Go cannot satisfy the requirement.

The repository should use a Go module at its root. The minimum Go version is pinned in `go.mod` when Phase 0/1 implementation begins and is updated deliberately through PRs.

## 7. Configuration and profiles

### 7.1 Base configuration

fake-nvidia must generate or compose upstream-compatible Mock NVML YAML rather than create an unrelated low-level format.

A higher-level fake-nvidia config may provide concise shortcuts, but generated effective state must be inspectable.

Example conceptual configuration:

```yaml
system:
  driver_version: "580.173.02"
  cuda_version: "13.0"

gpus:
  - profile: rtx4060ti-16gb
  - profile: rtx4060ti-16gb
```

### 7.2 Profiles

Initial profiles/topologies should include:

- RTX 4060 Ti 16 GiB
- RTX 4090 24 GiB
- T4 16 GiB
- L40S 48 GiB
- A100 40 GiB
- H100 80 GiB
- 2x RTX 4060 Ti 16 GiB
- 4x RTX 4090 24 GiB
- Mixed-size/model topology

Reuse upstream profiles for cards already modeled upstream. fake-nvidia-specific profiles should be configuration files, not hard-coded branches in application logic.

Profiles model identity/capacity metadata, not real-world performance.

## 8. Runtime state and control

### 8.1 Source of truth

Use the upstream Mock NVML base config plus runtime override mechanism as the effective low-level state.

### 8.2 Runtime mutability

Where upstream permits runtime overrides, fake-nvidia should expose concise commands for:

- Memory used/free/reserved
- GPU utilization
- Memory utilization
- Process list
- Per-process memory/utilization
- Supported failures
- Reset/clear overrides

Identity/topology fields that upstream intentionally constructs only at initialization may require environment restart. fake-nvidia should expose/document this boundary rather than simulate live hot-plug incorrectly.

### 8.3 Scenario runner

A declarative scenario may contain:

- Base profile
- Initial overrides
- Timed actions
- Triggered actions
- Cleanup/reset behavior
- Optional assertions

Prefer event-driven triggers to arbitrary sleeps. Example triggers may include:

- after process starts
- after health endpoint is ready
- after a command succeeds
- after a named test checkpoint is signaled

## 9. Required NVIDIA compatibility surfaces

### 9.1 GPU discovery

Must support the exact current LlamaCPP-Manager command:

```bash
nvidia-smi \
  --query-gpu=index,uuid,name,memory.total,memory.used,utilization.gpu \
  --format=csv,noheader,nounits
```

Also target:

- `nvidia-smi`
- `nvidia-smi -L`
- `nvidia-smi -q`
- Relevant CSV query combinations supported by upstream

### 9.2 Process accounting

Must support:

```bash
nvidia-smi \
  --query-compute-apps=pid,gpu_uuid,used_memory,process_name \
  --format=csv,noheader,nounits
```

Use upstream process-record support and runtime overrides.

### 9.3 Process utilization / pmon

Must support the current LlamaCPP-Manager fallback sequence:

```bash
nvidia-smi pmon -c 1 -s u
nvidia-smi pmon -c 1
```

Preferred implementation order:

1. Identify and implement the missing upstream Mock NVML/internal export needed by the real `nvidia-smi` binary.
2. Contribute the generic fix upstream if practical.
3. Only if the internal ABI is not safely supportable, introduce a narrowly scoped `pmon` compatibility wrapper while leaving all other `nvidia-smi` behavior on the upstream real binary.

Do not replace all of `nvidia-smi` solely to solve `pmon`.

## 10. Docker/runtime injection

fake-nvidia must provide a reproducible way to expose its NVIDIA userspace/device surface inside consumer containers.

Expected injected assets may include:

- `libnvidia-ml.so` symlink chain
- Mock-NVML-backed `nvidia-smi`
- Optional `libcuda.so` shim
- NVIDIA-style device nodes/placeholders where required
- Base configuration
- Runtime override/control files

Requirements:

- Do not modify host NVIDIA driver files.
- Keep injection scoped to the test environment/container.
- Work on CPU-only Linux.
- Document behavior on a host that also has a real NVIDIA driver.
- LlamaCPP-Manager integration must require deployment configuration only, not manager source changes.

## 11. Limited CUDA shim

### 11.1 Purpose

The CUDA shim exists to support device discovery, capability checks, memory accounting, and deterministic error paths—not compute.

### 11.2 Priority API behavior

Prioritize APIs needed for:

- Initialization
- Device count/enumeration
- Device properties
- Get/set current device
- Memory info
- Allocation/free accounting
- CUDA OOM/error injection

### 11.3 Shared accounting

Simulated CUDA allocations must update the same effective memory state observed through NVML/`nvidia-smi`.

### 11.4 Unsupported operations

Kernel/module/PTX execution APIs should return a clear documented unsupported/error result unless a future phase implements semantically correct behavior.

No-op success for compute APIs is unacceptable when it could falsely indicate that a workload executed.

## 12. Optional fake llama-server

The fake server is a test companion, not an NVIDIA driver component.

It should:

- Run as a normal OS process with a real PID.
- Accept the subset of llama-server CLI options needed by integration tests.
- Register itself as a fake GPU process.
- Reserve/release simulated VRAM.
- Support configurable load delay and resource requirements.
- Expose health/readiness behavior used by the manager.
- Expose minimal OpenAI-compatible inference endpoints.
- Support streaming responses.
- Emit deterministic fake output.
- Release state on termination.

Supported deterministic failures should include:

- startup failure
- startup timeout
- simulated CUDA OOM
- crash after ready
- shutdown hang
- sudden VRAM growth

It must not parse/execute GGUF models or claim llama.cpp-equivalent performance.

## 13. LlamaCPP-Manager E2E matrix

The E2E suite should cover at least:

### Discovery
- no GPU
- 1 GPU
- multiple GPUs
- mixed GPUs
- GPU unavailable/failure

### Capacity/placement
- model fits one card
- model requires multiple cards
- external VRAM usage
- insufficient aggregate VRAM
- state changes during launch planning

### Lifecycle/eviction
- load A then B
- evict A before loading B
- protected/non-evictable workload behavior
- stop/crash releases VRAM
- external process creates pressure

### Telemetry
- per-process VRAM
- process utilization
- correct GPU/process association
- live update visibility

### Failure handling
- OOM
- startup timeout
- NVML failure
- GPU failure/offline
- fake server crash

Tests must run against an unmodified manager revision and record that revision in CI/release metadata.

## 14. Kubernetes/CDI

Kubernetes support is a later integration phase and must reuse the same profiles/state semantics as Docker/local execution.

Target:

- Kind/CPU-only CI
- CDI injection
- NVIDIA-style device nodes
- Mock NVML/CUDA injection
- Optional compatibility with device-plugin/GPU Operator test environments

Do not create a separate Kubernetes-only simulation engine.

## 15. Testing strategy

### 15.1 Layers

1. Unit tests for config/profile/scenario logic.
2. Upstream compatibility tests for pinned Mock NVML revision.
3. Command-level golden/contract tests for exact `nvidia-smi` invocations.
4. Shared-library/CUDA shim tests.
5. Docker injection tests.
6. fake llama-server lifecycle tests.
7. LlamaCPP-Manager E2E tests.
8. Kubernetes/Kind tests after Phase 9.

### 15.2 CPU-only requirement

All fake-nvidia correctness tests must be runnable without physical NVIDIA hardware. Optional comparison tests against real hardware may exist separately, but must never be the only validation.

### 15.3 Compatibility over cosmetic output

Match output fields/semantics consumed by target software. Avoid brittle golden tests for unrelated whitespace or fields unless exact formatting is itself part of compatibility.

## 16. Safety and isolation

- Never overwrite host driver libraries.
- Never create persistent host device/runtime modifications outside an explicitly configured test root.
- Clean up temporary device nodes, runtime state, and containers on teardown.
- Make it obvious when a process is using fake-nvidia.
- Do not expose the control surface broadly by default.
- Test setup must be safe to run on hosts that also contain real NVIDIA hardware.

## 17. Versioning and compatibility

Every release should record:

- fake-nvidia version
- pinned NVIDIA `k8s-test-infra` revision/release
- Mock NVML version/revision
- Mock CUDA version/revision
- bundled/reported driver and CUDA versions
- tested LlamaCPP-Manager revision
- supported profiles
- known compatibility gaps

## 18. Git and contribution policy

**Never push directly to `main`.**

- Work from dedicated branches.
- Open a PR for every change intended for `main`.
- Keep issues/phases independently reviewable where practical.
- Do not bypass CI.
- Do not mix unrelated implementation phases in one PR unless explicitly justified.

See `AGENTS.md` for execution rules.

## 19. Roadmap / issue mapping

| Phase | Issue | Deliverable |
|---|---|---|
| 0 | #1 | Bootstrap docs/repository workflow |
| 1 | #2 | Profiles + upstream runtime-state integration |
| 2 | #3 | Mock NVML + GPU discovery |
| 3 | #4 | Process accounting + `pmon` |
| 4 | #5 | Control/scenario UX |
| 5 | #6 | Docker/runtime injection |
| 6 | #7 | Limited CUDA shim |
| 7 | #8 | fake llama-server |
| 8 | #9 | LlamaCPP-Manager E2E suite |
| 9 | #10 | Kubernetes/CDI integration |
| 10 | #11 | CI/releases/compatibility matrix |

## 20. Open technical decisions

These should be resolved with evidence during the relevant issue rather than guessed during bootstrap:

1. Exact method for consuming/pinning upstream `k8s-test-infra` (source dependency, subtree/vendor, build artifact, or other reproducible approach).
2. Exact missing internal ABI/export required for real `nvidia-smi pmon` and whether it is appropriate for upstream contribution.
3. Whether a long-running fake-nvidia control service is ever required beyond upstream runtime overrides.
4. Exact CUDA Driver/Runtime API subset required by real target consumers.
5. Exact fake llama-server argument/HTTP compatibility subset required by current manager code.
6. Project license selection and resulting packaging/notices.
