# syntax=docker/dockerfile:1.7

ARG UPSTREAM_GO_VERSION=1.26.6
ARG GOLANG_IMAGE_DIGEST=sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36
ARG DEBIAN_IMAGE_DIGEST=sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171

FROM golang:${UPSTREAM_GO_VERSION}-bookworm@${GOLANG_IMAGE_DIGEST} AS mocknvml
ARG UPSTREAM_REVISION=f7bbbf025110c63c04567cf42e357af32fa2f62d

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

FROM debian:bookworm-20260824-slim@${DEBIAN_IMAGE_DIGEST} AS nvidia_smi
ARG DEBIAN_SNAPSHOT=20260828T000000Z
ARG CA_CERTIFICATES_VERSION=20250419~deb12u1
ARG CURL_VERSION=7.88.1-10+deb12u15
ARG GNUPG2_VERSION=2.2.40-1.1+deb12u2
ARG PATCHELF_VERSION=0.14.3-1+b1
ARG NVIDIA_KEY_FINGERPRINT=EB693B3035CD5710E231E123A4B469963BF863CC
ARG NVIDIA_UTILS_VERSION=580.65.06-0ubuntu1
ARG TARGETARCH

RUN set -eux; \
    rm -f /etc/apt/sources.list.d/debian.sources; \
    printf '%s\n' \
      "deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}/ bookworm main" \
      "deb [check-valid-until=no] http://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT}/ bookworm-security main" \
      > /etc/apt/sources.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      "ca-certificates=${CA_CERTIFICATES_VERSION}" \
      "curl=${CURL_VERSION}" \
      "gnupg2=${GNUPG2_VERSION}" \
      "patchelf=${PATCHELF_VERSION}"; \
    rm -rf /var/lib/apt/lists/*; \
    repo_arch=$(case "${TARGETARCH}" in arm64) echo sbsa;; *) echo x86_64;; esac); \
    key_url="https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/${repo_arch}/3bf863cc.pub"; \
    curl -fsSL "${key_url}" -o /tmp/nvidia.pub; \
    fingerprint=$(gpg --batch --show-keys --with-colons /tmp/nvidia.pub | awk -F: '$1 == "fpr" { print $10; exit }'); \
    test "${fingerprint}" = "${NVIDIA_KEY_FINGERPRINT}"; \
    gpg --batch --dearmor -o /usr/share/keyrings/nvidia.gpg /tmp/nvidia.pub; \
    rm -f /tmp/nvidia.pub; \
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
