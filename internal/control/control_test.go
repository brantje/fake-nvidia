package control

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

type call struct {
	name string
	args []string
}
type fakeRunner struct {
	calls []call
	out   []byte
	err   error
}

// Run implements the corresponding fake-nvidia operation.
func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, call{name: name, args: append([]string(nil), args...)})
	return f.out, f.err
}

// clientWithFake implements the corresponding fake-nvidia operation.
func clientWithFake(f *fakeRunner) *Client {
	return &Client{Binary: "nvml-mock-ctl", ConfigFile: "/state/config.yaml", OverrideFile: "/state/overrides.yaml", Runner: f}
}

// TestSetMemoryDelegatesToUpstreamCtl verifies the corresponding behavior and regression contract.
func TestSetMemoryDelegatesToUpstreamCtl(t *testing.T) {
	f := &fakeRunner{}
	c := clientWithFake(f)
	if err := c.SetMemory(context.Background(), "0", 1024, 2048); err != nil {
		t.Fatal(err)
	}
	want := []string{"--file", "/state/overrides.yaml", "--config", "/state/config.yaml", "set", "--gpu", "0", "memory.used_bytes=1024", "memory.free_bytes=2048"}
	if !reflect.DeepEqual(f.calls[0].args, want) {
		t.Fatalf("args=%v want=%v", f.calls[0].args, want)
	}
}

// TestSetUtilizationDisablesDynamicMaskForSplitValues verifies the corresponding behavior and regression contract.
func TestSetUtilizationDisablesDynamicMaskForSplitValues(t *testing.T) {
	f := &fakeRunner{}
	c := clientWithFake(f)
	if err := c.SetUtilization(context.Background(), "all", 73, 21); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(f.calls[0].args, " ")
	for _, want := range []string{"set --gpu all", "utilization.gpu=73", "utilization.memory=21", "dynamic_metrics.utilization=null"} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %q missing %q", args, want)
		}
	}
}

// TestSetProcessesUsesUpstreamSetScalarParsing verifies the corresponding behavior and regression contract.
func TestSetProcessesUsesUpstreamSetScalarParsing(t *testing.T) {
	f := &fakeRunner{}
	c := clientWithFake(f)
	if err := c.SetProcesses(context.Background(), "GPU-abc", []Process{{PID: 42, Type: "C", Name: "llama-server", UsedMemoryMiB: 512, SMUtil: 67}}); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(f.calls[0].args, " ")
	if !strings.Contains(args, `processes=[{"pid":42,"type":"C","name":"llama-server","used_memory_mib":512,"sm_util":67}]`) {
		t.Fatalf("unexpected args: %s", args)
	}
}

// TestAllThenDeviceMutationsPreserveUpstreamPrecedenceTargets verifies the corresponding behavior and regression contract.
func TestAllThenDeviceMutationsPreserveUpstreamPrecedenceTargets(t *testing.T) {
	f := &fakeRunner{}
	c := clientWithFake(f)
	if err := c.SetUtilization(context.Background(), "all", 10, 10); err != nil {
		t.Fatal(err)
	}
	if err := c.SetUtilization(context.Background(), "1", 90, 90); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("calls=%d", len(f.calls))
	}
	if !strings.Contains(strings.Join(f.calls[0].args, " "), "util --gpu all 10") {
		t.Fatalf("all call=%v", f.calls[0].args)
	}
	if !strings.Contains(strings.Join(f.calls[1].args, " "), "util --gpu 1 90") {
		t.Fatalf("device call=%v", f.calls[1].args)
	}
}

// TestFailureAndReset verifies the corresponding behavior and regression contract.
func TestFailureAndReset(t *testing.T) {
	f := &fakeRunner{}
	c := clientWithFake(f)
	if err := c.SetFailure(context.Background(), "2", "fallen_off_bus", 12, 79); err != nil {
		t.Fatal(err)
	}
	if err := c.Reset(context.Background(), "2"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.calls[0].args, " "); !strings.Contains(got, "fail --gpu 2 --mode fallen_off_bus --after-calls 12 --xid 79") {
		t.Fatalf("got %q", got)
	}
	if got := strings.Join(f.calls[1].args, " "); !strings.Contains(got, "reset --gpu 2") {
		t.Fatalf("got %q", got)
	}
}

// TestStatusOmitsGPUForAll verifies the corresponding behavior and regression contract.
func TestStatusOmitsGPUForAll(t *testing.T) {
	f := &fakeRunner{out: []byte("ok")}
	c := clientWithFake(f)
	got, err := c.Status(context.Background(), "all")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ok" {
		t.Fatalf("output=%q", got)
	}
	want := []string{"--file", "/state/overrides.yaml", "--config", "/state/config.yaml", "status"}
	if !reflect.DeepEqual(f.calls[0].args, want) {
		t.Fatalf("args=%v want=%v", f.calls[0].args, want)
	}
}

// TestStatusKeepsGPUForSpecificTarget verifies the corresponding behavior and regression contract.
func TestStatusKeepsGPUForSpecificTarget(t *testing.T) {
	f := &fakeRunner{}
	c := clientWithFake(f)
	if _, err := c.Status(context.Background(), "2"); err != nil {
		t.Fatal(err)
	}
	want := []string{"--file", "/state/overrides.yaml", "--config", "/state/config.yaml", "status", "--gpu", "2"}
	if !reflect.DeepEqual(f.calls[0].args, want) {
		t.Fatalf("args=%v want=%v", f.calls[0].args, want)
	}
}
