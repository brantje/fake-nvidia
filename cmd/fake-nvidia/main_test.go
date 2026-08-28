package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestRenderRepeatedProfile(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run([]string{"render", "--profile", "rtx4060ti-16gb", "--count", "2"}, &out, &errOut)
	if err != nil {
		t.Fatalf("run: %v stderr=%s", err, errOut.String())
	}
	if strings.Count(out.String(), "  - index:") != 2 {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestRenderPerDeviceVRAM(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run([]string{"render", "--device", "rtx4060ti-16gb@8192", "--device", "rtx4090-24gb@20480"}, &out, &errOut)
	if err != nil {
		t.Fatalf("run: %v stderr=%s", err, errOut.String())
	}
	for _, want := range []string{"total_bytes: 8589934592", "total_bytes: 21474836480"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s\n%s", want, out.String())
		}
	}
}

func TestRenderSpecFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/spec.json"
	spec := `{"devices":[{"profile":"rtx4090-24gb","used_mib":2048,"gpu_util":44,"processes":[{"pid":123,"type":"C","name":"worker"}]}]}`
	if err := os.WriteFile(path, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := run([]string{"render", "--spec", path}, &out, &errOut); err != nil {
		t.Fatalf("run: %v stderr=%s", err, errOut.String())
	}
	for _, want := range []string{"used_bytes: 2147483648", "gpu: 44", "pid: 123", `name: "worker"`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q\n%s", want, out.String())
		}
	}
}
