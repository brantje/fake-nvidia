package control

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestSetProcessesReconciledUpdatesProcessAndMemoryAtomically(t *testing.T) {
	f := &fakeRunner{}
	c := clientWithFake(f)
	total := uint64(10 * 1024 * 1024 * 1024)
	reserved := uint64(1024 * 1024 * 1024)
	nonProcess := uint64(512 * 1024 * 1024)
	processes := []Process{
		{PID: 101, Name: "worker-a", UsedMemoryMiB: 1024, SMUtil: 25},
		{PID: 102, Name: "worker-b", UsedMemoryMiB: 2048, SMUtil: 50},
	}
	if err := c.SetProcessesReconciled(context.Background(), "0", processes, total, reserved, nonProcess); err != nil {
		t.Fatal(err)
	}

	args := strings.Join(f.calls[0].args, " ")
	used := nonProcess + 3072*processMiB
	free := total - reserved - used
	for _, want := range []string{
		"set --gpu 0",
		`processes=[{"pid":101,"name":"worker-a","used_memory_mib":1024,"sm_util":25},{"pid":102,"name":"worker-b","used_memory_mib":2048,"sm_util":50}]`,
		"memory.used_bytes=" + strconv.FormatUint(used, 10),
		"memory.free_bytes=" + strconv.FormatUint(free, 10),
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %q missing %q", args, want)
		}
	}
}

func TestSetProcessesReconciledRemovalReleasesProcessOwnedMemory(t *testing.T) {
	f := &fakeRunner{}
	c := clientWithFake(f)
	total := uint64(8 * 1024 * 1024 * 1024)
	reserved := uint64(256 * 1024 * 1024)
	nonProcess := uint64(384 * 1024 * 1024)
	if err := c.SetProcessesReconciled(context.Background(), "0", nil, total, reserved, nonProcess); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(f.calls[0].args, " ")
	if !strings.Contains(args, "processes=[]") {
		t.Fatalf("process list was not cleared: %s", args)
	}
	if !strings.Contains(args, "memory.used_bytes="+strconv.FormatUint(nonProcess, 10)) {
		t.Fatalf("process memory was not released: %s", args)
	}
	if !strings.Contains(args, "memory.free_bytes="+strconv.FormatUint(total-reserved-nonProcess, 10)) {
		t.Fatalf("free memory was not reconciled: %s", args)
	}
}

func TestSetProcessesReconciledRejectsOvercommit(t *testing.T) {
	f := &fakeRunner{}
	c := clientWithFake(f)
	err := c.SetProcessesReconciled(context.Background(), "0", []Process{{PID: 1, UsedMemoryMiB: 1024}}, 1024*processMiB, 256*processMiB, 1)
	if err == nil {
		t.Fatal("expected overcommit error")
	}
	if len(f.calls) != 0 {
		t.Fatalf("unexpected upstream mutation: %v", f.calls)
	}
}
