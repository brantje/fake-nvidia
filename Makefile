RUNTIME_DIR ?= $(CURDIR)/.runtime

.PHONY: runtime compatibility phase2 phase3 phase4

runtime:
	bash scripts/build-runtime.sh "$(RUNTIME_DIR)"

compatibility:
	test -x "$(RUNTIME_DIR)/bin/nvidia-smi"
	FAKE_NVIDIA_RUNTIME_DIR="$(RUNTIME_DIR)" go test -tags=integration -v ./tests/compatibility

phase4: runtime compatibility
	go build ./cmd/fake-nvidia-ctl

phase3: runtime compatibility

# Kept for compatibility with the Phase 2 workflow/documentation.
phase2: phase3
