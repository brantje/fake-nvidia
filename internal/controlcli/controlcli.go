package controlcli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/brantje/fake-nvidia/internal/control"
	"github.com/brantje/fake-nvidia/internal/scenario"
	"github.com/brantje/fake-nvidia/profiles"
)

const mib = uint64(1024 * 1024)

// Runtime executes the fake-nvidia control UX against upstream runtime overrides.
type Runtime struct {
	Client  *control.Client
	Manager *control.Manager
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// Run parses global control flags and executes one command.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("ctl", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ctlBin := fs.String("ctl-bin", envOr("FAKE_NVIDIA_CTL_BIN", "nvml-mock-ctl"), "nvml-mock-ctl binary")
	smiBin := fs.String("nvidia-smi-bin", envOr("FAKE_NVIDIA_SMI_BIN", "nvidia-smi"), "nvidia-smi binary")
	configPath := fs.String("config", os.Getenv("MOCK_NVML_CONFIG"), "Mock NVML base config path")
	overridePath := fs.String("overrides", os.Getenv("MOCK_NVML_OVERRIDES"), "Mock NVML runtime override path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return usage(stderr)
	}
	client := control.New(*ctlBin, *configPath, *overridePath)
	observer := control.NewObserver(*smiBin)
	env := map[string]string{}
	if *configPath != "" {
		env["MOCK_NVML_CONFIG"] = *configPath
	}
	if *overridePath != "" {
		env["MOCK_NVML_OVERRIDES"] = *overridePath
	}
	if len(env) != 0 {
		observer.Runner = control.EnvExecRunner{Values: env}
	}
	runtime := &Runtime{
		Client: client, Manager: control.NewManager(client, observer),
		Stdin: stdin, Stdout: stdout, Stderr: stderr,
	}
	return runtime.Execute(ctx, fs.Args())
}

// Execute runs one control operation without reparsing global flags. Scenario
// steps deliberately use this same grammar.
func (r *Runtime) Execute(ctx context.Context, args []string) error {
	if r == nil || r.Client == nil || r.Manager == nil {
		return errors.New("control runtime is not initialized")
	}
	if len(args) == 0 {
		return errors.New("control command is required")
	}
	switch args[0] {
	case "list":
		return r.list(ctx, args[1:])
	case "status":
		return r.status(ctx, args[1:])
	case "gpu":
		return r.gpu(ctx, args[1:])
	case "process":
		return r.process(ctx, args[1:])
	case "reset":
		target := "all"
		if len(args) > 2 {
			return errors.New("usage: reset [gpu]")
		}
		if len(args) == 2 {
			target = args[1]
		}
		return r.Client.Reset(ctx, target)
	case "scenario":
		return r.runScenario(ctx, args[1:])
	case "help", "-h", "--help":
		return usage(r.Stdout)
	default:
		return fmt.Errorf("unknown control command %q", args[0])
	}
}

func (r *Runtime) list(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return errors.New("usage: list")
	}
	devices, err := r.Manager.Snapshot(ctx)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(r.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(devices)
}

func (r *Runtime) status(ctx context.Context, args []string) error {
	if len(args) > 1 {
		return errors.New("usage: status [gpu-index]")
	}
	target := "all"
	if len(args) == 1 {
		target = args[0]
	}
	out, err := r.Client.Status(ctx, target)
	if err != nil {
		return err
	}
	_, err = r.Stdout.Write(out)
	return err
}

