package upstream

import "testing"

// TestPinnedRevisionAndOverrideContract verifies the corresponding behavior and regression contract.
func TestPinnedRevisionAndOverrideContract(t *testing.T) {
	if Revision == "" || len(Revision) != 40 {
		t.Fatalf("expected pinned 40-character revision, got %q", Revision)
	}
	want := []string{"base", "all", "device"}
	if len(OverrideLayers) != len(want) {
		t.Fatalf("unexpected override layers: %v", OverrideLayers)
	}
	for i := range want {
		if OverrideLayers[i] != want[i] {
			t.Fatalf("override precedence changed: got %v want %v", OverrideLayers, want)
		}
	}
}
