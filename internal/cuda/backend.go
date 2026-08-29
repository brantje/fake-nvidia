package cuda

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/brantje/fake-nvidia/internal/control"
	"github.com/brantje/fake-nvidia/profiles"
)

const defaultCUDAVersion = 13000

// ControlBackend makes the existing Mock-NVML-backed effective state the source
// of truth for CUDA enumeration and memory accounting.
type ControlBackend struct {
	manager     *control.Manager
	capability  map[string]profiles.ComputeCapability
	cudaVersion int
}

// NewControlBackendFromEnv constructs the production backend from the same
// config/override environment used by nvidia-smi inside the injected runtime.
func NewControlBackendFromEnv() (*ControlBackend, error) {
	configPath := strings.TrimSpace(os.Getenv("MOCK_NVML_CONFIG"))
	overridePath := strings.TrimSpace(os.Getenv("MOCK_NVML_OVERRIDES"))
	ctlBinary := envOr("FAKE_NVIDIA_CTL_BIN", "nvml-mock-ctl")
	smiBinary := envOr("FAKE_NVIDIA_SMI_BIN", "nvidia-smi")

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

	catalog, err := profiles.LoadCatalog()
	if err != nil {
		return nil, fmt.Errorf("load CUDA profile metadata: %w", err)
	}
	capability := make(map[string]profiles.ComputeCapability)
	for _, id := range catalog.ProfileIDs() {
		profile, ok := catalog.Profile(id)
		if ok {
			capability[profile.Name] = profile.ComputeCapability
		}
	}

	cudaVersion, err := configuredCUDAVersion(configPath)
	if err != nil {
		return nil, err
	}
	return &ControlBackend{
		manager:     control.NewManager(client, observer),
		capability:  capability,
		cudaVersion: cudaVersion,
	}, nil
}

// Devices returns effective consumer-visible state and profile-backed CUDA
// capability metadata.
func (b *ControlBackend) Devices(ctx context.Context) ([]Device, error) {
	if b == nil || b.manager == nil {
		return nil, errors.New("CUDA control backend is not initialized")
	}
	states, err := b.manager.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	devices := make([]Device, 0, len(states))
	for _, state := range states {
		cc := b.capability[state.Name]
		devices = append(devices, Device{
			Index:        state.Index,
			UUID:         state.UUID,
			Name:         state.Name,
			PCIBusID:     stablePCIBusID(state.Index),
			ComputeMajor: cc.Major,
			ComputeMinor: cc.Minor,
			TotalBytes:   state.TotalBytes,
			UsedBytes:    state.UsedBytes,
			FreeBytes:    state.FreeBytes,
		})
	}
	return devices, nil
}

// Reserve increases effective used VRAM through the Phase 4 mutation path.
func (b *ControlBackend) Reserve(ctx context.Context, device int, bytes uint64) error {
	if b == nil || b.manager == nil {
		return errors.New("CUDA control backend is not initialized")
	}
	err := b.manager.ReserveMemory(ctx, strconv.Itoa(device), bytes)
	if errors.Is(err, control.ErrInsufficientMemory) {
		return fmt.Errorf("%w: %v", ErrOutOfMemory, err)
	}
	return err
}

// Release decreases effective used VRAM through the Phase 4 mutation path.
func (b *ControlBackend) Release(ctx context.Context, device int, bytes uint64) error {
	if b == nil || b.manager == nil {
		return errors.New("CUDA control backend is not initialized")
	}
	return b.manager.ReleaseMemory(ctx, strconv.Itoa(device), bytes)
}

// CUDAVersion returns CUDA's packed numeric version, e.g. 13.0 -> 13000.
func (b *ControlBackend) CUDAVersion() int {
	if b == nil || b.cudaVersion <= 0 {
		return defaultCUDAVersion
	}
	return b.cudaVersion
}

// configuredCUDAVersion reads the configured major.minor CUDA version from the
// explicit override or generated Mock NVML configuration.
func configuredCUDAVersion(configPath string) (int, error) {
	if raw := strings.TrimSpace(os.Getenv("FAKE_NVIDIA_CUDA_VERSION")); raw != "" {
		return parseCUDAVersion(raw)
	}
	if configPath == "" {
		return defaultCUDAVersion, nil
	}
	file, err := os.Open(configPath)
	if err != nil {
		return 0, fmt.Errorf("open Mock NVML config for CUDA version: %w", err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "cuda_version:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "cuda_version:"))
		value = strings.Trim(value, "\"")
		return parseCUDAVersion(value)
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read Mock NVML config for CUDA version: %w", err)
	}
	return defaultCUDAVersion, nil
}

// parseCUDAVersion converts a strict major.minor version into CUDA's packed
// integer representation, for example 12.8 -> 12080.
func parseCUDAVersion(raw string) (int, error) {
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 2 {
		return 0, fmt.Errorf("CUDA version %q must be major.minor", raw)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return 0, fmt.Errorf("invalid CUDA version %q", raw)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 || minor > 99 {
		return 0, fmt.Errorf("invalid CUDA version %q", raw)
	}
	return major*1000 + minor*10, nil
}

// stablePCIBusID derives a deterministic fake PCI bus identifier from a device index.
func stablePCIBusID(index int) string {
	n := index + 1
	return fmt.Sprintf("%04x:%02x:00.0", n/256, n%256)
}

// envOr returns a trimmed environment value or fallback when it is unset.
func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
