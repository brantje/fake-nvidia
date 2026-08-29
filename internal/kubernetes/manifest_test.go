package kubernetes

import (
	"strings"
	"testing"
)

func TestRenderManifestUsesProvidedProfileAndCDIKind(t *testing.T) {
	data, err := RenderManifest(ManifestOptions{
		Namespace:   "fake-nvidia-system",
		Image:       "fake-nvidia-k8s:test",
		CDIKind:     "nvidia.com/gpu",
		DeviceCount: 2,
		ConfigYAML:  []byte("version: \"1.0\"\ndevices:\n  - index: 0\n  - index: 1\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"kind: DaemonSet",
		"image: fake-nvidia-k8s:test",
		"--device-count=2",
		"--cdi-kind=nvidia.com/gpu",
		"    version: \"1.0\"",
		"path: /var/run/cdi",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("manifest missing %q:\n%s", want, text)
		}
	}
}

func TestRenderManifestRejectsMissingImage(t *testing.T) {
	_, err := RenderManifest(ManifestOptions{DeviceCount: 1, ConfigYAML: []byte("version: \"1.0\"\n")})
	if err == nil {
		t.Fatal("expected missing image error")
	}
}
