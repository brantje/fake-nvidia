//go:build integration

package compatibility

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/brantje/fake-nvidia/internal/config"
)

// TestPhase7FakeLlamaServerLifecycle proves the packaged companion binary uses
// a real PID, shared NVML VRAM/process accounting, manager readiness, inference,
// streaming, and cleanup on SIGTERM.
func TestPhase7FakeLlamaServerLifecycle(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{{Profile: "rtx4090-24gb"}}})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := startFakeLlamaWithArgs(t, ctx, bundle, configPath, overridesPath,
		"--fake-vram", "64MiB",
		"--fake-response", "phase seven fake response",
		"--threads", "8",
		"--gpu-layers", "99",
	)
	defer stopFakeLlamaBestEffort(server)
	pid := server.cmd.Process.Pid
	base := server.base

	rows := queryGPUs(t, bundle, configPath, overridesPath, false)
	if got := strings.TrimSpace(rows[0][4]); got != "64" {
		t.Fatalf("memory.used=%q want 64; row=%v logs=%s", got, rows[0], server.logs.String())
	}
	processes := queryComputeApps(t, bundle, configPath, overridesPath)
	if len(processes) != 1 || strings.TrimSpace(processes[0][0]) != strconv.Itoa(pid) || strings.TrimSpace(processes[0][3]) != "fake-llama-server" {
		t.Fatalf("compute processes=%v pid=%d logs=%s", processes, pid, server.logs.String())
	}
	out, err := runSMI(bundle, configPath, overridesPath, "pmon", "-c", "1", "-s", "u")
	if err != nil {
		t.Fatalf("pmon: %v\n%s", err, out)
	}
	if got := parseManagerPMon(t, out)[processKey{pid: pid, deviceID: "CUDA0"}]; got != 35 {
		t.Fatalf("pmon utilization=%v want 35\n%s", got, out)
	}

	response, err := http.Post(base+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"instance-phase7","messages":[{"role":"user","content":"hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "phase seven fake response") {
		t.Fatalf("chat status=%d body=%s", response.StatusCode, body)
	}

	response, err = http.Post(base+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"instance-phase7","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(body), "data: [DONE]") {
		t.Fatalf("stream missing completion marker: %s", body)
	}

	stopFakeLlama(t, server)
	rows = queryGPUs(t, bundle, configPath, overridesPath, false)
	if got := strings.TrimSpace(rows[0][4]); got != "0" {
		t.Fatalf("memory.used after stop=%q want 0; row=%v logs=%s", got, rows[0], server.logs.String())
	}
	if processes := queryComputeApps(t, bundle, configPath, overridesPath); len(processes) != 0 {
		t.Fatalf("fake process remained after stop: %v", processes)
	}
}

// TestPhase7FakeLlamaServerInjectedOOM verifies the packaged process exits
// deterministically without mutating NVML state when load OOM is requested.
func TestPhase7FakeLlamaServerInjectedOOM(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{{Profile: "rtx4060ti-16gb"}}})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bundle.FakeLlamaServer(),
		"--model", "/models/fake-oom.gguf",
		"--host", "127.0.0.1",
		"--port", "0",
		"--fake-vram", "1GiB",
		"--fake-cuda-oom",
	)
	cmd.Env = bundle.Environment(os.Environ(), configPath, overridesPath)
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("injected OOM process hung until timeout: %s", out)
	}
	if err == nil {
		t.Fatalf("injected OOM unexpectedly succeeded: %s", out)
	}
	if !strings.Contains(strings.ToLower(string(out)), "out of memory") {
		t.Fatalf("injected OOM output is unclear: %s", out)
	}
	rows := queryGPUs(t, bundle, configPath, overridesPath, false)
	if got := strings.TrimSpace(rows[0][4]); got != "0" {
		t.Fatalf("injected OOM changed memory.used=%q row=%v", got, rows[0])
	}
	if processes := queryComputeApps(t, bundle, configPath, overridesPath); len(processes) != 0 {
		t.Fatalf("injected OOM registered a process: %v", processes)
	}
}

// pickFreeTCPPort returns a candidate loopback port. The listener is closed
// before return, so callers must tolerate a later bind race.
func pickFreeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
