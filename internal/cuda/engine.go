package cuda

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Status is the semantic CUDA result used by the Go-owned simulator engine.
// The native bridge maps these values to the distinct driver/runtime enums.
type Status int

const (
	StatusSuccess Status = iota
	StatusInvalidValue
	StatusOutOfMemory
	StatusInitialization
	StatusNoDevice
	StatusInvalidDevice
	StatusNotFound
	StatusNotSupported
	StatusUnknown
)

// ErrOutOfMemory lets a backend report a concurrent capacity failure without
// leaking backend-specific error strings into CUDA result mapping.
var ErrOutOfMemory = errors.New("simulated CUDA out of memory")

// Device is the CUDA-facing subset of effective GPU metadata/state.
type Device struct {
	Index        int
	UUID         string
	Name         string
	PCIBusID     string
	ComputeMajor int
	ComputeMinor int
	TotalBytes   uint64
	UsedBytes    uint64
	FreeBytes    uint64
}

// Backend connects CUDA operations to the effective simulator state.
type Backend interface {
	Devices(ctx context.Context) ([]Device, error)
	Reserve(ctx context.Context, device int, bytes uint64) error
	Release(ctx context.Context, device int, bytes uint64) error
	CUDAVersion() int
}

// FaultPolicy defines deterministic CUDA-specific error injection.
// OOMAfter < 0 disables injected OOM. OOMAfter == N allows N successful
// allocations in this process, then makes every later allocation return OOM.
type FaultPolicy struct {
	OOMAfter int64
}

// FaultPolicyFromEnv reads the process-local deterministic CUDA fault policy.
func FaultPolicyFromEnv() (FaultPolicy, error) {
	policy := FaultPolicy{OOMAfter: -1}
	raw := strings.TrimSpace(os.Getenv("FAKE_NVIDIA_CUDA_OOM_AFTER"))
	if raw == "" {
		return policy, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return FaultPolicy{}, fmt.Errorf("FAKE_NVIDIA_CUDA_OOM_AFTER must be a non-negative integer")
	}
	policy.OOMAfter = value
	return policy, nil
}

// Allocation records one fake device allocation. Token is a tiny native token
// owned by the ABI bridge; Size is the simulated VRAM reservation.
type Allocation struct {
	Token  uintptr
	Size   uint64
	Device int
}

// Engine owns CUDA device selection, allocation bookkeeping, and fault policy.
type Engine struct {
	mu                    sync.Mutex
	backend               Backend
	fault                 FaultPolicy
	currentDevice         int
	allocations           map[uintptr]Allocation
	successfulAllocations int64
}

// NewEngine creates a CUDA simulator engine over backend.
func NewEngine(backend Backend, policy FaultPolicy) (*Engine, error) {
	if backend == nil {
		return nil, errors.New("CUDA backend is required")
	}
	if policy.OOMAfter < -1 {
		return nil, errors.New("OOMAfter cannot be below -1")
	}
	return &Engine{
		backend:       backend,
		fault:         policy,
		currentDevice: 0,
		allocations:   make(map[uintptr]Allocation),
	}, nil
}

// Init validates that the effective simulator state can be read.
func (e *Engine) Init(ctx context.Context) Status {
	if e == nil || e.backend == nil {
		return StatusInitialization
	}
	if _, err := e.backend.Devices(ctx); err != nil {
		return StatusInitialization
	}
	return StatusSuccess
}

// DeviceCount returns the current effective CUDA device count.
func (e *Engine) DeviceCount(ctx context.Context) (int, Status) {
	if e == nil || e.backend == nil {
		return 0, StatusInitialization
	}
	devices, err := e.backend.Devices(ctx)
	if err != nil {
		return 0, StatusUnknown
	}
	return len(devices), StatusSuccess
}

// Device returns metadata for one effective device.
func (e *Engine) Device(ctx context.Context, index int) (Device, Status) {
	if e == nil || e.backend == nil {
		return Device{}, StatusInitialization
	}
	devices, err := e.backend.Devices(ctx)
	if err != nil {
		return Device{}, StatusUnknown
	}
	for _, device := range devices {
		if device.Index == index {
			return device, StatusSuccess
		}
	}
	return Device{}, StatusInvalidDevice
}

