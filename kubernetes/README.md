# Phase 9 Kubernetes / CDI integration

Phase 9 exposes the same fake-nvidia profiles and Mock-NVML override state used by local and Docker modes to Kubernetes nodes. It does not add a Kubernetes-specific simulator, allocator, or state service.

## Architecture

`fake-nvidia kubernetes` renders a ConfigMap plus a privileged installer DaemonSet. Each installer pod:

1. reuses `internal/injection.Prepare` to copy the already-built runtime and rendered profile into `/var/lib/fake-nvidia` on its node;
2. creates presence-only NVIDIA-style character device nodes under `/var/lib/fake-nvidia/dev` rather than touching the node's real `/dev`;
3. writes `/var/run/cdi/fake-nvidia.json`;
4. keeps the same `config.yaml` / `overrides.yaml` state used by Docker mode; and
5. removes only its owned CDI spec and injection root on normal DaemonSet termination.

The installer, example device plugin, and example consumer all require the explicit node label `fake-nvidia.com/enabled=true`. This is an enforced opt-in boundary, not only a documentation convention: unlabeled nodes are ignored even when the DaemonSets tolerate their taints.

The fake nodes are compatibility fixtures. They are not backed by an NVIDIA kernel driver and must not be used as evidence that CUDA kernels can execute. The Phase 6 CUDA shim remains the supported device/memory compatibility surface.

The generated CDI spec contains `0`, `1`, ... and `all` devices. CDI resolves the runtime binaries/libraries, the read-only state directory, and the selected fake device nodes in one runtime operation. Stock Kind nodes using containerd 2.x read `/var/run/cdi` without requiring NVIDIA Container Toolkit.

## Build the node installer image

```bash
make runtime
docker build -t fake-nvidia-k8s:local -f kubernetes/Dockerfile .
```

For Kind:

```bash
kind create cluster --name fake-nvidia
kubectl label nodes --all fake-nvidia.com/enabled=true --overwrite
kind load docker-image fake-nvidia-k8s:local --name fake-nvidia
```

For a non-Kind cluster, label only dedicated CPU-only/test nodes that are intended to host fake GPUs:

```bash
kubectl label node <test-node> fake-nvidia.com/enabled=true --overwrite
```

Do not apply this label to a node serving real NVIDIA GPUs.

## Install two fake GPUs

The same profile IDs and VRAM overrides used by `fake-nvidia up` are accepted:

```bash
go run ./cmd/fake-nvidia kubernetes \
  --profile rtx4090-24gb \
  --gpus 2 \
  --image fake-nvidia-k8s:local \
  | kubectl apply -f -

kubectl -n fake-nvidia-system rollout status daemonset/fake-nvidia
```

`fake-nvidia.com/gpu` is the default CDI kind so it cannot collide with NVIDIA-owned CDI specs on a real GPU node. The Phase 9 Kind suite resolves `fake-nvidia.com/gpu=all` through containerd and runs the bundled `nvidia-smi` without NVIDIA Container Toolkit.

Named topologies reuse the same catalog too:

```bash
go run ./cmd/fake-nvidia kubernetes \
  --topology mixed-gpu \
  --image fake-nvidia-k8s:local \
  | kubectl apply -f -
```

## NVIDIA device-plugin compatibility path

The example device-plugin DaemonSet is intentionally separate from the CDI smoke path. It runs the unmodified NVIDIA device plugin against fake-nvidia's Mock NVML and node-local device tree. The production plugin remains responsible for Kubernetes capacity advertisement and allocation; fake-nvidia does not implement a competing allocator.

```bash
kubectl apply -f kubernetes/examples/device-plugin.yaml
kubectl -n kube-system rollout status daemonset/nvidia-device-plugin-daemonset
kubectl apply -f kubernetes/examples/consumer.yaml
kubectl wait --for=condition=Ready pod/fake-nvidia-consumer --timeout=150s
kubectl exec fake-nvidia-consumer -- nvidia-smi -L
```

Both example workloads carry the same `fake-nvidia.com/enabled=true` node selector as the installer. The example plugin uses NVML discovery and `--pass-device-specs=true`, with its driver/device roots pointed at `/var/lib/fake-nvidia`. The consumer requests ordinary `nvidia.com/gpu` resources and mounts the same runtime/state tree read-only. This verifies scheduler/device-plugin behavior without requiring NVIDIA Container Toolkit or modifying the consumer application's source.

This is a tested compatibility target, not a claim that every GPU Operator/device-plugin configuration works. On CPU-only mock nodes, GPU Operator driver and toolkit management must remain disabled unless a dedicated compatibility test says otherwise.

## Runtime mutation / control

There is deliberately no network control service. Scenario tests mutate node-local state through `kubectl exec` into the installer DaemonSet, which is namespace/RBAC scoped and already has the node state mounted read-write:

```bash
kubectl -n fake-nvidia-system exec daemonset/fake-nvidia -- \
  fake-nvidia ctl \
    --ctl-bin /opt/fake-nvidia/runtime/bin/nvml-mock-ctl \
    --nvidia-smi-bin /opt/fake-nvidia/runtime/bin/nvidia-smi \
    --config /host/var/lib/fake-nvidia/state/config.yaml \
    --overrides /host/var/lib/fake-nvidia/state/overrides.yaml \
    gpu 0 utilization 77
```

Consumers mount the state directory read-only, but Mock NVML observes atomic override replacements made on the node. This preserves the Phase 4 state model without giving normal workload pods write access.

## CPU-only Kind verification

The Phase 9 workflow pins Kind and its node image, explicitly labels the test node with `fake-nvidia.com/enabled=true`, and checks all of the following without a real NVIDIA GPU:

- two GPUs rendered from the existing `rtx4090-24gb` profile;
- the installer DaemonSet creates an owned node root and CDI spec only on opted-in nodes;
- containerd resolves `fake-nvidia.com/gpu=all` and `nvidia-smi -L` reports both GPUs;
- the real NVIDIA device plugin advertises two `nvidia.com/gpu` resources;
- a pod requesting both GPUs sees the fake NVML library and allocated device nodes;
- a live utilization mutation is visible from the already-running pod; and
- teardown removes the owned CDI spec and fake-nvidia node root.

## Teardown

```bash
kubectl delete -f kubernetes/examples/consumer.yaml --ignore-not-found
kubectl delete -f kubernetes/examples/device-plugin.yaml --ignore-not-found
kubectl delete namespace fake-nvidia-system --ignore-not-found
```

The installer traps normal termination and removes its owned CDI spec and `/var/lib/fake-nvidia` injection root. If a node/container is killed without normal termination, re-applying the DaemonSet safely refreshes only roots carrying the fake-nvidia ownership marker.

Remove the opt-in label when the node is no longer reserved for fake-nvidia tests:

```bash
kubectl label node <test-node> fake-nvidia.com/enabled-
```

## Safety boundary

- No real NVIDIA driver or physical GPU is required.
- All Kubernetes fake-nvidia workloads require `fake-nvidia.com/enabled=true` and therefore ignore unlabeled nodes.
- The installer never writes fake nodes into the node's real `/dev` tree.
- The default CDI vendor is `fake-nvidia.com`, not `nvidia.com`.
- Do not label a node that is serving real NVIDIA GPUs for fake-nvidia use.
- Kubernetes remains optional. Local and Docker modes do not depend on Kind, kubelet, containerd, or CDI.