func (r *Runtime) gpu(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: gpu <index|uuid> <operation> ...")
	}
	target, op := args[0], args[1]
	rest := args[2:]
	switch op {
	case "utilization":
		value, err := onePercent(rest)
		if err != nil {
			return err
		}
		return r.Client.SetGPUUtilization(ctx, target, value)
	case "memory-utilization":
		value, err := onePercent(rest)
		if err != nil {
			return err
		}
		return r.Client.SetMemoryUtilization(ctx, target, value)
	case "memory":
		return r.gpuMemory(ctx, target, rest)
	case "failure":
		if len(rest) == 0 {
			return errors.New("usage: gpu <gpu> failure <mode> [--after-calls N] [--xid CODE]")
		}
		mode := rest[0]
		fs := flag.NewFlagSet("gpu failure", flag.ContinueOnError)
		fs.SetOutput(r.Stderr)
		afterCalls := fs.Int("after-calls", 0, "trip after N guarded calls")
		xid := fs.Uint64("xid", 0, "Xid code")
		if err := fs.Parse(rest[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("unexpected failure arguments: %s", strings.Join(fs.Args(), " "))
		}
		return r.Client.SetFailure(ctx, target, mode, *afterCalls, *xid)
	case "offline":
		if len(rest) != 0 {
			return errors.New("usage: gpu <gpu> offline")
		}
		return r.Client.SetFailure(ctx, target, "lost", 0, 0)
	case "online":
		if len(rest) != 0 {
			return errors.New("usage: gpu <gpu> online")
		}
		return r.Client.SetFailure(ctx, target, "healthy", 0, 0)
	default:
		return fmt.Errorf("unknown GPU operation %q", op)
	}
}

func (r *Runtime) gpuMemory(ctx context.Context, target string, args []string) error {
	if len(args) == 1 && strings.HasPrefix(args[0], "used=") {
		value, err := parseBytes(strings.TrimPrefix(args[0], "used="))
		if err != nil {
			return err
		}
		return r.Manager.SetUsedMemory(ctx, target, value)
	}
	if len(args) != 2 {
		return errors.New("usage: gpu <gpu> memory used=<size> | reserve <size> | release <size>")
	}
	value, err := parseBytes(args[1])
	if err != nil {
		return err
	}
	switch args[0] {
	case "reserve":
		return r.Manager.ReserveMemory(ctx, target, value)
	case "release":
		return r.Manager.ReleaseMemory(ctx, target, value)
	default:
		return fmt.Errorf("unknown memory operation %q", args[0])
	}
}

func (r *Runtime) process(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: process <add|update|remove> ...")
	}
	switch args[0] {
	case "add", "update":
		return r.upsertProcess(ctx, args[0], args[1:])
	case "remove":
		return r.removeProcess(ctx, args[1:])
	default:
		return fmt.Errorf("unknown process operation %q", args[0])
	}
}

