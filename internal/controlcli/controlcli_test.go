package controlcli

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/brantje/fake-nvidia/internal/control"
)

type call struct {
	name string
	args []string
}

type recordingRunner struct {
	calls   []call
	outputs [][]byte
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, call{name: name, args: append([]string(nil), args...)})
	if len(r.outputs) == 0 {
		return nil, nil
	}
	out := r.outputs[0]
	r.outputs = r.outputs[1:]
	return out, nil
}

func TestGPUUtilizationDelegatesToUpstreamControl(t *testing.T) {
	mutations := &recordingRunner{}
	client := &control.Client{Binary: "nvml-mock-ctl", Runner: mutations}
	runtime := &Runtime{Client: client, Manager: control.NewManager(client, &control.Observer{}), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	if err := runtime.Execute(context.Background(), []string{"gpu", "0", "utilization", "90"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"set", "--gpu", "0", "utilization.gpu=90", "dynamic_metrics.utilization=null"}
	if !reflect.DeepEqual(mutations.calls[0].args, want) {
		t.Fatalf("args=%v want=%v", mutations.calls[0].args, want)
	}
}

func TestProcessAddReconcilesVRAMFromOneSnapshot(t *testing.T) {
	observer := &recordingRunner{outputs: [][]byte{
		[]byte("0, GPU-a, Test GPU, 16384, 2048, 14336, 0, 0\n"),
		nil,
		nil,
	}}
	mutations := &recordingRunner{}
	client := &control.Client{Binary: "nvml-mock-ctl", Runner: mutations}
	obs := &control.Observer{Binary: "nvidia-smi", Runner: observer}
	runtime := &Runtime{Client: client, Manager: control.NewManager(client, obs), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	args := []string{"process", "add", "--pid", "123", "--gpu", "0", "--memory", "1GiB", "--name", "llama-server", "--sm", "70"}
	if err := runtime.Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if len(observer.calls) != 3 {
		t.Fatalf("observer calls=%d want=3", len(observer.calls))
	}
	got := strings.Join(mutations.calls[0].args, " ")
	for _, want := range []string{
		`processes=[{"pid":123,"type":"C","name":"llama-server","used_memory_mib":1024,"sm_util":70}]`,
		"memory.used_bytes=3221225472",
		"memory.free_bytes=13958643712",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("args=%q missing %q", got, want)
		}
	}
}

func TestParseBytes(t *testing.T) {
	for raw, want := range map[string]uint64{
		"12GiB":  12 * 1024 * 1024 * 1024,
		"512MiB": 512 * 1024 * 1024,
		"4096":   4096,
	} {
		got, err := parseBytes(raw)
		if err != nil {
			t.Fatalf("parseBytes(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("parseBytes(%q)=%d want=%d", raw, got, want)
		}
	}
}
