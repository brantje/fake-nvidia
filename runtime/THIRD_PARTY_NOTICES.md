# Third-party notices

Phase 2 does not commit NVIDIA binaries or a private copy of Mock NVML into this repository. The runtime bundle is assembled reproducibly from pinned upstream sources during `make runtime` / CI.

## NVIDIA k8s-test-infra

Mock NVML (`libnvidia-ml.so`) and `nvml-mock-ctl` are built from the pinned `NVIDIA/k8s-test-infra` revision recorded in `runtime/pins.env`.

That upstream project is licensed under the Apache License 2.0. The runtime build copies its `LICENSE` into the generated bundle at `licenses/k8s-test-infra-APACHE-2.0.txt`.

## NVIDIA nvidia-smi

The runtime build downloads the pinned `nvidia-utils-580` package directly from NVIDIA's official CUDA Ubuntu repository and extracts only `nvidia-smi`, following the strategy used by the pinned `NVIDIA/k8s-test-infra` deployment image.

The binary is not stored in this Git repository. Use and redistribution of that NVIDIA package/binary remain subject to NVIDIA's applicable package and repository terms. A future release-packaging phase must review those terms before publishing redistributed binary artifacts.

NVIDIA does not sponsor or endorse `fake-nvidia` by virtue of these compatibility dependencies.
