package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestRunKubernetesReusesProfileComposition verifies the CLI renders a two-GPU
// installer manifest from the same built-in profile catalog used by local mode.
func TestRunKubernetesReusesProfileComposition(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{
		"kubernetes",
		"--profile", "rtx4090-24gb",
		"--gpus", "2",
		"--image", "fake-nvidia-k8s:test",
		"--cdi-kind", "fake-nvidia.com/gpu",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run kubernetes: %v\nstderr: %s", err, stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{"kind: DaemonSet", "--device-count=2", "NVIDIA GeForce RTX 4090", "fake-nvidia.com/gpu"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered manifest missing %q:\n%s", want, text)
		}
	}
}
