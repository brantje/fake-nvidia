package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
)

type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type Client struct {
	Binary       string
	OverrideFile string
	ConfigFile   string
	Runner       Runner
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

func New(binary, configFile, overrideFile string) *Client {
	return &Client{Binary: binary, ConfigFile: configFile, OverrideFile: overrideFile, Runner: ExecRunner{}}
}

func (c *Client) SetMemory(ctx context.Context, target string, usedBytes, freeBytes uint64) error {
	return c.run(ctx, "set", "--gpu", target,
		"memory.used_bytes="+strconv.FormatUint(usedBytes, 10),
		"memory.free_bytes="+strconv.FormatUint(freeBytes, 10))
}

func (c *Client) SetUtilization(ctx context.Context, target string, gpu, memory uint32) error {
	if gpu > 100 || memory > 100 {
		return errors.New("utilization must be between 0 and 100")
	}
	if gpu == memory {
		return c.run(ctx, "util", "--gpu", target, strconv.FormatUint(uint64(gpu), 10))
	}
	return c.run(ctx, "set", "--gpu", target,
		"utilization.gpu="+strconv.FormatUint(uint64(gpu), 10),
		"utilization.memory="+strconv.FormatUint(uint64(memory), 10),
		"dynamic_metrics.utilization=null")
}

func (c *Client) SetProcesses(ctx context.Context, target string, processes []Process) error {
	for i, p := range processes {
		if err := validateProcess(p); err != nil {
			return fmt.Errorf("process %d: %w", i, err)
		}
	}
	payload, err := json.Marshal(processes)
	if err != nil {
		return err
	}
	return c.run(ctx, "set", "--gpu", target, "processes="+string(payload))
}

func (c *Client) SetTemperature(ctx context.Context, target string, celsius int) error {
	if celsius < 0 || celsius > 200 {
		return errors.New("temperature must be between 0 and 200 C")
	}
	return c.run(ctx, "temp", "--gpu", target, strconv.Itoa(celsius))
}

func (c *Client) SetPower(ctx context.Context, target string, watts float64) error {
	if watts < 0 || watts > 100000 {
		return errors.New("power must be between 0 and 100000 watts")
	}
	return c.run(ctx, "power", "--gpu", target, strconv.FormatFloat(watts, 'f', -1, 64))
}

func (c *Client) SetFailure(ctx context.Context, target, mode string, afterCalls int, xid uint64) error {
	switch mode {
	case "healthy", "lost", "fallen_off_bus", "ecc_uncorrectable":
	default:
		return fmt.Errorf("unsupported failure mode %q", mode)
	}
	if afterCalls < 0 {
		return errors.New("after-calls cannot be negative")
	}
	args := []string{"fail", "--gpu", target, "--mode", mode}
	if afterCalls > 0 {
		args = append(args, "--after-calls", strconv.Itoa(afterCalls))
	}
	if xid > 0 {
		args = append(args, "--xid", strconv.FormatUint(xid, 10))
	}
	return c.run(ctx, args...)
}

func (c *Client) Reset(ctx context.Context, target string) error {
	if target == "" || target == "all" {
		return c.run(ctx, "reset")
	}
	return c.run(ctx, "reset", "--gpu", target)
}

func (c *Client) Status(ctx context.Context, target string) ([]byte, error) {
	args := []string{"status"}
	if target != "" {
		args = append(args, "--gpu", target)
	}
	return c.runOutput(ctx, args...)
}

func (c *Client) run(ctx context.Context, args ...string) error {
	_, err := c.runOutput(ctx, args...)
	return err
}

func (c *Client) runOutput(ctx context.Context, args ...string) ([]byte, error) {
	if c == nil {
		return nil, errors.New("nil control client")
	}
	if c.Binary == "" {
		c.Binary = "nvml-mock-ctl"
	}
	if c.Runner == nil {
		c.Runner = ExecRunner{}
	}
	prefix := make([]string, 0, 4)
	if c.OverrideFile != "" {
		prefix = append(prefix, "--file", c.OverrideFile)
	}
	if c.ConfigFile != "" {
		prefix = append(prefix, "--config", c.ConfigFile)
	}
	full := append(prefix, args...)
	out, err := c.Runner.Run(ctx, c.Binary, full...)
	if err != nil {
		return out, fmt.Errorf("%s failed: %w: %s", c.Binary, err, string(out))
	}
	return out, nil
}

func validateProcess(p Process) error {
	if p.PID == 0 {
		return errors.New("pid must be positive")
	}
	if p.Type != "" && p.Type != "C" && p.Type != "G" {
		return errors.New("type must be C or G")
	}
	if p.SMUtil > 100 || p.MemoryUtil > 100 || p.EncoderUtil > 100 || p.DecoderUtil > 100 {
		return errors.New("process utilization must be <= 100")
	}
	return nil
}
