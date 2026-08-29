package cuda

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type fakeBackend struct {
	mu      sync.Mutex
	devices []Device
	version int
}

// Devices returns a copy of the fake backend's current device state.
func (b *fakeBackend) Devices(context.Context) ([]Device, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Device, len(b.devices))
	copy(out, b.devices)
	return out, nil
}

// Reserve applies simulated allocation accounting to one fake device.
func (b *fakeBackend) Reserve(_ context.Context, index int, bytes uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.devices {
		if b.devices[i].Index != index {
			continue
		}
		if bytes > b.devices[i].FreeBytes {
			return ErrOutOfMemory
		}
		b.devices[i].FreeBytes -= bytes
		b.devices[i].UsedBytes += bytes
		return nil
	}
	return errors.New("device not found")
}

// Release reverses simulated allocation accounting on one fake device.
func (b *fakeBackend) Release(_ context.Context, index int, bytes uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.devices {
		if b.devices[i].Index != index {
			continue
		}
		if bytes > b.devices[i].UsedBytes {
			return errors.New("release exceeds used bytes")
		}
		b.devices[i].UsedBytes -= bytes
		b.devices[i].FreeBytes += bytes
		return nil
	}
	return errors.New("device not found")
}

// CUDAVersion returns the configured packed CUDA version for engine tests.
func (b *fakeBackend) CUDAVersion() int { return b.version }

// testBackend returns a deterministic two-device backend fixture.
func testBackend() *fakeBackend {
	return &fakeBackend{
		version: 13000,
		devices: []Device{
			{Index: 0, UUID: "GPU-00000000-0000-0000-0000-000000000001", Name: "GPU A", ComputeMajor: 8, ComputeMinor: 9, TotalBytes: 1024, UsedBytes: 128, FreeBytes: 896},
			{Index: 1, UUID: "GPU-00000000-0000-0000-0000-000000000002", Name: "GPU B", ComputeMajor: 9, ComputeMinor: 0, TotalBytes: 2048, UsedBytes: 256, FreeBytes: 1792},
		},
	}
}

// TestEngineDeviceEnumerationAndSelection verifies discovery, selection, and memory queries.
func TestEngineDeviceEnumerationAndSelection(t *testing.T) {
	backend := testBackend()
	engine, err := NewEngine(backend, FaultPolicy{OOMAfter: -1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if status := engine.Init(ctx); status != StatusSuccess {
		t.Fatalf("Init status=%v", status)
	}
	count, status := engine.DeviceCount(ctx)
	if status != StatusSuccess || count != 2 {
		t.Fatalf("DeviceCount=(%d,%v), want (2,success)", count, status)
	}
	info, status := engine.Device(ctx, 1)
	if status != StatusSuccess || info.Name != "GPU B" || info.ComputeMajor != 9 {
		t.Fatalf("Device(1)=(%+v,%v)", info, status)
	}
	if status := engine.SetDevice(ctx, 1); status != StatusSuccess {
		t.Fatalf("SetDevice status=%v", status)
	}
	current, status := engine.CurrentDevice()
	if status != StatusSuccess || current != 1 {
		t.Fatalf("CurrentDevice=(%d,%v)", current, status)
	}
	free, total, status := engine.MemoryInfo(ctx)
	if status != StatusSuccess || free != 1792 || total != 2048 {
		t.Fatalf("MemoryInfo=(%d,%d,%v)", free, total, status)
	}
	if status := engine.SetDevice(ctx, 8); status != StatusInvalidDevice {
		t.Fatalf("invalid SetDevice status=%v", status)
	}
}

// TestEngineAllocationAccountingAndCapacityOOM verifies allocation accounting and capacity OOM behavior.
func TestEngineAllocationAccountingAndCapacityOOM(t *testing.T) {
	backend := testBackend()
	engine, err := NewEngine(backend, FaultPolicy{OOMAfter: -1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if status := engine.TrackAllocation(ctx, 0x1000, 256); status != StatusSuccess {
		t.Fatalf("TrackAllocation status=%v", status)
	}
	free, _, status := engine.MemoryInfo(ctx)
	if status != StatusSuccess || free != 640 {
		t.Fatalf("free after allocation=(%d,%v), want 640", free, status)
	}
	if status := engine.TrackAllocation(ctx, 0x2000, 641); status != StatusOutOfMemory {
		t.Fatalf("oversized allocation status=%v", status)
	}
	if engine.AllocationCount() != 1 {
		t.Fatalf("allocation count=%d, want 1", engine.AllocationCount())
	}
	if status := engine.FreeAllocation(ctx, 0x1000); status != StatusSuccess {
		t.Fatalf("FreeAllocation status=%v", status)
	}
	free, _, _ = engine.MemoryInfo(ctx)
	if free != 896 {
		t.Fatalf("free after release=%d, want 896", free)
	}
	if status := engine.FreeAllocation(ctx, 0x1000); status != StatusInvalidValue {
		t.Fatalf("double-free status=%v", status)
	}
}

// TestEngineDeterministicInjectedOOM verifies the configured allocation-count failure threshold.
func TestEngineDeterministicInjectedOOM(t *testing.T) {
	backend := testBackend()
	engine, err := NewEngine(backend, FaultPolicy{OOMAfter: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if status := engine.TrackAllocation(ctx, 0x1000, 64); status != StatusSuccess {
		t.Fatalf("first allocation status=%v", status)
	}
	if status := engine.TrackAllocation(ctx, 0x2000, 64); status != StatusOutOfMemory {
		t.Fatalf("second allocation status=%v, want OOM", status)
	}
	free, _, _ := engine.MemoryInfo(ctx)
	if free != 832 {
		t.Fatalf("injected OOM changed free memory: got %d want 832", free)
	}
}

// TestEngineResetReleasesOnlyCurrentDevice verifies reset does not release another device's allocations.
func TestEngineResetReleasesOnlyCurrentDevice(t *testing.T) {
	backend := testBackend()
	engine, err := NewEngine(backend, FaultPolicy{OOMAfter: -1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if status := engine.TrackAllocation(ctx, 0x1000, 64); status != StatusSuccess {
		t.Fatal(status)
	}
	if status := engine.SetDevice(ctx, 1); status != StatusSuccess {
		t.Fatal(status)
	}
	if status := engine.TrackAllocation(ctx, 0x2000, 128); status != StatusSuccess {
		t.Fatal(status)
	}
	if status := engine.ResetDevice(ctx); status != StatusSuccess {
		t.Fatalf("ResetDevice status=%v", status)
	}
	if engine.AllocationCount() != 1 {
		t.Fatalf("allocation count=%d, want 1", engine.AllocationCount())
	}
	if _, ok := engine.Allocation(0x1000); !ok {
		t.Fatal("device 0 allocation was unexpectedly removed")
	}
	free, _, _ := engine.MemoryInfo(ctx)
	if free != 1792 {
		t.Fatalf("device 1 free after reset=%d, want 1792", free)
	}
}

// TestParseCUDAVersion verifies valid major.minor versions map to CUDA's packed representation.
func TestParseCUDAVersion(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  int
	}{
		{input: "13.0", want: 13000},
		{input: "12.8", want: 12080},
		{input: "11.4", want: 11040},
	} {
		got, err := parseCUDAVersion(tc.input)
		if err != nil || got != tc.want {
			t.Fatalf("parseCUDAVersion(%q)=(%d,%v), want %d", tc.input, got, err, tc.want)
		}
	}
}
