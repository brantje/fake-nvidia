//go:build integration

package compatibility

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brantje/fake-nvidia/internal/config"
	"github.com/brantje/fake-nvidia/internal/control"
)

const computeAppsQuery = "--query-compute-apps=pid,gpu_uuid,used_memory,process_name"

func TestComputeAppsQueryCompatibility(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{{
		Profile: "rtx4090-24gb",
		UsedMiB: 3072,
		Processes: []config.Process{{PID: 3101, Type: "C", Name: "llama-server", UsedMemoryMiB: 2048, SMUtil: 42, MemoryUtil: 11}},
	}}})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)

	rows := queryComputeApps(t, bundle, configPath, overridesPath)
	if len(rows) != 1 || len(rows[0]) < 4 {
		t.Fatalf("compute rows=%v", rows)
	}
	want := []string{"3101", cfg.Devices[0].UUID, "2048", "llama-server"}
	for i, value := range want {
		if got := strings.TrimSpace(rows[0][i]); got != value {
			t.Fatalf("column %d=%q want %q; row=%v", i, got, value, rows[0])
		}
	}
}

func TestPMonLlamaCPPManagerForms(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{{
		Profile: "rtx4060ti-16gb",
		UsedMiB: 1024,
		Processes: []config.Process{{PID: 3201, Type: "C", Name: "llama-server", UsedMemoryMiB: 768, SMUtil: 63, MemoryUtil: 17}},
	}}})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)

	for _, args := range [][]string{{"pmon", "-c", "1", "-s", "u"}, {"pmon", "-c", "1"}} {
		out, err := runSMI(bundle, configPath, overridesPath, args...)
		if err != nil {
			t.Fatalf("nvidia-smi %v: %v\n%s", args, err, out)
		}
		parsed := parseManagerPMon(t, out)
		if got := parsed[processKey{pid: 3201, deviceID: "CUDA0"}]; got != 63 {
			t.Fatalf("nvidia-smi %v utilization=%v want 63\n%s", args, parsed, out)
		}
	}
}

func TestPMonMultipleProcessesAndOnePIDOnMultipleGPUs(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{
		{
			Profile: "rtx4090-24gb",
			UsedMiB: 4096,
			Processes: []config.Process{
				{PID: 3301, Name: "shared-worker", UsedMemoryMiB: 1024, SMUtil: 21},
				{PID: 3302, Name: "worker-a", UsedMemoryMiB: 2048, SMUtil: 54},
			},
		},
		{
			Profile: "rtx4060ti-16gb",
			UsedMiB: 2048,
			Processes: []config.Process{{PID: 3301, Name: "shared-worker", UsedMemoryMiB: 1536, SMUtil: 78}},
		},
	}})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)

	out, err := runSMI(bundle, configPath, overridesPath, "pmon", "-c", "1", "-s", "u")
	if err != nil {
		t.Fatalf("pmon: %v\n%s", err, out)
	}
	parsed := parseManagerPMon(t, out)
	want := map[processKey]float64{
		{pid: 3301, deviceID: "CUDA0"}: 21,
		{pid: 3302, deviceID: "CUDA0"}: 54,
		{pid: 3301, deviceID: "CUDA1"}: 78,
	}
	if len(parsed) != len(want) {
		t.Fatalf("parsed=%v want=%v\n%s", parsed, want, out)
	}
	for key, value := range want {
		if parsed[key] != value {
			t.Fatalf("%v=%v want %v\n%s", key, parsed[key], value, out)
		}
	}
}

func TestRuntimeProcessMutationIsVisibleAcrossInvocations(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{{
		Profile: "rtx4090-24gb",
		UsedMiB: 2048,
		Processes: []config.Process{{PID: 3401, Name: "old-worker", UsedMemoryMiB: 1024, SMUtil: 15}},
	}}})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)
	client := control.New(bundle.Control(), configPath, overridesPath)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := client.SetProcesses(ctx, "0", []control.Process{{PID: 3402, Type: "C", Name: "new-worker", UsedMemoryMiB: 1536, SMUtil: 81, MemoryUtil: 24}}); err != nil {
		t.Fatal(err)
	}

	out, err := runSMI(bundle, configPath, overridesPath, "pmon", "-c", "1")
	if err != nil {
		t.Fatalf("pmon after mutation: %v\n%s", err, out)
	}
	parsed := parseManagerPMon(t, out)
	if len(parsed) != 1 || parsed[processKey{pid: 3402, deviceID: "CUDA0"}] != 81 {
		t.Fatalf("mutated pmon=%v\n%s", parsed, out)
	}
	rows := queryComputeApps(t, bundle, configPath, overridesPath)
	if len(rows) != 1 || strings.TrimSpace(rows[0][0]) != "3402" || strings.TrimSpace(rows[0][3]) != "new-worker" {
		t.Fatalf("mutated compute rows=%v", rows)
	}

	if err := client.SetProcesses(ctx, "0", nil); err != nil {
		t.Fatal(err)
	}
	out, err = runSMI(bundle, configPath, overridesPath, "pmon", "-c", "1")
	if err != nil {
		t.Fatalf("empty pmon: %v\n%s", err, out)
	}
	if parsed := parseManagerPMon(t, out); len(parsed) != 0 {
		t.Fatalf("empty pmon parsed=%v\n%s", parsed, out)
	}
	if rows := queryComputeApps(t, bundle, configPath, overridesPath); len(rows) != 0 {
		t.Fatalf("empty compute rows=%v", rows)
	}
}