// SetDevice changes the process-local current device after validating it.
func (e *Engine) SetDevice(ctx context.Context, index int) Status {
	if _, status := e.Device(ctx, index); status != StatusSuccess {
		return status
	}
	e.mu.Lock()
	e.currentDevice = index
	e.mu.Unlock()
	return StatusSuccess
}

// CurrentDevice returns the process-local current device.
func (e *Engine) CurrentDevice() (int, Status) {
	if e == nil {
		return 0, StatusInitialization
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.currentDevice, StatusSuccess
}

// MemoryInfo returns effective free/total bytes for the current device.
func (e *Engine) MemoryInfo(ctx context.Context) (free, total uint64, status Status) {
	if e == nil || e.backend == nil {
		return 0, 0, StatusInitialization
	}
	e.mu.Lock()
	current := e.currentDevice
	e.mu.Unlock()
	device, status := e.Device(ctx, current)
	if status != StatusSuccess {
		return 0, 0, status
	}
	return device.FreeBytes, device.TotalBytes, StatusSuccess
}

// TrackAllocation reserves simulated VRAM and associates it with a native token.
func (e *Engine) TrackAllocation(ctx context.Context, token uintptr, size uint64) Status {
	if e == nil || e.backend == nil {
		return StatusInitialization
	}
	if token == 0 || size == 0 {
		return StatusInvalidValue
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.allocations[token]; exists {
		return StatusInvalidValue
	}
	if e.shouldInjectOOMLocked() {
		return StatusOutOfMemory
	}

	devices, err := e.backend.Devices(ctx)
	if err != nil {
		return StatusUnknown
	}
	device, ok := findDevice(devices, e.currentDevice)
	if !ok {
		return StatusInvalidDevice
	}
	if size > device.FreeBytes {
		return StatusOutOfMemory
	}
	if err := e.backend.Reserve(ctx, e.currentDevice, size); err != nil {
		if errors.Is(err, ErrOutOfMemory) {
			return StatusOutOfMemory
		}
		return StatusUnknown
	}

	e.allocations[token] = Allocation{Token: token, Size: size, Device: e.currentDevice}
	e.successfulAllocations++
	return StatusSuccess
}

// FreeAllocation releases a tracked simulated allocation.
func (e *Engine) FreeAllocation(ctx context.Context, token uintptr) Status {
	if e == nil || e.backend == nil {
		return StatusInitialization
	}
	if token == 0 {
		return StatusSuccess
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	allocation, ok := e.allocations[token]
	if !ok {
		return StatusInvalidValue
	}
	if err := e.backend.Release(ctx, allocation.Device, allocation.Size); err != nil {
		return StatusUnknown
	}
	delete(e.allocations, token)
	return StatusSuccess
}

// ResetDevice releases every tracked allocation owned by the current device.
func (e *Engine) ResetDevice(ctx context.Context) Status {
	if e == nil || e.backend == nil {
		return StatusInitialization
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	var total uint64
	for _, allocation := range e.allocations {
		if allocation.Device != e.currentDevice {
			continue
		}
		if allocation.Size > ^uint64(0)-total {
			return StatusUnknown
		}
		total += allocation.Size
	}
	if total != 0 {
		if err := e.backend.Release(ctx, e.currentDevice, total); err != nil {
			return StatusUnknown
		}
	}
	for token, allocation := range e.allocations {
		if allocation.Device == e.currentDevice {
			delete(e.allocations, token)
		}
	}
	return StatusSuccess
}

// CUDAVersion returns the numeric CUDA API version reported by the backend.
func (e *Engine) CUDAVersion() (int, Status) {
	if e == nil || e.backend == nil {
		return 0, StatusInitialization
	}
	version := e.backend.CUDAVersion()
	if version <= 0 {
		return 0, StatusUnknown
	}
	return version, StatusSuccess
}

// Allocation returns bookkeeping for tests and bridge cleanup.
func (e *Engine) Allocation(token uintptr) (Allocation, bool) {
	if e == nil {
		return Allocation{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	allocation, ok := e.allocations[token]
	return allocation, ok
}

// AllocationCount returns the active allocation count.
func (e *Engine) AllocationCount() int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.allocations)
}

func (e *Engine) shouldInjectOOMLocked() bool {
	return e.fault.OOMAfter >= 0 && e.successfulAllocations >= e.fault.OOMAfter
}

func findDevice(devices []Device, index int) (Device, bool) {
	for _, device := range devices {
		if device.Index == index {
			return device, true
		}
	}
	return Device{}, false
}
