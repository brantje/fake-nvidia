package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

const processMiB = uint64(1024 * 1024)

// SetProcessesReconciled updates the process list and device memory accounting
// in one upstream override transaction. nonProcessUsedBytes represents explicit
// system/external usage that is not owned by the supplied fake processes.
func (c *Client) SetProcessesReconciled(ctx context.Context, target string, processes []Process, totalBytes, reservedBytes, nonProcessUsedBytes uint64) error {
	payload, processBytes, err := validatedProcessPayload(processes)
	if err != nil {
		return err
	}
	if reservedBytes > totalBytes {
		return errors.New("reserved memory exceeds total memory")
	}
	available := totalBytes - reservedBytes
	if nonProcessUsedBytes > available {
		return errors.New("non-process used memory exceeds available memory")
	}
	if processBytes > available-nonProcessUsedBytes {
		return errors.New("process memory plus non-process memory exceeds available memory")
	}
	usedBytes := nonProcessUsedBytes + processBytes
	freeBytes := available - usedBytes
	return c.run(ctx, "set", "--gpu", target,
		"processes="+payload,
		"memory.used_bytes="+strconv.FormatUint(usedBytes, 10),
		"memory.free_bytes="+strconv.FormatUint(freeBytes, 10))
}

func validatedProcessPayload(processes []Process) (string, uint64, error) {
	var processBytes uint64
	for i, process := range processes {
		if err := validateProcess(process); err != nil {
			return "", 0, fmt.Errorf("process %d: %w", i, err)
		}
		if process.UsedMemoryMiB > ^uint64(0)/processMiB {
			return "", 0, fmt.Errorf("process %d: used memory is too large", i)
		}
		bytes := process.UsedMemoryMiB * processMiB
		if bytes > ^uint64(0)-processBytes {
			return "", 0, errors.New("aggregate process memory is too large")
		}
		processBytes += bytes
	}
	if processes == nil {
		processes = []Process{}
	}
	payload, err := json.Marshal(processes)
	if err != nil {
		return "", 0, err
	}
	return string(payload), processBytes, nil
}
