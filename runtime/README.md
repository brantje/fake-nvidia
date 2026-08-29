# fake-nvidia runtime bundle

The runtime bundle assembles the NVIDIA-facing userspace surface and companion test binaries needed for CPU-only GPU-aware integration tests without installing or overwriting host NVIDIA drivers.

## Build

```bash
make runtime
```

The build runs in Docker and produces a local, ignored `.runtime/` tree:

```text
.runtime/
├── bin/
│   ├── fake-llama-server
│   ├── nvidia-smi
│   ├── nvidia-smi.real
│   └── nvml-mock-ctl
├── lib/
│   ├── libcuda.so
│   ├── libcuda.so.1
│   ├── libcudart.so
│   ├── libcudart.so.12
│   ├── libcudart.so.13
│   ├── libnvidia-ml.so
│   ├── libnvidia-ml.so.1
│   └── libnvidia-ml.so.<version>
└── licenses/
```

The native build contract is recorded in `runtime/pins.env`: the exact NVIDIA `k8s-test-infra` revision, upstream build Go version, immutable Docker base-image digests, dated Debian snapshot, exact top-level Debian package versions, expected NVIDIA repository-key fingerprint, `nvidia-utils` package version, and architecture-specific SHA-256 hashes for the downloaded `nvidia-utils` package bytes. The build rejects a downloaded NVIDIA signing key whose full fingerprint does not match the pin before trusting the repository, and verifies the downloaded `.deb` with its architecture-specific SHA-256 before extraction.

## Use with a generated profile

```bash
go run ./cmd/fake-nvidia render \
  --profile rtx4090-24gb \
  --output .runtime/config/config.yaml

export PATH="$PWD/.runtime/bin:$PATH"
export LD_LIBRARY_PATH="$PWD/.runtime/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export MOCK_NVML_CONFIG="$PWD/.runtime/config/config.yaml"
export MOCK_NVML_OVERRIDES="$PWD/.runtime/config/overrides.yaml"

nvidia-smi -L
nvidia-smi --query-gpu=index,uuid,name,memory.total,memory.used,utilization.gpu \
  --format=csv,noheader,nounits
nvidia-smi --query-compute-apps=pid,gpu_uuid,used_memory,process_name \
  --format=csv,noheader,nounits
nvidia-smi pmon -c 1 -s u
```

## NVIDIA-SMI dispatch boundary

`nvidia-smi.real` is NVIDIA's real userspace utility from the pinned official package. It loads the pinned upstream Mock NVML shared library and remains responsible for discovery, `-L`, `-q`, CSV queries, compute-process enumeration, and every other ordinary NVIDIA-SMI command.

The `nvidia-smi` entry point is a small Go dispatcher added for Phase 3. It intercepts only these one-shot process-utilization forms used by LlamaCPP-Manager:

```bash
nvidia-smi pmon -c 1 -s u
nvidia-smi pmon -c 1
```

Those rows are populated from upstream Mock NVML's public `nvmlDeviceGetProcessUtilization` API. The pinned upstream revision documents that the real NVIDIA `pmon` path uses a separate private/internal entry point which the mock does not map. Fake-nvidia does not guess that private ABI and does not replace the rest of NVIDIA-SMI.

Any other arguments are forwarded unchanged to the sibling `nvidia-smi.real` process with the same environment, stdio, and exit status.

Runtime changes continue to use upstream `nvml-mock-ctl`. New consumer processes see the override immediately when they start, while already-running Mock NVML consumers refresh the shared override file on upstream's short TTL.

## Process and memory state

Process records can be supplied in the generated base configuration or changed at runtime with the existing upstream-backed `SetProcesses` control method. Each record can include PID, name, used VRAM, SM utilization, memory utilization, encoder utilization, and decoder utilization.

Upstream intentionally treats process records and device memory counters as independent configuration fields. Higher-level fake-nvidia scenarios that own process memory use the reconciled process path: it updates the process list together with `memory.used_bytes` and `memory.free_bytes` through the upstream override machinery. Explicit non-process/system usage is preserved, and removing a fake process releases only the process-owned portion.

Phase 7's `fake-llama-server` uses that same reconciliation path. Its actual OS PID and simulated VRAM therefore appear together in `--query-compute-apps`, `pmon`, and device memory usage. It does not maintain a second reservation database.

## Fake llama-server

`fake-llama-server` is a Go companion process for full manager lifecycle tests. It accepts the manager-owned `--model`, `--host`, and `--port` arguments, exposes `GET /health`, implements minimal OpenAI-compatible completion endpoints, and supports deterministic streaming.

It can simulate model-load delay, startup failure, CUDA OOM, a crash after readiness, shutdown hang, and sudden VRAM growth. It does not parse or execute GGUF files and must not be used for performance testing.

See [`../FAKE_LLAMA_SERVER.md`](../FAKE_LLAMA_SERVER.md) for the resource model, GPU selection rules, HTTP contract, failure controls, and LlamaCPP-Manager substitution instructions.

## Verification

For the full Phase 7 surface:

```bash
make phase7
```

The CPU-only compatibility suite verifies discovery, process telemetry, the limited CUDA surface, runtime/injection behavior, and the packaged fake llama-server. Phase 7 specifically verifies that the child process's real PID and process-owned VRAM appear through NVIDIA-SMI, health/inference/streaming work, termination cleans shared state, and injected OOM leaves that state unchanged.

Earlier phase targets remain available for their documented compatibility workflows.

## Safety and phase boundary

- No host NVIDIA library or binary is replaced.
- A physical NVIDIA GPU is not required.
- Generated artifacts stay under `.runtime/` unless an explicit output directory is supplied.
- The `pmon` dispatcher is intentionally narrow; unsupported or unrelated `pmon` modes are delegated to the real NVIDIA binary rather than silently emulated.
- `fake-llama-server` is a test companion and never claims to perform real llama.cpp inference.
- Runtime/container injection remains scoped to explicitly prepared fake-nvidia test environments.
