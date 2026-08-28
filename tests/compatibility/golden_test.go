//go:build integration

package compatibility

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/fake-nvidia/internal/config"
)

// TestDiscoveryQueryGolden snapshots the consumer-facing values returned by the real nvidia-smi query.
func TestDiscoveryQueryGolden(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{{
		Profile: "rtx4090-24gb",
		UsedMiB: 2048,
		GPUUtil: 37,
	}}})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)

	out, err := runSMI(bundle, configPath, overridesPath,
		"--query-gpu=index,uuid,name,memory.total,memory.used,utilization.gpu",
		"--format=csv,noheader,nounits")
	if err != nil {
		t.Fatalf("discovery query: %v\n%s", err, out)
	}
	got := normalizeDiscoveryGolden(parseCSV(t, out))
	wantPath := filepath.Join("testdata", "discovery_single.golden")
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != strings.TrimSpace(string(want)) {
		t.Fatalf("discovery golden mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func normalizeDiscoveryGolden(rows [][]string) string {
	var lines []string
	for _, row := range rows {
		fields := make([]string, len(row))
		for i := range row {
			fields[i] = strings.TrimSpace(row[i])
		}
		lines = append(lines, strings.Join(fields, "|"))
	}
	return strings.Join(lines, "\n")
}
