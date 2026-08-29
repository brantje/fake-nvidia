package fakellama

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/brantje/fake-nvidia/internal/control"
)

// ErrOutOfMemory indicates that the requested fake llama-server process memory
// cannot fit in the current effective fake GPU state.
var ErrOutOfMemory = errors.New("fake llama-server CUDA out of memory")

// ProcessRegistry owns only the fake llama-server process records. The
// underlying effective GPU state remains the Mock NVML config + override files.
type ProcessRegistry interface {
	Register(ctx context.Context, pid uint32, name string, targets []string, totalBytes uint64, tensorSplit []float64, smUtil, memUtil uint32) error
	Resize(ctx context.Context, pid uint32, name string, targets []string, totalBytes uint64, tensorSplit []float64, smUtil, memUtil uint32) error
	Release(ctx context.Context, pid uint32, targets []string) error
}

// NVMLRegistry reconciles fake llama-server PID records through the existing
// cross-process control manager, so process telemetry and VRAM are one update.
type NVMLRegistry struct {
	manager *control.Manager
}

// NewNVMLRegistry constructs a process registry around an existing manager.
func NewNVMLRegistry(manager *control.Manager) *NVMLRegistry {
	return &NVMLRegistry{manager: manager}
}

// NewNVMLRegistryFromEnv connects to the same runtime state used by the CUDA
// shim and bundled nvidia-smi tools.
func NewNVMLRegistryFromEnv() (*NVMLRegistry, error) {
	configPath := strings.TrimSpace(os.Getenv("MOCK_NVML_CONFIG"))
	overridePath := strings.TrimSpace(os.Getenv("MOCK_NVML_OVERRIDES"))
	ctlBinary := envString("FAKE_NVIDIA_CTL_BIN", "nvml-mock-ctl")
	smiBinary := envString("FAKE_NVIDIA_SMI_BIN", "nvidia-smi")
	client := control.New(ctlBinary, configPath, overridePath)
	observer := control.NewObserver(smiBinary)
	overlay := map[string]string{}
	if configPath != "" {
		overlay["MOCK_NVML_CONFIG"] = configPath
	}
	if overridePath != "" {
		overlay["MOCK_NVML_OVERRIDES"] = overridePath
	}
	if len(overlay) != 0 {
		observer.Runner = control.EnvExecRunner{Values: overlay}
	}
	return NewNVMLRegistry(control.NewManager(client, observer)), nil
}

// Register adds or replaces this PID on the selected fake GPUs.
func (r *NVMLRegistry) Register(ctx context.Context, pid uint32, name string, targets []string, totalBytes uint64, tensorSplit []float64, smUtil, memUtil uint32) error {
	return r.apply(ctx, pid, name, targets, totalBytes, tensorSplit, smUtil, memUtil)
}

// Resize updates the same PID records, preserving unrelated concurrent process
// changes through Manager.ReplaceProcessesFromState.
func (r *NVMLRegistry) Resize(ctx context.Context, pid uint32, name string, targets []string, totalBytes uint64, tensorSplit []float64, smUtil, memUtil uint32) error {
	return r.apply(ctx, pid, name, targets, totalBytes, tensorSplit, smUtil, memUtil)
}

func (r *NVMLRegistry) apply(ctx context.Context, pid uint32, name string, targets []string, totalBytes uint64, tensorSplit []float64, smUtil, memUtil uint32) error {
	if r == nil || r.manager == nil {
		return errors.New("fake llama-server process registry is not initialized")
	}
	if pid == 0 {
		return errors.New("fake llama-server pid must be positive")
	}
	if len(targets) == 0 {
		return errors.New("fake llama-server requires at least one GPU target")
	}
	amounts, err := splitProcessMiB(totalBytes, targets, tensorSplit)
	if err != nil {
		return err
	}

	type prior struct {
		target  string
		process control.Process
		existed bool
	}
	applied := make([]prior, 0, len(targets))
	for i, target := range targets {
		before, old, existed, err := r.deviceAndProcess(ctx, target, pid)
		if err != nil {
			r.rollback(ctx, pid, applied)
			return err
		}
		oldBytes := uint64(0)
		if existed {
			if old.UsedMemoryMiB > math.MaxUint64/mib {
				r.rollback(ctx, pid, applied)
				return errors.New("existing fake process memory overflows bytes")
			}
			oldBytes = old.UsedMemoryMiB * mib
		}
		newBytes := amounts[i] * mib
		if newBytes > oldBytes && newBytes-oldBytes > before.FreeBytes {
			r.rollback(ctx, pid, applied)
			return fmt.Errorf("%w on GPU %s: need %d additional bytes, %d free", ErrOutOfMemory, target, newBytes-oldBytes, before.FreeBytes)
		}

		desired := withoutPID(before.Processes, pid)
		desired = append(desired, control.Process{
			PID:           pid,
			Type:          "C",
			Name:          name,
			UsedMemoryMiB: amounts[i],
			SMUtil:        smUtil,
			MemoryUtil:    memUtil,
		})
		if err := r.manager.ReplaceProcessesFromState(ctx, target, before, desired); err != nil {
			r.rollback(ctx, pid, applied)
			return fmt.Errorf("register fake llama-server on GPU %s: %w", target, err)
		}
		applied = append(applied, prior{target: target, process: old, existed: existed})
	}
	return nil
}

