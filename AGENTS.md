# AGENTS.md

Instructions for coding agents and contributors working in `brantje/fake-nvidia`.

## Mandatory repository rule

**NEVER PUSH DIRECTLY TO `main`.**

This rule applies to documentation, CI, fixes, dependency updates, generated files, and implementation code.

Required workflow:

1. Read the relevant issue, `SPEC.md`, and this file.
2. Start from the latest intended base revision.
3. Create/use a dedicated non-main branch.
4. Make the smallest coherent change for the issue.
5. Run all relevant tests/checks.
6. Push only the feature/fix/docs branch.
7. Open a pull request targeting `main`.
8. Fix CI/review findings on that branch.
9. Merge through the PR workflow only.

## Project objective

Build a CPU-only NVIDIA GPU environment simulator that lets unmodified consumer software exercise NVIDIA discovery, telemetry, resource accounting, scheduling, failure handling, and selected CUDA device/memory paths.

The initial integration target is LlamaCPP-Manager, but fake-nvidia must remain a standalone/generic project.

## Read before changing code

Always read:

- `SPEC.md`
- `README.md`
- The GitHub issue being implemented
- Relevant upstream NVIDIA `k8s-test-infra` code/docs

When changing compatibility behavior, inspect the actual consumer command/API usage rather than assuming what it needs.

## Implementation language

**Use Go for project code.**

Required Go surfaces include:

- command-line tools
- configuration/profile handling
- scenario/control orchestration
- integration helpers
- test harnesses
- fake `llama-server`
- any future control service

C/CGo is permitted only for a narrow native ABI/shared-library boundary that cannot reasonably be expressed in pure Go, including NVIDIA-compatible exported symbols or direct upstream native integration. Keep policy and orchestration in Go.

Do not add Python, Rust, Node.js, or another core implementation language without an explicit specification change approved in its own issue/PR. Shell scripts are acceptable only for small build/bootstrap glue where Go would add no value.

Prefer the Go standard library unless a dependency materially simplifies a requirement. Add dependencies deliberately and keep `go.mod`/`go.sum` reproducible.

## Architecture rules

### Upstream first

NVIDIA `k8s-test-infra` Mock NVML is the low-level foundation.

Before implementing NVIDIA-facing behavior yourself:

1. Check whether upstream Mock NVML/Mock CUDA already supports it.
2. Check whether it can be configured or mutated through `nvml-mock-ctl`/runtime overrides.
3. If upstream has a narrow gap, prefer a targeted extension and consider an upstream contribution.
4. Do not build a parallel NVML implementation or general `nvidia-smi` replacement without strong evidence that upstream cannot support the requirement.

### No duplicate state daemon by default

Upstream already provides cross-process runtime override state and locking.

Do not introduce a fake-nvidia database, state daemon, custom lock protocol, or second low-level state format unless the issue demonstrates a concrete requirement that upstream state cannot satisfy.

### Transparent consumer behavior

Do not modify consumer application source code just to detect/use fake-nvidia.

Deployment/runtime injection and test-specific binary substitution (such as the optional fake `llama-server`) are acceptable. A `FAKE_NVIDIA=true` branch inside LlamaCPP-Manager is not the intended architecture.

### No fake compute claims

Do not claim that fake-nvidia executes CUDA workloads unless it genuinely does.

Unsupported kernel/module/PTX operations should fail clearly if returning success would cause a false-positive test.

Never use fake-nvidia benchmark results as evidence of real GPU performance.

## Compatibility contracts

The initial LlamaCPP-Manager integration requires these exact command families to work without manager source changes.

GPU discovery:

```bash
nvidia-smi \
  --query-gpu=index,uuid,name,memory.total,memory.used,utilization.gpu \
  --format=csv,noheader,nounits
```

Process accounting:

```bash
nvidia-smi \
  --query-compute-apps=pid,gpu_uuid,used_memory,process_name \
  --format=csv,noheader,nounits
```

Process utilization fallback sequence:

```bash
nvidia-smi pmon -c 1 -s u
nvidia-smi pmon -c 1
```

When adding compatibility tests, exercise these exact forms in addition to lower-level unit tests.

