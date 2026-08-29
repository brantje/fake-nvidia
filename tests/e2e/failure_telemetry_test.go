//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPhase8GPUOfflineAfterStartup(t *testing.T) {
	h := startManager(t, gpuScenario{profile: "rtx4060ti-16gb", count: 1, vramMiB: 16 * 1024})
	if snapshot := h.hardware(); len(snapshot.GPUs) != 1 {
		t.Fatalf("initial hardware=%+v", snapshot)
	}

	client := h.controlClient()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.SetFailure(ctx, "0", "lost", 0, 79); err != nil {
		t.Fatal(err)
	}
	waitHardwareStatus(t, h, http.StatusServiceUnavailable, 10*time.Second)

	if err := client.Reset(ctx, "0"); err != nil {
		t.Fatal(err)
	}
	recovered := pollHardware(t, h, 10*time.Second, func(snapshot hardwareSnapshot) bool {
		return len(snapshot.GPUs) == 1 && snapshot.GPUs[0].ID == "CUDA0"
	})
	if recovered.GPUs[0].ID != "CUDA0" {
		t.Fatalf("GPU did not recover after reset: %+v", recovered)
	}
}

func TestPhase8GPUQueryFailureIsVisibleAndRecoverable(t *testing.T) {
	h := startManager(t, gpuScenario{profile: "rtx4060ti-16gb", count: 1, vramMiB: 16 * 1024})
	if snapshot := h.hardware(); len(snapshot.GPUs) != 1 {
		t.Fatalf("initial hardware=%+v", snapshot)
	}

	path := filepath.Join(h.layout.RuntimeDir, "bin", "nvidia-smi")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 42\n"), info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	waitHardwareStatus(t, h, http.StatusServiceUnavailable, 10*time.Second)

	if err := os.WriteFile(path, original, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	recovered := pollHardware(t, h, 10*time.Second, func(snapshot hardwareSnapshot) bool {
		return len(snapshot.GPUs) == 1 && snapshot.GPUs[0].ID == "CUDA0"
	})
	if recovered.GPUs[0].ID != "CUDA0" {
		t.Fatalf("hardware probe did not recover after restoring nvidia-smi: %+v", recovered)
	}
}

func TestPhase8StartupTimeoutReleasesReservedVRAM(t *testing.T) {
	h := startManager(t, gpuScenario{
		profile: "rtx4060ti-16gb", count: 1, vramMiB: 16 * 1024,
		extraEnv: map[string]string{
			"LCM_STARTUP_TIMEOUT_SECONDS": "1",
			"FAKE_LLAMA_LOAD_DELAY":       "5s",
		},
	})
	baseline := h.hardware()
	modelID := h.createSparseModel("startup-timeout", 4*gib)
	instanceID := h.createInstance(modelID, "startup-timeout-worker", true)
	h.startInstance(instanceID, http.StatusServiceUnavailable)

	runtime := h.runtime(instanceID)
	if runtime.State != "FAILED" {
		t.Fatalf("runtime after startup timeout=%+v", runtime)
	}
	clean := pollHardware(t, h, 10*time.Second, func(snapshot hardwareSnapshot) bool {
		return len(snapshot.Processes) == 0 && len(snapshot.GPUs) == 1 && snapshot.GPUs[0].UsedBytes == baseline.GPUs[0].UsedBytes
	})
	if len(clean.Processes) != 0 {
		t.Fatalf("startup timeout leaked process state: %+v", clean.Processes)
	}
}

func TestPhase8MultipleInstancesMapToRequestedFakeDevices(t *testing.T) {
	h := startManager(t, gpuScenario{profile: "rtx4060ti-16gb", count: 2, vramMiB: 16 * 1024})
	modelID := h.createSparseModel("dual-worker", 4*gib)
	leftID := h.createManualInstance(modelID, "worker-cuda0", "CUDA0")
	rightID := h.createManualInstance(modelID, "worker-cuda1", "CUDA1")

	left := h.startInstance(leftID, http.StatusAccepted)
	right := h.startInstance(rightID, http.StatusAccepted)
	if left.State != "READY" || right.State != "READY" {
		t.Fatalf("workers not ready: left=%+v right=%+v", left, right)
	}

	snapshot := pollHardware(t, h, 10*time.Second, func(snapshot hardwareSnapshot) bool {
		seen := map[int]string{}
		for _, process := range snapshot.Processes {
			seen[process.PID] = process.DeviceID
		}
		return seen[left.PID] == "CUDA0" && seen[right.PID] == "CUDA1"
	})
	seen := map[int]hardwareProcess{}
	for _, process := range snapshot.Processes {
		seen[process.PID] = process
	}
	if seen[left.PID].UsedBytes != 4*gib || seen[right.PID].UsedBytes != 4*gib {
		t.Fatalf("unexpected per-instance VRAM: left=%+v right=%+v", seen[left.PID], seen[right.PID])
	}
}

func (h *managerHarness) createManualInstance(modelID, name, device string) string {
	h.t.Helper()
	var response instanceResponse
	h.requestJSON(http.MethodPost, "/api/v1/instances", map[string]any{
		"model_id":          modelID,
		"name":              name,
		"enabled":           true,
		"autoload_enabled":  true,
		"eviction_enabled":  true,
		"gpu_mode":          "manual",
		"gpu_devices":       []string{device},
		"request_log_mode":  "metadata",
	}, http.StatusCreated, &response)
	if response.ID == "" || response.ModelID != modelID {
		h.t.Fatalf("unexpected instance response: %+v", response)
	}
	return response.ID
}

func waitHardwareStatus(t *testing.T, h *managerHarness, wantStatus int, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastStatus int
	var lastBody []byte
	for time.Now().Before(deadline) {
		lastStatus, lastBody = h.rawRequest(http.MethodGet, "/api/v1/hardware", nil, true)
		if lastStatus == wantStatus {
			return lastBody
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("hardware status did not become %d; last status=%d body=%s", wantStatus, lastStatus, lastBody)
	return nil
}
