package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/brantje/fake-nvidia/internal/config"
	fakenvidiak8s "github.com/brantje/fake-nvidia/internal/kubernetes"
	"github.com/brantje/fake-nvidia/profiles"
)

// runKubernetes renders the Phase 9 ConfigMap and node-installer DaemonSet from
// the same profile/topology composition used by local and Docker modes.
func runKubernetes(catalog *profiles.Catalog, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("kubernetes", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profile := fs.String("profile", "", "profile id to expose")
	topology := fs.String("topology", "", "named topology to expose")
	gpus := fs.Int("gpus", 1, "number of devices when --profile is used")
	vram := fs.Uint64("vram-mib", 0, "override VRAM MiB for --profile devices")
	driver := fs.String("driver-version", "580.173.02", "reported driver version")
	cuda := fs.String("cuda-version", "13.0", "reported CUDA version")
	namespace := fs.String("namespace", "fake-nvidia-system", "Kubernetes namespace")
	image := fs.String("image", "fake-nvidia-k8s:local", "node installer image")
	cdiKind := fs.String("cdi-kind", fakenvidiak8s.DefaultCDIKind, "CDI kind to publish")
	output := fs.String("output", "-", "manifest output path, or - for stdout")
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
	configYAML, err := config.RenderYAML(cfg)
	if err != nil {
		return err
	}
	manifest, err := fakenvidiak8s.RenderManifest(fakenvidiak8s.ManifestOptions{
		Namespace:   *namespace,
		Image:       *image,
		CDIKind:     *cdiKind,
		DeviceCount: len(cfg.Devices),
		ConfigYAML:  configYAML,
	})
	if err != nil {
		return err
	}
	if *output == "-" {
		_, err = stdout.Write(manifest)
		return err
	}
	return os.WriteFile(*output, manifest, 0o644)
}
