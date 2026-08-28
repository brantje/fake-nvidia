package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/brantje/fake-nvidia/internal/config"
	"github.com/brantje/fake-nvidia/internal/upstream"
	"github.com/brantje/fake-nvidia/profiles"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "fake-nvidia:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stderr)
		return errors.New("command is required")
	}
	catalog, err := profiles.LoadCatalog()
	if err != nil {
		return err
	}
	switch args[0] {
	case "profiles":
		for _, id := range catalog.ProfileIDs() {
			fmt.Fprintln(stdout, id)
		}
		return nil
	case "topologies":
		for _, id := range catalog.TopologyIDs() {
			fmt.Fprintln(stdout, id)
		}
		return nil
	case "version":
		fmt.Fprintf(stdout, "upstream %s@%s\n", upstream.Repository, upstream.Revision)
		return nil
	case "render":
		return runRender(catalog, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return nil
	default:
		usage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runRender(catalog *profiles.Catalog, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profile := fs.String("profile", "", "profile id to repeat")
	count := fs.Int("count", 1, "number of devices when --profile is used")
	vram := fs.Uint64("vram-mib", 0, "override VRAM MiB for --profile devices")
	topology := fs.String("topology", "", "named topology")
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
	if selected != 1 {
		return errors.New("choose exactly one of --profile, --topology, or one/more --device")
	}

	system := config.System{DriverVersion: *driver, CUDAVersion: *cuda}
	var cfg config.MockConfig
	var err error
	switch {
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

func usage(w io.Writer) {
	fmt.Fprint(w, `fake-nvidia Phase 1 profile/configuration tool

usage:
  fake-nvidia profiles
  fake-nvidia topologies
  fake-nvidia version
  fake-nvidia render --profile <id> [--count N] [--vram-mib MiB]
  fake-nvidia render --device <profile[@MiB]> [--device ...]
  fake-nvidia render --topology <id>

render writes upstream Mock NVML YAML. Runtime mutation is deliberately delegated
to NVIDIA's nvml-mock-ctl override mechanism.
`)
}
