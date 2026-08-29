package fakellama

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestParseConfigAcceptsManagerArgsAndIgnoresUnrelatedFlags verifies the fake
// binary can replace llama-server without requiring the manager to strip flags.
func TestParseConfigAcceptsManagerArgsAndIgnoresUnrelatedFlags(t *testing.T) {
	env := map[string]string{"FAKE_LLAMA_VRAM": "1.5GiB"}
	cfg, err := ParseConfig([]string{
		"--model", "/models/test.gguf",
		"--host", "0.0.0.0",
		"--port", "9090",
		"--ctx-size", "4096",
		"--tensor-split", "1,0,2",
		"--threads", "8",
		"--gpu-layers", "99",
		"--cors-origins", "localhost",
	}, func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ModelPath != "/models/test.gguf" || cfg.Host != "0.0.0.0" || cfg.Port != 9090 || cfg.ContextSize != 4096 {
		t.Fatalf("unexpected parsed config: %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.Targets, []string{"0", "2"}) {
		t.Fatalf("targets=%v want [0 2]", cfg.Targets)
	}
	want := uint64(1536 * 1024 * 1024)
	if cfg.VRAMBytes != want || !cfg.VRAMExplicit {
		t.Fatalf("VRAM=%d explicit=%v want %d,true", cfg.VRAMBytes, cfg.VRAMExplicit, want)
	}
}

// TestParseConfigFakeFlagsOverrideEnvironment verifies direct test controls are
// deterministic even when the surrounding runtime exports defaults.
func TestParseConfigFakeFlagsOverrideEnvironment(t *testing.T) {
	env := map[string]string{
		"FAKE_LLAMA_GPUS":        "3",
		"FAKE_LLAMA_VRAM":        "1GiB",
		"FAKE_LLAMA_STARTUP_FAIL": "false",
	}
	cfg, err := ParseConfig([]string{
		"--model=/tmp/model.gguf",
		"--fake-gpus=1,2",
		"--fake-vram=2GiB",
		"--fake-startup-fail=true",
		"--fake-cuda-oom",
	}, func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Targets, []string{"1", "2"}) {
		t.Fatalf("targets=%v", cfg.Targets)
	}
	if cfg.VRAMBytes != 2*1024*1024*1024 || !cfg.StartupFail || !cfg.ForceOOM {
		t.Fatalf("unexpected fake controls: %+v", cfg)
	}
}

// TestRequiredVRAMBytesUsesModelSizeAndOptionalKVApproximation verifies the
// documented deterministic fallback when no explicit VRAM value is supplied.
func TestRequiredVRAMBytesUsesModelSizeAndOptionalKVApproximation(t *testing.T) {
	model := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(model, make([]byte, 3*1024*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{ModelPath: model, ContextSize: 100, KVBytesPerToken: 1024}
	got, err := cfg.RequiredVRAMBytes()
	if err != nil {
		t.Fatal(err)
	}
	want := uint64(3*1024*1024 + 100*1024)
	if got != want {
		t.Fatalf("required bytes=%d want=%d", got, want)
	}
}

// TestParseBytes verifies common test-friendly byte units.
func TestParseBytes(t *testing.T) {
	cases := map[string]uint64{
		"1024":   1024,
		"1KiB":   1024,
		"64MiB":  64 * 1024 * 1024,
		"1.5GiB": 1536 * 1024 * 1024,
		"2GB":    2_000_000_000,
	}
	for raw, want := range cases {
		got, err := ParseBytes(raw)
		if err != nil {
			t.Fatalf("ParseBytes(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("ParseBytes(%q)=%d want=%d", raw, got, want)
		}
	}
}
