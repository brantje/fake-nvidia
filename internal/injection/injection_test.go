package injection

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPrepareAndDownPreserveRuntimeAndSymlinks verifies isolation and symlink fidelity.
func TestPrepareAndDownPreserveRuntimeAndSymlinks(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	runtimeRoot := filepath.Join(base, "runtime-source")
	writeTestBundle(t, runtimeRoot)
	sentinel := filepath.Join(runtimeRoot, "sentinel")
	if err := os.WriteFile(sentinel, []byte("host-safe"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(base, "injection")
	layout, err := Prepare(root, runtimeRoot, []byte("version: '1.0'\n"))
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if layout.Root != root {
		t.Fatalf("layout root = %q, want %q", layout.Root, root)
	}
	if data, err := os.ReadFile(layout.ConfigPath); err != nil || string(data) != "version: '1.0'\n" {
		t.Fatalf("config = %q, %v", data, err)
	}

	link := filepath.Join(layout.RuntimeDir, "lib", "libnvidia-ml.so.1")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink", link)
	}
	if target, err := os.Readlink(link); err != nil || target != "libnvidia-ml.so.580.65.06" {
		t.Fatalf("symlink target = %q, %v", target, err)
	}

	if err := Down(root); err != nil {
		t.Fatalf("Down() error = %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("injection root still exists: %v", err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "host-safe" {
		t.Fatalf("runtime sentinel changed: %q, %v", data, err)
	}
}

// TestPrepareReplacesOnlyOwnedRoot verifies unmarked paths are never replaced or removed.
func TestPrepareReplacesOnlyOwnedRoot(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	runtimeRoot := filepath.Join(base, "runtime-source")
	writeTestBundle(t, runtimeRoot)
	root := filepath.Join(base, "injection")

	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(root, "keep")
	if err := os.WriteFile(keep, []byte("do-not-delete"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(root, runtimeRoot, []byte("x: y\n")); err == nil || !strings.Contains(err.Error(), "unowned") {
		t.Fatalf("Prepare() error = %v, want unowned-path refusal", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("unowned file was modified: %v", err)
	}
	if err := Down(root); err == nil || !strings.Contains(err.Error(), "unowned") {
		t.Fatalf("Down() error = %v, want unowned-path refusal", err)
	}
}

// TestPrepareRejectsOverlappingRoots verifies source and destination cannot overlap.
func TestPrepareRejectsOverlappingRoots(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	runtimeRoot := filepath.Join(base, "runtime-source")
	writeTestBundle(t, runtimeRoot)

	for _, root := range []string{
		runtimeRoot,
		filepath.Join(runtimeRoot, "injection"),
		base,
	} {
		if _, err := Prepare(root, runtimeRoot, []byte("x: y\n")); err == nil {
			t.Fatalf("Prepare(%q) succeeded for overlapping root", root)
		}
	}
}

// TestPrepareCanRefreshOwnedRoot verifies config refresh preserves Phase 4 overrides.
func TestPrepareCanRefreshOwnedRoot(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	runtimeRoot := filepath.Join(base, "runtime-source")
	writeTestBundle(t, runtimeRoot)
	root := filepath.Join(base, "injection")

	first, err := Prepare(root, runtimeRoot, []byte("generation: one\n"))
	if err != nil {
		t.Fatal(err)
	}
	const overrides = "Device:\n  GPU-00000000-0000-0000-0000-000000000000:\n    UtilizationRates:\n      Gpu: 77\n"
	if err := os.WriteFile(first.OverridesPath, []byte(overrides), 0o600); err != nil {
		t.Fatal(err)
	}

	layout, err := Prepare(root, runtimeRoot, []byte("generation: two\n"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(layout.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "generation: two\n" {
		t.Fatalf("config = %q", data)
	}
	preserved, err := os.ReadFile(layout.OverridesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(preserved) != overrides {
		t.Fatalf("overrides = %q, want %q", preserved, overrides)
	}
	info, err := os.Stat(layout.OverridesPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("override permissions = %o, want 600", info.Mode().Perm())
	}
}

// TestPrepareRejectsNonRegularExistingOverrides prevents refresh from following state symlinks.
func TestPrepareRejectsNonRegularExistingOverrides(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	runtimeRoot := filepath.Join(base, "runtime-source")
	writeTestBundle(t, runtimeRoot)
	root := filepath.Join(base, "injection")

	first, err := Prepare(root, runtimeRoot, []byte("generation: one\n"))
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside-overrides.yaml")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, first.OverridesPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(root, runtimeRoot, []byte("generation: two\n")); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Prepare() error = %v, want non-regular override refusal", err)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "outside\n" {
		t.Fatalf("outside override changed: %q, %v", data, err)
	}
}

// writeTestBundle creates the minimum runtime bundle required by validation.
func writeTestBundle(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"nvidia-smi", "nvidia-smi.real", "nvml-mock-ctl"} {
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	realLib := filepath.Join(root, "lib", "libnvidia-ml.so.580.65.06")
	if err := os.WriteFile(realLib, []byte("mock"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libnvidia-ml.so.580.65.06", filepath.Join(root, "lib", "libnvidia-ml.so.1")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libnvidia-ml.so.1", filepath.Join(root, "lib", "libnvidia-ml.so")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lib", "libcuda.so.1"), []byte("mock-cuda"), 0o644); err != nil {
		t.Fatal(err)
	}
}
