package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/brantje/fake-nvidia/profiles"
)

const mib = uint64(1024 * 1024)

type System struct {
	DriverVersion string `json:"driver_version"`
	CUDAVersion   string `json:"cuda_version"`
}

type Process struct {
	PID           uint32 `json:"pid"`
	Type          string `json:"type,omitempty"`
	Name          string `json:"name,omitempty"`
	UsedMemoryMiB uint64 `json:"used_memory_mib,omitempty"`
	SMUtil        uint32 `json:"sm_util,omitempty"`
	MemoryUtil    uint32 `json:"mem_util,omitempty"`
	EncoderUtil   uint32 `json:"enc_util,omitempty"`
	DecoderUtil   uint32 `json:"dec_util,omitempty"`
}

type Failure struct {
	Mode        string  `json:"mode"`
	Probability float64 `json:"probability,omitempty"`
	AfterCalls  int64   `json:"after_calls,omitempty"`
	XID         uint64  `json:"xid,omitempty"`
}

type DeviceRequest struct {
	Profile      string    `json:"profile"`
	VRAMMiB      uint64    `json:"vram_mib,omitempty"`
	UsedMiB      uint64    `json:"used_mib,omitempty"`
	GPUUtil      uint32    `json:"gpu_util,omitempty"`
	MemoryUtil   uint32    `json:"memory_util,omitempty"`
	TemperatureC *int      `json:"temperature_c,omitempty"`
	PowerDrawMW  *uint32   `json:"power_draw_mw,omitempty"`
	Processes    []Process `json:"processes,omitempty"`
	Failure      *Failure  `json:"failure,omitempty"`
}

type Spec struct {
	System  System          `json:"system"`
	Devices []DeviceRequest `json:"devices"`
}

type MockConfig struct {
	Version string
	System  MockSystem
	Devices []Device
}

type MockSystem struct {
	DriverVersion    string
	CUDAVersion      string
	CUDAVersionMajor int
	CUDAVersionMinor int
}

type Device struct {
	Index             int
	UUID              string
	Name              string
	Architecture      string
	ComputeCapability profiles.ComputeCapability
	Memory            Memory
	PCI               PCI
	Utilization       Utilization
	Thermal           Thermal
	Power             Power
	Processes         []Process
	Failure           *Failure
	ProfileSource     string
	UpstreamConfig    string
}

type Memory struct {
	TotalBytes    uint64
	ReservedBytes uint64
	UsedBytes     uint64
	FreeBytes     uint64
}

type PCI struct {
	BusID string
}

type Utilization struct {
	GPU    uint32
	Memory uint32
}

type Thermal struct {
	TemperatureGPUC int
}

type Power struct {
	CurrentDrawMW uint32
}

func LoadSpecJSON(r io.Reader) (Spec, error) {
	var spec Spec
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return Spec{}, fmt.Errorf("decode spec: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return Spec{}, errors.New("decode spec: multiple JSON documents")
		}
		return Spec{}, fmt.Errorf("decode spec: %w", err)
	}
	return spec, nil
}

func Compose(catalog *profiles.Catalog, spec Spec) (MockConfig, error) {
	if catalog == nil {
		return MockConfig{}, errors.New("profile catalog is required")
	}
	if len(spec.Devices) == 0 {
		return MockConfig{}, errors.New("at least one device is required")
	}
	system, err := normalizeSystem(spec.System)
	if err != nil {
		return MockConfig{}, err
	}
	out := MockConfig{Version: "1.0", System: system, Devices: make([]Device, 0, len(spec.Devices))}
	for i, req := range spec.Devices {
		p, ok := catalog.Profile(req.Profile)
		if !ok {
			return MockConfig{}, fmt.Errorf("device %d: unknown profile %q", i, req.Profile)
		}
		device, err := composeDevice(i, p, req)
		if err != nil {
			return MockConfig{}, fmt.Errorf("device %d: %w", i, err)
		}
		out.Devices = append(out.Devices, device)
	}
	return out, nil
}

func ComposeTopology(catalog *profiles.Catalog, system System, topologyID string) (MockConfig, error) {
	topology, ok := catalog.Topology(topologyID)
	if !ok {
		return MockConfig{}, fmt.Errorf("unknown topology %q", topologyID)
	}
	spec := Spec{System: system, Devices: make([]DeviceRequest, 0, len(topology.Devices))}
	for _, d := range topology.Devices {
		spec.Devices = append(spec.Devices, DeviceRequest{
			Profile: d.Profile, VRAMMiB: d.VRAMMiB, UsedMiB: d.UsedMiB,
			GPUUtil: d.GPUUtil, MemoryUtil: d.MemoryUtil,
		})
	}
	return Compose(catalog, spec)
}

func Repeated(profile string, count int, vramMiB uint64) ([]DeviceRequest, error) {
	if count <= 0 {
		return nil, errors.New("count must be positive")
	}
	devices := make([]DeviceRequest, count)
	for i := range devices {
		devices[i] = DeviceRequest{Profile: profile, VRAMMiB: vramMiB}
	}
	return devices, nil
}

