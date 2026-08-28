//go:build integration

package compatibility

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brantje/fake-nvidia/internal/config"
	"github.com/brantje/fake-nvidia/internal/control"
	"github.com/brantje/fake-nvidia/internal/runtimebundle"
	"github.com/brantje/fake-nvidia/profiles"
)

const mib = uint64(1024 * 1024)

// TestNvidiaSMIDiscoveryProfiles verifies the exact Phase 2 discovery contract.
func TestNvidiaSMIDiscoveryProfiles(t *testing.T) {
	catalog := loadCatalog(t)
	cases := []struct {
		name string
		cfg  config.MockConfig
	}{
		{name: "single", cfg: compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{{Profile: "rtx4090-24gb", UsedMiB: 2048, GPUUtil: 37}}})},
		{name: "multiple", cfg: composeTopology(t, catalog, "dual-rtx4060ti-16gb")},
		{name: "mixed", cfg: composeTopology(t, catalog, "mixed-gpu")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bundle := requireBundle(t)
			configPath, overridesPath := writeConfig(t, tc.cfg)
			rows := queryGPUs(t, bundle, configPath, overridesPath, false)
			assertDiscovery(t, rows, tc.cfg)
		})
	}
}

// TestCurrentLlamaCPPManagerDiscoveryFlow verifies the manager's enriched query and fallback shape.
func TestCurrentLlamaCPPManagerDiscoveryFlow(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{{Profile: "rtx4060ti-16gb", UsedMiB: 512, GPUUtil: 18}}})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)

	rows := queryGPUs(t, bundle, configPath, overridesPath, true)
	assertDiscovery(t, rows, cfg)
	if len(rows[0]) > 6 {
		clock := strings.TrimSpace(rows[0][6])
		if clock == "" {
			t.Fatal("enriched discovery returned an empty clocks.max.memory field")
		}
		// Phase 1 deliberately does not invent clock values. The real nvidia-smi
		// renderer should surface upstream NOT_SUPPORTED as N/A rather than a fake number.
		if !strings.Contains(strings.ToUpper(clock), "N/A") {
			if value, err := strconv.ParseFloat(clock, 64); err != nil || value < 0 {
				t.Fatalf("unexpected clocks.max.memory value %q", clock)
			}
		}
	}
}

// TestBaselineNvidiaSMICommands verifies ordinary command forms remain usable.
func TestBaselineNvidiaSMICommands(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := composeTopology(t, catalog, "mixed-gpu")
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)

	for _, args := range [][]string{{}, {"-L"}, {"-q"}} {
		out, err := runSMI(bundle, configPath, overridesPath, args...)
		if err != nil {
			t.Fatalf("nvidia-smi %v: %v\n%s", args, err, out)
		}
		if strings.TrimSpace(out) == "" {
			t.Fatalf("nvidia-smi %v returned no output", args)
		}
	}

	out, err := runSMI(bundle, configPath, overridesPath, "-L")
	if err != nil {
		t.Fatal(err)
	}
	for _, device := range cfg.Devices {
		if !strings.Contains(out, device.Name) || !strings.Contains(out, device.UUID) {
			t.Fatalf("-L output missing %s/%s:\n%s", device.Name, device.UUID, out)
		}
	}
}

// TestRuntimeMutationIsVisibleToSubsequentNvidiaSMI verifies shared override state across processes.
func TestRuntimeMutationIsVisibleToSubsequentNvidiaSMI(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{{Profile: "rtx4090-24gb", UsedMiB: 1024, GPUUtil: 10, MemoryUtil: 5}}})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)

	before := queryGPUs(t, bundle, configPath, overridesPath, false)
	assertDiscovery(t, before, cfg)

	newUsed := uint64(4096) * mib
	newFree := cfg.Devices[0].Memory.TotalBytes - cfg.Devices[0].Memory.ReservedBytes - newUsed
	client := control.New(bundle.Control(), configPath, overridesPath)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.SetMemory(ctx, "0", newUsed, newFree); err != nil {
		t.Fatalf("set memory: %v", err)
	}
	if err := client.SetUtilization(ctx, "0", 77, 33); err != nil {
		t.Fatalf("set utilization: %v", err)
	}

	after := queryGPUs(t, bundle, configPath, overridesPath, false)
	if got := strings.TrimSpace(after[0][4]); got != "4096" {
		t.Fatalf("memory.used after mutation=%q want 4096; row=%v", got, after[0])
	}
	if got := strings.TrimSpace(after[0][5]); got != "77" {
		t.Fatalf("utilization.gpu after mutation=%q want 77; row=%v", got, after[0])
	}
}

