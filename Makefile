RUNTIME_DIR ?= $(CURDIR)/.runtime

.PHONY: runtime compatibility docker-integration phase2 phase3 phase4 phase5 phase6 phase7

runtime:
	bash scripts/build-runtime.sh "$(RUNTIME_DIR)"

compatibility: runtime
	test -x "$(RUNTIME_DIR)/bin/nvidia-smi"
	test -x "$(RUNTIME_DIR)/bin/fake-llama-server"
	test -f "$(RUNTIME_DIR)/lib/libcuda.so.1"
	test -f "$(RUNTIME_DIR)/lib/libcudart.so"
	FAKE_NVIDIA_RUNTIME_DIR="$(RUNTIME_DIR)" go test -tags=integration -v ./tests/compatibility

docker-integration: runtime
	test -x "$(RUNTIME_DIR)/bin/nvidia-smi"
	FAKE_NVIDIA_RUNTIME_DIR="$(RUNTIME_DIR)" go test -tags=docker_integration -v ./tests/docker

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
