package control

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type memoryCapacityRunner struct{}

// Run returns one deterministic GPU snapshot with 24 MiB free and no processes.
func (memoryCapacityRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "--query-gpu="):
		return []byte("0, GPU-test, Test GPU, 1024, 1000, 24, 0, 0\n"), nil
	case strings.Contains(joined, "--query-compute-apps="):
		return nil, nil
	case strings.Contains(joined, "pmon"):
		return nil, nil
	default:
		return nil, nil
	}
}

// TestReserveMemoryReturnsTypedCapacityError verifies callers can classify OOM
// without depending on human-readable error wording.
func TestReserveMemoryReturnsTypedCapacityError(t *testing.T) {
	manager := &Manager{
		Client:   &Client{},
		Observer: &Observer{Binary: "nvidia-smi", Runner: memoryCapacityRunner{}},
	}
	err := manager.ReserveMemory(context.Background(), "0", 25*mib)
	if !errors.Is(err, ErrInsufficientMemory) {
		t.Fatalf("ReserveMemory error=%v, want ErrInsufficientMemory", err)
	}
}
