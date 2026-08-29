# Phase 4 control and scenarios

`fake-nvidia-ctl` is the Go control/scenario UX for a running fake-nvidia environment.
It does **not** introduce a daemon or a second state format.

- Effective state is read through the bundled `nvidia-smi`, so `list` shows what an ordinary consumer sees.
- Every mutation is translated to the pinned upstream `nvml-mock-ctl` runtime override mechanism.
- The upstream override file, file lock, atomic writes, merge precedence, and reload TTL remain authoritative.
- Identity/topology changes are still construction-time state. Render a scenario base before starting consumers when a different topology is required.

## Environment

The command uses the standard runtime environment when available:

```bash
export MOCK_NVML_CONFIG=/path/to/config.yaml
export MOCK_NVML_OVERRIDES=/path/to/overrides.yaml
export PATH=/path/to/fake-nvidia-runtime/bin:$PATH
export LD_LIBRARY_PATH=/path/to/fake-nvidia-runtime/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}
```

Paths can also be provided explicitly:

```bash
fake-nvidia-ctl \
  --ctl-bin /runtime/bin/nvml-mock-ctl \
  --nvidia-smi-bin /runtime/bin/nvidia-smi \
  --config /state/config.yaml \
  --overrides /state/overrides.yaml \
  list
```

`--config` and `--overrides` are also applied to effective-state `nvidia-smi` reads.

## Effective state

```bash
fake-nvidia-ctl list
fake-nvidia-ctl status
fake-nvidia-ctl status 0
```

`list` emits JSON containing GPU identity, total/used/free memory, GPU/memory utilization, and process rows. Process utilization is enriched from the Phase 3 one-shot `pmon` compatibility path when available.

`status` is intentionally different: it exposes the active upstream override document, not the merged effective state.

## GPU mutations

```bash
fake-nvidia-ctl gpu 0 utilization 90
fake-nvidia-ctl gpu 0 memory-utilization 35
fake-nvidia-ctl gpu 0 memory used=12GiB
fake-nvidia-ctl gpu 0 memory reserve 2GiB
fake-nvidia-ctl gpu 0 memory release 1GiB
```

Memory operations first read the effective device state and then update `memory.used_bytes` and `memory.free_bytes` in one upstream `set` transaction. The currently visible used+free pool is preserved so profile-level reserved memory is not accidentally erased.

`release` will not reduce device usage below process-owned VRAM reported by the process table. Process mutations preserve existing non-process/system usage.

Supported size suffixes are `B`, `KiB`, `MiB`, `GiB`, `KB`, `MB`, and `GB`; a bare integer is interpreted as bytes.

## Processes

```bash
fake-nvidia-ctl process add \
  --pid 1234 --gpu 0 --memory 8GiB --name llama-server --sm 70 --mem-util 25

fake-nvidia-ctl process update \
  --pid 1234 --gpu 0 --memory 10GiB --sm 85

fake-nvidia-ctl process remove --pid 1234 --gpu 0
```

A process update preserves fields that were not supplied. Add/update/remove reconciles the process list and device used/free VRAM in one upstream override transaction using the Phase 3 accounting helper.

One PID can still be represented on multiple GPUs by adding one row on each GPU.

## Failure/offline state

```bash
fake-nvidia-ctl gpu 0 failure lost
fake-nvidia-ctl gpu 0 failure fallen_off_bus --after-calls 5 --xid 79
fake-nvidia-ctl gpu 0 failure ecc_uncorrectable --xid 48
fake-nvidia-ctl gpu 0 offline
fake-nvidia-ctl gpu 0 online
```

`offline` is an ergonomic alias for upstream `lost`; `online` clears the failure using upstream `healthy` semantics.

CUDA OOM/failure operations are intentionally not exposed yet. They belong to Phase 6 once the CUDA shim exists.

## Reset

```bash
fake-nvidia-ctl reset
fake-nvidia-ctl reset 0
```

Reset delegates to upstream `nvml-mock-ctl reset`, returning the selected runtime state to the base profile.

## Declarative scenarios

Scenarios are strict JSON so the implementation remains dependency-free and deterministic.

```json
{
  "version": 1,
  "base": {
    "profile": "rtx4060ti-16gb",
    "count": 1
  },
  "initial": [
    {"args": ["gpu", "0", "memory", "reserve", "2GiB"]}
  ],
  "steps": [
    {
      "name": "pressure-after-ready",
      "event": "model-ready",
      "do": {"args": ["gpu", "0", "memory", "reserve", "8GiB"]}
    },
    {
      "name": "utilization-falls",
      "after": "500ms",
      "do": {"args": ["gpu", "0", "utilization", "15"]}
    }
  ],
  "cleanup": [
    {"args": ["reset"]}
  ]
}
```

Scenario operations use the exact same grammar as the CLI after the executable name. Nested scenario execution is rejected.

### Base profile/topology

A scenario may declare exactly one of:

- `base.profile` with optional `count` and `vram_mib`
- `base.topology`
- `base.devices` using the same device requests as the Phase 1 JSON spec

Render the base before starting consumers:

```bash
fake-nvidia-ctl scenario render --output /state/config.yaml scenario.json
```

This separation is deliberate: Mock NVML identity/topology fields are construction-time state and cannot safely be changed under an already-running consumer process.

### Timed steps

`after` accepts Go duration syntax such as `250ms`, `2s`, or `1m`. Tests use an injectable clock so timing behavior can be tested without wall-clock sleeps.

### Event-triggered steps

Event steps consume newline-delimited event names from stdin:

```bash
printf 'model-started\nmodel-ready\n' | fake-nvidia-ctl scenario run scenario.json
```

The runner ignores unrelated event names until the requested event arrives. This gives an E2E harness a deterministic trigger mechanism without a long-running fake-nvidia control server.

### Cleanup

`cleanup` operations are attempted even when an initial mutation or scenario step fails. Use `reset` there when the test should always return to its base profile.

## Concurrency

Memory and process operations are effective-state read-modify-write transactions, so separate `fake-nvidia-ctl` processes coordinate those transactions with a sibling flock before observing state and keep it held through the delegated upstream mutation. Process changes are rebased onto the fresh process list while that lock is held: unrelated concurrent PID changes are preserved, while conflicting changes to the same PID return an error instead of silently overwriting state.

The override document itself is still written only by `nvml-mock-ctl`; its own file lock, validation, and atomic rename remain authoritative for the on-disk format. CPU-only integration coverage exercises concurrent writers and verifies the resulting state remains readable by a separate `nvidia-smi` process.
