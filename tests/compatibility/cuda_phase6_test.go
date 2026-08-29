//go:build integration

package compatibility

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brantje/fake-nvidia/internal/config"
)

func TestPhase6CUDADeviceMemoryAndUnsupportedCompute(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{
		System: config.System{CUDAVersion: "12.8"},
		Devices: []config.DeviceRequest{
			{Profile: "rtx4060ti-16gb"},
			{Profile: "h100-80gb"},
		},
	})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)
	probe := buildCUDAProbe(t)

	out, err := runCUDAProbe(t, probe, bundle.Environment(os.Environ(), configPath, overridesPath),
		"basic",
		strconv.Itoa(len(cfg.Devices)),
		strconv.FormatUint(cfg.Devices[0].Memory.TotalBytes, 10),
		strconv.Itoa(cfg.Devices[0].ComputeCapability.Major),
		strconv.Itoa(cfg.Devices[0].ComputeCapability.Minor),
		"12080",
	)
	if err != nil {
		t.Fatalf("CUDA basic probe: %v\n%s", err, out)
	}
	if !strings.Contains(out, "basic-ok") {
		t.Fatalf("CUDA basic probe missing success marker:\n%s", out)
	}

	rows := queryGPUs(t, bundle, configPath, overridesPath, false)
	if got := strings.TrimSpace(rows[0][4]); got != "0" {
		t.Fatalf("CUDA probe leaked simulated VRAM: memory.used=%q row=%v", got, rows[0])
	}
}

func TestPhase6CUDAForcedOOMDoesNotMutateNVML(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{
		System:  config.System{CUDAVersion: "12.8"},
		Devices: []config.DeviceRequest{{Profile: "rtx4090-24gb"}},
	})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)
	probe := buildCUDAProbe(t)
	env := append(bundle.Environment(os.Environ(), configPath, overridesPath), "FAKE_NVIDIA_CUDA_OOM_AFTER=0")

	out, err := runCUDAProbe(t, probe, env,
		"oom",
		"1",
		strconv.FormatUint(cfg.Devices[0].Memory.TotalBytes, 10),
		strconv.Itoa(cfg.Devices[0].ComputeCapability.Major),
		strconv.Itoa(cfg.Devices[0].ComputeCapability.Minor),
		"12080",
	)
	if err != nil {
		t.Fatalf("CUDA OOM probe: %v\n%s", err, out)
	}
	if !strings.Contains(out, "oom-ok") {
		t.Fatalf("CUDA OOM probe missing success marker:\n%s", out)
	}
	rows := queryGPUs(t, bundle, configPath, overridesPath, false)
	if got := strings.TrimSpace(rows[0][4]); got != "0" {
		t.Fatalf("forced CUDA OOM changed NVML memory.used=%q row=%v", got, rows[0])
	}
}

func buildCUDAProbe(t *testing.T) string {
	t.Helper()
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("C compiler is required for native CUDA ABI compatibility test")
	}
	probe := filepath.Join(t.TempDir(), "cuda-probe")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cc, "-std=c11", "-Wall", "-Wextra", "-O2", "testdata/cuda_probe.c", "-ldl", "-o", probe)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("compile CUDA probe timed out: %v", ctx.Err())
	}
	if err != nil {
		t.Fatalf("compile CUDA probe: %v\n%s", err, out)
	}
	return probe
}

func runCUDAProbe(t *testing.T, probe string, env []string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, probe, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("CUDA probe timed out: %w", ctx.Err())
	}
	return string(out), err
}
