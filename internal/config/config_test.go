package config

import (
	"strings"
	"testing"

	"github.com/brantje/fake-nvidia/profiles"
)

// testCatalog implements the corresponding fake-nvidia operation.
func testCatalog(t *testing.T) *profiles.Catalog {
	t.Helper()
	c, err := profiles.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestComposeSingleAndArbitraryVRAM verifies the corresponding behavior and regression contract.
func TestComposeSingleAndArbitraryVRAM(t *testing.T) {
	cfg, err := Compose(testCatalog(t), Spec{
		System:  System{DriverVersion: "580.173.02", CUDAVersion: "13.0"},
		Devices: []DeviceRequest{{Profile: "rtx4060ti-16gb", VRAMMiB: 12288, UsedMiB: 1024, GPUUtil: 17, MemoryUtil: 9}},
	})
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Devices[0]
	if d.Memory.TotalBytes != 12288*mib {
		t.Fatalf("total=%d", d.Memory.TotalBytes)
	}
	if d.Memory.FreeBytes != (12288-256-1024)*mib {
		t.Fatalf("free=%d", d.Memory.FreeBytes)
	}
	if d.UUID != "GPU-00000000-0000-0000-0000-000000000001" {
		t.Fatalf("uuid=%s", d.UUID)
	}
}

// TestComposeMultiAndStableIdentity verifies the corresponding behavior and regression contract.
func TestComposeMultiAndStableIdentity(t *testing.T) {
	devices, err := Repeated("rtx4060ti-16gb", 4, 16384)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Compose(testCatalog(t), Spec{Devices: devices})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compose(testCatalog(t), Spec{Devices: devices})
	if err != nil {
		t.Fatal(err)
	}
	for i := range first.Devices {
		if first.Devices[i].UUID != second.Devices[i].UUID || first.Devices[i].PCI.BusID != second.Devices[i].PCI.BusID {
			t.Fatalf("identity changed at %d", i)
		}
	}
}

// TestComposeMixedTopology verifies the corresponding behavior and regression contract.
func TestComposeMixedTopology(t *testing.T) {
	cfg, err := ComposeTopology(testCatalog(t), System{}, "mixed-gpu")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Devices) != 4 {
		t.Fatalf("devices=%d", len(cfg.Devices))
	}
	seen := map[string]bool{}
	for _, d := range cfg.Devices {
		seen[d.Name] = true
	}
	if len(seen) < 4 {
		t.Fatalf("expected mixed models, got %v", seen)
	}
}

