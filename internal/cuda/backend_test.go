package cuda

import "testing"

func TestParseCUDAVersionRejectsInvalidFormats(t *testing.T) {
	for _, input := range []string{
		"12",
		"12.8.1",
		"12.",
		".8",
		"12.-1",
		"12.100",
		"x.8",
	} {
		if got, err := parseCUDAVersion(input); err == nil {
			t.Fatalf("parseCUDAVersion(%q)=%d, want error", input, got)
		}
	}
}
