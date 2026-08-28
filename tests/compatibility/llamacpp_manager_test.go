//go:build integration

package compatibility

import (
	"strconv"
	"strings"
	"testing"

	"github.com/brantje/fake-nvidia/internal/config"
)

const testedLlamaCPPManagerRevision = "a1f385d2b477ed6fc0b5b36b821b0e2771300b71"

type llamaCPPManagerGPU struct {
	index    int
	uuid     string
	name     string
	totalMiB int64
	usedMiB  int64
	util     float64
}

// TestLlamaCPPManagerParserCompatibility mirrors the current manager's NVIDIA discovery parser.
func TestLlamaCPPManagerParserCompatibility(t *testing.T) {
	t.Logf("parser contract from brantje/llamacpp-manager@%s", testedLlamaCPPManagerRevision)
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{{
		Profile: "rtx4060ti-16gb",
		UsedMiB: 512,
		GPUUtil: 18,
	}}})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)

	fields := "index,uuid,name,memory.total,memory.used,utilization.gpu"
	out, err := runSMI(bundle, configPath, overridesPath,
		"--query-gpu="+fields+",clocks.max.memory", "--format=csv,noheader,nounits")
	if err != nil {
		out, err = runSMI(bundle, configPath, overridesPath,
			"--query-gpu="+fields, "--format=csv,noheader,nounits")
		if err != nil {
			t.Fatalf("manager discovery fallback: %v\n%s", err, out)
		}
	}

	gpus := parseLlamaCPPManagerGPUs(out)
	if len(gpus) != 1 {
		t.Fatalf("manager parser found %d GPUs in %q", len(gpus), out)
	}
	got := gpus[0]
	device := cfg.Devices[0]
	if got.index != device.Index || got.uuid != device.UUID || got.name != device.Name {
		t.Fatalf("manager identity=%+v device=%+v", got, device)
	}
	if got.totalMiB != int64(device.Memory.TotalBytes/mib) || got.usedMiB != int64(device.Memory.UsedBytes/mib) {
		t.Fatalf("manager memory=%+v device=%+v", got, device.Memory)
	}
	if got.util != float64(device.Utilization.GPU) {
		t.Fatalf("manager utilization=%v want=%d", got.util, device.Utilization.GPU)
	}
}

// parseLlamaCPPManagerGPUs intentionally mirrors hardware.Detector.nvidiaGPUs parsing.
func parseLlamaCPPManagerGPUs(out string) []llamaCPPManagerGPU {
	var gpus []llamaCPPManagerGPU
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		if len(parts) < 6 {
			continue
		}
		index, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		totalMiB, _ := strconv.ParseInt(parts[3], 10, 64)
		usedMiB, _ := strconv.ParseInt(parts[4], 10, 64)
		util, _ := strconv.ParseFloat(parts[5], 64)
		gpus = append(gpus, llamaCPPManagerGPU{
			index: index, uuid: parts[1], name: parts[2],
			totalMiB: totalMiB, usedMiB: usedMiB, util: util,
		})
	}
	return gpus
}
