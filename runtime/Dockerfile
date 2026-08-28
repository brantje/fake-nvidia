# syntax=docker/dockerfile:1.7

ARG UPSTREAM_GO_VERSION=1.26.6
FROM golang:${UPSTREAM_GO_VERSION}-bookworm AS mocknvml
ARG UPSTREAM_REVISION=f7bbbf025110c63c04567cf42e357af32fa2f62d

RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
RUN git init . \
    && git remote add origin https://github.com/NVIDIA/k8s-test-infra.git \
    && git fetch --depth=1 origin "${UPSTREAM_REVISION}" \
    && git checkout --detach FETCH_HEAD \
    && test "$(git rev-parse HEAD)" = "${UPSTREAM_REVISION}"

RUN make -C pkg/gpu/mocknvml clean \
    && make -C pkg/gpu/mocknvml

RUN mkdir -p /out/bin /out/lib /out/licenses \
    && cp -a pkg/gpu/mocknvml/libnvidia-ml.so* /out/lib/ \
    && go build -mod=vendor -o /out/bin/nvml-mock-ctl ./cmd/nvml-mock-ctl \
    && cp LICENSE /out/licenses/k8s-test-infra-APACHE-2.0.txt

FROM debian:bookworm-slim AS nvidia_smi
ARG NVIDIA_UTILS_VERSION=580.65.06-0ubuntu1
ARG TARGETARCH

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl gnupg2 patchelf; \
    repo_arch=$(case "${TARGETARCH}" in arm64) echo sbsa;; *) echo x86_64;; esac); \
    curl -fsSL "https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/${repo_arch}/3bf863cc.pub" \
      | gpg --dearmor -o /usr/share/keyrings/nvidia.gpg; \
    echo "deb [signed-by=/usr/share/keyrings/nvidia.gpg] https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/${repo_arch} /" \
      > /etc/apt/sources.list.d/nvidia.list; \
    apt-get update; \
    cd /tmp; \
    apt-get download "nvidia-utils-580=${NVIDIA_UTILS_VERSION}"; \
    dpkg-deb -x /tmp/nvidia-utils-580*.deb /tmp/nvidia-utils; \
    mkdir -p /out/bin; \
    cp /tmp/nvidia-utils/usr/bin/nvidia-smi /out/bin/nvidia-smi; \
    chmod 0755 /out/bin/nvidia-smi; \
    patchelf --set-rpath '$ORIGIN/../lib' /out/bin/nvidia-smi

FROM scratch AS export
COPY --from=mocknvml /out/ /
COPY --from=nvidia_smi /out/ /
COPY runtime/pins.env /pins.env
COPY runtime/THIRD_PARTY_NOTICES.md /THIRD_PARTY_NOTICES.md
