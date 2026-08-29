package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const defaultOverrideFile = "/var/lib/nvml-mock/driver/config/overrides.yaml"

// ErrInsufficientMemory marks a reservation that exceeds the device's current
// effective free VRAM.
var ErrInsufficientMemory = errors.New("insufficient GPU memory")

// Manager composes effective-state reads with upstream-backed mutations.
type Manager struct {
	Client   *Client
	Observer *Observer

	// MutationLockPath serializes effective-state read-modify-write operations
	// performed by separate fake-nvidia control processes. Leave empty when the
	// manager is embedded with an already-serialized or in-memory backend.
	MutationLockPath string
}

// NewManager constructs a high-level control manager.
func NewManager(client *Client, observer *Observer) *Manager {
	m := &Manager{Client: client, Observer: observer}
	if client == nil {
		return m
	}
	if _, ok := client.Runner.(ExecRunner); !ok {
		return m
	}
	path := client.OverrideFile
	if path == "" {
		path = defaultOverrideFile
	}
	m.MutationLockPath = path + ".fake-nvidia-memory.lock"
	return m
}

// SetGPUUtilization pins only GPU utilization while preserving the configured
// static memory-utilization value and disabling the dynamic utilization mask.
func (c *Client) SetGPUUtilization(ctx context.Context, target string, value uint32) error {
	if value > 100 {
		return errors.New("utilization must be between 0 and 100")
	}
	return c.run(ctx, "set", "--gpu", target,
		fmt.Sprintf("utilization.gpu=%d", value),
		"dynamic_metrics.utilization=null")
}

// SetMemoryUtilization pins only memory utilization while disabling the dynamic
// utilization mask so the requested value is deterministic.
func (c *Client) SetMemoryUtilization(ctx context.Context, target string, value uint32) error {
	if value > 100 {
		return errors.New("utilization must be between 0 and 100")
	}
	return c.run(ctx, "set", "--gpu", target,
		fmt.Sprintf("utilization.memory=%d", value),
		"dynamic_metrics.utilization=null")
}

// SetUsedMemory changes synthetic used VRAM while preserving the current
// effective used+free pool, including any profile-level reserved memory.
func (m *Manager) SetUsedMemory(ctx context.Context, target string, usedBytes uint64) error {
	return m.withMutationLock(func() error {
		device, err := m.device(ctx, target)
		if err != nil {
			return err
		}
		pool, err := addBytes(device.UsedBytes, device.FreeBytes)
		if err != nil {
			return err
		}
		if usedBytes > pool {
			return fmt.Errorf("requested used memory %d exceeds allocatable pool %d", usedBytes, pool)
		}
		owned, err := processBytes(device.Processes)
		if err != nil {
			return err
		}
		if usedBytes < owned {
			return fmt.Errorf("requested used memory %d is below process-owned memory %d", usedBytes, owned)
		}
		return m.Client.SetMemory(ctx, target, usedBytes, pool-usedBytes)
	})
}

// ReserveMemory increases synthetic non-profile VRAM usage by delta bytes.
func (m *Manager) ReserveMemory(ctx context.Context, target string, delta uint64) error {
	return m.withMutationLock(func() error {
		device, err := m.device(ctx, target)
		if err != nil {
			return err
		}
		if delta > device.FreeBytes {
			return fmt.Errorf("%w: cannot reserve %d bytes: only %d bytes free", ErrInsufficientMemory, delta, device.FreeBytes)
		}
		used, err := addBytes(device.UsedBytes, delta)
		if err != nil {
			return err
		}
		return m.Client.SetMemory(ctx, target, used, device.FreeBytes-delta)
	})
}

// ReleaseMemory decreases synthetic VRAM usage by delta bytes.
func (m *Manager) ReleaseMemory(ctx context.Context, target string, delta uint64) error {
	return m.withMutationLock(func() error {
		device, err := m.device(ctx, target)
		if err != nil {
			return err
		}
		owned, err := processBytes(device.Processes)
		if err != nil {
			return err
		}
		if owned > device.UsedBytes {
			return errors.New("process-owned memory exceeds effective used memory")
		}
		releasable := device.UsedBytes - owned
		if delta > releasable {
			return fmt.Errorf("cannot release %d bytes: only %d non-process bytes are releasable", delta, releasable)
		}
		free, err := addBytes(device.FreeBytes, delta)
		if err != nil {
			return err
		}
		return m.Client.SetMemory(ctx, target, device.UsedBytes-delta, free)
	})
}

// ReplaceProcesses reconciles the supplied process list with current effective
// device memory while the same mutation lock used by memory operations is held.
func (m *Manager) ReplaceProcesses(ctx context.Context, target string, processes []Process) error {
	return m.withMutationLock(func() error {
		device, err := m.device(ctx, target)
		if err != nil {
			return err
		}
		return m.reconcileProcessesFromState(ctx, target, device, processes)
	})
}

