package config

import (
	"strings"
	"testing"

	"github.com/brantje/fake-nvidia/profiles"
)

func testCatalog(t *testing.T) *profiles.Catalog {
	t.Helper()
	c, err := profiles.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

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

func TestValidationRejectsImpossibleMemory(t *testing.T) {
	_, err := Compose(testCatalog(t), Spec{Devices: []DeviceRequest{{Profile: "rtx4060ti-16gb", VRAMMiB: 512, UsedMiB: 300}}})
	if err == nil {
		t.Fatal("expected memory validation error")
	}
}
