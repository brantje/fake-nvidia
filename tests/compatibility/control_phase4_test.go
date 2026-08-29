//go:build integration

package compatibility

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brantje/fake-nvidia/internal/config"
	"github.com/brantje/fake-nvidia/internal/control"
)

// TestPhase4ReserveMemoryIsVisibleToSeparateConsumer verifies that the high-level
// control UX reads effective state and mutates the upstream override plane.
func TestPhase4ReserveMemoryIsVisibleToSeparateConsumer(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{{Profile: "rtx4090-24gb", UsedMiB: 1024}}})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)

	client := control.New(bundle.Control(), configPath, overridesPath)
	observer := control.NewObserver(bundle.NvidiaSMI())
	libraryPath := bundle.LibraryDir()
	if inherited := os.Getenv("LD_LIBRARY_PATH"); inherited != "" {
		libraryPath += string(os.PathListSeparator) + inherited
	}
	observer.Runner = control.EnvExecRunner{Values: map[string]string{
		"MOCK_NVML_CONFIG":    configPath,
		"MOCK_NVML_OVERRIDES": overridesPath,
		"LD_LIBRARY_PATH":     libraryPath,
	}}
	manager := control.NewManager(client, observer)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := manager.ReserveMemory(ctx, "0", 2*1024*1024*1024); err != nil {
		t.Fatalf("reserve memory: %v", err)
	}

	rows := queryGPUs(t, bundle, configPath, overridesPath, false)
	if got := strings.TrimSpace(rows[0][4]); got != "3072" {
		t.Fatalf("separate nvidia-smi memory.used=%q want 3072; row=%v", got, rows[0])
	}
}

// TestPhase4ConcurrentWritersLeaveReadableState exercises upstream override-file
// locking through simultaneous fake-nvidia control clients.
func TestPhase4ConcurrentWritersLeaveReadableState(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{{Profile: "rtx4060ti-16gb"}}})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const writers = 12
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := control.New(bundle.Control(), configPath, overridesPath)
			if err := client.SetGPUUtilization(ctx, "0", uint32(10+i)); err != nil {
				errs <- fmt.Errorf("writer %d: %w", i, err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		return
	}

	rows := queryGPUs(t, bundle, configPath, overridesPath, false)
	got := strings.TrimSpace(rows[0][5])
	value, err := strconv.Atoi(got)
	if err != nil || value < 10 || value >= 10+writers {
		t.Fatalf("final utilization=%q, expected one complete writer value", got)
	}
}