// ReplaceProcessesFromState applies the caller's process-list changes to a
// fresh effective-state snapshot before reconciling memory. When cross-process
// locking is enabled, this preserves concurrent changes to unrelated PIDs and
// detects conflicting changes to the same PID instead of overwriting them.
func (m *Manager) ReplaceProcessesFromState(ctx context.Context, target string, before DeviceState, desired []Process) error {
	if m == nil || m.Client == nil {
		return errors.New("control client is required")
	}
	if m.MutationLockPath == "" {
		return m.reconcileProcessesFromState(ctx, target, before, desired)
	}
	return m.withMutationLock(func() error {
		current, err := m.device(ctx, target)
		if err != nil {
			return err
		}
		rebased, err := rebaseProcesses(before.Processes, desired, current.Processes)
		if err != nil {
			return err
		}
		return m.reconcileProcessesFromState(ctx, target, current, rebased)
	})
}

func (m *Manager) reconcileProcessesFromState(ctx context.Context, target string, device DeviceState, processes []Process) error {
	currentProcessBytes, err := processBytes(device.Processes)
	if err != nil {
		return err
	}
	if currentProcessBytes > device.UsedBytes {
		return errors.New("process-owned memory exceeds effective used memory")
	}
	nonProcessUsed := device.UsedBytes - currentProcessBytes
	reservedBytes := uint64(0)
	visible, err := addBytes(device.UsedBytes, device.FreeBytes)
	if err != nil {
		return err
	}
	if visible <= device.TotalBytes {
		reservedBytes = device.TotalBytes - visible
	}
	return m.Client.SetProcessesReconciled(ctx, target, processes, device.TotalBytes, reservedBytes, nonProcessUsed)
}

// Snapshot returns the effective state used by the control UX.
func (m *Manager) Snapshot(ctx context.Context) ([]DeviceState, error) {
	if m == nil || m.Observer == nil {
		return nil, errors.New("observer is required")
	}
	return m.Observer.Snapshot(ctx)
}

func (m *Manager) device(ctx context.Context, target string) (DeviceState, error) {
	if m == nil || m.Client == nil || m.Observer == nil {
		return DeviceState{}, errors.New("control client and observer are required")
	}
	return m.Observer.Device(ctx, target)
}

func (m *Manager) withMutationLock(fn func() error) error {
	if m == nil {
		return errors.New("control manager is required")
	}
	if m.MutationLockPath == "" {
		return fn()
	}
	if err := os.MkdirAll(filepath.Dir(m.MutationLockPath), 0o755); err != nil {
		return fmt.Errorf("create mutation lock directory: %w", err)
	}
	lock, err := os.OpenFile(m.MutationLockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open mutation lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock mutation state: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	return fn()
}

func rebaseProcesses(before, desired, current []Process) ([]Process, error) {
	beforeByPID, err := processesByPID(before)
	if err != nil {
		return nil, fmt.Errorf("previous process list: %w", err)
	}
	desiredByPID, err := processesByPID(desired)
	if err != nil {
		return nil, fmt.Errorf("desired process list: %w", err)
	}
	if _, err := processesByPID(current); err != nil {
		return nil, fmt.Errorf("current process list: %w", err)
	}

	result := append([]Process(nil), current...)
	for _, previous := range before {
		if _, keep := desiredByPID[previous.PID]; keep {
			continue
		}
		index := processIndex(result, previous.PID)
		if index < 0 {
			return nil, fmt.Errorf("process %d changed concurrently: no longer exists", previous.PID)
		}
		if result[index] != previous {
			return nil, fmt.Errorf("process %d changed concurrently", previous.PID)
		}
		result = append(result[:index], result[index+1:]...)
	}

	for _, wanted := range desired {
		previous, existed := beforeByPID[wanted.PID]
		if !existed || wanted == previous {
			continue
		}
		index := processIndex(result, wanted.PID)
		if index < 0 {
			return nil, fmt.Errorf("process %d changed concurrently: no longer exists", wanted.PID)
		}
		if result[index] != previous {
			return nil, fmt.Errorf("process %d changed concurrently", wanted.PID)
		}
		result[index] = wanted
	}

	for _, wanted := range desired {
		if _, existed := beforeByPID[wanted.PID]; existed {
			continue
		}
		if processIndex(result, wanted.PID) >= 0 {
			return nil, fmt.Errorf("process %d changed concurrently: already exists", wanted.PID)
		}
		result = append(result, wanted)
	}
	return result, nil
}

func processesByPID(processes []Process) (map[uint32]Process, error) {
	out := make(map[uint32]Process, len(processes))
	for _, process := range processes {
		if _, exists := out[process.PID]; exists {
			return nil, fmt.Errorf("duplicate process pid %d", process.PID)
		}
		out[process.PID] = process
	}
	return out, nil
}

func processIndex(processes []Process, pid uint32) int {
	for i := range processes {
		if processes[i].PID == pid {
			return i
		}
	}
	return -1
}

func processBytes(processes []Process) (uint64, error) {
	var total uint64
	for _, process := range processes {
		if process.UsedMemoryMiB > ^uint64(0)/mib {
			return 0, errors.New("process memory overflows bytes")
		}
		bytes := process.UsedMemoryMiB * mib
		var err error
		total, err = addBytes(total, bytes)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func addBytes(a, b uint64) (uint64, error) {
	if b > ^uint64(0)-a {
		return 0, errors.New("memory value overflows uint64")
	}
	return a + b, nil
}
