package control

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceProcessesFromStateRebasesConcurrentAddition(t *testing.T) {
	const gib = uint64(1024 * 1024 * 1024)
	before := DeviceState{
		Index:      0,
		UUID:       "GPU-a",
		TotalBytes: 16 * gib,
		UsedBytes:  2 * gib,
		FreeBytes:  14 * gib,
		Processes:  []Process{{PID: 100, Type: "C", Name: "existing", UsedMemoryMiB: 1024}},
	}
	desired := append([]Process(nil), before.Processes...)
	desired = append(desired, Process{PID: 200, Type: "C", Name: "ours", UsedMemoryMiB: 1024})

	observerRunner := &sequenceRunner{outputs: [][]byte{
		[]byte("0, GPU-a, Test GPU, 16384, 3072, 13312, 0, 0\n"),
		[]byte("100, GPU-a, 1024, existing\n300, GPU-a, 1024, concurrent\n"),
		nil,
	}}
	mutationRunner := &fakeRunner{}
	client := clientWithFake(mutationRunner)
	manager := NewManager(client, &Observer{Binary: "nvidia-smi", Runner: observerRunner})
	manager.MutationLockPath = filepath.Join(t.TempDir(), "mutation.lock")

	if err := manager.ReplaceProcessesFromState(context.Background(), "0", before, desired); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(mutationRunner.calls[0].args, " ")
	for _, want := range []string{
		`{"pid":100,"type":"C","name":"existing","used_memory_mib":1024}`,
		`{"pid":300,"name":"concurrent","used_memory_mib":1024}`,
		`{"pid":200,"type":"C","name":"ours","used_memory_mib":1024}`,
		"memory.used_bytes=4294967296",
		"memory.free_bytes=12884901888",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("args=%q missing %q", got, want)
		}
	}
}

func TestRebaseProcessesRejectsSamePIDConflict(t *testing.T) {
	before := []Process{{PID: 100, Type: "C", Name: "old", UsedMemoryMiB: 1024}}
	desired := []Process{{PID: 100, Type: "C", Name: "ours", UsedMemoryMiB: 1024}}
	current := []Process{{PID: 100, Type: "C", Name: "theirs", UsedMemoryMiB: 1024}}

	_, err := rebaseProcesses(before, desired, current)
	if err == nil || !strings.Contains(err.Error(), "changed concurrently") {
		t.Fatalf("err=%v, want concurrent-change error", err)
	}
}
