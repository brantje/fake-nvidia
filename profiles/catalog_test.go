package profiles

import "testing"

// TestCatalogContainsPhase1Profiles verifies the corresponding behavior and regression contract.
func TestCatalogContainsPhase1Profiles(t *testing.T) {
	c, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"rtx4060ti-16gb", "rtx4090-24gb", "t4-16gb", "l40s-48gb", "a100-40gb", "h100-80gb",
	}
	for _, id := range want {
		if _, ok := c.Profile(id); !ok {
			t.Errorf("missing profile %q", id)
		}
	}
	for _, id := range []string{"dual-rtx4060ti-16gb", "mixed-gpu"} {
		if _, ok := c.Topology(id); !ok {
			t.Errorf("missing topology %q", id)
		}
	}
}

// TestUpstreamProfilesCarryPinnedSourcePath verifies the corresponding behavior and regression contract.
func TestUpstreamProfilesCarryPinnedSourcePath(t *testing.T) {
	c, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		"t4-16gb":   "pkg/gpu/mocknvml/configs/mock-nvml-config-t4.yaml",
		"l40s-48gb": "pkg/gpu/mocknvml/configs/mock-nvml-config-l40s.yaml",
		"a100-40gb": "pkg/gpu/mocknvml/configs/mock-nvml-config-a100.yaml",
		"h100-80gb": "pkg/gpu/mocknvml/configs/mock-nvml-config-h100.yaml",
	}
	for id, path := range expected {
		p, ok := c.Profile(id)
		if !ok {
			t.Fatalf("missing profile %s", id)
		}
		if p.Source != "upstream" || p.UpstreamConfig != path {
			t.Fatalf("profile %s source/path = %q/%q, want upstream/%q", id, p.Source, p.UpstreamConfig, path)
		}
	}
}

// TestTopologyValidationRejectsOverflowingUsedMemory verifies the corresponding behavior and regression contract.
func TestTopologyValidationRejectsOverflowingUsedMemory(t *testing.T) {
	c := &Catalog{profiles: map[string]Profile{
		"test": {ID: "test", TotalMemoryMiB: 1024, ReservedMemoryMiB: 1},
	}}
	err := c.validateTopology(Topology{ID: "overflow", Devices: []TopologyDevice{{Profile: "test", UsedMiB: ^uint64(0)}}})
	if err == nil {
		t.Fatal("expected used-memory validation error")
	}
}
