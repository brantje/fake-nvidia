package control

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const mib = uint64(1024 * 1024)

// DeviceState is the effective consumer-visible state reported by nvidia-smi.
type DeviceState struct {
	Index             int       `json:"index"`
	UUID              string    `json:"uuid"`
	Name              string    `json:"name"`
	TotalBytes        uint64    `json:"total_bytes"`
	UsedBytes         uint64    `json:"used_bytes"`
	FreeBytes         uint64    `json:"free_bytes"`
	GPUUtilization    uint32    `json:"gpu_utilization"`
	MemoryUtilization uint32    `json:"memory_utilization"`
	Processes         []Process `json:"processes"`
}

// Observer reads effective state through the same nvidia-smi surface consumers use.
type Observer struct {
	Binary string
	Runner Runner
}

// NewObserver constructs an effective-state observer.
func NewObserver(binary string) *Observer {
	return &Observer{Binary: binary, Runner: ExecRunner{}}
}

// Snapshot returns all effective simulated devices and their processes.
func (o *Observer) Snapshot(ctx context.Context) ([]DeviceState, error) {
	if o == nil {
		return nil, errors.New("nil observer")
	}
	if o.Binary == "" {
		o.Binary = "nvidia-smi"
	}
	if o.Runner == nil {
		o.Runner = ExecRunner{}
	}

	out, err := o.Runner.Run(ctx, o.Binary,
		"--query-gpu=index,uuid,name,memory.total,memory.used,memory.free,utilization.gpu,utilization.memory",
		"--format=csv,noheader,nounits")
	if err != nil {
		return nil, fmt.Errorf("%s GPU query failed: %w: %s", o.Binary, err, string(out))
	}
	devices, err := parseGPUQuery(out)
	if err != nil {
		return nil, err
	}

	processOut, processErr := o.Runner.Run(ctx, o.Binary,
		"--query-compute-apps=pid,gpu_uuid,used_memory,process_name",
		"--format=csv,noheader,nounits")
	if processErr != nil {
		return nil, fmt.Errorf("%s process query failed: %w: %s", o.Binary, processErr, string(processOut))
	}
	if err := attachProcesses(devices, processOut); err != nil {
		return nil, err
	}

	pmonOut, pmonErr := o.Runner.Run(ctx, o.Binary, "pmon", "-c", "1", "-s", "u")
	if pmonErr == nil {
		attachPmon(devices, pmonOut)
	}
	return devices, nil
}

// Device returns one effective device selected by index or UUID.
func (o *Observer) Device(ctx context.Context, target string) (DeviceState, error) {
	devices, err := o.Snapshot(ctx)
	if err != nil {
		return DeviceState{}, err
	}
	for _, device := range devices {
		if strconv.Itoa(device.Index) == target || device.UUID == target {
			return device, nil
		}
	}
	return DeviceState{}, fmt.Errorf("GPU %q not found", target)
}

func parseGPUQuery(out []byte) ([]DeviceState, error) {
	lines := nonEmptyLines(string(out))
	devices := make([]DeviceState, 0, len(lines))
	for lineNo, line := range lines {
		parts := splitCSV(line)
		if len(parts) != 8 {
			return nil, fmt.Errorf("parse nvidia-smi GPU line %d: expected 8 fields, got %d", lineNo+1, len(parts))
		}
		index, err := strconv.Atoi(parts[0])
		if err != nil || index < 0 {
			return nil, fmt.Errorf("parse nvidia-smi GPU line %d: invalid index %q", lineNo+1, parts[0])
		}
		total, err := parseMiB(parts[3])
		if err != nil {
			return nil, fmt.Errorf("parse nvidia-smi GPU line %d total memory: %w", lineNo+1, err)
		}
		used, err := parseMiB(parts[4])
		if err != nil {
			return nil, fmt.Errorf("parse nvidia-smi GPU line %d used memory: %w", lineNo+1, err)
		}
		free, err := parseMiB(parts[5])
		if err != nil {
			return nil, fmt.Errorf("parse nvidia-smi GPU line %d free memory: %w", lineNo+1, err)
		}
		gpu, err := parsePercent(parts[6])
		if err != nil {
			return nil, fmt.Errorf("parse nvidia-smi GPU line %d GPU utilization: %w", lineNo+1, err)
		}
		memory, err := parsePercent(parts[7])
		if err != nil {
			return nil, fmt.Errorf("parse nvidia-smi GPU line %d memory utilization: %w", lineNo+1, err)
		}
		devices = append(devices, DeviceState{
			Index: index, UUID: parts[1], Name: parts[2], TotalBytes: total,
			UsedBytes: used, FreeBytes: free, GPUUtilization: gpu, MemoryUtilization: memory,
			Processes: []Process{},
		})
	}
	return devices, nil
}

