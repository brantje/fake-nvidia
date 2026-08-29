# Limited CUDA shim

Phase 6 adds a deliberately limited CUDA driver/runtime compatibility layer for CPU-only integration tests.

It exists for device discovery, capability checks, memory accounting, allocation/OOM behavior, and deterministic failure paths. It does **not** execute CUDA kernels, PTX, SASS, or GPU workloads.

## Runtime artifacts

`make runtime` now packages:

```text
.runtime/lib/libcuda.so.1
.runtime/lib/libcuda.so -> libcuda.so.1
.runtime/lib/libcudart.so.12 -> libcuda.so.1
.runtime/lib/libcudart.so.13 -> libcuda.so.1
.runtime/lib/libcudart.so -> libcudart.so.13
```

The driver and runtime entry points are exported from the same CGo shared library. The existing Phase 5 container injection already mounts the complete runtime library directory and prepends it to `LD_LIBRARY_PATH`, so no consumer source change is required.

## Source of truth

CUDA does not maintain a second GPU inventory or VRAM database.

Device enumeration and memory information are read from the same effective Mock NVML state used by `nvidia-smi`. CUDA allocation/free operations use the existing fake-nvidia control manager, which writes upstream Mock NVML runtime overrides while holding the existing cross-process mutation lock.

As a result, successful `cudaMalloc` / `cuMemAlloc*` reservations are visible to a separate `nvidia-smi` process, and `cudaFree` / `cuMemFree*` releases restore that capacity.

Profile identity remains owned by the fake-nvidia profile catalog. Device names and total memory come from effective NVML state; compute capability is resolved from the matching profile.

## Supported driver API

| API | Phase 6 behavior |
| --- | --- |
| `cuInit` | Validates simulator state; only flags `0` are accepted. |
| `cuDriverGetVersion` | Reports the configured CUDA version in CUDA's packed integer form. |
| `cuDeviceGetCount` | Returns the effective fake GPU count. |
| `cuDeviceGet` | Returns an ordinal-backed fake device handle. |
| `cuDeviceGetName` | Returns the configured profile/device name. |
| `cuDeviceGetUuid`, `cuDeviceGetUuid_v2` | Returns deterministic bytes derived from the configured GPU UUID. |
| `cuDeviceGetPCIBusId` | Returns the deterministic fake PCI bus ID. |
| `cuDeviceTotalMem`, `cuDeviceTotalMem_v2` | Returns effective total VRAM. |
| `cuDeviceComputeCapability` | Returns profile-backed compute capability. |
| `cuDeviceGetAttribute` | Supports a small capability-check subset: thread/block/grid limits, shared/constant memory limits, warp size, unified addressing, compute capability, and managed-memory flag. Other attributes return `CUDA_ERROR_NOT_SUPPORTED`. |
| `cuMemGetInfo`, `cuMemGetInfo_v2` | Returns effective free/total VRAM from Mock NVML state. |
| `cuMemAlloc`, `cuMemAlloc_v2` | Reserves simulated VRAM and returns a tiny opaque host token; it does not allocate the requested amount of host RAM. |
| `cuMemFree`, `cuMemFree_v2` | Releases a tracked simulated VRAM reservation. |
| `cuGetErrorName`, `cuGetErrorString` | Returns stable strings for the supported driver result set. |
| `cuGetProcAddress`, `cuGetProcAddress_v2` | Resolves only the explicitly supported/explicitly-failing Phase 6 driver symbols. Unknown symbols return `CUDA_ERROR_NOT_FOUND`. |

Driver error values are kept distinct from runtime error values where CUDA defines different numeric constants, notably invalid-device handling.

## Supported runtime API

