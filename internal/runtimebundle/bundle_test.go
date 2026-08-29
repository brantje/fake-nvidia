package runtimebundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateRequiresRuntimeArtifacts verifies the bundle contract.
func TestValidateRequiresRuntimeArtifacts(t *testing.T) {
	root := t.TempDir()
	b := New(root)
	if err := b.Validate(); err == nil {
		t.Fatal("expected missing-artifact error")
	}
	for _, artifact := range []struct {
		path string
		mode os.FileMode
	}{
		{path: b.NvidiaSMI(), mode: 0o755},
		{path: b.RealNvidiaSMI(), mode: 0o755},
		{path: b.Control(), mode: 0o755},
		{path: filepath.Join(b.LibraryDir(), "libnvidia-ml.so.1"), mode: 0o644},
		{path: b.CUDA(), mode: 0o755},
	} {
		if err := os.MkdirAll(filepath.Dir(artifact.path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(artifact.path, []byte("fixture"), artifact.mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestValidateRejectsNonExecutableBinaries verifies validation catches unusable command artifacts.
func TestValidateRejectsNonExecutableBinaries(t *testing.T) {
	root := t.TempDir()
	b := New(root)
	for _, artifact := range []struct {
		path string
		mode os.FileMode
	}{
		{path: b.NvidiaSMI(), mode: 0o644},
		{path: b.RealNvidiaSMI(), mode: 0o755},
		{path: b.Control(), mode: 0o755},
		{path: filepath.Join(b.LibraryDir(), "libnvidia-ml.so.1"), mode: 0o644},
		{path: b.CUDA(), mode: 0o755},
	} {
		if err := os.MkdirAll(filepath.Dir(artifact.path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(artifact.path, []byte("fixture"), artifact.mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Validate(); err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("err=%v", err)
	}
}

// TestValidateRejectsDirectoryArtifacts verifies validation requires regular files.
func TestValidateRejectsDirectoryArtifacts(t *testing.T) {
	root := t.TempDir()
	b := New(root)
	if err := os.MkdirAll(b.NvidiaSMI(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := b.Validate(); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("err=%v", err)
	}
}

// TestEnvironmentPrependsRuntimeAndReplacesMockPaths verifies consumer isolation.
func TestEnvironmentPrependsRuntimeAndReplacesMockPaths(t *testing.T) {
	b := New("/tmp/fake-nvidia")
	base := []string{
		"HOME=/home/test",
		"PATH=/usr/bin",
		"LD_LIBRARY_PATH=/opt/lib",
		ConfigEnv + "=/old/config.yaml",
		OverridesEnv + "=/old/overrides.yaml",
		"PATH=/usr/local/bin",
	}
	env := b.Environment(base, "/state/config.yaml", "/state/overrides.yaml")
	values := envMap(env)
	if got, want := values["PATH"], "/tmp/fake-nvidia/bin:/usr/local/bin"; got != want {
		t.Fatalf("PATH=%q want %q", got, want)
	}
	if got, want := values["LD_LIBRARY_PATH"], "/tmp/fake-nvidia/lib:/opt/lib"; got != want {
		t.Fatalf("LD_LIBRARY_PATH=%q want %q", got, want)
	}
	if values[ConfigEnv] != "/state/config.yaml" || values[OverridesEnv] != "/state/overrides.yaml" {
		t.Fatalf("mock env not replaced: %v", values)
	}
	for _, key := range []string{"PATH", "LD_LIBRARY_PATH", ConfigEnv, OverridesEnv} {
		if countKey(env, key) != 1 {
			t.Fatalf("%s appears more than once: %v", key, env)
		}
	}
}

// TestEnvironmentCanClearMockPaths verifies stale host variables do not leak in.
func TestEnvironmentCanClearMockPaths(t *testing.T) {
	b := New("/runtime")
	env := b.Environment([]string{ConfigEnv + "=/old", OverridesEnv + "=/old2"}, "", "")
	values := envMap(env)
	if _, ok := values[ConfigEnv]; ok {
		t.Fatalf("%s unexpectedly present", ConfigEnv)
	}
	if _, ok := values[OverridesEnv]; ok {
		t.Fatalf("%s unexpectedly present", OverridesEnv)
	}
}

// envMap converts environment entries into a key/value map for assertions.
func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

// countKey counts occurrences of one environment key in the serialized environment.
func countKey(env []string, key string) int {
	count := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, key+"=") {
			count++
		}
	}
	return count
}
