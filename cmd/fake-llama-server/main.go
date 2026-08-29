package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/brantje/fake-nvidia/internal/fakellama"
)

const fakeLlamaServerVersion = "fake-llama-server 0.1.0 (fake-nvidia)"

const fakeLlamaServerHelp = `usage: fake-llama-server [options]

  -m, --model PATH            model path
      --host HOST             bind host
      --port N                bind port
  -c, --ctx-size N            context size
      --device DEVICES        comma-separated llama.cpp devices, for example CUDA0,CUDA1
      --main-gpu N            main GPU index
      --tensor-split SPLIT    comma-separated tensor split weights
      --fake-gpus DEVICES     fake-nvidia-only explicit device indices
      --fake-vram SIZE        fake-nvidia-only explicit VRAM reservation
      --fake-startup-fail     inject startup failure
      --fake-cuda-oom         inject CUDA out-of-memory failure
      --fake-crash-after-ready DURATION
                              crash after reaching ready state
      --fake-hang-shutdown    release GPU state but keep the process alive on shutdown
`

func main() {
	if handleInfoRequest(os.Args[1:], os.Stdout) {
		return
	}
	args, err := withManagerDeviceTargets(os.Args[1:], os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake-llama-server:", err)
		os.Exit(2)
	}
	cfg, err := fakellama.ParseConfig(args, os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake-llama-server:", err)
		os.Exit(2)
	}
	registry, err := fakellama.NewNVMLRegistryFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake-llama-server:", err)
		os.Exit(2)
	}
	server := fakellama.NewServer(cfg, registry, uint32(os.Getpid()), os.Stdout, os.Stderr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		for sig := range signals {
			if cfg.HangShutdown {
				fmt.Fprintf(os.Stderr, "fake-llama-server: injected shutdown hang after %s; GPU resources released while process remains alive\n", sig)
				_ = server.ReleaseResources(context.Background())
				continue
			}
			fmt.Fprintf(os.Stdout, "fake-llama-server: received %s, shutting down\n", sig)
			cancel()
			return
		}
	}()

	if err := server.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "fake-llama-server:", err)
		if errors.Is(err, fakellama.ErrInjectedCrash) {
			os.Exit(42)
		}
		os.Exit(1)
	}
}

// handleInfoRequest implements the side-effect-free llama-server probes used by
// LlamaCPP-Manager during startup discovery. These paths intentionally do not
// require a model or initialize the fake NVIDIA runtime.
func handleInfoRequest(args []string, out io.Writer) bool {
	for _, arg := range args {
		switch strings.TrimSpace(arg) {
		case "--version", "-version":
			fmt.Fprintln(out, fakeLlamaServerVersion)
			return true
		case "--help", "-h", "-help":
			fmt.Fprint(out, fakeLlamaServerHelp)
			return true
		}
	}
	return false
}

// withManagerDeviceTargets translates the manager-owned llama.cpp --device
// selection into fake-nvidia device indices. Explicit fake targets remain the
// highest-priority test control and are never overwritten by this translation.
func withManagerDeviceTargets(args []string, getenv func(string) string) ([]string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if strings.TrimSpace(getenv("FAKE_LLAMA_GPUS")) != "" || hasFakeGPUOverride(args) {
		return append([]string(nil), args...), nil
	}

	deviceValue := ""
	found := false
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if strings.HasPrefix(arg, "--device=") {
			deviceValue = strings.TrimSpace(strings.TrimPrefix(arg, "--device="))
			found = true
			continue
		}
		if arg != "--device" {
			continue
		}
		if i+1 >= len(args) {
			return nil, errors.New("--device requires a value")
		}
		deviceValue = strings.TrimSpace(args[i+1])
		found = true
		i++
	}
	if !found {
		return append([]string(nil), args...), nil
	}

	targets, err := normalizeManagerDevices(deviceValue)
	if err != nil {
		return nil, err
	}
	out := append([]string(nil), args...)
	out = append(out, "--fake-gpus="+strings.Join(targets, ","))
	return out, nil
}

func hasFakeGPUOverride(args []string) bool {
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "--fake-gpus" || strings.HasPrefix(arg, "--fake-gpus=") {
			return true
		}
	}
	return false
}

func normalizeManagerDevices(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	targets := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		device := strings.TrimSpace(part)
		if device == "" {
			return nil, fmt.Errorf("invalid --device value %q", raw)
		}
		upper := strings.ToUpper(device)
		indexText := device
		if strings.HasPrefix(upper, "CUDA") {
			indexText = device[len("CUDA"):]
		}
		index, err := strconv.Atoi(indexText)
		if err != nil || index < 0 {
			return nil, fmt.Errorf("unsupported fake NVIDIA device %q in --device %q", device, raw)
		}
		target := strconv.Itoa(index)
		if !seen[target] {
			targets = append(targets, target)
			seen[target] = true
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("invalid --device value %q", raw)
	}
	return targets, nil
}
