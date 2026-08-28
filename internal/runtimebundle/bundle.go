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

// NvidiaSMI returns the bundled nvidia-smi path.
func (b Bundle) NvidiaSMI() string { return filepath.Join(b.Root, "bin", "nvidia-smi") }

// Control returns the bundled nvml-mock-ctl path.
func (b Bundle) Control() string { return filepath.Join(b.Root, "bin", "nvml-mock-ctl") }

// LibraryDir returns the directory containing libnvidia-ml.so.
func (b Bundle) LibraryDir() string { return filepath.Join(b.Root, "lib") }

// Validate checks that the minimum Phase 2 runtime artifacts are present.
func (b Bundle) Validate() error {
	if strings.TrimSpace(b.Root) == "" {
		return errors.New("runtime bundle root is required")
	}
	for _, path := range []string{
		b.NvidiaSMI(),
		b.Control(),
		filepath.Join(b.LibraryDir(), "libnvidia-ml.so.1"),
	} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("runtime bundle missing %s: %w", path, err)
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

func setEnv(env map[string]string, order *[]string, key, value string) {
	if _, exists := env[key]; !exists {
		*order = append(*order, key)
	}
	env[key] = value
}

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
