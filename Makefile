RUNTIME_DIR ?= $(CURDIR)/.runtime
K8S_IMAGE ?= fake-nvidia-k8s:local
KIND_CLUSTER ?= fake-nvidia

.PHONY: runtime compatibility docker-integration phase7-integration phase8-e2e kubernetes-image kubernetes-integration phase2 phase3 phase4 phase5 phase6 phase7 phase8 phase9

runtime:
	bash scripts/build-runtime.sh "$(RUNTIME_DIR)"

compatibility: runtime
	test -x "$(RUNTIME_DIR)/bin/nvidia-smi"
	test -x "$(RUNTIME_DIR)/bin/fake-llama-server"
	test -f "$(RUNTIME_DIR)/lib/libcuda.so.1"
	test -f "$(RUNTIME_DIR)/lib/libcudart.so"
	FAKE_NVIDIA_RUNTIME_DIR="$(RUNTIME_DIR)" go test -tags=integration -v ./tests/compatibility

phase7-integration: runtime
	test -x "$(RUNTIME_DIR)/bin/nvidia-smi"
	test -x "$(RUNTIME_DIR)/bin/fake-llama-server"
	FAKE_NVIDIA_RUNTIME_DIR="$(RUNTIME_DIR)" go test -tags=integration -run '^TestPhase7' -v ./tests/compatibility

docker-integration: runtime
	test -x "$(RUNTIME_DIR)/bin/nvidia-smi"
	FAKE_NVIDIA_RUNTIME_DIR="$(RUNTIME_DIR)" go test -tags=docker_integration -v ./tests/docker

phase8-e2e: runtime
	test -x "$(RUNTIME_DIR)/bin/nvidia-smi"
	test -x "$(RUNTIME_DIR)/bin/nvml-mock-ctl"
	test -x "$(RUNTIME_DIR)/bin/fake-llama-server"
	FAKE_NVIDIA_RUNTIME_DIR="$(RUNTIME_DIR)" go test -tags=e2e -count=1 -v ./tests/e2e

kubernetes-image: runtime
	docker build -t "$(K8S_IMAGE)" -f kubernetes/Dockerfile .

kubernetes-integration:
	FAKE_NVIDIA_K8S_IMAGE="$(K8S_IMAGE)" FAKE_NVIDIA_KIND_CLUSTER="$(KIND_CLUSTER)" go test -tags=kubernetes_integration -count=1 -v ./tests/kubernetes

phase9: compatibility docker-integration
	go build ./cmd/fake-nvidia ./cmd/fake-llama-server ./cmd/fake-nvidia-k8s-installer

phase8: compatibility docker-integration phase8-e2e
	go build ./cmd/fake-nvidia ./cmd/fake-llama-server

phase7: compatibility docker-integration
	go build ./cmd/fake-nvidia ./cmd/fake-llama-server

phase6: compatibility docker-integration
	go build ./cmd/fake-nvidia

phase5: compatibility docker-integration
	go build ./cmd/fake-nvidia

phase4: compatibility
	go build ./cmd/fake-nvidia-ctl

phase3: compatibility

# Kept for compatibility with the Phase 2 workflow/documentation.
phase2: phase3