func requireBundle(t *testing.T) runtimebundle.Bundle {
	t.Helper()
	root := os.Getenv("FAKE_NVIDIA_RUNTIME_DIR")
	if root == "" {
		t.Fatal("FAKE_NVIDIA_RUNTIME_DIR is required for integration tests")
	}
	bundle := runtimebundle.New(root)
	if err := bundle.Validate(); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func loadCatalog(t *testing.T) *profiles.Catalog {
	t.Helper()
	catalog, err := profiles.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func compose(t *testing.T, catalog *profiles.Catalog, spec config.Spec) config.MockConfig {
	t.Helper()
	cfg, err := config.Compose(catalog, spec)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func composeTopology(t *testing.T, catalog *profiles.Catalog, id string) config.MockConfig {
	t.Helper()
	cfg, err := config.ComposeTopology(catalog, config.System{}, id)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func writeConfig(t *testing.T, cfg config.MockConfig) (string, string) {
	t.Helper()
	data, err := config.RenderYAML(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath, filepath.Join(dir, "overrides.yaml")
}

func queryGPUs(t *testing.T, bundle runtimebundle.Bundle, configPath, overridesPath string, managerFlow bool) [][]string {
	t.Helper()
	fields := "index,uuid,name,memory.total,memory.used,utilization.gpu"
	if managerFlow {
		out, err := runSMI(bundle, configPath, overridesPath,
			"--query-gpu="+fields+",clocks.max.memory", "--format=csv,noheader,nounits")
		if err == nil {
			return parseCSV(t, out)
		}
	}
	out, err := runSMI(bundle, configPath, overridesPath,
		"--query-gpu="+fields, "--format=csv,noheader,nounits")
	if err != nil {
		t.Fatalf("LCM discovery query: %v\n%s", err, out)
	}
	return parseCSV(t, out)
}

func runSMI(bundle runtimebundle.Bundle, configPath, overridesPath string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bundle.NvidiaSMI(), args...)
	cmd.Env = bundle.Environment(os.Environ(), configPath, overridesPath)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("nvidia-smi timed out: %w", ctx.Err())
	}
	if err != nil {
		return string(out), fmt.Errorf("nvidia-smi %v: %w", args, err)
	}
	return string(out), nil
}

func parseCSV(t *testing.T, text string) [][]string {
	t.Helper()
	reader := csv.NewReader(strings.NewReader(strings.TrimSpace(text)))
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("parse nvidia-smi CSV: %v\n%s", err, text)
	}
	return rows
}

func assertDiscovery(t *testing.T, rows [][]string, cfg config.MockConfig) {
	t.Helper()
	if len(rows) != len(cfg.Devices) {
		t.Fatalf("rows=%d devices=%d rows=%v", len(rows), len(cfg.Devices), rows)
	}
	for i, device := range cfg.Devices {
		if len(rows[i]) < 6 {
			t.Fatalf("row %d has %d fields: %v", i, len(rows[i]), rows[i])
		}
		want := []string{
			strconv.Itoa(device.Index),
			device.UUID,
			device.Name,
			strconv.FormatUint(device.Memory.TotalBytes/mib, 10),
			strconv.FormatUint(device.Memory.UsedBytes/mib, 10),
			strconv.FormatUint(uint64(device.Utilization.GPU), 10),
		}
		for column := range want {
			if got := strings.TrimSpace(rows[i][column]); got != want[column] {
				t.Fatalf("row %d column %d=%q want %q; row=%v", i, column, got, want[column], rows[i])
			}
		}
	}
}
