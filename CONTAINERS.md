# Container/runtime injection

Phase 5 packages the existing Mock NVML runtime for transparent Linux container consumers. The consumer application does not need fake-nvidia-specific source code or a fake-device branch: deployment configuration overlays the NVIDIA-facing binaries/libraries and points them at generated state.

## Prerequisites

- Linux with Docker Engine / Docker Compose.
- No physical NVIDIA GPU is required.
- No NVIDIA Container Toolkit is required.
- Build the pinned runtime once with `make runtime`.

The runtime is built from the pinned NVIDIA `k8s-test-infra` Mock NVML implementation documented in `UPSTREAM.md`. Phase 5 follows the same core deployment idea as upstream's `deployments/nvml-mock`: inject the mock NVIDIA userspace surface into an isolated container rather than replacing host driver files.

## Prepare an injection root

```bash
make runtime
go run ./cmd/fake-nvidia up --gpus 2 --profile rtx4060ti-16gb
export FAKE_NVIDIA_ROOT="$(pwd)/.fake-nvidia"
```

`fake-nvidia up` creates an isolated root with this shape:

```text
.fake-nvidia/
  .fake-nvidia-injection
  runtime/
    bin/nvidia-smi
    bin/nvidia-smi.real
    bin/nvml-mock-ctl
    lib/libnvidia-ml.so -> ...
    lib/libnvidia-ml.so.1 -> ...
    ...
  state/
    config.yaml
```

The runtime is copied instead of modified in place. `state/overrides.yaml` is created by the upstream control mechanism when a runtime override is first written.

You can also prepare a named topology:

```bash
go run ./cmd/fake-nvidia up --topology dual-rtx4060ti-16gb
```

Use `--runtime-dir` and `--root` when the runtime or injection root lives elsewhere. `FAKE_NVIDIA_RUNTIME_DIR` is honored as the default runtime directory.

## CPU-only Compose smoke test

The repository contains a complete CPU-only consumer plus the reusable injection override:

```bash
docker compose \
  -f examples/docker/compose.yaml \
  -f examples/docker/compose.override.yaml \
  run --rm consumer
```

Expected output contains one `GPU N:` line per configured fake GPU. The override mounts only:

- the `nvidia-smi` compatibility wrapper and its untouched `nvidia-smi.real` delegate into `/usr/local/bin`;
- `nvml-mock-ctl` for optional in-container inspection/control;
- the fake NVIDIA library directory at `/opt/fake-nvidia/lib`;
- the generated state directory at `/var/lib/fake-nvidia`.

It sets:

```text
LD_LIBRARY_PATH=/opt/fake-nvidia/lib
MOCK_NVML_CONFIG=/var/lib/fake-nvidia/config.yaml
MOCK_NVML_OVERRIDES=/var/lib/fake-nvidia/overrides.yaml
```

For another Compose stack, copy `examples/docker/compose.override.yaml`, rename the `consumer` service to the target service, and keep the same mounts/environment. This is deployment-only configuration; the application itself remains unchanged.

If the target image already has a custom `LD_LIBRARY_PATH`, append its existing directories after `/opt/fake-nvidia/lib` rather than discarding them.

## LlamaCPP-Manager

`examples/llamacpp-manager/compose.fake-nvidia.yaml` targets the current `backend` service in `brantje/llamacpp-manager/docker-compose.yml`.

From a LlamaCPP-Manager checkout, point Compose at the override in this repository (or copy it beside the manager compose file) and make `FAKE_NVIDIA_ROOT` absolute:

```bash
export FAKE_NVIDIA_ROOT=/absolute/path/to/fake-nvidia/.fake-nvidia

docker compose \
  -f docker-compose.yml \
  -f /absolute/path/to/fake-nvidia/examples/llamacpp-manager/compose.fake-nvidia.yaml \
  up --build
```

Do **not** include LlamaCPP-Manager's `docker-compose.nvidia.yml` for this test. That file requests real NVIDIA runtime resources, while this Phase 5 path is intentionally CPU-only.

Inside the backend container, the existing manager discovery command runs unchanged:

```bash
nvidia-smi \
  --query-gpu=index,uuid,name,memory.total,memory.used,utilization.gpu \
  --format=csv,noheader,nounits
```

Phase 5 proves discovery/telemetry injection. It does not make a real `llama-server` execute CUDA kernels on fake hardware; the limited CUDA surface belongs to Phase 6 and the fake server belongs to Phase 7.

## Runtime control while injected

There is no fake-nvidia state daemon or control socket in Phase 5. Runtime mutation continues to use upstream Mock NVML's file-backed override state from Phase 4. The consumer sees the shared `state` mount, so host-side control can target the prepared runtime/state explicitly:

```bash
FAKE_NVIDIA_RUNTIME_DIR="$FAKE_NVIDIA_ROOT/runtime" \
MOCK_NVML_CONFIG="$FAKE_NVIDIA_ROOT/state/config.yaml" \
MOCK_NVML_OVERRIDES="$FAKE_NVIDIA_ROOT/state/overrides.yaml" \
go run ./cmd/fake-nvidia ctl device 0 utilization --gpu 80 --memory 25
```

Subsequent `nvidia-smi` calls in the consumer observe the override through the shared state mount.

## Teardown and host safety

```bash
go run ./cmd/fake-nvidia down
```

`down` removes only a directory containing fake-nvidia's exact ownership marker. It refuses to replace/remove an unmarked path, refuses the filesystem root, and refuses injection/runtime paths that overlap. The source runtime bundle is never removed by `down`.

Phase 5 does not:

- overwrite `/usr/lib`, `/usr/bin`, or NVIDIA files on the host;
- create or modify host `/dev/nvidia*` device nodes;
- automatically expose a real host GPU to the consumer;
- silently combine real and fake NVIDIA devices.

On a host that has real NVIDIA drivers, the fake surface exists only in containers where the explicit override is applied. Do not combine this override with real NVIDIA device/runtime injection unless a future compatibility mode explicitly defines and tests that behavior.

Regular-file `/dev/nvidia*` placeholders are intentionally not created: current NVML/`nvidia-smi`/LlamaCPP-Manager discovery does not require them, and pretending that a regular file is a usable NVIDIA character device would be misleading. A later phase may add a narrowly tested device surface if a real consumer requires it.

## CI coverage

`make phase5` builds the pinned runtime, runs the existing native compatibility suite, and executes a Docker integration test that:

- starts a plain Debian consumer with no GPU/device/runtime flags;
- verifies `/dev/nvidiactl` is absent;
- runs `nvidia-smi -L` with two fake GPUs;
- runs the exact LlamaCPP-Manager discovery command and verifies two rows;
- tears the injection root down;
- verifies the source runtime artifacts are byte-for-byte unchanged.