func TestReconciledProcessRemovalReleasesOwnedVRAM(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{{
		Profile: "rtx4090-24gb",
		UsedMiB: 512,
	}}})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)
	client := control.New(bundle.Control(), configPath, overridesPath)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	device := cfg.Devices[0]
	nonProcessUsed := uint64(512) * mib
	processes := []control.Process{{PID: 3501, Type: "C", Name: "managed-worker", UsedMemoryMiB: 1024, SMUtil: 68}}
	if err := client.SetProcessesReconciled(ctx, "0", processes, device.Memory.TotalBytes, device.Memory.ReservedBytes, nonProcessUsed); err != nil {
		t.Fatal(err)
	}

	rows := queryGPUs(t, bundle, configPath, overridesPath, false)
	if got := strings.TrimSpace(rows[0][4]); got != "1536" {
		t.Fatalf("memory.used with process=%q want 1536; row=%v", got, rows[0])
	}
	out, err := runSMI(bundle, configPath, overridesPath, "pmon", "-c", "1")
	if err != nil {
		t.Fatalf("pmon with managed process: %v\n%s", err, out)
	}
	if parsed := parseManagerPMon(t, out); parsed[processKey{pid: 3501, deviceID: "CUDA0"}] != 68 {
		t.Fatalf("managed process missing from pmon: %v\n%s", parsed, out)
	}

	if err := client.SetProcessesReconciled(ctx, "0", nil, device.Memory.TotalBytes, device.Memory.ReservedBytes, nonProcessUsed); err != nil {
		t.Fatal(err)
	}
	rows = queryGPUs(t, bundle, configPath, overridesPath, false)
	if got := strings.TrimSpace(rows[0][4]); got != "512" {
		t.Fatalf("memory.used after process removal=%q want 512; row=%v", got, rows[0])
	}
	out, err = runSMI(bundle, configPath, overridesPath, "pmon", "-c", "1")
	if err != nil {
		t.Fatalf("pmon after managed process removal: %v\n%s", err, out)
	}
	if parsed := parseManagerPMon(t, out); len(parsed) != 0 {
		t.Fatalf("pmon retained removed process: %v\n%s", parsed, out)
	}
}

func TestNonPMonCommandsStillUseRealNvidiaSMI(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{{Profile: "rtx4090-24gb"}}})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)

	wrapped, err := runSMI(bundle, configPath, overridesPath, "-L")
	if err != nil {
		t.Fatalf("wrapped -L: %v\n%s", err, wrapped)
	}
	real, err := runBinary(bundle.RealNvidiaSMI(), bundle, configPath, overridesPath, "-L")
	if err != nil {
		t.Fatalf("real -L: %v\n%s", err, real)
	}
	if wrapped != real {
		t.Fatalf("wrapper changed ordinary nvidia-smi output\nwrapped:\n%s\nreal:\n%s", wrapped, real)
	}
}

type processKey struct {
	pid      int
	deviceID string
}

func parseManagerPMon(t *testing.T, out string) map[processKey]float64 {
	t.Helper()
	result := map[processKey]float64{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		gpuIndex, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil || pid <= 0 || fields[3] == "-" {
			continue
		}
		utilization, err := strconv.ParseFloat(strings.TrimSuffix(fields[3], "%"), 64)
		if err != nil {
			continue
		}
		result[processKey{pid: pid, deviceID: "CUDA" + strconv.Itoa(gpuIndex)}] = utilization
	}
	return result
}

func queryComputeApps(t *testing.T, bundle runtimeBundle, configPath, overridesPath string) [][]string {
	t.Helper()
	out, err := runSMI(bundle, configPath, overridesPath, computeAppsQuery, "--format=csv,noheader,nounits")
	if err != nil {
		t.Fatalf("compute apps query: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	return parseCSV(t, out)
}
