package profiles

import "testing"

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

func TestUpstreamProfilesCarryPinnedSourcePath(t *testing.T) {
	c, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"t4-16gb", "l40s-48gb", "a100-40gb", "h100-80gb"} {
		p, _ := c.Profile(id)
		if p.Source != "upstream" || p.UpstreamConfig == "" {
			t.Fatalf("profile %s is not marked as upstream-backed: %+v", id, p)
		}
	}
}
