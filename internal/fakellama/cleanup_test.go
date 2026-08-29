package fakellama

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

type retryReleaseRegistry struct {
	releases int
}

// Register satisfies ProcessRegistry for the cleanup retry fixture.
func (*retryReleaseRegistry) Register(context.Context, uint32, string, []string, uint64, []float64, uint32, uint32) error {
	return nil
}

// Resize satisfies ProcessRegistry for the cleanup retry fixture.
func (*retryReleaseRegistry) Resize(context.Context, uint32, string, []string, uint64, []float64, uint32, uint32) error {
	return nil
}

// Release fails once, then succeeds, to exercise retryable cleanup state.
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

// TestRunRetriesDeferredReleaseBeforeExit verifies Run itself retries a
// transient cleanup failure before returning to the process owner.
func TestRunRetriesDeferredReleaseBeforeExit(t *testing.T) {
	registry := &retryReleaseRegistry{}
	server := NewServer(Config{
		Host: "127.0.0.1", Port: 0, ModelPath: "/models/retry.gguf",
		Targets: []string{"0"}, VRAMBytes: mib, VRAMExplicit: true,
	}, registry, 321, io.Discard, io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for !server.Ready() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !server.Ready() {
		cancel()
		t.Fatal("server did not become ready")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run cleanup retry returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not finish after cleanup retry")
	}
	if registry.releases != 2 {
		t.Fatalf("deferred release calls=%d want=2", registry.releases)
	}
	if server.registered.Load() {
		t.Fatal("Run returned with fake GPU resources still registered")
	}
}
