# Upstream Mock NVML pin

Phase 1 targets NVIDIA `k8s-test-infra` at:

- Repository: `NVIDIA/k8s-test-infra`
- Revision: `f7bbbf025110c63c04567cf42e357af32fa2f62d`
- Mock NVML: `pkg/gpu/mocknvml`
- Configuration reference: `docs/configuration.md`
- Runtime control reference: `docs/nvml-mock-ctl.md`

The revision is intentionally recorded in Go (`internal/upstream`) as well as
here so later build/package phases can consume the exact same source revision.

## Profile reuse

The built-in T4, L40S, A100 40 GB, and H100 80 GB profile descriptors are marked
as upstream-backed and record the corresponding upstream configuration path.
`fake-nvidia` only keeps the identity/capacity subset needed to compose a mixed
or resized Phase 1 configuration; it does not copy the full upstream profiles.

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
upstream, so fake-nvidia's memory helper always writes used and free together.

Runtime-mutable Phase 1 fields include memory, utilization, process records,
temperature, power, and failure state. Identity/topology fields including name,
architecture, brand, compute capability, UUID, PCI bus ID, and BAR1 state are
construction-time state and require an environment restart when changed.

## Phase boundary

Phase 1 generates upstream-compatible configuration and supplies the Go control
orchestration layer. Phase 2 packages/builds the pinned Mock NVML shared library
and `nvidia-smi` surface and adds command-level compatibility tests against those
artifacts. The Phase 1 control tests therefore verify that mutations are sent to
upstream rather than reimplementing its lock/merge protocol locally.
