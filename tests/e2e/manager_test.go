//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brantje/fake-nvidia/internal/config"
	"github.com/brantje/fake-nvidia/internal/control"
	"github.com/brantje/fake-nvidia/internal/injection"
	"github.com/brantje/fake-nvidia/profiles"
)

const (
	managerRevision     = "0c26e8e19635c5047d06babc7ba3b0173570e6ce"
	defaultManagerImage = "ghcr.io/brantje/llamacpp-manager@sha256:1cbd6bf1d31893cdcdf6126e1e4239d39f1a903e4837251d0cd528d7a7a70586"
	mib                 = int64(1024 * 1024)
	gib                 = int64(1024 * 1024 * 1024)
)

var (
	pullOnce sync.Once
	pullErr  error
)

type gpuScenario struct {
	profile  string
	count    int
	vramMiB  uint64
	topology string
	noGPU    bool
	extraEnv map[string]string
}

type managerHarness struct {
	t          *testing.T
	container  string
	baseURL    string
	token      string
	modelsDir  string
	layout     *injection.Layout
	runtimeDir string
}

type hardwareSnapshot struct {
	GPUs      []hardwareGPU     `json:"gpus"`
	Processes []hardwareProcess `json:"processes"`
}

type hardwareGPU struct {
	ID             string  `json:"id"`
	Index          int     `json:"index"`
	UUID           string  `json:"uuid"`
	Name           string  `json:"name"`
	TotalBytes     int64   `json:"total_bytes"`
	UsedBytes      int64   `json:"used_bytes"`
	FreeBytes      int64   `json:"free_bytes"`
	UtilizationPct float64 `json:"utilization_pct"`
}

type hardwareProcess struct {
	PID         int    `json:"pid"`
	DeviceID    string `json:"device_id"`
	UsedBytes   int64  `json:"used_bytes"`
	ProcessName string `json:"process_name"`
}

type modelResponse struct {
	Model struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		GGUFPath   string `json:"gguf_path"`
		TotalBytes int64  `json:"total_bytes"`
	} `json:"model"`
}

type instanceResponse struct {
	ID              string   `json:"id"`
	ModelID         string   `json:"model_id"`
	Name            string   `json:"name"`
	EvictionEnabled bool     `json:"eviction_enabled"`
	GPUMode         string   `json:"gpu_mode"`
	GPUDevices      []string `json:"gpu_devices"`
}

type runtimeResponse struct {
	InstanceID string `json:"instance_id"`
	ModelID    string `json:"model_id"`
	State      string `json:"state"`
	PID        int    `json:"pid"`
	Port       int    `json:"port"`
	LastError  string `json:"last_error"`
}

func TestPhase8PublishedManagerRevisionIsPinned(t *testing.T) {
	if !strings.Contains(defaultManagerImage, "@sha256:") {
		t.Fatalf("Phase 8 default manager image must be pinned by digest, got %q", defaultManagerImage)
	}
	if managerRevision == "" {
		t.Fatal("manager revision is not recorded")
	}
}

func TestPhase8DiscoveryMatrix(t *testing.T) {
	cases := []struct {
		name       string
		scenario   gpuScenario
		wantTotals []uint64
	}{
		{name: "one-8GiB", scenario: gpuScenario{profile: "rtx4060ti-16gb", count: 1, vramMiB: 8 * 1024}, wantTotals: []uint64{8 * 1024}},
		{name: "one-16GiB", scenario: gpuScenario{profile: "rtx4060ti-16gb", count: 1, vramMiB: 16 * 1024}, wantTotals: []uint64{16 * 1024}},
		{name: "two-16GiB", scenario: gpuScenario{profile: "rtx4060ti-16gb", count: 2, vramMiB: 16 * 1024}, wantTotals: []uint64{16 * 1024, 16 * 1024}},
		{name: "four-24GiB", scenario: gpuScenario{profile: "rtx4090-24gb", count: 4, vramMiB: 24 * 1024}, wantTotals: []uint64{24 * 1024, 24 * 1024, 24 * 1024, 24 * 1024}},
		{name: "mixed", scenario: gpuScenario{topology: "mixed-gpu"}, wantTotals: []uint64{16 * 1024, 24 * 1024, 16 * 1024, 48 * 1024}},
		{name: "no-gpu", scenario: gpuScenario{noGPU: true}, wantTotals: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := startManager(t, tc.scenario)
			snapshot := h.hardware()
			if len(snapshot.GPUs) != len(tc.wantTotals) {
				t.Fatalf("manager discovered %d GPUs, want %d: %+v", len(snapshot.GPUs), len(tc.wantTotals), snapshot.GPUs)
			}
			for i, wantMiB := range tc.wantTotals {
				gpu := snapshot.GPUs[i]
				if gpu.ID != fmt.Sprintf("CUDA%d", i) || gpu.Index != i {
					t.Fatalf("GPU %d identity=%+v", i, gpu)
				}
				if gotMiB := uint64(gpu.TotalBytes / mib); gotMiB != wantMiB {
					t.Fatalf("GPU %d total=%d MiB want=%d MiB", i, gotMiB, wantMiB)
				}
				if strings.TrimSpace(gpu.UUID) == "" || strings.TrimSpace(gpu.Name) == "" {
					t.Fatalf("GPU %d missing stable identity: %+v", i, gpu)
				}
			}
		})
	}
}

