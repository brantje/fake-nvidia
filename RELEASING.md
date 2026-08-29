# Releasing fake-nvidia

Releases are produced by GitHub Actions from signed/tagged repository state. Do not build or upload release artifacts manually as the canonical distribution path.

## Preconditions

- Never push release changes directly to `main`; use a PR.
- The current `main` commit must have the required core CI plus the specialized Phase 7, Phase 8, and Phase 9 workflows green.
- `release/compatibility.template.json` must match the repository pins and supported profile catalog.
- Release notes must call out known NVML/CUDA/manager compatibility gaps and must not describe simulated performance as real GPU performance.

## Tagging

Release workflow triggers on tags matching `v*`.

Create a release tag only from an accepted `main` commit. The tag value becomes the fake-nvidia version written into release compatibility metadata.

## CI release artifacts

The release workflow builds and tests on CPU-only Linux and publishes, where applicable:

- pure-Go `fake-nvidia`, `fake-nvidia-ctl`, `fake-llama-server`, and `fake-nvidia-k8s-installer` binaries for `linux/amd64` and `linux/arm64`;
- the native runtime/injection bundle containing pinned Mock NVML, real `nvidia-smi`, the CUDA shim, and fake llama-server;
- Docker/Compose examples and Kubernetes example manifests with the binary archive;
- `compatibility.json` generated from the versioned release contract;
- SHA-256 checksums and build provenance metadata;
- the Kubernetes installer container image in GHCR when the release job has package-write permission.

Pure-Go binaries are built with `CGO_ENABLED=0`. The runtime bundle is architecture-specific and contains native/CGo components; consumers must use the matching architecture bundle.

## Reproducing checks locally

The baseline release gate is:

```bash
make phase10
```

This enforces formatting, unit tests, race tests, vet, profile/release-contract validation, all Go command builds, the pinned native compatibility suite, and Docker injection coverage.

The heavier consumer/Kubernetes suites remain separately selectable:

```bash
make phase8-e2e
make kubernetes-integration
```

Kind setup and the pinned LlamaCPP-Manager container are handled by their corresponding GitHub Actions workflows.

## Updating release inputs

For NVIDIA upstream or LlamaCPP-Manager pin updates, follow `COMPATIBILITY.md`. A pin update must be explicit, auditable, and validated in a PR before the next release tag is created.
