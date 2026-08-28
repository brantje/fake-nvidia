# Upstream Mock NVML pin

Phase 1-3 target NVIDIA `k8s-test-infra` at:

- Repository: `NVIDIA/k8s-test-infra`
- Revision: `f7bbbf025110c63c04567cf42e357af32fa2f62d`
- Mock NVML: `pkg/gpu/mocknvml`
- Configuration reference: `docs/configuration.md`
- Runtime control reference: `docs/nvml-mock-ctl.md`
- Upstream Mock NVML builder Go version: `1.26.6`
- `nvidia-smi` package: `nvidia-utils-580=580.65.06-0ubuntu1`

The native build pins live in `runtime/pins.env` and are mirrored by constants in
`internal/upstream`. A Go test fails if those two contracts drift apart.

## Runtime artifact strategy

`make runtime` assembles the NVIDIA-facing userspace surface from official pinned
inputs rather than checking generated binaries or a private NVML fork into this
repository.

The build:

1. Fetches the exact `NVIDIA/k8s-test-infra` revision above.
2. Builds upstream `pkg/gpu/mocknvml` with its own Makefile, preserving the
   `libnvidia-ml.so` SONAME/symlink chain and versioned NVML symbol aliases.
3. Builds the matching upstream `nvml-mock-ctl` command.
4. Downloads `nvidia-utils-580=580.65.06-0ubuntu1` from NVIDIA's official CUDA
   Ubuntu repository and extracts the real `nvidia-smi` binary, following the
   strategy in the pinned upstream `deployments/nvml-mock/Dockerfile`.
5. Stores that untouched utility as `nvidia-smi.real` and patches it with an
   origin-relative RPATH so the local bundle finds
   `.runtime/lib/libnvidia-ml.so.1` without modifying host driver paths.
6. Builds a small Go `nvidia-smi` dispatcher. It intercepts only the supported
   Phase 3 one-shot `pmon` forms; every other invocation is delegated unchanged
   to `nvidia-smi.real`.

The pinned Mock NVML Makefile currently names its versioned shared library
`libnvidia-ml.so.550.163.01`; the real `nvidia-smi` binary comes from the 580
package above. This is intentional upstream compatibility behavior: the dynamic
loader uses the `libnvidia-ml.so.1` SONAME, while the driver version reported to
applications comes from the generated Mock NVML configuration.

Generated artifacts are ignored under `.runtime/`. Third-party licensing notes
are recorded in `runtime/THIRD_PARTY_NOTICES.md`; the upstream Apache-2.0 license
is copied into every generated runtime bundle.

## Why Phase 3 wraps only `pmon`

The pinned upstream changelog explicitly documents that configured `processes:`
already surface through normal `nvidia-smi`, `-q`, and
`--query-compute-apps`, and that runtime process overrides are supported. The
same revision implements the public `nvmlDeviceGetProcessUtilization` API with
SM, memory, encoder, and decoder utilization per configured PID.

The remaining upstream gap is specific to `nvidia-smi pmon`: NVIDIA's utility
uses a separate private/internal entry point that Mock NVML does not map. That
private ABI is not a stable public NVML contract and the pinned upstream source
does not define the missing interface well enough for fake-nvidia to safely
invent it.

Phase 3 therefore uses the narrow fallback allowed by the project specification:
`nvidia-smi pmon -c 1 -s u` and `nvidia-smi pmon -c 1` are rendered from the
public `nvmlDeviceGetProcessUtilization` surface that upstream already owns. The
wrapper does not implement discovery, `-q`, CSV queries, process enumeration, or
other NVIDIA-SMI behavior; those continue through the real NVIDIA binary.

This keeps the compatibility workaround isolated and makes it removable if
upstream later maps the private `pmon` entry point.

## Profile reuse

The built-in T4, L40S, A100 40 GB, and H100 80 GB profile descriptors are marked
as upstream-backed and record the corresponding upstream configuration path.
`fake-nvidia` only keeps the identity/capacity subset needed to compose a mixed
or resized configuration; it does not copy the full upstream profiles.

The RTX 4060 Ti 16 GB and RTX 4090 24 GB descriptors are fake-nvidia additions
because the pinned upstream profile directory does not contain those cards.
These descriptors model identity/capacity for tests, not hardware performance.

## Runtime state contract

`fake-nvidia` does not implement a second state daemon or override-file lock.
`internal/control` invokes the pinned `nvml-mock-ctl` command and lets upstream
perform schema validation, locking, atomic writes, and reload behavior.

The upstream precedence contract is:

```text
base config.yaml < overrides all: < overrides devices[<index>]:
```

The pinned implementation reloads the override file in consumer processes on a
short TTL (1 second by default). Memory values are not auto-reconciled by
upstream. The low-level `SetProcesses` method intentionally preserves upstream
semantics, while fake-nvidia's `SetProcessesReconciled` helper writes process
records plus `memory.used_bytes` and `memory.free_bytes` in one upstream `set`
transaction. This lets higher-level fake-nvidia scenarios account for explicit
non-process/system usage and release process-owned VRAM when a fake process is
removed without introducing another state store.

Runtime-mutable fields include memory, utilization, process records, temperature,
power, and failure state. Identity/topology fields including name, architecture,
brand, compute capability, UUID, PCI bus ID, and BAR1 state are construction-time
state and require an environment restart when changed.

## Compatibility validation

CPU-only CI builds the native bundle and drives the bundled NVIDIA-facing
surface against configurations generated by fake-nvidia. The suite covers:

- single, multiple, and mixed GPU discovery;
- baseline `nvidia-smi`, `-L`, and `-q` behavior through `nvidia-smi.real`;
- LlamaCPP-Manager's GPU discovery query and fallback;
- exact `--query-compute-apps=pid,gpu_uuid,used_memory,process_name` behavior;
- both LlamaCPP-Manager `pmon` command forms;
- multiple processes on one GPU and one PID on multiple GPUs;
- runtime process mutation and empty process lists;
- reconciled process-owned VRAM accounting.

Run the native checks locally with:

```bash
make phase2
```

Transparent Docker/Compose injection into an unrelated consumer container stays
in Phase 5. Phase 3 extends the local NVIDIA userspace compatibility surface; it
does not change the deployment boundary.