func TestPhase8LifecycleTelemetryAndLiveMutation(t *testing.T) {
	h := startManager(t, gpuScenario{profile: "rtx4060ti-16gb", count: 1, vramMiB: 16 * 1024})
	baseline := h.hardware()
	if len(baseline.GPUs) != 1 {
		t.Fatalf("baseline GPUs=%+v", baseline.GPUs)
	}

	modelID := h.createSparseModel("telemetry", 4*gib)
	instanceID := h.createInstance(modelID, "telemetry-worker", true)
	started := h.startInstance(instanceID, http.StatusAccepted)
	if started.State != "READY" || started.PID <= 0 {
		t.Fatalf("runtime after start=%+v", started)
	}

	afterStart := pollHardware(t, h, 10*time.Second, func(snapshot hardwareSnapshot) bool {
		return len(snapshot.GPUs) == 1 && len(snapshot.Processes) == 1 && snapshot.Processes[0].PID == started.PID
	})
	process := afterStart.Processes[0]
	if process.DeviceID != "CUDA0" {
		t.Fatalf("process device=%q want CUDA0", process.DeviceID)
	}
	if process.UsedBytes != 4*gib {
		t.Fatalf("process used=%d want=%d", process.UsedBytes, 4*gib)
	}
	if got := afterStart.GPUs[0].UsedBytes - baseline.GPUs[0].UsedBytes; got != 4*gib {
		t.Fatalf("GPU used delta=%d want=%d", got, 4*gib)
	}

	client := h.controlClient()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.SetUtilization(ctx, "0", 77, 33); err != nil {
		t.Fatal(err)
	}
	mutated := pollHardware(t, h, 10*time.Second, func(snapshot hardwareSnapshot) bool {
		return len(snapshot.GPUs) == 1 && int(snapshot.GPUs[0].UtilizationPct) == 77
	})
	if int(mutated.GPUs[0].UtilizationPct) != 77 {
		t.Fatalf("manager did not observe live utilization mutation: %+v", mutated.GPUs[0])
	}

	h.stopInstance(instanceID, http.StatusNoContent)
	afterStop := pollHardware(t, h, 10*time.Second, func(snapshot hardwareSnapshot) bool {
		return len(snapshot.Processes) == 0 && len(snapshot.GPUs) == 1 && snapshot.GPUs[0].UsedBytes == baseline.GPUs[0].UsedBytes
	})
	if len(afterStop.Processes) != 0 {
		t.Fatalf("process state leaked after stop: %+v", afterStop.Processes)
	}
}

func TestPhase8MultiGPUPlacementUsesManagerDeviceSelection(t *testing.T) {
	h := startManager(t, gpuScenario{profile: "rtx4060ti-16gb", count: 2, vramMiB: 16 * 1024})
	modelID := h.createSparseModel("split", 20*gib)
	instanceID := h.createInstance(modelID, "split-worker", true)
	started := h.startInstance(instanceID, http.StatusAccepted)
	if started.State != "READY" {
		t.Fatalf("runtime=%+v", started)
	}

	snapshot := pollHardware(t, h, 10*time.Second, func(snapshot hardwareSnapshot) bool {
		if len(snapshot.Processes) != 2 {
			return false
		}
		devices := map[string]bool{}
		for _, process := range snapshot.Processes {
			if process.PID == started.PID {
				devices[process.DeviceID] = true
			}
		}
		return devices["CUDA0"] && devices["CUDA1"]
	})

	var total int64
	devices := map[string]bool{}
	for _, process := range snapshot.Processes {
		if process.PID != started.PID {
			continue
		}
		devices[process.DeviceID] = true
		total += process.UsedBytes
	}
	if !devices["CUDA0"] || !devices["CUDA1"] {
		t.Fatalf("manager-selected fake devices not both used: %+v", snapshot.Processes)
	}
	if delta := total - 20*gib; delta < -2*mib || delta > 2*mib {
		t.Fatalf("split process VRAM total=%d want approximately %d", total, 20*gib)
	}
}

