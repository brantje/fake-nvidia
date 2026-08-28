//go:build integration

package compatibility

import (
	"strings"
	"testing"

	"github.com/brantje/fake-nvidia/internal/config"
)

// TestUnsupportedClockFieldIsNotInvented verifies upstream NOT_SUPPORTED semantics reach nvidia-smi.
func TestUnsupportedClockFieldIsNotInvented(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{{Profile: "rtx4090-24gb"}}})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)

	out, err := runSMI(bundle, configPath, overridesPath,
		"--query-gpu=clocks.max.memory", "--format=csv,noheader,nounits")
	upper := strings.ToUpper(out)
	if err != nil {
		if !strings.Contains(upper, "NOT SUPPORTED") && !strings.Contains(upper, "N/A") {
			t.Fatalf("unsupported field returned unexpected error: %v\n%s", err, out)
		}
		return
	}
	if !strings.Contains(upper, "N/A") {
		t.Fatalf("unsupported clocks.max.memory should render N/A, got %q", strings.TrimSpace(out))
	}
}
