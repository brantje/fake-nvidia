package fakellama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrInjectedCrash is returned after a configured post-readiness crash.
var ErrInjectedCrash = errors.New("fake llama-server injected crash after readiness")

// Server simulates the lifecycle and HTTP surface of llama-server while
// delegating GPU state to ProcessRegistry.
type Server struct {
	cfg      Config
	registry ProcessRegistry
	pid      uint32
	stdout   io.Writer
	stderr   io.Writer

	ready      atomic.Bool
	registered atomic.Bool
	addrMu     sync.RWMutex
	addr       string
	releaseMu  sync.Mutex
}

// NewServer constructs a fake llama-server process model for pid.
func NewServer(cfg Config, registry ProcessRegistry, pid uint32, stdout, stderr io.Writer) *Server {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	return &Server{cfg: cfg, registry: registry, pid: pid, stdout: stdout, stderr: stderr}
}

// Addr returns the actual listening address once Run has opened the socket.
func (s *Server) Addr() string {
	s.addrMu.RLock()
	defer s.addrMu.RUnlock()
	return s.addr
}

// Ready reports whether health and inference endpoints should accept traffic.
func (s *Server) Ready() bool { return s != nil && s.ready.Load() }

// Handler exposes the minimal manager/inference HTTP surface for unit tests and
// embedding. Run uses the same handler on the configured TCP listener.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("POST /v1/completions", s.handleCompletions)
	return mux
}

// Run starts the HTTP server, registers the real process PID in fake NVML,
// waits through the configured model-load phase, and blocks until cancellation
// or an injected failure. Registered resources are released on every return.
func (s *Server) Run(ctx context.Context) error {
	if s == nil || s.registry == nil {
		return errors.New("fake llama-server is not initialized")
	}
	if s.pid == 0 {
		return errors.New("fake llama-server requires a positive pid")
	}
	if s.cfg.StartupFail {
		fmt.Fprintln(s.stderr, "fake-llama-server: injected startup failure before model load")
		return errors.New("fake llama-server injected startup failure")
	}

	required, err := s.cfg.RequiredVRAMBytes()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(s.cfg.Host, fmt.Sprint(s.cfg.Port)))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.addrMu.Lock()
	s.addr = listener.Addr().String()
	s.addrMu.Unlock()

	httpServer := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	serveErr := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()
	defer func() {
		s.ready.Store(false)
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
		_ = httpServer.Shutdown(shutdownCtx)
		cancelShutdown()
		releaseCtx, cancelRelease := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.ReleaseResources(releaseCtx)
		cancelRelease()
	}()

	fmt.Fprintf(s.stdout, "fake-llama-server: loading model %s\n", s.cfg.ModelPath)
	fmt.Fprintf(s.stdout, "fake-llama-server: selected fake GPU targets %s\n", strings.Join(s.cfg.Targets, ","))
	if s.cfg.ForceOOM {
		fmt.Fprintln(s.stderr, "ggml_cuda_init: CUDA error: out of memory (injected by fake-llama-server)")
		return ErrOutOfMemory
	}
	if err := s.registry.Register(ctx, s.pid, "fake-llama-server", s.cfg.Targets, required, s.cfg.TensorSplit, s.cfg.SMUtil, s.cfg.MemoryUtil); err != nil {
		if errors.Is(err, ErrOutOfMemory) {
			fmt.Fprintf(s.stderr, "ggml_cuda_init: CUDA error: out of memory: %v\n", err)
		}
		return err
	}
	s.registered.Store(true)
	fmt.Fprintf(s.stdout, "fake-llama-server: reserved %d simulated bytes for pid %d\n", required, s.pid)

	if s.cfg.LoadDelay > 0 {
		timer := time.NewTimer(s.cfg.LoadDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case err := <-serveErr:
			if !timer.Stop() {
				<-timer.C
			}
			if err != nil {
				return fmt.Errorf("serve during model load: %w", err)
			}
			return errors.New("HTTP server stopped during model load")
		case <-timer.C:
		}
	}

	s.ready.Store(true)
	fmt.Fprintf(s.stdout, "fake-llama-server: model load simulation complete; ready at http://%s\n", s.Addr())

	var crash <-chan time.Time
	var crashTimer *time.Timer
	if s.cfg.CrashAfterReady > 0 {
		crashTimer = time.NewTimer(s.cfg.CrashAfterReady)
		crash = crashTimer.C
		defer crashTimer.Stop()
	}
	var growth <-chan time.Time
	var growthTimer *time.Timer
	if s.cfg.GrowthAfter > 0 && s.cfg.GrowthBytes > 0 {
		growthTimer = time.NewTimer(s.cfg.GrowthAfter)
		growth = growthTimer.C
		defer growthTimer.Stop()
	}

	currentBytes := required
	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-serveErr:
			if ok && err != nil {
				return fmt.Errorf("serve: %w", err)
			}
			return errors.New("HTTP server stopped unexpectedly")
		case <-growth:
			if currentBytes > ^uint64(0)-s.cfg.GrowthBytes {
				return errors.New("fake llama-server VRAM growth overflows uint64")
			}
			currentBytes += s.cfg.GrowthBytes
			if err := s.registry.Resize(ctx, s.pid, "fake-llama-server", s.cfg.Targets, currentBytes, s.cfg.TensorSplit, s.cfg.SMUtil, s.cfg.MemoryUtil); err != nil {
				return fmt.Errorf("fake llama-server VRAM growth: %w", err)
			}
			fmt.Fprintf(s.stdout, "fake-llama-server: simulated VRAM grew to %d bytes\n", currentBytes)
			growth = nil
		case <-crash:
			s.ready.Store(false)
			fmt.Fprintln(s.stderr, "fake-llama-server: injected crash after readiness")
			return ErrInjectedCrash
		}
	}
}

