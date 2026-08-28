# fake-nvidia runtime bundle

The runtime bundle assembles the NVIDIA-facing userspace surface needed for CPU-only discovery and process-telemetry tests without installing or overwriting host NVIDIA drivers.

## Build

```bash
make runtime
```

The build runs in Docker and produces a local, ignored `.runtime/` tree:

```text
.runtime/
├── bin/
│   ├── nvidia-smi
│   ├── nvidia-smi.real
│   └── nvml-mock-ctl
├── lib/
│   ├── libnvidia-ml.so
│   ├── libnvidia-ml.so.1
│   └── libnvidia-ml.so.<version>
└── licenses/
```

The native build contract is recorded in `runtime/pins.env`: the exact NVIDIA `k8s-test-infra` revision, upstream build Go version, immutable Docker base-image digests, dated Debian snapshot, exact top-level Debian package versions, expected NVIDIA repository-key fingerprint, and `nvidia-utils` package version. The build rejects a downloaded NVIDIA signing key whose full fingerprint does not match the pin before trusting the repository.

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

Upstream intentionally treats process records and device memory counters as independent configuration fields. Higher-level fake-nvidia scenarios that own process memory should use `SetProcessesReconciled`: it writes the new process list together with `memory.used_bytes` and `memory.free_bytes` in one `nvml-mock-ctl set` transaction. Explicit non-process/system usage is preserved, and removing the process list releases only the process-owned portion.

## Verification

```bash
make phase3
```

The native compatibility suite covers both Phase 2 discovery and Phase 3 process telemetry. It verifies single/multiple/mixed GPU discovery; baseline `nvidia-smi`, `-L`, and `-q`; LlamaCPP-Manager's discovery flow; the exact compute-app query; both supported `pmon` forms; multiple processes; one PID on multiple GPUs; runtime process changes; empty process state; non-`pmon` delegation to the real NVIDIA binary; and reconciled process-owned VRAM accounting.

`make phase2` remains as a backward-compatible alias for the same native suite.

## Safety and phase boundary

- No host NVIDIA library or binary is replaced.
- A physical NVIDIA GPU is not required.
- Generated artifacts stay under `.runtime/` unless an explicit output directory is supplied.
- The `pmon` dispatcher is intentionally narrow; unsupported or unrelated `pmon` modes are delegated to the real NVIDIA binary rather than silently emulated.
- This is a local/CI runtime bundle. Transparent Docker/Compose consumer injection is Phase 5 and is deliberately not implemented here.
