//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/brantje/fake-nvidia/internal/injection"
	fakenvidiak8s "github.com/brantje/fake-nvidia/internal/kubernetes"
)

// main runs the privileged node-local Phase 9 installer.
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fake-nvidia-k8s-installer:", err)
		os.Exit(1)
	}
}

// run prepares the host injection root, device nodes, and CDI specification.
func run(args []string) error {
	fs := flag.NewFlagSet("fake-nvidia-k8s-installer", flag.ContinueOnError)
	hostRoot := fs.String("host-root", "/host/var/lib/fake-nvidia", "fake-nvidia root as mounted inside the installer")
	nodeRoot := fs.String("node-root", "/var/lib/fake-nvidia", "same root as seen by the node runtime")
	cdiDir := fs.String("cdi-dir", "/host/var/run/cdi", "host CDI directory as mounted inside the installer")
	configPath := fs.String("config", "/config/config.yaml", "rendered Mock NVML config")
	runtimeRoot := fs.String("runtime", "/opt/fake-nvidia/runtime", "packaged fake-nvidia runtime")
	deviceCount := fs.Int("device-count", 1, "number of fake GPU device nodes")
	cdiKind := fs.String("cdi-kind", fakenvidiak8s.DefaultCDIKind, "CDI kind to publish")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	configYAML, err := os.ReadFile(*configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	layout, err := injection.Prepare(*hostRoot, *runtimeRoot, configYAML)
	if err != nil {
		return err
	}
	cleanupLayout := true
	defer func() {
		if cleanupLayout {
			_ = injection.Down(*hostRoot)
		}
	}()

	if err := createDeviceNodes(filepath.Join(layout.Root, "dev"), *deviceCount); err != nil {
		return err
	}
	spec, err := fakenvidiak8s.GenerateCDISpec(*cdiKind, *nodeRoot, *deviceCount)
	if err != nil {
		return err
	}
	specPath := filepath.Join(*cdiDir, "fake-nvidia.json")
	if err := installOwnedFile(specPath, spec, 0o644); err != nil {
		return err
	}
	defer func() { _ = removeOwnedFile(specPath, spec) }()

	fmt.Printf("installed %d fake NVIDIA device(s); CDI kind %s\n", *deviceCount, *cdiKind)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	if err := removeOwnedFile(specPath, spec); err != nil {
		return err
	}
	if err := injection.Down(*hostRoot); err != nil {
		return err
	}
	cleanupLayout = false
	return nil
}

// createDeviceNodes stages presence-only NVIDIA-style character devices.
func createDeviceNodes(root string, count int) error {
	if count <= 0 || count > 8 {
		return fmt.Errorf("device count must be between 1 and 8, got %d", count)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create device directory: %w", err)
	}
	nodes := []struct {
		name         string
		major, minor uint32
	}{
		{name: "nvidiactl", major: 195, minor: 255},
		{name: "nvidia-uvm", major: 511, minor: 0},
		{name: "nvidia-uvm-tools", major: 511, minor: 1},
	}
	for i := 0; i < count; i++ {
		nodes = append(nodes, struct {
			name         string
			major, minor uint32
		}{name: fmt.Sprintf("nvidia%d", i), major: 195, minor: uint32(i)})
	}
	for _, node := range nodes {
		path := filepath.Join(root, node.name)
		if err := syscall.Mknod(path, uint32(syscall.S_IFCHR|0o666), makeDevice(node.major, node.minor)); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return fmt.Errorf("create fake device node %s: %w", path, err)
		}
	}
	return nil
}

// makeDevice encodes Linux device major/minor numbers used by mknod.
func makeDevice(major, minor uint32) int {
	return int((major << 8) | (minor & 0xff))
}

// installOwnedFile atomically installs a CDI spec without replacing foreign data.
func installOwnedFile(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create CDI directory: %w", err)
	}
	if current, err := os.ReadFile(path); err == nil {
		if !bytes.Equal(current, content) {
			return fmt.Errorf("refusing to replace unowned CDI spec %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect CDI spec: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fake-nvidia-cdi-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install CDI spec: %w", err)
	}
	return nil
}

// removeOwnedFile removes the CDI spec only if it is still the content we installed.
func removeOwnedFile(path string, content []byte) error {
	current, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(current, content) {
		return fmt.Errorf("refusing to remove changed CDI spec %s", path)
	}
	return os.Remove(path)
}