func (r *Runtime) upsertProcess(ctx context.Context, mode string, args []string) error {
	fs := flag.NewFlagSet("process "+mode, flag.ContinueOnError)
	fs.SetOutput(r.Stderr)
	pid := fs.Uint("pid", 0, "process PID")
	gpu := fs.String("gpu", "", "GPU index or UUID")
	memory := fs.String("memory", "", "process VRAM, e.g. 8GiB")
	name := fs.String("name", "", "process name")
	processType := fs.String("type", "C", "process type C or G")
	sm := fs.Uint("sm", 0, "SM utilization percentage")
	memUtil := fs.Uint("mem-util", 0, "memory utilization percentage")
	enc := fs.Uint("enc", 0, "encoder utilization percentage")
	dec := fs.Uint("dec", 0, "decoder utilization percentage")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected process arguments: %s", strings.Join(fs.Args(), " "))
	}
	if *pid == 0 || uint64(*pid) > uint64(^uint32(0)) || *gpu == "" {
		return errors.New("--pid and --gpu are required")
	}
	if *processType != "C" && *processType != "G" {
		return errors.New("process type must be C or G")
	}
	device, err := r.Manager.Observer.Device(ctx, *gpu)
	if err != nil {
		return err
	}
	before := device
	before.Processes = append([]control.Process(nil), device.Processes...)
	index := -1
	for i := range device.Processes {
		if device.Processes[i].PID == uint32(*pid) {
			index = i
			break
		}
	}
	if mode == "add" && index >= 0 {
		return fmt.Errorf("process %d already exists on GPU %s", *pid, *gpu)
	}
	if mode == "update" && index < 0 {
		return fmt.Errorf("process %d does not exist on GPU %s", *pid, *gpu)
	}

	process := control.Process{PID: uint32(*pid), Type: *processType, Name: *name, SMUtil: uint32(*sm), MemoryUtil: uint32(*memUtil), EncoderUtil: uint32(*enc), DecoderUtil: uint32(*dec)}
	provided := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	if *memory != "" {
		memoryMiB, parseErr := parseMiBAligned(*memory)
		if parseErr != nil {
			return parseErr
		}
		process.UsedMemoryMiB = memoryMiB
	}
	if mode == "update" {
		process = device.Processes[index]
		if provided["memory"] {
			memoryMiB, parseErr := parseMiBAligned(*memory)
			if parseErr != nil {
				return parseErr
			}
			process.UsedMemoryMiB = memoryMiB
		}
		if provided["name"] {
			process.Name = *name
		}
		if provided["type"] {
			process.Type = *processType
		}
		if provided["sm"] {
			process.SMUtil = uint32(*sm)
		}
		if provided["mem-util"] {
			process.MemoryUtil = uint32(*memUtil)
		}
		if provided["enc"] {
			process.EncoderUtil = uint32(*enc)
		}
		if provided["dec"] {
			process.DecoderUtil = uint32(*dec)
		}
	}
	if *sm > 100 || *memUtil > 100 || *enc > 100 || *dec > 100 {
		return errors.New("process utilization values must be between 0 and 100")
	}
	if mode == "add" {
		device.Processes = append(device.Processes, process)
	} else {
		device.Processes[index] = process
	}
	return r.Manager.ReplaceProcessesFromState(ctx, *gpu, before, device.Processes)
}

func (r *Runtime) removeProcess(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("process remove", flag.ContinueOnError)
	fs.SetOutput(r.Stderr)
	pid := fs.Uint("pid", 0, "process PID")
	gpu := fs.String("gpu", "", "GPU index or UUID")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *pid == 0 || uint64(*pid) > uint64(^uint32(0)) || *gpu == "" {
		return errors.New("usage: process remove --pid <pid> --gpu <index|uuid>")
	}
	device, err := r.Manager.Observer.Device(ctx, *gpu)
	if err != nil {
		return err
	}
	filtered := make([]control.Process, 0, len(device.Processes))
	found := false
	for _, process := range device.Processes {
		if process.PID == uint32(*pid) {
			found = true
			continue
		}
		filtered = append(filtered, process)
	}
	if !found {
		return fmt.Errorf("process %d does not exist on GPU %s", *pid, *gpu)
	}
	return r.Manager.ReplaceProcessesFromState(ctx, *gpu, device, filtered)
}

func (r *Runtime) runScenario(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: scenario <run|render> ...")
	}
	switch args[0] {
	case "run":
		if len(args) != 2 {
			return errors.New("usage: scenario run <scenario.json>")
		}
		doc, err := loadScenario(args[1])
		if err != nil {
			return err
		}
		runner := scenario.Runner{Executor: scenarioExecutor{runtime: r}, Clock: scenario.RealClock{}, Events: scenario.NewLineEvents(r.Stdin)}
		return runner.Run(ctx, doc)
	case "render":
		fs := flag.NewFlagSet("scenario render", flag.ContinueOnError)
		fs.SetOutput(r.Stderr)
		output := fs.String("output", "-", "output path, or - for stdout")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("usage: scenario render [--output path] <scenario.json>")
		}
		doc, err := loadScenario(fs.Arg(0))
		if err != nil {
			return err
		}
		catalog, err := profiles.LoadCatalog()
		if err != nil {
			return err
		}
		data, err := scenario.RenderBase(catalog, doc)
		if err != nil {
			return err
		}
		if *output == "-" {
			_, err = r.Stdout.Write(data)
			return err
		}
		return os.WriteFile(*output, data, 0o644)
	default:
		return fmt.Errorf("unknown scenario operation %q", args[0])
	}
}