func (r *NVMLRegistry) rollback(ctx context.Context, pid uint32, applied []struct {
	target  string
	process control.Process
	existed bool
}) {
	for i := len(applied) - 1; i >= 0; i-- {
		item := applied[i]
		before, _, _, err := r.deviceAndProcess(ctx, item.target, pid)
		if err != nil {
			continue
		}
		desired := withoutPID(before.Processes, pid)
		if item.existed {
			desired = append(desired, item.process)
		}
		_ = r.manager.ReplaceProcessesFromState(ctx, item.target, before, desired)
	}
}

// Release removes only this PID from each selected device and leaves unrelated
// process records and non-process memory untouched.
func (r *NVMLRegistry) Release(ctx context.Context, pid uint32, targets []string) error {
	if r == nil || r.manager == nil {
		return errors.New("fake llama-server process registry is not initialized")
	}
	var joined error
	for _, target := range targets {
		before, _, existed, err := r.deviceAndProcess(ctx, target, pid)
		if err != nil {
			joined = errors.Join(joined, err)
			continue
		}
		if !existed {
			continue
		}
		if err := r.manager.ReplaceProcessesFromState(ctx, target, before, withoutPID(before.Processes, pid)); err != nil {
			joined = errors.Join(joined, fmt.Errorf("release fake llama-server from GPU %s: %w", target, err))
		}
	}
	return joined
}

func (r *NVMLRegistry) deviceAndProcess(ctx context.Context, target string, pid uint32) (control.DeviceState, control.Process, bool, error) {
	states, err := r.manager.Snapshot(ctx)
	if err != nil {
		return control.DeviceState{}, control.Process{}, false, err
	}
	for _, state := range states {
		if strconv.Itoa(state.Index) != target && state.UUID != target {
			continue
		}
		for _, process := range state.Processes {
			if process.PID == pid {
				return state, process, true, nil
			}
		}
		return state, control.Process{}, false, nil
	}
	return control.DeviceState{}, control.Process{}, false, fmt.Errorf("GPU %q not found", target)
}

func withoutPID(processes []control.Process, pid uint32) []control.Process {
	out := make([]control.Process, 0, len(processes))
	for _, process := range processes {
		if process.PID != pid {
			out = append(out, process)
		}
	}
	return out
}

func splitProcessMiB(totalBytes uint64, targets []string, tensorSplit []float64) ([]uint64, error) {
	totalMiB := totalBytes / mib
	if totalBytes%mib != 0 {
		totalMiB++
	}
	weights := make([]float64, len(targets))
	var sum float64
	for i, target := range targets {
		weight := float64(1)
		if index, err := strconv.Atoi(target); err == nil && index >= 0 && index < len(tensorSplit) && tensorSplit[index] > 0 {
			weight = tensorSplit[index]
		}
		weights[i] = weight
		sum += weight
	}
	if sum <= 0 || math.IsInf(sum, 0) || math.IsNaN(sum) {
		return nil, errors.New("invalid fake llama-server GPU weights")
	}
	amounts := make([]uint64, len(targets))
	var assigned uint64
	for i := 0; i < len(targets)-1; i++ {
		amounts[i] = uint64(math.Floor(float64(totalMiB) * weights[i] / sum))
		assigned += amounts[i]
	}
	if len(targets) != 0 {
		amounts[len(targets)-1] = totalMiB - assigned
	}
	return amounts, nil
}

func envString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
