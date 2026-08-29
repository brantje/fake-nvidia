# Compatibility matrix

fake-nvidia is a CPU-only test simulator. Compatibility here means that the documented discovery, telemetry, control, limited CUDA device/memory, fake llama-server, Docker, and Kubernetes test surfaces have been exercised in CI. It does **not** imply CUDA compute correctness or real GPU performance.

The machine-readable source for this matrix is `release/compatibility.template.json`. Release CI replaces `${FAKE_NVIDIA_VERSION}` with the release tag and publishes the resulting `compatibility.json` beside release artifacts.

| fake-nvidia | Go | Mock NVML upstream | Mock CUDA upstream reference | bundled `nvidia-smi` | architectures | tested LlamaCPP-Manager |
|---|---|---|---|---|---|---|
| `main` / next release | 1.23 | `NVIDIA/k8s-test-infra@f7bbbf025110c63c04567cf42e357af32fa2f62d` | same pinned revision; fake-nvidia ships its own limited CUDA device/memory shim | `nvidia-utils-580=580.65.06-0ubuntu1` | `linux/amd64`, `linux/arm64` | `0c26e8e19635c5047d06babc7ba3b0173570e6ce` |

The runtime build itself uses the separately pinned upstream builder Go version in `runtime/pins.env`; that is not the minimum Go version required to build the fake-nvidia Go module.

## Stable profiles

The release contract contains and tests these profiles:

- `rtx4060ti-16gb` — RTX 4060 Ti 16 GB
- `rtx4090-24gb` — RTX 4090 24 GB
- `t4-16gb` — T4 16 GB
- `l40s-48gb` — L40S 48 GB
- `a100-40gb` — A100 40 GB
- `h100-80gb` — H100 80 GB

Stable topology examples are `dual-rtx4060ti-16gb` and `mixed-gpu`.

Profiles describe identity, capacity, and default telemetry values only. They are not performance models and must never be used to infer throughput, latency, kernel behavior, thermal behavior, or benchmark results for real hardware.

## Surface maturity

- **NVML / `nvidia-smi`:** primary compatibility surface, backed by pinned NVIDIA `k8s-test-infra` Mock NVML plus the narrow documented `pmon` dispatcher.
- **CUDA:** intentionally limited to device discovery/properties, current device, memory information, allocation/free accounting, and deterministic errors/OOM. Kernel/module/PTX execution is unsupported.
- **fake llama-server:** management/lifecycle test double only. It does not parse GGUF or perform inference.
- **Docker/Compose:** supported CPU-only injection mode.
- **Kubernetes/CDI:** supported CPU-only test mode from Phase 9. It is not a replacement for a real NVIDIA driver, device plugin, or GPU Operator installation.

## Updating upstream pins

Upstream changes are never consumed implicitly. To update NVIDIA `k8s-test-infra` or the bundled NVIDIA utility:

1. Create a dedicated non-`main` branch and PR.
2. Review upstream release notes/changelog and the exact Mock NVML/Mock CUDA paths affected by the proposed revision.
3. Update `runtime/pins.env`, `internal/upstream`, and `release/compatibility.template.json` together.
4. If the LlamaCPP-Manager target changes, update `.github/workflows/phase8.yml` and the release contract in the same PR.
5. Run `make phase10` plus the specialized Phase 8 and Phase 9 workflows.
6. Confirm the release-contract tests pass; they intentionally fail when Go, NVIDIA, LlamaCPP-Manager, profile, or architecture pins drift.
7. Record compatibility changes and known gaps in the PR and release notes.

Do not claim support for a newer upstream/API/consumer revision merely because it builds.

## Adding a profile

Add the JSON profile under `profiles/`, keep its ID stable, and prefer an upstream-backed profile when the pinned NVIDIA tree already contains one. Then add or update topology examples as needed and update `release/compatibility.template.json`. `go test ./...` validates the catalog and the release contract, including that the published profile list exactly matches the embedded catalog.

## Adding an NVIDIA API compatibility function

Before adding an NVML or CUDA entry point, identify an actual consumer requirement. Add the lowest-level failing test first, implement only the required semantics, add a command/shared-library compatibility test where useful, and update this document if the maturity or supported surface changes. Unsupported compute operations should fail clearly rather than return fake success.

## LlamaCPP-Manager scenarios

The pinned full-stack suite is run by `.github/workflows/phase8.yml` and locally with:

```bash
make phase8-e2e
```

The manager image and source revision are immutable compatibility pins. Phase 8 is intentionally separately identifiable in GitHub Actions so a manager regression cannot be confused with the generic Go/native checks.