func TestPhase8ExternalPressureAndEviction(t *testing.T) {
	t.Run("external-pressure", func(t *testing.T) {
		h := startManager(t, gpuScenario{profile: "rtx4060ti-16gb", count: 1, vramMiB: 16 * 1024})
		baseline := h.hardware()
		if len(baseline.GPUs) != 1 {
			t.Fatalf("baseline GPUs=%+v", baseline.GPUs)
		}
		gpu := baseline.GPUs[0]
		pressure := int64(8 * gib)
		if gpu.FreeBytes <= pressure {
			t.Fatalf("not enough baseline free memory for pressure scenario: %+v", gpu)
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

		modelID := h.createSparseModel("pressure", 10*gib)
		instanceID := h.createInstance(modelID, "pressure-worker", true)
		h.startInstance(instanceID, http.StatusServiceUnavailable)

		if err := client.Reset(ctx, "0"); err != nil {
			t.Fatal(err)
		}
		pollHardware(t, h, 10*time.Second, func(snapshot hardwareSnapshot) bool {
			return len(snapshot.GPUs) == 1 && snapshot.GPUs[0].FreeBytes == gpu.FreeBytes
		})
		started := h.startInstance(instanceID, http.StatusAccepted)
		if started.State != "READY" {
			t.Fatalf("worker did not start after external pressure was removed: %+v", started)
		}
	})

	t.Run("evict-eligible-worker", func(t *testing.T) {
		h := startManager(t, gpuScenario{profile: "rtx4060ti-16gb", count: 1, vramMiB: 16 * 1024})
		victimModel := h.createSparseModel("victim", 8*gib)
		victimID := h.createInstance(victimModel, "victim-worker", true)
		if runtime := h.startInstance(victimID, http.StatusAccepted); runtime.State != "READY" {
			t.Fatalf("victim runtime=%+v", runtime)
		}

		targetModel := h.createSparseModel("target", 12*gib)
		targetID := h.createInstance(targetModel, "target-worker", true)
		if runtime := h.startInstance(targetID, http.StatusAccepted); runtime.State != "READY" {
			t.Fatalf("target runtime=%+v", runtime)
		}
		victimRuntime := pollRuntime(t, h, victimID, 10*time.Second, func(runtime runtimeResponse) bool { return runtime.State == "UNLOADED" })
		if victimRuntime.State != "UNLOADED" {
			t.Fatalf("eligible victim was not evicted: %+v", victimRuntime)
		}
	})

	t.Run("protected-worker-blocks-eviction", func(t *testing.T) {
		h := startManager(t, gpuScenario{profile: "rtx4060ti-16gb", count: 1, vramMiB: 16 * 1024})
		victimModel := h.createSparseModel("protected", 8*gib)
		victimID := h.createInstance(victimModel, "protected-worker", false)
		if runtime := h.startInstance(victimID, http.StatusAccepted); runtime.State != "READY" {
			t.Fatalf("protected runtime=%+v", runtime)
		}

		targetModel := h.createSparseModel("blocked", 12*gib)
		targetID := h.createInstance(targetModel, "blocked-worker", true)
		h.startInstance(targetID, http.StatusServiceUnavailable)
		if runtime := h.runtime(victimID); runtime.State != "READY" {
			t.Fatalf("protected worker was evicted unexpectedly: %+v", runtime)
		}
	})
}

func TestPhase8FailurePathsReleaseSimulatorState(t *testing.T) {
	t.Run("cuda-oom", func(t *testing.T) {
		h := startManager(t, gpuScenario{
			profile: "rtx4060ti-16gb", count: 1, vramMiB: 16 * 1024,
			extraEnv: map[string]string{"FAKE_LLAMA_CUDA_OOM": "true"},
		})
		modelID := h.createSparseModel("oom", 4*gib)
		instanceID := h.createInstance(modelID, "oom-worker", true)
		h.startInstance(instanceID, http.StatusServiceUnavailable)
		snapshot := pollHardware(t, h, 10*time.Second, func(snapshot hardwareSnapshot) bool { return len(snapshot.Processes) == 0 })
		if len(snapshot.Processes) != 0 {
			t.Fatalf("OOM leaked process state: %+v", snapshot.Processes)
		}
	})

	t.Run("crash-after-ready", func(t *testing.T) {
		h := startManager(t, gpuScenario{
			profile: "rtx4060ti-16gb", count: 1, vramMiB: 16 * 1024,
			extraEnv: map[string]string{"FAKE_LLAMA_CRASH_AFTER_READY": "750ms"},
		})
		baseline := h.hardware()
		if len(baseline.GPUs) != 1 {
			t.Fatalf("baseline GPUs=%+v", baseline.GPUs)
		}
		modelID := h.createSparseModel("crash", 4*gib)
		instanceID := h.createInstance(modelID, "crash-worker", true)
		started := h.startInstance(instanceID, http.StatusAccepted)
		if started.State != "READY" {
			t.Fatalf("worker never reached ready before injected crash: %+v", started)
		}
		pollRuntime(t, h, instanceID, 10*time.Second, func(runtime runtimeResponse) bool { return runtime.State != "READY" })
		pollHardware(t, h, 10*time.Second, func(snapshot hardwareSnapshot) bool {
			return len(snapshot.Processes) == 0 && len(snapshot.GPUs) == 1 && snapshot.GPUs[0].UsedBytes == baseline.GPUs[0].UsedBytes
		})
	})
}

func startManager(t *testing.T, scenario gpuScenario) *managerHarness {
	t.Helper()
	ensureDocker(t)
	ensureManagerImage(t)

	repoRoot := repositoryRoot(t)
	runtimeDir := strings.TrimSpace(os.Getenv("FAKE_NVIDIA_RUNTIME_DIR"))
	if runtimeDir == "" {
		runtimeDir = filepath.Join(repoRoot, ".runtime")
	}
	if !filepath.IsAbs(runtimeDir) {
		runtimeDir = filepath.Join(repoRoot, runtimeDir)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "bin", "fake-llama-server")); err != nil {
		t.Fatalf("fake runtime missing fake-llama-server (%s): %v; run make runtime", runtimeDir, err)
	}

	workDir := t.TempDir()
	configDir := filepath.Join(workDir, "config")
	modelsDir := filepath.Join(workDir, "models")
	for _, dir := range []string{configDir, modelsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	h := &managerHarness{t: t, modelsDir: modelsDir, runtimeDir: runtimeDir}
	if !scenario.noGPU {
		layout := prepareInjection(t, runtimeDir, filepath.Join(workDir, "fake-nvidia"), scenario)
		h.layout = &layout
	}

	name := "fake-nvidia-phase8-" + sanitizeName(t.Name()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	h.container = name
	args := []string{
		"run", "--rm", "-d", "--name", name,
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"-p", "127.0.0.1::8000",
		"-e", "LCM_LISTEN_ADDR=:8000",
		"-e", "LCM_DATA_DIR=/config",
		"-e", "LCM_MODELS_DIR=/models",
		"-e", "LCM_STARTUP_TIMEOUT_SECONDS=5",
		"-v", configDir + ":/config",
		"-v", modelsDir + ":/models",
	}
	if h.layout != nil {
		args = append(args,
			"-e", "LCM_LLAMA_SERVER=/opt/fake-nvidia/runtime/bin/fake-llama-server",
			"-e", "PATH=/opt/fake-nvidia/runtime/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"-e", "LD_LIBRARY_PATH=/opt/fake-nvidia/runtime/lib",
			"-e", "MOCK_NVML_CONFIG=/var/lib/fake-nvidia/config.yaml",
			"-e", "MOCK_NVML_OVERRIDES=/var/lib/fake-nvidia/overrides.yaml",
			"-v", h.layout.RuntimeDir+":/opt/fake-nvidia/runtime:ro",
			"-v", h.layout.StateDir+":/var/lib/fake-nvidia",
		)
	} else {
		fakeServer := filepath.Join(runtimeDir, "bin", "fake-llama-server")
		args = append(args,
			"-e", "LCM_LLAMA_SERVER=/tmp/fake-llama-server",
			"-v", fakeServer+":/tmp/fake-llama-server:ro",
		)
	}
	keys := make([]string, 0, len(scenario.extraEnv))
	for key := range scenario.extraEnv {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "-e", key+"="+scenario.extraEnv[key])
	}
	args = append(args, managerImage())

	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("start manager container: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if t.Failed() {
			if logs, err := exec.Command("docker", "logs", name).CombinedOutput(); err == nil {
				t.Logf("LlamaCPP-Manager container logs:\n%s", logs)
			}
		}
		_, _ = exec.Command("docker", "rm", "-f", name).CombinedOutput()
	})

	portOutput, err := exec.Command("docker", "port", name, "8000/tcp").CombinedOutput()
	if err != nil {
		t.Fatalf("resolve manager port: %v: %s", err, portOutput)
	}
	endpoint := strings.TrimSpace(string(portOutput))
	if index := strings.LastIndex(endpoint, ":"); index >= 0 {
		endpoint = endpoint[index+1:]
	}
	if _, err := strconv.Atoi(endpoint); err != nil {
		t.Fatalf("unexpected docker port output %q", portOutput)
	}
	h.baseURL = "http://127.0.0.1:" + endpoint
	h.waitHealthy()
	h.bootstrapAndLogin()
	return h
}

