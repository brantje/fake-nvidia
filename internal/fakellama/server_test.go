package fakellama

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRegistry struct {
	mu        sync.Mutex
	registers int
	resizes   int
	releases  int
	lastBytes uint64
	lastPID   uint32
	err       error
}

func (f *fakeRegistry) Register(_ context.Context, pid uint32, _ string, _ []string, totalBytes uint64, _ []float64, _, _ uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.registers++
	f.lastBytes = totalBytes
	f.lastPID = pid
	return nil
}

func (f *fakeRegistry) Resize(_ context.Context, pid uint32, _ string, _ []string, totalBytes uint64, _ []float64, _, _ uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.resizes++
	f.lastBytes = totalBytes
	f.lastPID = pid
	return nil
}

func (f *fakeRegistry) Release(_ context.Context, pid uint32, _ []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases++
	f.lastPID = pid
	return nil
}

func (f *fakeRegistry) counts() (registers, resizes, releases int, bytes uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registers, f.resizes, f.releases, f.lastBytes
}

// TestServerLifecycleReadinessAndInference verifies the process reserves state,
// transitions to ready, serves OpenAI-compatible responses, and cleans up.
func TestServerLifecycleReadinessAndInference(t *testing.T) {
	registry := &fakeRegistry{}
	cfg := Config{
		Host: "127.0.0.1", Port: 0, ModelPath: "/models/test.gguf",
		Targets: []string{"0"}, VRAMBytes: 64 * mib, VRAMExplicit: true,
		LoadDelay: 30 * time.Millisecond, Response: "alpha beta gamma",
	}
	server := NewServer(cfg, registry, 4242, io.Discard, io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- server.Run(ctx) }()

	base := waitForAddr(t, server)
	resp, err := http.Get(base + "/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable && resp.StatusCode != http.StatusOK {
		t.Fatalf("initial health status=%d", resp.StatusCode)
	}
	waitForReady(t, base)

	body := bytes.NewBufferString(`{"model":"instance-a","messages":[{"role":"user","content":"hi"}]}`)
	resp, err = http.Post(base+"/v1/chat/completions", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), "alpha beta gamma") || !strings.Contains(string(data), "instance-a") {
		t.Fatalf("chat response status=%d body=%s", resp.StatusCode, data)
	}

	streamBody := bytes.NewBufferString(`{"model":"instance-a","stream":true}`)
	resp, err = http.Post(base+"/v1/chat/completions", "application/json", streamBody)
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(stream), "data: [DONE]") || !strings.Contains(string(stream), "alpha ") {
		t.Fatalf("stream response=%s", stream)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after cancellation")
	}
	registers, _, releases, gotBytes := registry.counts()
	if registers != 1 || releases != 1 || gotBytes != 64*mib {
		t.Fatalf("registry registers=%d releases=%d bytes=%d", registers, releases, gotBytes)
	}
}

// TestServerInjectedOOMDoesNotRegisterProcess verifies the forced load failure
// happens before shared fake GPU state is mutated.
func TestServerInjectedOOMDoesNotRegisterProcess(t *testing.T) {
	registry := &fakeRegistry{}
	cfg := Config{
		Host: "127.0.0.1", Port: 0, ModelPath: "/models/test.gguf",
		Targets: []string{"0"}, VRAMBytes: mib, VRAMExplicit: true, ForceOOM: true,
	}
	server := NewServer(cfg, registry, 12, io.Discard, io.Discard)
	err := server.Run(context.Background())
	if !errors.Is(err, ErrOutOfMemory) {
		t.Fatalf("error=%v want ErrOutOfMemory", err)
	}
	registers, _, releases, _ := registry.counts()
	if registers != 0 || releases != 0 {
		t.Fatalf("forced OOM mutated registry: registers=%d releases=%d", registers, releases)
	}
}

// TestServerGrowthAndCrashCleanup verifies deterministic post-ready resource
// growth and crash injection still release the process record.
func TestServerGrowthAndCrashCleanup(t *testing.T) {
	registry := &fakeRegistry{}
	cfg := Config{
		Host: "127.0.0.1", Port: 0, ModelPath: "/models/test.gguf",
		Targets: []string{"0"}, VRAMBytes: 8 * mib, VRAMExplicit: true,
		GrowthAfter: 10 * time.Millisecond, GrowthBytes: 4 * mib,
		CrashAfterReady: 35 * time.Millisecond, Response: "ok",
	}
	server := NewServer(cfg, registry, 99, io.Discard, io.Discard)
	err := server.Run(context.Background())
	if !errors.Is(err, ErrInjectedCrash) {
		t.Fatalf("error=%v want injected crash", err)
	}
	registers, resizes, releases, gotBytes := registry.counts()
	if registers != 1 || resizes != 1 || releases != 1 || gotBytes != 12*mib {
		t.Fatalf("registers=%d resizes=%d releases=%d bytes=%d", registers, resizes, releases, gotBytes)
	}
}

// TestReleaseResourcesIsIdempotent verifies shutdown-hang handling can release
// GPU state before the process is eventually force-killed by a supervisor.
func TestReleaseResourcesIsIdempotent(t *testing.T) {
	registry := &fakeRegistry{}
	cfg := Config{Host: "127.0.0.1", Port: 0, ModelPath: "/models/test.gguf", Targets: []string{"0"}, VRAMBytes: mib, VRAMExplicit: true}
	server := NewServer(cfg, registry, 77, io.Discard, io.Discard)
	server.registered.Store(true)
	if err := server.ReleaseResources(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := server.ReleaseResources(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, releases, _ := registry.counts()
	if releases != 1 {
		t.Fatalf("releases=%d want=1", releases)
	}
}

func waitForAddr(t *testing.T, server *Server) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if addr := server.Addr(); addr != "" {
			return "http://" + addr
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server did not bind a port")
	return ""
}

func waitForReady(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server did not become ready")
}