## `pmon` rule

At bootstrap time, upstream Mock NVML supports runtime processes and public per-process utilization but the real Mock-NVML-backed `nvidia-smi pmon` path is not mapped.

For the `pmon` issue:

- First identify the actual internal ABI/export gap.
- Prefer implementing/fixing it in the upstream-compatible layer.
- Prefer an upstream contribution if generally useful.
- Only use a narrow `pmon` wrapper if the real binary's internal interface cannot be supported safely.
- Do not replace unrelated `nvidia-smi` behavior.

## Profiles and state

- Reuse upstream GPU profiles where they already exist.
- Add fake-nvidia profiles only for missing cards/topologies or integration-specific composition.
- Profiles represent identity/capacity metadata, not performance predictions.
- Runtime mutations should use upstream override semantics.
- Preserve VRAM consistency in fake-nvidia-managed scenarios.
- Keep UUID/index identity deterministic for tests.

## CUDA shim rules

The limited CUDA shim is for:

- initialization
- device enumeration
- device properties
- current/set device
- memory info
- allocation/free accounting
- deterministic OOM/error paths

It is not permission to implement fake no-op kernel success.

When extending the CUDA surface:

1. Identify a real consumer call/requirement.
2. Add a failing compatibility test.
3. Implement correct-enough semantics for that API.
4. Keep unsupported APIs explicit.
5. Update the compatibility matrix/docs.

## fake llama-server rules

The optional fake server exists to test management behavior, not inference quality.

It may simulate:

- process lifecycle
- readiness
- load delays
- fake VRAM reservations
- GPU process registration
- deterministic OpenAI-compatible responses
- streaming
- OOM/startup/crash/shutdown failures

It must not pretend to execute GGUF inference.

## Testing requirements

Core tests must run on CPU-only Linux.

For Go code, run as applicable:

```bash
go test ./...
go test -race ./...
go vet ./...
```

Also run repository-specific build/lint/integration commands added by the project.

For compatibility changes, add tests at the lowest useful layer plus an end-to-end/command-level test when appropriate.

Do not weaken tests, remove validation, or change expected behavior merely to make CI pass.

## Upstream dependency discipline

- Pin upstream revisions/releases reproducibly.
- Record compatibility/version information.
- Inspect upstream changelogs before updating.
- Run the fake-nvidia compatibility suite after updates.
- Avoid copying large upstream code regions when a dependency/build integration works.
- Preserve licenses and notices.

## Docker/runtime safety

- Never overwrite real host NVIDIA driver files.
- Keep fake libraries/device surfaces scoped to an explicit test root/container/runtime.
- Ensure cleanup is deterministic.
- Be careful on hosts that contain real NVIDIA hardware.
- Do not silently mix real and fake devices unless a dedicated issue/spec explicitly defines and tests that behavior.

## Kubernetes rule

Kubernetes/CDI support must reuse the same simulator profiles and state semantics. Do not create a second Kubernetes-only simulator engine.

## Scope discipline

Implement the issue you are working on.

Avoid:

- speculative architecture rewrites
- unrelated cleanup
- adding a daemon because it feels architecturally cleaner
- adding broad CUDA/NVML APIs without a consumer/test need
- changing LlamaCPP-Manager to accommodate fake-nvidia
- mixing multiple roadmap phases into one PR without a clear dependency reason

If a missing prerequisite is found, create/document the prerequisite rather than hiding it inside an unrelated change.

## Documentation

Update documentation when behavior, compatibility, setup, profiles, or limitations change.

Never document unsupported behavior as supported.

## Completion checklist

Before considering an issue implementation complete:

- [ ] Work is on a non-main branch.
- [ ] Relevant issue/spec requirements are satisfied.
- [ ] Unit tests pass.
- [ ] Race tests pass where applicable.
- [ ] Vet/static checks pass where applicable.
- [ ] Compatibility tests pass.
- [ ] CPU-only execution is preserved.
- [ ] No real host driver state is modified.
- [ ] Documentation/compatibility matrix is updated.
- [ ] Unsupported limitations are explicit.
- [ ] PR targets `main`; nothing was pushed directly to `main`.
