package kubernetes

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateCDISpec(t *testing.T) {
	data, err := GenerateCDISpec("nvidia.com/gpu", "/var/lib/fake-nvidia", 2)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if spec["cdiVersion"] != "0.6.0" || spec["kind"] != "nvidia.com/gpu" {
		t.Fatalf("unexpected spec header: %#v", spec)
	}
	text := string(data)
	for _, want := range []string{"/dev/nvidia0", "/dev/nvidia1", `"name": "all"`, "/opt/fake-nvidia/state"} {
		if !strings.Contains(text, want) {
			t.Fatalf("CDI spec missing %q:\n%s", want, text)
		}
	}
}

func TestGenerateCDISpecRejectsUnsafeInputs(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		root  string
		count int
	}{
		{"gpu", "/var/lib/fake-nvidia", 1},
		{"nvidia.com/gpu", "relative", 1},
		{"nvidia.com/gpu", "/", 1},
		{"nvidia.com/gpu", "/var/lib/fake-nvidia", 0},
		{"nvidia.com/gpu", "/var/lib/fake-nvidia", 9},
	} {
		if _, err := GenerateCDISpec(tc.kind, tc.root, tc.count); err == nil {
			t.Fatalf("GenerateCDISpec(%q, %q, %d) unexpectedly succeeded", tc.kind, tc.root, tc.count)
		}
	}
}