func prepareInjection(t *testing.T, runtimeDir, root string, scenario gpuScenario) injection.Layout {
	t.Helper()
	catalog, err := profiles.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	system := config.System{DriverVersion: "580.173.02", CUDAVersion: "13.0"}
	var cfg config.MockConfig
	if scenario.topology != "" {
		cfg, err = config.ComposeTopology(catalog, system, scenario.topology)
	} else {
		count := scenario.count
		if count == 0 {
			count = 1
		}
		devices, repeatErr := config.Repeated(scenario.profile, count, scenario.vramMiB)
		if repeatErr != nil {
			t.Fatal(repeatErr)
		}
		cfg, err = config.Compose(catalog, config.Spec{System: system, Devices: devices})
	}
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := config.RenderYAML(cfg)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := injection.Prepare(root, runtimeDir, rendered)
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

func (h *managerHarness) waitHealthy() {
	h.t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		resp, err := http.Get(h.baseURL + "/health")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			last = fmt.Sprintf("status=%d body=%s", resp.StatusCode, body)
			if resp.StatusCode == http.StatusOK {
				return
			}
		} else {
			last = err.Error()
		}
		time.Sleep(250 * time.Millisecond)
	}
	logs, _ := exec.Command("docker", "logs", h.container).CombinedOutput()
	h.t.Fatalf("manager did not become healthy: %s\n%s", last, logs)
}

