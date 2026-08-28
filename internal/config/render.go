package config

import (
	"bytes"
	"fmt"
	"strconv"
)

// RenderYAML implements the corresponding fake-nvidia operation.
func RenderYAML(cfg MockConfig) ([]byte, error) {
	if cfg.Version == "" {
		return nil, fmt.Errorf("config version is required")
	}
	if len(cfg.Devices) == 0 {
		return nil, fmt.Errorf("at least one device is required")
	}
	if len(cfg.Devices) > maxDevices {
		return nil, fmt.Errorf("device count %d exceeds Mock NVML limit %d", len(cfg.Devices), maxDevices)
	}
	var b bytes.Buffer
	q := strconv.Quote
	fmt.Fprintf(&b, "version: %s\n\n", q(cfg.Version))
	b.WriteString("system:\n")
	fmt.Fprintf(&b, "  driver_version: %s\n", q(cfg.System.DriverVersion))
	fmt.Fprintf(&b, "  cuda_version: %s\n", q(cfg.System.CUDAVersion))
	fmt.Fprintf(&b, "  cuda_version_major: %d\n", cfg.System.CUDAVersionMajor)
	fmt.Fprintf(&b, "  cuda_version_minor: %d\n", cfg.System.CUDAVersionMinor)
	fmt.Fprintf(&b, "  num_devices: %d\n\n", len(cfg.Devices))
	b.WriteString("device_defaults: {}\n\n")
	b.WriteString("devices:\n")
	for _, d := range cfg.Devices {
		fmt.Fprintf(&b, "  - index: %d\n", d.Index)
		fmt.Fprintf(&b, "    minor_number: %d\n", d.Index)
		fmt.Fprintf(&b, "    uuid: %s\n", q(d.UUID))
		fmt.Fprintf(&b, "    name: %s\n", q(d.Name))
		fmt.Fprintf(&b, "    architecture: %s\n", q(d.Architecture))
		b.WriteString("    compute_capability:\n")
		fmt.Fprintf(&b, "      major: %d\n", d.ComputeCapability.Major)
		fmt.Fprintf(&b, "      minor: %d\n", d.ComputeCapability.Minor)
		b.WriteString("    memory:\n")
		fmt.Fprintf(&b, "      total_bytes: %d\n", d.Memory.TotalBytes)
		fmt.Fprintf(&b, "      reserved_bytes: %d\n", d.Memory.ReservedBytes)
		fmt.Fprintf(&b, "      free_bytes: %d\n", d.Memory.FreeBytes)
		fmt.Fprintf(&b, "      used_bytes: %d\n", d.Memory.UsedBytes)
		b.WriteString("    pci:\n")
		fmt.Fprintf(&b, "      bus_id: %s\n", q(d.PCI.BusID))
		b.WriteString("    utilization:\n")
		fmt.Fprintf(&b, "      gpu: %d\n", d.Utilization.GPU)
		fmt.Fprintf(&b, "      memory: %d\n", d.Utilization.Memory)
		b.WriteString("    thermal:\n")
		fmt.Fprintf(&b, "      temperature_gpu_c: %d\n", d.Thermal.TemperatureGPUC)
		b.WriteString("    power:\n")
		fmt.Fprintf(&b, "      current_draw_mw: %d\n", d.Power.CurrentDrawMW)
		if len(d.Processes) > 0 {
			b.WriteString("    processes:\n")
			for _, p := range d.Processes {
				fmt.Fprintf(&b, "      - pid: %d\n", p.PID)
				if p.Type != "" {
					fmt.Fprintf(&b, "        type: %s\n", q(p.Type))
				}
				if p.Name != "" {
					fmt.Fprintf(&b, "        name: %s\n", q(p.Name))
				}
				if p.UsedMemoryMiB != 0 {
					fmt.Fprintf(&b, "        used_memory_mib: %d\n", p.UsedMemoryMiB)
				}
				if p.SMUtil != 0 {
					fmt.Fprintf(&b, "        sm_util: %d\n", p.SMUtil)
				}
				if p.MemoryUtil != 0 {
					fmt.Fprintf(&b, "        mem_util: %d\n", p.MemoryUtil)
				}
				if p.EncoderUtil != 0 {
					fmt.Fprintf(&b, "        enc_util: %d\n", p.EncoderUtil)
				}
				if p.DecoderUtil != 0 {
					fmt.Fprintf(&b, "        dec_util: %d\n", p.DecoderUtil)
				}
			}
		}
		if d.Failure != nil {
			b.WriteString("    failure:\n")
			fmt.Fprintf(&b, "      mode: %s\n", q(d.Failure.Mode))
			if d.Failure.Probability != 0 {
				fmt.Fprintf(&b, "      probability: %s\n", strconv.FormatFloat(d.Failure.Probability, 'g', -1, 64))
			}
			if d.Failure.AfterCalls != 0 {
				fmt.Fprintf(&b, "      after_calls: %d\n", d.Failure.AfterCalls)
			}
			if d.Failure.XID != 0 {
				b.WriteString("      xid:\n")
				fmt.Fprintf(&b, "        code: %d\n", d.Failure.XID)
			}
		}
	}
	return b.Bytes(), nil
}
