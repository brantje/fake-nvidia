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
		"fake-nvidia.com/enabled: \"true\"",
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

func TestRenderManifestRejectsInvalidCDIKind(t *testing.T) {
	for _, kind := range []string{"vendor.com/", "vendor.com/gpu/extra", "Vendor.com/gpu"} {
		_, err := RenderManifest(ManifestOptions{
			Image:       "fake-nvidia-k8s:test",
			CDIKind:     kind,
			DeviceCount: 1,
			ConfigYAML:  []byte("version: \"1.0\"\n"),
		})
		if err == nil {
			t.Fatalf("invalid CDI kind %q unexpectedly accepted", kind)
		}
	}
}
