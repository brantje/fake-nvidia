package runtimebundle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	ConfigEnv    = "MOCK_NVML_CONFIG"
	OverridesEnv = "MOCK_NVML_OVERRIDES"
)

type Bundle struct {
	Root string
}

// New returns a runtime bundle rooted at root.
func New(root string) Bundle { return Bundle{Root: root} }

// NvidiaSMI returns the bundled nvidia-smi compatibility entry point.
func (b Bundle) NvidiaSMI() string { return filepath.Join(b.Root, "bin", "nvidia-smi") }

// RealNvidiaSMI returns the untouched NVIDIA nvidia-smi binary used for all
// commands outside the narrow pmon compatibility surface.
func (b Bundle) RealNvidiaSMI() string { return filepath.Join(b.Root, "bin", "nvidia-smi.real") }

// Control returns the bundled nvml-mock-ctl path.
func (b Bundle) Control() string { return filepath.Join(b.Root, "bin", "nvml-mock-ctl") }

// FakeLlamaServer returns the Phase 7 manager-compatible test server binary.
func (b Bundle) FakeLlamaServer() string { return filepath.Join(b.Root, "bin", "fake-llama-server") }

// LibraryDir returns the directory containing NVIDIA-compatible shared libraries.
func (b Bundle) LibraryDir() string { return filepath.Join(b.Root, "lib") }

// CUDA returns the Phase 6 CUDA driver/runtime shim.
func (b Bundle) CUDA() string { return filepath.Join(b.LibraryDir(), "libcuda.so.1") }

// Validate checks that the minimum fake-nvidia runtime artifacts are present and usable.
func (b Bundle) Validate() error {
	if strings.TrimSpace(b.Root) == "" {
		return errors.New("runtime bundle root is required")
	}
	for _, artifact := range []struct {
		path       string
		executable bool
	}{
		{path: b.NvidiaSMI(), executable: true},
		{path: b.RealNvidiaSMI(), executable: true},
		{path: b.Control(), executable: true},
		{path: b.FakeLlamaServer(), executable: true},
		{path: filepath.Join(b.LibraryDir(), "libnvidia-ml.so.1")},
		{path: b.CUDA()},
	} {
		info, err := os.Stat(artifact.path)
		if err != nil {
			return fmt.Errorf("runtime bundle missing %s: %w", artifact.path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("runtime bundle artifact is not a regular file: %s", artifact.path)
		}
		if artifact.executable && info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("runtime bundle artifact is not executable: %s", artifact.path)
		}
	}

	for _, alias := range []string{
		filepath.Join(b.LibraryDir(), "libcuda.so"),
		filepath.Join(b.LibraryDir(), "libcudart.so.12"),
		filepath.Join(b.LibraryDir(), "libcudart.so.13"),
		filepath.Join(b.LibraryDir(), "libcudart.so"),
	} {
		info, err := os.Lstat(alias)
		if err != nil {
			return fmt.Errorf("runtime bundle missing CUDA alias %s: %w", alias, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("runtime bundle CUDA alias is not a symlink: %s", alias)
		}
		target, err := os.Stat(alias)
		if err != nil {
			return fmt.Errorf("runtime bundle CUDA alias is broken %s: %w", alias, err)
		}
		if !target.Mode().IsRegular() {
			return fmt.Errorf("runtime bundle CUDA alias target is not a regular file: %s", alias)
		}
	}
	return nil
}

// Environment returns a copy of base configured to run consumers against this bundle.
func (b Bundle) Environment(base []string, configPath, overridesPath string) []string {
	env := make(map[string]string, len(base)+4)
	order := make([]string, 0, len(base)+4)
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		if _, exists := env[key]; !exists {
			order = append(order, key)
		}
		env[key] = value
	}

	path := filepath.Join(b.Root, "bin")
	if old := env["PATH"]; old != "" {
		path += string(os.PathListSeparator) + old
	}
	setEnv(env, &order, "PATH", path)

	libraryPath := b.LibraryDir()
	if old := env["LD_LIBRARY_PATH"]; old != "" {
		libraryPath += string(os.PathListSeparator) + old
	}
	setEnv(env, &order, "LD_LIBRARY_PATH", libraryPath)

	if configPath != "" {
		setEnv(env, &order, ConfigEnv, configPath)
	} else {
		deleteEnv(env, &order, ConfigEnv)
	}
	if overridesPath != "" {
		setEnv(env, &order, OverridesEnv, overridesPath)
	} else {
		deleteEnv(env, &order, OverridesEnv)
	}

	out := make([]string, 0, len(order))
	for _, key := range order {
		if value, ok := env[key]; ok {
			out = append(out, key+"="+value)
		}
	}
	return out
}

// setEnv sets a value while preserving deterministic environment key ordering.
func setEnv(env map[string]string, order *[]string, key, value string) {
	if _, exists := env[key]; !exists {
		*order = append(*order, key)
	}
	env[key] = value
}

// deleteEnv removes a value and its key from the deterministic environment order.
func deleteEnv(env map[string]string, order *[]string, key string) {
	if _, exists := env[key]; !exists {
		return
	}
	delete(env, key)
	filtered := (*order)[:0]
	for _, existing := range *order {
		if existing != key {
			filtered = append(filtered, existing)
		}
	}
	*order = filtered
}