type scenarioExecutor struct{ runtime *Runtime }

func (s scenarioExecutor) Execute(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "scenario" {
		return errors.New("nested scenario execution is not allowed")
	}
	return s.runtime.Execute(ctx, args)
}

func loadScenario(path string) (scenario.Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return scenario.Document{}, err
	}
	defer f.Close()
	return scenario.Load(f)
}

func onePercent(args []string) (uint32, error) {
	if len(args) != 1 {
		return 0, errors.New("expected one utilization percentage")
	}
	value, err := strconv.ParseUint(args[0], 10, 32)
	if err != nil || value > 100 {
		return 0, fmt.Errorf("invalid utilization %q; expected 0-100", args[0])
	}
	return uint32(value), nil
}

func parseMiBAligned(raw string) (uint64, error) {
	bytes, err := parseBytes(raw)
	if err != nil {
		return 0, err
	}
	if bytes%mib != 0 {
		return 0, errors.New("process memory must be MiB-aligned")
	}
	return bytes / mib, nil
}

func parseBytes(raw string) (uint64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, errors.New("memory size is required")
	}
	units := []struct {
		suffix string
		mult   uint64
	}{
		{"GiB", 1024 * 1024 * 1024}, {"MiB", 1024 * 1024}, {"KiB", 1024},
		{"GB", 1000 * 1000 * 1000}, {"MB", 1000 * 1000}, {"KB", 1000}, {"B", 1},
	}
	multiplier := uint64(1)
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			multiplier = unit.mult
			value = strings.TrimSpace(strings.TrimSuffix(value, unit.suffix))
			break
		}
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory size %q", raw)
	}
	if multiplier != 0 && n > ^uint64(0)/multiplier {
		return 0, fmt.Errorf("memory size %q overflows bytes", raw)
	}
	return n * multiplier, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func usage(w io.Writer) error {
	_, err := fmt.Fprint(w, `fake-nvidia control/scenario UX

usage:
  fake-nvidia ctl [global flags] list
  fake-nvidia ctl [global flags] status [gpu-index]
  fake-nvidia ctl [global flags] gpu <gpu> utilization <0-100>
  fake-nvidia ctl [global flags] gpu <gpu> memory-utilization <0-100>
  fake-nvidia ctl [global flags] gpu <gpu> memory used=<size>
  fake-nvidia ctl [global flags] gpu <gpu> memory reserve <size>
  fake-nvidia ctl [global flags] gpu <gpu> memory release <size>
  fake-nvidia ctl [global flags] gpu <gpu> failure <mode> [--after-calls N] [--xid CODE]
  fake-nvidia ctl [global flags] gpu <gpu> offline|online
  fake-nvidia ctl [global flags] process add --pid N --gpu G [--memory 8GiB] [--name NAME] [--sm PCT]
  fake-nvidia ctl [global flags] process update --pid N --gpu G [fields...]
  fake-nvidia ctl [global flags] process remove --pid N --gpu G
  fake-nvidia ctl [global flags] reset [gpu]
  fake-nvidia ctl [global flags] scenario run <scenario.json>
  fake-nvidia ctl [global flags] scenario render [--output path] <scenario.json>

global flags:
  --ctl-bin PATH          nvml-mock-ctl binary (or FAKE_NVIDIA_CTL_BIN)
  --nvidia-smi-bin PATH   nvidia-smi binary (or FAKE_NVIDIA_SMI_BIN)
  --config PATH           base config (or MOCK_NVML_CONFIG)
  --overrides PATH        override file (or MOCK_NVML_OVERRIDES)

Scenario event steps consume newline-delimited event names from stdin. All state
mutations are delegated to nvml-mock-ctl; no fake-nvidia state daemon is used.
`)
	return err
}