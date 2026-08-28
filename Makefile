RUNTIME_DIR ?= $(CURDIR)/.runtime

.PHONY: runtime compatibility phase2

runtime:
	bash scripts/build-runtime.sh "$(RUNTIME_DIR)"

compatibility:
	test -x "$(RUNTIME_DIR)/bin/nvidia-smi"
	FAKE_NVIDIA_RUNTIME_DIR="$(RUNTIME_DIR)" go test -tags=integration -v ./tests/compatibility

phase2: runtime compatibility