| API | Phase 6 behavior |
| --- | --- |
| `cudaDriverGetVersion`, `cudaRuntimeGetVersion` | Reports the configured CUDA version. |
| `cudaGetDeviceCount` | Returns the effective fake GPU count. |
| `cudaSetDevice`, `cudaGetDevice` | Tracks the current device inside the consumer process. |
| `cudaGetDeviceProperties`, `cudaGetDeviceProperties_v2` | Initializes the complete targeted CUDA 12.8 `cudaDeviceProp` layout. Modeled identity, memory, launch-limit, and compute-capability fields are populated; unmodeled capability fields are zero. |
| `cudaMemGetInfo` | Returns effective free/total VRAM. |
| `cudaMalloc` | Reserves simulated VRAM and returns a tiny opaque host token. |
| `cudaFree` | Releases a tracked simulated allocation. `cudaFree(NULL)` succeeds. |
| `cudaDeviceReset` | Releases tracked simulated allocations on the current device. |
| `cudaMemcpy` | Host-to-host copies are real CPU copies. Device-involving copy kinds return `cudaErrorNotSupported`. |
| `cudaDeviceSynchronize` | Succeeds because Phase 6 has no queued asynchronous compute work. |
| `cudaGetErrorName`, `cudaGetErrorString` | Returns stable strings for the supported runtime result set. |
| `cudaGetLastError`, `cudaPeekAtLastError` | Exposes the process-local runtime error state. |

## OOM behavior

Capacity OOM is automatic: an allocation larger than the current effective free VRAM returns the appropriate CUDA out-of-memory result and does not mutate NVML state.

For deterministic fault testing, set:

```bash
export FAKE_NVIDIA_CUDA_OOM_AFTER=N
```

`N` is the number of successful CUDA allocations allowed in that process before subsequent allocations fail with OOM. Examples:

- `FAKE_NVIDIA_CUDA_OOM_AFTER=0`: every allocation fails.
- `FAKE_NVIDIA_CUDA_OOM_AFTER=1`: the first allocation succeeds, later allocations fail.
- unset: only real simulated-capacity OOM applies.

The injected failure happens before the shared NVML state is changed.

## Explicitly unsupported compute

The following exported entry points deliberately fail with `*_ERROR_NOT_SUPPORTED`:

- `cuModuleLoadData`
- `cuModuleLoadDataEx`
- `cuModuleGetFunction`
- `cuLaunchKernel`
- `cudaLaunchKernel`
- device-involving `cudaMemcpy` directions

Other unimplemented driver functions requested through `cuGetProcAddress*` return `CUDA_ERROR_NOT_FOUND`.

This is intentional. Returning success for kernel/module/PTX execution would falsely tell a consumer that computation happened. Phase 6 does not emulate computation.

## Upstream boundary

The pinned NVIDIA `k8s-test-infra` revision contains an early Mock CUDA implementation, but its current surface does not share allocation accounting with Mock NVML and returns success for kernel launches/device-copy no-ops. fake-nvidia therefore keeps the upstream-first architecture for NVML while providing a narrow project-owned CUDA ABI whose policy/state semantics satisfy this project's stricter no-fake-compute and shared-accounting requirements.

The implementation remains split at the intended boundary:

- Go: state lookup, profile metadata, current-device policy, allocation tracking, OOM policy, shared NVML mutation.
- C/CGo: exported ABI types/symbols, tiny opaque allocation tokens, UUID/property layout helpers, and symbol lookup.

## Verification

Run:

```bash
make phase6
```

The compatibility suite builds a native C probe without CUDA headers and validates on CPU-only Linux that:

- driver and runtime discovery return the configured device count;
- name, UUID, PCI identity, total VRAM, and compute capability are available;
- the targeted CUDA 12.8 device-property structure is fully initialized, including trailing reserved fields;
- current-device selection works;
- driver/runtime memory-info agree;
- `cudaMalloc` and `cuMemAlloc_v2` change memory seen by a separate `nvidia-smi` process;
- freeing restores the prior NVML memory state;
- capacity OOM and `FAKE_NVIDIA_CUDA_OOM_AFTER` do not mutate memory state;
- supported `cuGetProcAddress` lookup works and unknown symbols fail;
- kernel launches and device-memory copies fail explicitly rather than pretending to compute.
