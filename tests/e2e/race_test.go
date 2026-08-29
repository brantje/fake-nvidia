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

func TestPhase8ResourceChangeBetweenPlanningAndLaunch(t *testing.T) {
	const gatePath = "/models/.phase8-register-gate"
	h := startManager(t, gpuScenario{
		profile: "rtx4060ti-16gb", count: 1, vramMiB: 16 * 1024,
		extraEnv: map[string]string{"FAKE_LLAMA_REGISTER_GATE": gatePath},
	})
	baseline := h.hardware()
	if len(baseline.GPUs) != 1 {
		t.Fatalf("baseline hardware=%+v", baseline)
	}

	modelID := h.createSparseModel("plan-launch-race", 8*gib)
	instanceID := h.createInstance(modelID, "plan-launch-race-worker", true)

	type startResult struct {
		status int
		body   []byte
	}
	started := make(chan startResult, 1)
	go func() {
		status, body := h.rawRequest(http.MethodPost, "/api/v1/instances/"+instanceID+"/start", nil, true)
		started <- startResult{status: status, body: body}
	}()

	// LOADING is emitted only after LlamaCPP-Manager has already planned GPU
	// placement and launched the worker process. The fake worker is held at the
	// register gate, so no fake process/VRAM reservation exists yet.
	pollRuntime(t, h, instanceID, 10*time.Second, func(runtime runtimeResponse) bool {
		return runtime.State == "LOADING"
	})
	if snapshot := h.hardware(); len(snapshot.Processes) != 0 {
		t.Fatalf("worker reserved resources before register gate release: %+v", snapshot.Processes)
	}

	gpu := baseline.GPUs[0]
	pressure := int64(10 * gib)
	if gpu.FreeBytes <= pressure {
		t.Fatalf("not enough free memory for deterministic race: %+v", gpu)
	}
	client := h.controlClient()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.SetMemory(ctx, "0", uint64(gpu.UsedBytes+pressure), uint64(gpu.FreeBytes-pressure)); err != nil {
		t.Fatal(err)
	}
	pollHardware(t, h, 10*time.Second, func(snapshot hardwareSnapshot) bool {
		return len(snapshot.GPUs) == 1 && snapshot.GPUs[0].FreeBytes == gpu.FreeBytes-pressure
	})

	// Release the worker only after the state mutation. Its reservation must now
	// fail against current fake NVML state rather than the manager's stale plan.
	if err := os.WriteFile(filepath.Join(h.modelsDir, filepath.Base(gatePath)), []byte("release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-started:
		if result.status != http.StatusServiceUnavailable {
			t.Fatalf("start status=%d want=%d body=%s", result.status, http.StatusServiceUnavailable, result.body)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("manager start request did not finish after register gate release")
	}

	after := pollHardware(t, h, 10*time.Second, func(snapshot hardwareSnapshot) bool {
		return len(snapshot.Processes) == 0 && len(snapshot.GPUs) == 1 && snapshot.GPUs[0].UsedBytes == gpu.UsedBytes+pressure
	})
	if len(after.Processes) != 0 {
		t.Fatalf("failed launch leaked fake process state: %+v", after.Processes)
	}
	if err := client.Reset(ctx, "0"); err != nil {
		t.Fatal(err)
	}
}
