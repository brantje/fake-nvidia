# syntax=docker/dockerfile:1.7
ARG GO_VERSION=1.26.6
ARG GOLANG_IMAGE_DIGEST=sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36
ARG DEBIAN_IMAGE_DIGEST=sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171

FROM golang:${GO_VERSION}-bookworm@${GOLANG_IMAGE_DIGEST} AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/fake-nvidia-k8s-installer ./cmd/fake-nvidia-k8s-installer \
    && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/fake-nvidia ./cmd/fake-nvidia

FROM debian:bookworm-20260824-slim@${DEBIAN_IMAGE_DIGEST}
COPY .runtime/ /opt/fake-nvidia/runtime/
COPY --from=build /out/fake-nvidia-k8s-installer /usr/local/bin/fake-nvidia-k8s-installer
COPY --from=build /out/fake-nvidia /usr/local/bin/fake-nvidia
ENTRYPOINT ["/usr/local/bin/fake-nvidia-k8s-installer"]
