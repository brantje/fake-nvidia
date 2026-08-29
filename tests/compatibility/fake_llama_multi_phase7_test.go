//go:build integration

package compatibility

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/brantje/fake-nvidia/internal/config"
	"github.com/brantje/fake-nvidia/internal/runtimebundle"
)

type runningFakeLlama struct {
	cmd  *exec.Cmd
	logs bytes.Buffer
	base string
}

// TestPhase7MultipleFakeLlamaServers verifies multiple real child processes can
// share one fake GPU while another runs independently on a different fake GPU.
func TestPhase7MultipleFakeLlamaServers(t *testing.T) {
	catalog := loadCatalog(t)
	cfg := compose(t, catalog, config.Spec{Devices: []config.DeviceRequest{
		{Profile: "rtx4090-24gb"},
		{Profile: "rtx4060ti-16gb"},
	}})
	bundle := requireBundle(t)
	configPath, overridesPath := writeConfig(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	first := startFakeLlama(t, ctx, bundle, configPath, overridesPath, "0", "32MiB")
	second := startFakeLlama(t, ctx, bundle, configPath, overridesPath, "0", "48MiB")
	third := startFakeLlama(t, ctx, bundle, configPath, overridesPath, "1", "64MiB")
	defer stopFakeLlamaBestEffort(first)
	defer stopFakeLlamaBestEffort(second)
	defer stopFakeLlamaBestEffort(third)

	rows := queryGPUs(t, bundle, configPath, overridesPath, false)
	if got := strings.TrimSpace(rows[0][4]); got != "80" {
		t.Fatalf("GPU0 memory.used=%q want 80; row=%v", got, rows[0])
	}
	if got := strings.TrimSpace(rows[1][4]); got != "64" {
		t.Fatalf("GPU1 memory.used=%q want 64; row=%v", got, rows[1])
	}
	processes := queryComputeApps(t, bundle, configPath, overridesPath)
	if len(processes) != 3 {
		t.Fatalf("compute process rows=%v want 3 processes", processes)
	}
	for _, server := range []*runningFakeLlama{first, second, third} {
		if !containsProcessPID(processes, server.cmd.Process.Pid) {
			t.Fatalf("process rows=%v missing pid %d", processes, server.cmd.Process.Pid)
		}
	}

	stopFakeLlama(t, second)
	second = nil
	rows = queryGPUs(t, bundle, configPath, overridesPath, false)
	if got := strings.TrimSpace(rows[0][4]); got != "32" {
		t.Fatalf("GPU0 memory.used after one shared worker stops=%q want 32; row=%v", got, rows[0])
	}
	if got := strings.TrimSpace(rows[1][4]); got != "64" {
		t.Fatalf("GPU1 memory.used changed unexpectedly=%q want 64; row=%v", got, rows[1])
	}
	if processes := queryComputeApps(t, bundle, configPath, overridesPath); len(processes) != 2 {
		t.Fatalf("process rows after one stop=%v want 2", processes)
	}

	stopFakeLlama(t, first)
	first = nil
	stopFakeLlama(t, third)
	third = nil
	rows = queryGPUs(t, bundle, configPath, overridesPath, false)
	if strings.TrimSpace(rows[0][4]) != "0" || strings.TrimSpace(rows[1][4]) != "0" {
		t.Fatalf("GPU memory not fully released: %v", rows)
	}
	if processes := queryComputeApps(t, bundle, configPath, overridesPath); len(processes) != 0 {
		t.Fatalf("process rows remained after all stops: %v", processes)
	}
}

func startFakeLlama(t *testing.T, ctx context.Context, bundle runtimebundle.Bundle, configPath, overridesPath, gpu, vram string) *runningFakeLlama {
	t.Helper()
	port := freeTCPPort(t)
	server := &runningFakeLlama{}
	server.cmd = exec.CommandContext(ctx, bundle.FakeLlamaServer(),
		"--model", "/models/multi.gguf",
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--fake-gpus", gpu,
		"--fake-vram", vram,
	)
	server.cmd.Env = bundle.Environment(os.Environ(), configPath, overridesPath)
	server.cmd.Stdout = &server.logs
	server.cmd.Stderr = &server.logs
	if err := server.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	server.base = fmt.Sprintf("http://127.0.0.1:%d", port)
	waitPhase7Ready(t, ctx, server.base, &server.logs)
	return server
}

func stopFakeLlama(t *testing.T, server *runningFakeLlama) {
	t.Helper()
	if server == nil || server.cmd == nil || server.cmd.Process == nil {
		return
	}
	if err := server.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("fake llama-server pid %d exit: %v logs=%s", server.cmd.Process.Pid, err, server.logs.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("fake llama-server pid %d did not stop; logs=%s", server.cmd.Process.Pid, server.logs.String())
	}
	server.cmd = nil
}

func stopFakeLlamaBestEffort(server *runningFakeLlama) {
	if server == nil || server.cmd == nil || server.cmd.Process == nil {
		return
	}
	_ = server.cmd.Process.Kill()
	_, _ = server.cmd.Process.Wait()
	server.cmd = nil
}

func containsProcessPID(rows [][]string, pid int) bool {
	want := strconv.Itoa(pid)
	for _, row := range rows {
		if len(row) > 0 && strings.TrimSpace(row[0]) == want {
			return true
		}
	}
	return false
}
