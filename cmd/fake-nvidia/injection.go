package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/brantje/fake-nvidia/internal/config"
	"github.com/brantje/fake-nvidia/internal/injection"
	"github.com/brantje/fake-nvidia/profiles"
)

func runUp(catalog *profiles.Catalog, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profile := fs.String("profile", "", "profile id to expose")
	topology := fs.String("topology", "", "named topology to expose")
	gpus := fs.Int("gpus", 1, "number of devices when --profile is used")
	vram := fs.Uint64("vram-mib", 0, "override VRAM MiB for --profile devices")
	runtimeDir := fs.String("runtime-dir", defaultRuntimeDir(), "built fake-nvidia runtime bundle")
	root := fs.String("root", ".fake-nvidia", "isolated container injection root")
	driver := fs.String("driver-version", "580.173.02", "reported driver version")
	cuda := fs.String("cuda-version", "13.0", "reported CUDA version")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if (*profile == "") == (*topology == "") {
		return errors.New("choose exactly one of --profile or --topology")
	}

	system := config.System{DriverVersion: *driver, CUDAVersion: *cuda}
	var cfg config.MockConfig
	var err error
	if *profile != "" {
		devices, repeatErr := config.Repeated(*profile, *gpus, *vram)
		if repeatErr != nil {
			return repeatErr
		}
		cfg, err = config.Compose(catalog, config.Spec{System: system, Devices: devices})
	} else {
		if *gpus != 1 || *vram != 0 {
			return errors.New("--gpus and --vram-mib are only valid with --profile")
		}
		cfg, err = config.ComposeTopology(catalog, system, *topology)
	}
	if err != nil {
		return err
	}
	data, err := config.RenderYAML(cfg)
	if err != nil {
		return err
	}
	layout, err := injection.Prepare(*root, *runtimeDir, data)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(stdout, "prepared fake-nvidia injection root: %s\n", layout.Root); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "export FAKE_NVIDIA_ROOT=%q\n", layout.Root); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "use examples/docker/compose.override.yaml (or examples/llamacpp-manager/compose.fake-nvidia.yaml) with your consumer stack")
	return err
}

func runDown(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".fake-nvidia", "isolated container injection root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if err := injection.Down(*root); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "removed fake-nvidia injection root: %s\n", *root)
	return err
}

func defaultRuntimeDir() string {
	if value := strings.TrimSpace(os.Getenv("FAKE_NVIDIA_RUNTIME_DIR")); value != "" {
		return value
	}
	return ".runtime"
}