func attachProcesses(devices []DeviceState, out []byte) error {
	byUUID := make(map[string]int, len(devices))
	for i := range devices {
		byUUID[devices[i].UUID] = i
	}
	for lineNo, line := range nonEmptyLines(string(out)) {
		parts := splitCSV(line)
		if len(parts) != 4 {
			return fmt.Errorf("parse nvidia-smi process line %d: expected 4 fields, got %d", lineNo+1, len(parts))
		}
		pid, err := strconv.ParseUint(parts[0], 10, 32)
		if err != nil || pid == 0 {
			return fmt.Errorf("parse nvidia-smi process line %d: invalid pid %q", lineNo+1, parts[0])
		}
		used, err := strconv.ParseUint(parts[2], 10, 64)
		if err != nil {
			return fmt.Errorf("parse nvidia-smi process line %d: invalid used memory %q", lineNo+1, parts[2])
		}
		deviceIndex, ok := byUUID[parts[1]]
		if !ok {
			return fmt.Errorf("parse nvidia-smi process line %d: unknown GPU UUID %q", lineNo+1, parts[1])
		}
		devices[deviceIndex].Processes = append(devices[deviceIndex].Processes, Process{
			PID: uint32(pid), Type: "C", Name: parts[3], UsedMemoryMiB: used,
		})
	}
	return nil
}

func attachPmon(devices []DeviceState, out []byte) {
	for _, line := range nonEmptyLines(string(out)) {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		gpuIndex, gpuErr := strconv.Atoi(fields[0])
		pid64, pidErr := strconv.ParseUint(fields[1], 10, 32)
		if gpuErr != nil || pidErr != nil {
			continue
		}
		utils := make([]uint32, 4)
		valid := true
		for i := range utils {
			v, err := strconv.ParseUint(fields[3+i], 10, 32)
			if err != nil || v > 100 {
				valid = false
				break
			}
			utils[i] = uint32(v)
		}
		if !valid {
			continue
		}
		for d := range devices {
			if devices[d].Index != gpuIndex {
				continue
			}
			for p := range devices[d].Processes {
				if devices[d].Processes[p].PID != uint32(pid64) {
					continue
				}
				devices[d].Processes[p].Type = fields[2]
				devices[d].Processes[p].SMUtil = utils[0]
				devices[d].Processes[p].MemoryUtil = utils[1]
				devices[d].Processes[p].EncoderUtil = utils[2]
				devices[d].Processes[p].DecoderUtil = utils[3]
			}
		}
	}
}

func parseMiB(raw string) (uint64, error) {
	v, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid MiB value %q", raw)
	}
	if v > ^uint64(0)/mib {
		return 0, errors.New("MiB value overflows bytes")
	}
	return v * mib, nil
}

func parsePercent(raw string) (uint32, error) {
	v, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 32)
	if err != nil || v > 100 {
		return 0, fmt.Errorf("invalid percentage %q", raw)
	}
	return uint32(v), nil
}

func nonEmptyLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func splitCSV(line string) []string {
	parts := strings.Split(line, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
