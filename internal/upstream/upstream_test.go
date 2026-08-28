package upstream

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPinnedRevisionAndOverrideContract verifies the corresponding behavior and regression contract.
func TestPinnedRevisionAndOverrideContract(t *testing.T) {
	if Revision == "" || len(Revision) != 40 {
		t.Fatalf("expected pinned 40-character revision, got %q", Revision)
	}
	want := []string{"base", "all", "device"}
	if len(OverrideLayers) != len(want) {
		t.Fatalf("unexpected override layers: %v", OverrideLayers)
	}
	for i := range want {
		if OverrideLayers[i] != want[i] {
			t.Fatalf("override precedence changed: got %v want %v", OverrideLayers, want)
		}
	}
}

// TestRuntimePinsMatchGoContract prevents the native build manifest and Go metadata from drifting apart.
func TestRuntimePinsMatchGoContract(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file")
	}
	path := filepath.Join(filepath.Dir(currentFile), "..", "..", "runtime", "pins.env")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	pins := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || key == "" || value == "" {
			t.Fatalf("invalid pin line %q", line)
		}
		pins[key] = value
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"UPSTREAM_REVISION":    Revision,
		"UPSTREAM_GO_VERSION":  UpstreamGoVersion,
		"NVIDIA_UTILS_VERSION": NvidiaSMIVersion,
	}
	for key, value := range want {
		if got := pins[key]; got != value {
			t.Fatalf("%s=%q want %q", key, got, value)
		}
	}
	for _, key := range []string{"NVIDIA_UTILS_SHA256_AMD64", "NVIDIA_UTILS_SHA256_ARM64"} {
		value := pins[key]
		if len(value) != 64 || strings.Trim(value, "0123456789abcdef") != "" {
			t.Fatalf("%s must be a lowercase 64-character SHA-256, got %q", key, value)
		}
	}
}
