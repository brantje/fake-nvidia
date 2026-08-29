package fakellama

import (
	"reflect"
	"testing"
)

// TestSplitProcessMiBUsesTensorSplitWeights verifies selected GPU indices use
// their corresponding tensor-split weights while preserving the total amount.
func TestSplitProcessMiBUsesTensorSplitWeights(t *testing.T) {
	got, err := splitProcessMiB(100*mib, []string{"0", "2"}, []float64{1, 0, 3})
	if err != nil {
		t.Fatal(err)
	}
	want := []uint64{25, 75}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("split=%v want=%v", got, want)
	}
}

// TestSplitProcessMiBRoundsTotalUp verifies byte-level resource requests are
// represented conservatively on nvidia-smi's MiB process-memory surface.
func TestSplitProcessMiBRoundsTotalUp(t *testing.T) {
	got, err := splitProcessMiB(mib+1, []string{"0", "1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint64{1, 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("split=%v want=%v", got, want)
	}
}

// TestSplitProcessMiBRejectsEmptyTargets verifies callers cannot silently drop
// requested process memory by passing no devices.
func TestSplitProcessMiBRejectsEmptyTargets(t *testing.T) {
	if _, err := splitProcessMiB(mib, nil, nil); err == nil {
		t.Fatal("expected empty-target error")
	}
}