func normalizeSystem(in System) (MockSystem, error) {
	if in.DriverVersion == "" {
		in.DriverVersion = "580.173.02"
	}
	if in.CUDAVersion == "" {
		in.CUDAVersion = "13.0"
	}
	parts := strings.Split(in.CUDAVersion, ".")
	if len(parts) < 2 {
		return MockSystem{}, fmt.Errorf("cuda version %q must be major.minor", in.CUDAVersion)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return MockSystem{}, fmt.Errorf("invalid cuda version %q", in.CUDAVersion)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return MockSystem{}, fmt.Errorf("invalid cuda version %q", in.CUDAVersion)
	}
	return MockSystem{
		DriverVersion: in.DriverVersion, CUDAVersion: in.CUDAVersion,
		CUDAVersionMajor: major, CUDAVersionMinor: minor,
	}, nil
}

func composeDevice(index int, p profiles.Profile, req DeviceRequest) (Device, error) {
	totalMiB := p.TotalMemoryMiB
	if req.VRAMMiB != 0 {
		totalMiB = req.VRAMMiB
	}
	if totalMiB <= p.ReservedMemoryMiB {
		return Device{}, fmt.Errorf("total VRAM %d MiB must exceed reserved VRAM %d MiB", totalMiB, p.ReservedMemoryMiB)
	}
	if req.UsedMiB+p.ReservedMemoryMiB > totalMiB {
		return Device{}, fmt.Errorf("used (%d MiB) + reserved (%d MiB) exceeds total (%d MiB)", req.UsedMiB, p.ReservedMemoryMiB, totalMiB)
	}
	if req.GPUUtil > 100 || req.MemoryUtil > 100 {
		return Device{}, errors.New("utilization must be between 0 and 100")
	}
	for i, proc := range req.Processes {
		if err := validateProcess(proc); err != nil {
			return Device{}, fmt.Errorf("process %d: %w", i, err)
		}
	}
	if req.Failure != nil {
		if err := validateFailure(*req.Failure); err != nil {
			return Device{}, err
		}
	}
	temp := p.TemperatureC
	if req.TemperatureC != nil {
		temp = *req.TemperatureC
	}
	if temp < 0 || temp > 200 {
		return Device{}, errors.New("temperature must be between 0 and 200 C")
	}
	power := p.PowerDrawMW
	if req.PowerDrawMW != nil {
		power = *req.PowerDrawMW
	}
	total := totalMiB * mib
	reserved := p.ReservedMemoryMiB * mib
	used := req.UsedMiB * mib
	return Device{
		Index: index, UUID: stableUUID(index), Name: p.Name, Architecture: p.Architecture,
		ComputeCapability: p.ComputeCapability,
		Memory:            Memory{TotalBytes: total, ReservedBytes: reserved, UsedBytes: used, FreeBytes: total - reserved - used},
		PCI:               PCI{BusID: stablePCIBusID(index)},
		Utilization:       Utilization{GPU: req.GPUUtil, Memory: req.MemoryUtil},
		Thermal:           Thermal{TemperatureGPUC: temp}, Power: Power{CurrentDrawMW: power},
		Processes: append([]Process(nil), req.Processes...), Failure: cloneFailure(req.Failure),
		ProfileSource: p.Source, UpstreamConfig: p.UpstreamConfig,
	}, nil
}

func validateProcess(p Process) error {
	if p.PID == 0 {
		return errors.New("pid must be positive")
	}
	if p.Type != "" && p.Type != "C" && p.Type != "G" {
		return errors.New("process type must be C or G")
	}
	if p.SMUtil > 100 || p.MemoryUtil > 100 || p.EncoderUtil > 100 || p.DecoderUtil > 100 {
		return errors.New("process utilization must be between 0 and 100")
	}
	return nil
}

func validateFailure(f Failure) error {
	switch f.Mode {
	case "lost", "fallen_off_bus", "ecc_uncorrectable":
	case "healthy", "":
		return errors.New("healthy/empty failure should be represented by no failure block")
	default:
		return fmt.Errorf("unsupported failure mode %q", f.Mode)
	}
	if f.Probability < 0 || f.Probability > 1 {
		return errors.New("failure probability must be between 0 and 1")
	}
	if f.AfterCalls < 0 {
		return errors.New("failure after_calls cannot be negative")
	}
	return nil
}

func stableUUID(index int) string {
	return fmt.Sprintf("GPU-00000000-0000-0000-0000-%012x", index+1)
}

func stablePCIBusID(index int) string {
	n := index + 1
	return fmt.Sprintf("%04x:%02x:00.0", n/256, n%256)
}

func cloneFailure(f *Failure) *Failure {
	if f == nil {
		return nil
	}
	copy := *f
	return &copy
}