// TestRenderUpstreamCompatibleFields verifies the corresponding behavior and regression contract.
func TestRenderUpstreamCompatibleFields(t *testing.T) {
	cfg, err := Compose(testCatalog(t), Spec{Devices: []DeviceRequest{{
		Profile: "t4-16gb", UsedMiB: 100,
		Processes: []Process{{PID: 1234, Type: "C", Name: "llama-server", UsedMemoryMiB: 100, SMUtil: 50, MemoryUtil: 20}},
		Failure:   &Failure{Mode: "lost", AfterCalls: 5, XID: 79},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	yaml, err := RenderYAML(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(yaml)
	for _, want := range []string{
		"version: \"1.0\"", "driver_version:", "cuda_version:", "num_devices: 1",
		"name: \"NVIDIA T4\"", "architecture: \"turing\"", "compute_capability:",
		"total_bytes:", "reserved_bytes:", "used_bytes:", "free_bytes:",
		"bus_id: \"0000:01:00.0\"", "utilization:", "temperature_gpu_c:", "current_draw_mw:",
		"processes:", "used_memory_mib: 100", "sm_util: 50", "failure:", "after_calls: 5", "code: 79",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("render missing %q\n%s", want, s)
		}
	}
}

// TestValidationRejectsImpossibleMemory verifies the corresponding behavior and regression contract.
func TestValidationRejectsImpossibleMemory(t *testing.T) {
	_, err := Compose(testCatalog(t), Spec{Devices: []DeviceRequest{{Profile: "rtx4060ti-16gb", VRAMMiB: 512, UsedMiB: 300}}})
	if err == nil {
		t.Fatal("expected memory validation error")
	}
}

// TestLoadSpecJSONExposesPerDeviceState verifies the corresponding behavior and regression contract.
func TestLoadSpecJSONExposesPerDeviceState(t *testing.T) {
	spec, err := LoadSpecJSON(strings.NewReader(`{
  "system":{"driver_version":"580.173.02","cuda_version":"13.0"},
  "devices":[{
    "profile":"rtx4090-24gb","vram_mib":20480,"used_mib":4096,
    "gpu_util":72,"memory_util":31,"temperature_c":49,"power_draw_mw":180000,
    "processes":[{"pid":99,"type":"C","name":"worker","used_memory_mib":2048,"sm_util":60}],
    "failure":{"mode":"lost","after_calls":7,"xid":79}
  }]
}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Compose(testCatalog(t), spec)
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Devices[0]
	if d.Memory.TotalBytes != 20480*mib || d.Memory.UsedBytes != 4096*mib || d.Utilization.GPU != 72 || d.Thermal.TemperatureGPUC != 49 {
		t.Fatalf("unexpected device: %+v", d)
	}
	if len(d.Processes) != 1 || d.Failure == nil || d.Failure.AfterCalls != 7 {
		t.Fatalf("state not loaded: %+v", d)
	}
}

// TestLoadSpecJSONRejectsUnknownFields verifies the corresponding behavior and regression contract.
func TestLoadSpecJSONRejectsUnknownFields(t *testing.T) {
	_, err := LoadSpecJSON(strings.NewReader(`{"devices":[],"typo":true}`))
	if err == nil {
		t.Fatal("expected unknown-field error")
	}
}

// TestComposeTopologyRejectsNilCatalog verifies the corresponding behavior and regression contract.
func TestComposeTopologyRejectsNilCatalog(t *testing.T) {
	_, err := ComposeTopology(nil, System{}, "mixed-gpu")
	if err == nil || !strings.Contains(err.Error(), "profile catalog is required") {
		t.Fatalf("err=%v", err)
	}
}

// TestComposeRejectsMoreThanMockNVMLLimit verifies the corresponding behavior and regression contract.
func TestComposeRejectsMoreThanMockNVMLLimit(t *testing.T) {
	devices, err := Repeated("rtx4060ti-16gb", maxDevices+1, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compose(testCatalog(t), Spec{Devices: devices})
	if err == nil || !strings.Contains(err.Error(), "exceeds Mock NVML limit") {
		t.Fatalf("err=%v", err)
	}
}

// TestRenderRejectsMoreThanMockNVMLLimit verifies the corresponding behavior and regression contract.
func TestRenderRejectsMoreThanMockNVMLLimit(t *testing.T) {
	cfg := MockConfig{Version: "1.0", Devices: make([]Device, maxDevices+1)}
	_, err := RenderYAML(cfg)
	if err == nil || !strings.Contains(err.Error(), "exceeds Mock NVML limit") {
		t.Fatalf("err=%v", err)
	}
}

// TestComposeRejectsMiBToByteOverflow verifies the corresponding behavior and regression contract.
func TestComposeRejectsMiBToByteOverflow(t *testing.T) {
	_, err := Compose(testCatalog(t), Spec{Devices: []DeviceRequest{{
		Profile: "rtx4060ti-16gb",
		VRAMMiB: maxMiB + 1,
	}}})
	if err == nil || !strings.Contains(err.Error(), "too large to convert") {
		t.Fatalf("err=%v", err)
	}
}

// TestComposeRejectsProcessMemoryAboveDeviceUsed verifies the corresponding behavior and regression contract.
func TestComposeRejectsProcessMemoryAboveDeviceUsed(t *testing.T) {
	_, err := Compose(testCatalog(t), Spec{Devices: []DeviceRequest{{
		Profile: "rtx4060ti-16gb",
		UsedMiB: 100,
		Processes: []Process{
			{PID: 1, Type: "C", UsedMemoryMiB: 60},
			{PID: 2, Type: "C", UsedMemoryMiB: 50},
		},
	}}})
	if err == nil || !strings.Contains(err.Error(), "process memory exceeds device used VRAM") {
		t.Fatalf("err=%v", err)
	}
}

// TestRenderIncludesDistinctMinorNumbers verifies the corresponding behavior and regression contract.
func TestRenderIncludesDistinctMinorNumbers(t *testing.T) {
	devices, err := Repeated("rtx4060ti-16gb", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Compose(testCatalog(t), Spec{Devices: devices})
	if err != nil {
		t.Fatal(err)
	}
	yaml, err := RenderYAML(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(yaml)
	for _, want := range []string{"  - index: 0\n    minor_number: 0", "  - index: 1\n    minor_number: 1"} {
		if !strings.Contains(s, want) {
			t.Fatalf("render missing %q\n%s", want, s)
		}
	}
}
