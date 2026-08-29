package fakellama

import (
	"context"
	"errors"
	"io"
	"testing"
)

type retryReleaseRegistry struct {
	releases int
}

func (*retryReleaseRegistry) Register(context.Context, uint32, string, []string, uint64, []float64, uint32, uint32) error {
	return nil
}

func (*retryReleaseRegistry) Resize(context.Context, uint32, string, []string, uint64, []float64, uint32, uint32) error {
	return nil
}

func (r *retryReleaseRegistry) Release(context.Context, uint32, []string) error {
	r.releases++
	if r.releases == 1 {
		return errors.New("transient release failure")
	}
	return nil
}

// TestReleaseResourcesRetriesAfterFailure verifies a transient control failure
// does not permanently suppress cleanup of registered fake GPU state.
func TestReleaseResourcesRetriesAfterFailure(t *testing.T) {
	registry := &retryReleaseRegistry{}
	server := NewServer(Config{Targets: []string{"0"}}, registry, 123, io.Discard, io.Discard)
	server.registered.Store(true)
	if err := server.ReleaseResources(context.Background()); err == nil {
		t.Fatal("expected first release to fail")
	}
	if !server.registered.Load() {
		t.Fatal("failed release incorrectly marked resources as cleaned")
	}
	if err := server.ReleaseResources(context.Background()); err != nil {
		t.Fatalf("retry release: %v", err)
	}
	if server.registered.Load() {
		t.Fatal("successful retry did not clear registration state")
	}
	if registry.releases != 2 {
		t.Fatalf("release calls=%d want=2", registry.releases)
	}
}
