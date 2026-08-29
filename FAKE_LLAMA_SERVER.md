# Fake llama-server

Phase 7 adds `fake-llama-server`, an optional Go companion binary for exercising process supervision, scheduling, VRAM pressure, telemetry, routing, eviction, and failure handling without running GGUF inference.

It is deliberately **not** an inference emulator. It does not parse GGUF files, execute llama.cpp, run CUDA kernels, or produce meaningful model output/performance. Its HTTP content and resource usage are deterministic test state.

## Manager compatibility

The binary accepts the worker-owned arguments currently used by LlamaCPP-Manager:

```text
--model <path>
--host <address>
--port <port>
```

It also understands placement/resource hints that are useful to the simulator:

```text
--ctx-size <tokens>
--main-gpu <index>
--tensor-split <weights>
```

Other nonessential llama.cpp arguments are ignored so the manager can substitute this binary without adding a fake-server code path. The process is a normal OS child with a real PID.

LlamaCPP-Manager currently considers the worker ready when this request returns 2xx:

```http
GET /health
```

`fake-llama-server` returns `503` while its configured load delay is active and `200` after the simulated model load completes.

## Runtime bundle

Build Phase 7 with:

```bash
make phase7
```

The runtime bundle includes:

```text
.runtime/bin/fake-llama-server
```

With the usual fake-nvidia runtime environment exported, the binary automatically uses the same Mock NVML config/override state as `nvidia-smi`, `nvml-mock-ctl`, and the limited CUDA shim.

For example:

```bash
export PATH="$PWD/.runtime/bin:$PATH"
export LD_LIBRARY_PATH="$PWD/.runtime/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export MOCK_NVML_CONFIG="$PWD/.runtime/config/config.yaml"
export MOCK_NVML_OVERRIDES="$PWD/.runtime/config/overrides.yaml"

fake-llama-server \
  --model ./models/example.gguf \
  --host 127.0.0.1 \
  --port 8080 \
  --fake-vram 8GiB
```

For LlamaCPP-Manager integration, configure its llama-server binary path to the bundled `fake-llama-server`. The manager still launches and supervises it through its normal worker path.

## Shared GPU process and VRAM accounting

After startup, the binary registers its **real process PID** through fake-nvidia's existing upstream-backed process reconciliation path. It does not maintain a separate VRAM database and it does not double-count memory through a second reservation.

The same transaction updates the process record and effective GPU used/free memory. Consequently the process is visible through the normal compatibility surfaces:

```bash
nvidia-smi \
  --query-compute-apps=pid,gpu_uuid,used_memory,process_name \
  --format=csv,noheader,nounits

nvidia-smi pmon -c 1 -s u
```

Stopping the process removes only its PID records and releases only its process-owned simulated VRAM, preserving unrelated processes and explicit non-process/system memory.

Process memory is represented in MiB because the NVIDIA-SMI process-memory compatibility surface reports MiB. Byte-level resource requirements are rounded up conservatively when converted to process records.

## GPU selection

GPU targets are selected in this order:

1. `--fake-gpus` or `FAKE_LLAMA_GPUS`.
2. `CUDA_VISIBLE_DEVICES`, when it selects devices.
3. Positive entries from `--tensor-split`.
4. `--main-gpu`.
5. GPU `0`.

`--fake-gpus` accepts comma-, semicolon-, or whitespace-separated indexes/UUIDs.

When multiple GPUs are selected, simulated process VRAM is split by the corresponding positive `--tensor-split` weights when available. Otherwise it is divided evenly.

## Resource model

For deterministic tests, explicitly set the simulated requirement:

```text
--fake-vram 12GiB
FAKE_LLAMA_VRAM=12GiB
```

Supported suffixes include `B`, `KB`, `KiB`, `MB`, `MiB`, `GB`, and `GiB`.

If explicit VRAM is not provided, the default base requirement is the size of the model file. This is only a deterministic scheduling approximation; the file is not parsed or executed.

An optional context/KV approximation can be added:

```text
--ctx-size 8192
--fake-kv-bytes-per-token 256KiB
```

