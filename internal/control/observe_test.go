package control

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

type sequenceRunner struct {
	outputs [][]byte
	calls   []call
}

func (s *sequenceRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	s.calls = append(s.calls, call{name: name, args: append([]string(nil), args...)})
	if len(s.outputs) == 0 {
		return nil, nil
	}
	out := s.outputs[0]
	s.outputs = s.outputs[1:]
	return out, nil
}

func TestObserverSnapshotCombinesGPUProcessAndPmonState(t *testing.T) {
	runner := &sequenceRunner{outputs: [][]byte{
		[]byte("0, GPU-a, NVIDIA RTX 4090, 24576, 3072, 21504, 44, 12\n"),
		[]byte("123, GPU-a, 1024, llama-server\n"),
		[]byte("# gpu pid type sm mem enc dec command\n0 123 C 67 21 0 0 llama-server\n"),
	}}
	observer := &Observer{Binary: "nvidia-smi", Runner: runner}
	devices, err := observer.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("devices=%d", len(devices))
	}
	device := devices[0]
	if device.Index != 0 || device.UUID != "GPU-a" || device.UsedBytes != 3*1024*1024*1024 || device.FreeBytes != 21*1024*1024*1024 {
		t.Fatalf("device=%+v", device)
	}
	wantProcess := Process{PID: 123, Type: "C", Name: "llama-server", UsedMemoryMiB: 1024, SMUtil: 67, MemoryUtil: 21}
	if !reflect.DeepEqual(device.Processes, []Process{wantProcess}) {
		t.Fatalf("processes=%+v want=%+v", device.Processes, wantProcess)
	}
}

func TestManagerReserveMemoryPreservesVisiblePool(t *testing.T) {
	observerRunner := &sequenceRunner{outputs: [][]byte{
		[]byte("0, GPU-a, Test GPU, 16384, 2048, 14336, 0, 0\n"), nil, nil,
	}}
	mutationRunner := &fakeRunner{}
	client := clientWithFake(mutationRunner)
	manager := NewManager(client, &Observer{Binary: "nvidia-smi", Runner: observerRunner})
	if err := manager.ReserveMemory(context.Background(), "0", 1024*1024*1024); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(mutationRunner.calls[0].args, " ")
	for _, want := range []string{"memory.used_bytes=3221225472", "memory.free_bytes=13958643712"} {
		if !strings.Contains(got, want) {
			t.Fatalf("args=%q missing %q", got, want)
		}
	}
}

func TestManagerReplaceProcessesPreservesNonProcessUsage(t *testing.T) {
	observerRunner := &sequenceRunner{outputs: [][]byte{
		[]byte("0, GPU-a, Test GPU, 16384, 3072, 13312, 0, 0\n"),
		[]byte("100, GPU-a, 1024, old\n"),
		nil,
	}}
	mutationRunner := &fakeRunner{}
	client := clientWithFake(mutationRunner)
	manager := NewManager(client, &Observer{Binary: "nvidia-smi", Runner: observerRunner})
	processes := []Process{{PID: 200, Type: "C", Name: "new", UsedMemoryMiB: 2048}}
	if err := manager.ReplaceProcesses(context.Background(), "0", processes); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(mutationRunner.calls[0].args, " ")
	for _, want := range []string{
		`processes=[{"pid":200,"type":"C","name":"new","used_memory_mib":2048}]`,
		"memory.used_bytes=4294967296",
		"memory.free_bytes=12884901888",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("args=%q missing %q", got, want)
		}
	}
}
