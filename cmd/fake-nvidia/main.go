package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/brantje/fake-nvidia/internal/config"
	"github.com/brantje/fake-nvidia/internal/controlcli"
	"github.com/brantje/fake-nvidia/internal/upstream"
	"github.com/brantje/fake-nvidia/profiles"
)

type stringList []string

// String formats repeated string flags for flag package diagnostics.
func (s *stringList) String() string { return strings.Join(*s, ",") }

// Set appends one occurrence of a repeated string flag.
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// main runs the fake-nvidia command-line entry point.
func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "fake-nvidia:", err)
		os.Exit(1)
	}
}

// run implements the corresponding fake-nvidia operation.
func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		if err := usage(stderr); err != nil {
			return err
		}
		return errors.New("command is required")
	}
	catalog, err := profiles.LoadCatalog()
	if err != nil {
		return err
	}
	switch args[0] {
	case "profiles":
		for _, id := range catalog.ProfileIDs() {
			if _, err := fmt.Fprintln(stdout, id); err != nil {
				return err
			}
		}
		return nil
	case "topologies":
		for _, id := range catalog.TopologyIDs() {
			if _, err := fmt.Fprintln(stdout, id); err != nil {
				return err
			}
		}
		return nil
	case "version":
		_, err := fmt.Fprintf(stdout, "upstream %s@%s\n", upstream.Repository, upstream.Revision)
		return err
	case "render":
		return runRender(catalog, args[1:], stdout, stderr)
	case "up":
		return runUp(catalog, args[1:], stdout, stderr)
	case "down":
		return runDown(args[1:], stdout, stderr)
	case "kubernetes":
		return runKubernetes(catalog, args[1:], stdout, stderr)
	case "ctl":
		return controlcli.Run(context.Background(), args[1:], os.Stdin, stdout, stderr)
	case "help", "-h", "--help":
		return usage(stdout)
	default:
		if err := usage(stderr); err != nil {
			return err
		}
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// runRender implements the corresponding fake-nvidia operation.
func runRender(catalog *profiles.Catalog, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profile := fs.String("profile", "", "profile id to repeat")
	count := fs.Int("count", 1, "number of devices when --profile is used")
	vram := fs.Uint64("vram-mib", 0, "override VRAM MiB for --profile devices")
	topology := fs.String("topology", "", "named topology")
	specPath := fs.String("spec", "", "JSON spec containing system and per-device state")
	driver := fs.String("driver-version", "580.173.02", "reported driver version")
	cuda := fs.String("cuda-version", "13.0", "reported CUDA version")
	output := fs.String("output", "-", "output path, or - for stdout")
	var rawDevices stringList
	fs.Var(&rawDevices, "device", "device profile[@vramMiB]; repeat for mixed/per-device VRAM")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	selected := 0
	if *profile != "" {
		selected++
	}
	if *topology != "" {
		selected++
	}
	if len(rawDevices) > 0 {
		selected++
	}
	if *specPath != "" {
		selected++
	}
	if selected != 1 {
		return errors.New("choose exactly one of --profile, --topology, --spec, or one/more --device")
	}

	system := config.System{DriverVersion: *driver, CUDAVersion: *cuda}
	var cfg config.MockConfig
	var err error
	switch {
	case *specPath != "":
		f, openErr := os.Open(*specPath)
		if openErr != nil {
			return openErr
		}
		spec, loadErr := config.LoadSpecJSON(f)
		closeErr := f.Close()
		if loadErr != nil {
			return loadErr
		}
		if closeErr != nil {
			return closeErr
		}
		cfg, err = config.Compose(catalog, spec)
	case *topology != "":
		cfg, err = config.ComposeTopology(catalog, system, *topology)
	case *profile != "":
		var devices []config.DeviceRequest
		devices, err = config.Repeated(*profile, *count, *vram)
		if err == nil {
			cfg, err = config.Compose(catalog, config.Spec{System: system, Devices: devices})
		}
	default:
		devices := make([]config.DeviceRequest, 0, len(rawDevices))
		for _, raw := range rawDevices {
			d, parseErr := parseDevice(raw)
			if parseErr != nil {
				return parseErr
			}
			devices = append(devices, d)
		}
		cfg, err = config.Compose(catalog, config.Spec{System: system, Devices: devices})
	}
	if err != nil {
		return err
	}
	data, err := config.RenderYAML(cfg)
	if err != nil {
		return err
	}
	if *output == "-" {
		_, err = stdout.Write(data)
		return err
	}
	return os.WriteFile(*output, data, 0o644)
}

// parseDevice implements the corresponding fake-nvidia operation.
func parseDevice(raw string) (config.DeviceRequest, error) {
	profile := raw
	var vram uint64
	if at := strings.LastIndexByte(raw, '@'); at >= 0 {
		profile = raw[:at]
		if profile == "" || at == len(raw)-1 {
			return config.DeviceRequest{}, fmt.Errorf("invalid --device %q", raw)
		}
		parsed, err := strconv.ParseUint(raw[at+1:], 10, 64)
		if err != nil || parsed == 0 {
			return config.DeviceRequest{}, fmt.Errorf("invalid VRAM in --device %q", raw)
		}
		vram = parsed
	}
	if strings.TrimSpace(profile) == "" {
		return config.DeviceRequest{}, fmt.Errorf("invalid --device %q", raw)
	}
	return config.DeviceRequest{Profile: profile, VRAMMiB: vram}, nil
}

// usage implements the corresponding fake-nvidia operation.
func usage(w io.Writer) error {
	_, err := fmt.Fprint(w, `fake-nvidia profile/configuration, injection, and control tool

usage:
  fake-nvidia profiles
  fake-nvidia topologies
  fake-nvidia version
  fake-nvidia render --profile <id> [--count N] [--vram-mib MiB]
  fake-nvidia render --device <profile[@MiB]> [--device ...]
  fake-nvidia render --topology <id>
  fake-nvidia render --spec <config.json>
  fake-nvidia up --profile <id> [--gpus N] [--vram-mib MiB]
  fake-nvidia up --topology <id>
  fake-nvidia down [--root .fake-nvidia]
  fake-nvidia kubernetes --profile <id> [--gpus N] [--vram-mib MiB] [--image <ref>]
  fake-nvidia kubernetes --topology <id> [--image <ref>]
  fake-nvidia ctl <control command> [args...]

render writes upstream Mock NVML YAML. up prepares an isolated runtime/state root for
Docker/Compose injection, kubernetes renders the Phase 9 ConfigMap/node-installer
DaemonSet from the same profiles, and down removes only fake-nvidia-owned injection
roots. ctl composes the upstream nvml-mock-ctl override mechanism and is also
available as the standalone fake-nvidia-ctl binary.
`)
	return err
}
