package main

import (
	"bytes"
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