or:

```bash
export FAKE_LLAMA_KV_BYTES_PER_TOKEN=256KiB
```

The resulting requirement is:

```text
model-file-size + context-size * fake-kv-bytes-per-token
```

Tests that do not have a real model file should use explicit `--fake-vram` / `FAKE_LLAMA_VRAM`.

## HTTP API

The Phase 7 surface is intentionally small:

| Endpoint | Behavior |
| --- | --- |
| `GET /health` | `503` while loading, `200` when ready. |
| `GET /v1/models` | Returns the configured model basename as a fake model entry. |
| `POST /v1/chat/completions` | Returns deterministic OpenAI-compatible chat content. |
| `POST /v1/completions` | Returns deterministic text-completion content. |

Chat completions support `"stream": true`. Streaming uses server-sent events with deterministic content chunks and terminates with:

```text
data: [DONE]
```

Configure the content with:

```text
--fake-response "deterministic response"
FAKE_LLAMA_RESPONSE="deterministic response"
```

Optional per-token streaming delay:

```text
--fake-token-delay 25ms
FAKE_LLAMA_TOKEN_DELAY=25ms
```

## Failure and lifecycle injection

### Model-load delay / startup timeout

```text
--fake-load-delay 30s
FAKE_LLAMA_LOAD_DELAY=30s
```

The HTTP listener is available during loading, but `/health` remains `503`. A manager with a shorter startup timeout will exercise its normal timeout/termination path.

### Startup failure

```text
--fake-startup-fail
FAKE_LLAMA_STARTUP_FAIL=true
```

The process exits before registering GPU state.

### CUDA OOM during load

```text
--fake-cuda-oom
FAKE_LLAMA_CUDA_OOM=true
```

The process emits a clear CUDA-like out-of-memory message and exits before changing NVML process/memory state.

Normal capacity exhaustion is also reported as fake llama-server OOM when the requested process memory cannot fit in effective free VRAM.

### Crash after readiness

```text
--fake-crash-after-ready 5s
FAKE_LLAMA_CRASH_AFTER_READY=5s
```

The process becomes ready, then exits with an injected failure. Its registered GPU resources are released during cleanup.

### Hang on shutdown

```text
--fake-hang-shutdown
FAKE_LLAMA_HANG_SHUTDOWN=true
```

On `SIGTERM`, the process releases its fake GPU process/VRAM records but intentionally remains alive. This allows a supervisor to exercise its shutdown timeout and eventual `SIGKILL` path without leaving stale simulated GPU state.

### Sudden VRAM growth

```text
--fake-vram-growth-after 2s
--fake-vram-growth 4GiB
```

or:

```bash
export FAKE_LLAMA_VRAM_GROWTH_AFTER=2s
export FAKE_LLAMA_VRAM_GROWTH=4GiB
```

After readiness, the same PID's reconciled process memory grows by the configured amount. If the additional memory does not fit, the process fails rather than silently overcommitting.

## Telemetry controls

Defaults are intentionally deterministic rather than hardware-derived:

```text
SM utilization:     35%
Memory utilization: 20%
```

Override them with:

```text
--fake-sm-util 70
--fake-memory-util 30
```

or `FAKE_LLAMA_SM_UTIL` / `FAKE_LLAMA_MEMORY_UTIL`.

These values are synthetic telemetry and must not be interpreted as GPU performance measurements.

## Verification

`make phase7` runs the normal runtime/injection compatibility suite plus Phase 7 coverage. Tests verify, on CPU-only Linux, that:

- manager-style CLI arguments are accepted while unrelated llama.cpp flags are harmless;
- readiness transitions from loading to ready;
- non-streaming and streaming OpenAI-compatible responses work;
- the subprocess's actual OS PID appears in NVIDIA-SMI process telemetry;
- simulated process VRAM is visible in device memory usage;
- `pmon` reports the configured process utilization;
- normal termination removes the process and releases VRAM;
- deterministic OOM does not mutate shared state;
- growth/crash paths clean up registered resources;
- tensor-split memory distribution is deterministic.
