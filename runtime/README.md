# Phase 2 runtime bundle

Phase 2 assembles the NVIDIA-facing userspace surface needed for CPU-only discovery tests without installing or overwriting host NVIDIA drivers.

## Build

```bash
make runtime
```

The build runs in Docker and produces a local, ignored `.runtime/` tree:

```text
.runtime/
├── bin/
│   ├── nvidia-smi
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
```

The bundled `nvidia-smi` is NVIDIA's real userspace utility from the pinned package. It loads the pinned upstream Mock NVML shared library; fake-nvidia does not replace its normal command parser or output renderer.

Runtime changes continue to use upstream `nvml-mock-ctl`. New consumer processes see the override immediately when they start, while already-running Mock NVML consumers refresh the shared override file on upstream's short TTL.

## Verification

```bash
make phase2
```

This builds the runtime and runs the command-level compatibility suite against the real `nvidia-smi` / Mock NVML pair. Tests cover single, multiple, and mixed GPU discovery; baseline `nvidia-smi`, `-L`, and `-q`; LlamaCPP-Manager's current enriched discovery query with its six-field fallback; and live memory/utilization mutation.

## Safety and phase boundary

- No host NVIDIA library or binary is replaced.
- A physical NVIDIA GPU is not required.
- Generated artifacts stay under `.runtime/` unless an explicit output directory is supplied.
- This is a local/CI runtime bundle. Transparent Docker/Compose consumer injection is Phase 5 and is deliberately not implemented here.