func (h *managerHarness) bootstrapAndLogin() {
	h.t.Helper()
	credentials := map[string]string{"username": "phase8-admin", "password": "phase8-test-password"}
	status, body := h.rawRequest(http.MethodPost, "/api/v1/auth/bootstrap", credentials, false)
	if status != http.StatusCreated {
		h.t.Fatalf("bootstrap status=%d body=%s", status, body)
	}
	status, body = h.rawRequest(http.MethodPost, "/api/v1/auth/login", credentials, false)
	if status != http.StatusOK {
		h.t.Fatalf("login status=%d body=%s", status, body)
	}
	var login struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &login); err != nil {
		h.t.Fatalf("decode login: %v: %s", err, body)
	}
	if login.AccessToken == "" {
		h.t.Fatalf("login returned empty access token: %s", body)
	}
	h.token = login.AccessToken
}

func (h *managerHarness) hardware() hardwareSnapshot {
	h.t.Helper()
	var snapshot hardwareSnapshot
	h.requestJSON(http.MethodGet, "/api/v1/hardware", nil, http.StatusOK, &snapshot)
	return snapshot
}

func (h *managerHarness) createSparseModel(name string, size int64) string {
	h.t.Helper()
	fileName := sanitizeName(name) + ".gguf"
	path := filepath.Join(h.modelsDir, fileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := file.Truncate(size); err != nil {
		file.Close()
		h.t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		h.t.Fatal(err)
	}
	var response modelResponse
	h.requestJSON(http.MethodPost, "/api/v1/models", map[string]any{
		"name": name, "gguf_path": fileName,
	}, http.StatusCreated, &response)
	if response.Model.ID == "" || response.Model.TotalBytes != size {
		h.t.Fatalf("unexpected model response: %+v", response.Model)
	}
	return response.Model.ID
}

func (h *managerHarness) createInstance(modelID, name string, evictionEnabled bool) string {
	h.t.Helper()
	var response instanceResponse
	h.requestJSON(http.MethodPost, "/api/v1/instances", map[string]any{
		"model_id":         modelID,
		"name":             name,
		"enabled":          true,
		"autoload_enabled": true,
		"eviction_enabled": evictionEnabled,
		"gpu_mode":         "auto",
	}, http.StatusCreated, &response)
	if response.ID == "" || response.ModelID != modelID {
		h.t.Fatalf("unexpected instance response: %+v", response)
	}
	return response.ID
}

func (h *managerHarness) startInstance(instanceID string, wantStatus int) runtimeResponse {
	h.t.Helper()
	status, body := h.rawRequest(http.MethodPost, "/api/v1/instances/"+instanceID+"/start", nil, true)
	if status != wantStatus {
		h.t.Fatalf("start %s status=%d want=%d body=%s", instanceID, status, wantStatus, body)
	}
	var runtime runtimeResponse
	if status >= 200 && status < 300 && len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &runtime); err != nil {
			h.t.Fatalf("decode start runtime: %v: %s", err, body)
		}
	}
	return runtime
}

