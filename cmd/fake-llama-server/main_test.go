package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestHandleInfoRequestMatchesManagerDiscovery(t *testing.T) {
	var out bytes.Buffer
	if !handleInfoRequest([]string{"--version"}, &out) {
		t.Fatal("--version was not handled")
	}
	if !strings.Contains(out.String(), "fake-llama-server") {
		t.Fatalf("unexpected version output %q", out.String())
	}

	out.Reset()
	if !handleInfoRequest([]string{"--help"}, &out) {
		t.Fatal("--help was not handled")
	}
	help := out.String()
	for _, flag := range []string{"--model", "--host", "--port", "--ctx-size", "--device", "--tensor-split"} {
		if !strings.Contains(help, flag) {
			t.Fatalf("help output missing %s: %q", flag, help)
		}
	}
}

func TestWaitForRegisterGate(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "release")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- waitForRegisterGate(ctx, gate) }()

	select {
	case err := <-done:
		t.Fatalf("gate returned before release: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	if err := os.WriteFile(gate, []byte("release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("gate did not release")
	}
}

func TestWithManagerDeviceTargetsNormalizesCUDADevices(t *testing.T) {
	args, err := withManagerDeviceTargets([]string{
		"--model", "/models/test.gguf",
		"--device", "CUDA0,CUDA2",
		"--tensor-split", "1,2",
	}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := "--fake-gpus=0,2"
	if len(args) == 0 || args[len(args)-1] != wantSuffix {
		t.Fatalf("args=%v want suffix %q", args, wantSuffix)
	}
}

func TestWithManagerDeviceTargetsHonorsExplicitFakeOverride(t *testing.T) {
	original := []string{"--model", "/models/test.gguf", "--device=CUDA1", "--fake-gpus=3"}
	got, err := withManagerDeviceTargets(original, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("args=%v want %v", got, original)
	}

	original = []string{"--model", "/models/test.gguf", "--device=CUDA1"}
	got, err = withManagerDeviceTargets(original, func(key string) string {
		if key == "FAKE_LLAMA_GPUS" {
			return "4"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("environment override changed args: %v want %v", got, original)
	}
}

func TestWithManagerDeviceTargetsRejectsUnsupportedBackend(t *testing.T) {
	_, err := withManagerDeviceTargets([]string{"--model", "/models/test.gguf", "--device", "ROCm0"}, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "unsupported fake NVIDIA device") {
		t.Fatalf("expected unsupported-device error, got %v", err)
	}
}