// ReleaseResources removes this PID from all selected fake GPUs. It is safe to
// call more than once. A failed release remains retryable so transient control
// failures do not permanently hide stale simulated process state.
func (s *Server) ReleaseResources(ctx context.Context) error {
	if s == nil || s.registry == nil {
		return nil
	}
	s.releaseMu.Lock()
	defer s.releaseMu.Unlock()
	if !s.registered.Load() {
		return nil
	}
	s.ready.Store(false)
	if err := s.registry.Release(ctx, s.pid, s.cfg.Targets); err != nil {
		return err
	}
	s.registered.Store(false)
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !s.Ready() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "loading"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
}

func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	if !s.requireReady(w) {
		return
	}
	model := filepath.Base(s.cfg.ModelPath)
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data": []map[string]any{{"id": model, "object": "model", "owned_by": "fake-nvidia"}},
	})
}

type chatRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if !s.requireReady(w) {
		return
	}
	var request chatRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "invalid JSON request", "type": "invalid_request_error"}})
		return
	}
	model := request.Model
	if model == "" {
		model = filepath.Base(s.cfg.ModelPath)
	}
	if request.Stream {
		s.writeChatStream(w, r, model)
		return
	}
	completionTokens := len(strings.Fields(s.cfg.Response))
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      "chatcmpl-fake-nvidia",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{{
			"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": s.cfg.Response},
		}},
		"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": completionTokens, "total_tokens": completionTokens + 1},
	})
}

func (s *Server) writeChatStream(w http.ResponseWriter, r *http.Request, model string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"message": "streaming unsupported"}})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	words := strings.Fields(s.cfg.Response)
	for i, word := range words {
		content := word
		if i != len(words)-1 {
			content += " "
		}
		chunk := map[string]any{
			"id": "chatcmpl-fake-nvidia", "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model,
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": content}, "finish_reason": nil}},
		}
		data, _ := json.Marshal(chunk)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return
		}
		flusher.Flush()
		if s.cfg.TokenDelay > 0 {
			timer := time.NewTimer(s.cfg.TokenDelay)
			select {
			case <-r.Context().Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
			}
		}
	}
	final := map[string]any{
		"id": "chatcmpl-fake-nvidia", "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
	}
	data, _ := json.Marshal(final)
	if _, err := fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data); err != nil {
		return
	}
	flusher.Flush()
}

func (s *Server) handleCompletions(w http.ResponseWriter, r *http.Request) {
	if !s.requireReady(w) {
		return
	}
	var request chatRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "invalid JSON request", "type": "invalid_request_error"}})
		return
	}
	model := request.Model
	if model == "" {
		model = filepath.Base(s.cfg.ModelPath)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": "cmpl-fake-nvidia", "object": "text_completion", "created": time.Now().Unix(), "model": model,
		"choices": []map[string]any{{"index": 0, "text": s.cfg.Response, "finish_reason": "stop"}},
	})
}

func (s *Server) requireReady(w http.ResponseWriter) bool {
	if s.Ready() {
		return true
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"message": "model is still loading", "type": "server_error"}})
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