func (h *managerHarness) stopInstance(instanceID string, wantStatus int) {
	h.t.Helper()
	status, body := h.rawRequest(http.MethodPost, "/api/v1/instances/"+instanceID+"/stop", nil, true)
	if status != wantStatus {
		h.t.Fatalf("stop %s status=%d want=%d body=%s", instanceID, status, wantStatus, body)
	}
}

func (h *managerHarness) runtime(instanceID string) runtimeResponse {
	h.t.Helper()
	var runtime runtimeResponse
	h.requestJSON(http.MethodGet, "/api/v1/instances/"+instanceID+"/runtime", nil, http.StatusOK, &runtime)
	return runtime
}

func (h *managerHarness) controlClient() *control.Client {
	h.t.Helper()
	if h.layout == nil {
		h.t.Fatal("control client requested for no-GPU harness")
	}
	return control.New(filepath.Join(h.layout.RuntimeDir, "bin", "nvml-mock-ctl"), h.layout.ConfigPath, h.layout.OverridesPath)
}

func (h *managerHarness) requestJSON(method, path string, request any, wantStatus int, response any) {
	h.t.Helper()
	status, body := h.rawRequest(method, path, request, true)
	if status != wantStatus {
		h.t.Fatalf("%s %s status=%d want=%d body=%s", method, path, status, wantStatus, body)
	}
	if response != nil && len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, response); err != nil {
			h.t.Fatalf("decode %s %s response: %v: %s", method, path, err, body)
		}
	}
}

func (h *managerHarness) rawRequest(method, path string, payload any, authenticated bool) (int, []byte) {
	h.t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			h.t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, h.baseURL+path, body)
	if err != nil {
		h.t.Fatal(err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authenticated && h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	return resp.StatusCode, data
}

func pollHardware(t *testing.T, h *managerHarness, timeout time.Duration, predicate func(hardwareSnapshot) bool) hardwareSnapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last hardwareSnapshot
	for time.Now().Before(deadline) {
		last = h.hardware()
		if predicate(last) {
			return last
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("hardware predicate did not become true; last=%+v", last)
	return last
}

func pollRuntime(t *testing.T, h *managerHarness, instanceID string, timeout time.Duration, predicate func(runtimeResponse) bool) runtimeResponse {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last runtimeResponse
	for time.Now().Before(deadline) {
		last = h.runtime(instanceID)
		if predicate(last) {
			return last
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("runtime predicate did not become true; last=%+v", last)
	return last
}

func ensureDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is required for Phase 8 E2E tests")
	}
	if out, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		t.Skipf("docker daemon unavailable: %v: %s", err, out)
	}
}

func ensureManagerImage(t *testing.T) {
	t.Helper()
	pullOnce.Do(func() {
		out, err := exec.Command("docker", "pull", managerImage()).CombinedOutput()
		if err != nil {
			pullErr = fmt.Errorf("docker pull %s: %w: %s", managerImage(), err, out)
		}
	})
	if pullErr != nil {
		t.Fatal(pullErr)
	}
}

func managerImage() string {
	if image := strings.TrimSpace(os.Getenv("LLAMACPP_MANAGER_IMAGE")); image != "" {
		return image
	}
	return defaultManagerImage
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func sanitizeName(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if b.Len() > 0 && !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
