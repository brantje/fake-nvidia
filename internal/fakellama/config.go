package fakellama

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

const mib = uint64(1024 * 1024)

// Config describes the manager-compatible fake llama-server process and its
// deterministic resource/failure behavior.
type Config struct {
	Host                string
	Port                int
	ModelPath           string
	Targets             []string
	TensorSplit         []float64
	ContextSize         int
	VRAMBytes           uint64
	VRAMExplicit        bool
	KVBytesPerToken     uint64
	LoadDelay           time.Duration
	StartupFail         bool
	ForceOOM            bool
	CrashAfterReady     time.Duration
	HangShutdown        bool
	GrowthAfter         time.Duration
	GrowthBytes         uint64
	SMUtil              uint32
	MemoryUtil          uint32
	TokenDelay          time.Duration
	Response             string
}

// ParseConfig accepts the subset of llama-server arguments owned by the
// manager and silently ignores unrelated llama.cpp options. fake-nvidia-only
// behavior can be supplied either with --fake-* flags or environment variables.
func ParseConfig(args []string, getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	cfg := Config{
		Host:       envOr(getenv, "FAKE_LLAMA_HOST", "127.0.0.1"),
		Port:       8080,
		Response:   envOr(getenv, "FAKE_LLAMA_RESPONSE", "This is a deterministic fake llama-server response."),
		SMUtil:     35,
		MemoryUtil: 20,
	}

	if raw := strings.TrimSpace(getenv("FAKE_LLAMA_PORT")); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 0 || port > 65535 {
			return Config{}, fmt.Errorf("invalid FAKE_LLAMA_PORT %q", raw)
		}
		cfg.Port = port
	}
	if raw := strings.TrimSpace(getenv("FAKE_LLAMA_VRAM")); raw != "" {
		value, err := ParseBytes(raw)
		if err != nil {
			return Config{}, fmt.Errorf("FAKE_LLAMA_VRAM: %w", err)
		}
		cfg.VRAMBytes, cfg.VRAMExplicit = value, true
	}
	if raw := strings.TrimSpace(getenv("FAKE_LLAMA_KV_BYTES_PER_TOKEN")); raw != "" {
		value, err := ParseBytes(raw)
		if err != nil {
			return Config{}, fmt.Errorf("FAKE_LLAMA_KV_BYTES_PER_TOKEN: %w", err)
		}
		cfg.KVBytesPerToken = value
	}
	var err error
	if cfg.LoadDelay, err = parseDurationEnv(getenv, "FAKE_LLAMA_LOAD_DELAY"); err != nil {
		return Config{}, err
	}
	if cfg.CrashAfterReady, err = parseDurationEnv(getenv, "FAKE_LLAMA_CRASH_AFTER_READY"); err != nil {
		return Config{}, err
	}
	if cfg.GrowthAfter, err = parseDurationEnv(getenv, "FAKE_LLAMA_VRAM_GROWTH_AFTER"); err != nil {
		return Config{}, err
	}
	if cfg.TokenDelay, err = parseDurationEnv(getenv, "FAKE_LLAMA_TOKEN_DELAY"); err != nil {
		return Config{}, err
	}
	if raw := strings.TrimSpace(getenv("FAKE_LLAMA_VRAM_GROWTH")); raw != "" {
		cfg.GrowthBytes, err = ParseBytes(raw)
		if err != nil {
			return Config{}, fmt.Errorf("FAKE_LLAMA_VRAM_GROWTH: %w", err)
		}
	}
	if cfg.StartupFail, err = parseBoolEnv(getenv, "FAKE_LLAMA_STARTUP_FAIL"); err != nil {
		return Config{}, err
	}
	if cfg.ForceOOM, err = parseBoolEnv(getenv, "FAKE_LLAMA_CUDA_OOM"); err != nil {
		return Config{}, err
	}
	if cfg.HangShutdown, err = parseBoolEnv(getenv, "FAKE_LLAMA_HANG_SHUTDOWN"); err != nil {
		return Config{}, err
	}
	if raw := strings.TrimSpace(getenv("FAKE_LLAMA_SM_UTIL")); raw != "" {
		cfg.SMUtil, err = parsePercent(raw)
		if err != nil {
			return Config{}, fmt.Errorf("FAKE_LLAMA_SM_UTIL: %w", err)
		}
	}
	if raw := strings.TrimSpace(getenv("FAKE_LLAMA_MEMORY_UTIL")); raw != "" {
		cfg.MemoryUtil, err = parsePercent(raw)
		if err != nil {
			return Config{}, fmt.Errorf("FAKE_LLAMA_MEMORY_UTIL: %w", err)
		}
	}

	explicitTargets := false
	if raw := strings.TrimSpace(getenv("FAKE_LLAMA_GPUS")); raw != "" {
		cfg.Targets = splitList(raw)
		explicitTargets = true
	}
	if !explicitTargets {
		if raw := strings.TrimSpace(getenv("CUDA_VISIBLE_DEVICES")); raw != "" && raw != "-1" && !strings.EqualFold(raw, "NoDevFiles") {
			cfg.Targets = splitList(raw)
			explicitTargets = len(cfg.Targets) != 0
		}
	}

	mainGPU := ""
	for i := 0; i < len(args); i++ {
		raw := args[i]
		key, inline, hasInline := strings.Cut(strings.TrimLeft(raw, "-"), "=")
		value := inline
		needsValue := func() (string, error) {
			if hasInline {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("--%s requires a value", key)
			}
			i++
			return args[i], nil
		}

		switch key {
		case "model", "m":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			cfg.ModelPath = v
		case "host":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			cfg.Host = v
		case "port":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			port, err := strconv.Atoi(v)
			if err != nil || port < 0 || port > 65535 {
				return Config{}, fmt.Errorf("invalid --port %q", v)
			}
			cfg.Port = port
		case "ctx-size", "c":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			ctxSize, err := strconv.Atoi(v)
			if err != nil || ctxSize < 0 {
				return Config{}, fmt.Errorf("invalid --ctx-size %q", v)
			}
			cfg.ContextSize = ctxSize
		case "main-gpu":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			mainGPU = strings.TrimSpace(v)
		case "tensor-split":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			cfg.TensorSplit, err = parseTensorSplit(v)
			if err != nil {
				return Config{}, err
			}
		case "fake-gpus":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			cfg.Targets = splitList(v)
			explicitTargets = true
		case "fake-vram":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			cfg.VRAMBytes, err = ParseBytes(v)
			if err != nil {
				return Config{}, err
			}
			cfg.VRAMExplicit = true
		case "fake-kv-bytes-per-token":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			cfg.KVBytesPerToken, err = ParseBytes(v)
			if err != nil {
				return Config{}, err
			}
		case "fake-load-delay":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			cfg.LoadDelay, err = time.ParseDuration(v)
			if err != nil || cfg.LoadDelay < 0 {
				return Config{}, fmt.Errorf("invalid --fake-load-delay %q", v)
			}
		case "fake-crash-after-ready":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			cfg.CrashAfterReady, err = time.ParseDuration(v)
			if err != nil || cfg.CrashAfterReady < 0 {
				return Config{}, fmt.Errorf("invalid --fake-crash-after-ready %q", v)
			}
		case "fake-vram-growth-after":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			cfg.GrowthAfter, err = time.ParseDuration(v)
			if err != nil || cfg.GrowthAfter < 0 {
				return Config{}, fmt.Errorf("invalid --fake-vram-growth-after %q", v)
			}
		case "fake-vram-growth":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			cfg.GrowthBytes, err = ParseBytes(v)
			if err != nil {
				return Config{}, err
			}
		case "fake-token-delay":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			cfg.TokenDelay, err = time.ParseDuration(v)
			if err != nil || cfg.TokenDelay < 0 {
				return Config{}, fmt.Errorf("invalid --fake-token-delay %q", v)
			}
		case "fake-response":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			cfg.Response = v
		case "fake-sm-util":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			cfg.SMUtil, err = parsePercent(v)
			if err != nil {
				return Config{}, err
			}
		case "fake-memory-util":
			v, err := needsValue()
			if err != nil {
				return Config{}, err
			}
			cfg.MemoryUtil, err = parsePercent(v)
			if err != nil {
				return Config{}, err
			}
		case "fake-startup-fail":
			cfg.StartupFail, err = parseOptionalBool(value, hasInline, true)
			if err != nil {
				return Config{}, err
			}
		case "fake-cuda-oom":
			cfg.ForceOOM, err = parseOptionalBool(value, hasInline, true)
			if err != nil {
				return Config{}, err
			}
		case "fake-hang-shutdown":
			cfg.HangShutdown, err = parseOptionalBool(value, hasInline, true)
			if err != nil {
				return Config{}, err
			}
		default:
			// Unknown llama.cpp flags are deliberately ignored. Their following
			// value, when any, is also harmlessly ignored on the next iteration.
		}
	}

	if strings.TrimSpace(cfg.Host) == "" {
		return Config{}, errors.New("host is required")
	}
	if cfg.ModelPath == "" {
		return Config{}, errors.New("--model is required")
	}
	if !explicitTargets {
		switch {
		case len(cfg.TensorSplit) > 0:
			for i, weight := range cfg.TensorSplit {
				if weight > 0 {
					cfg.Targets = append(cfg.Targets, strconv.Itoa(i))
				}
			}
		case mainGPU != "":
			cfg.Targets = []string{mainGPU}
		default:
			cfg.Targets = []string{"0"}
		}
	}
	if len(cfg.Targets) == 0 {
		return Config{}, errors.New("at least one fake GPU target is required")
	}
	return cfg, nil
}

// RequiredVRAMBytes resolves explicit VRAM first, otherwise using model file
// size plus the optional deterministic context/KV approximation.
func (c Config) RequiredVRAMBytes() (uint64, error) {
	base := c.VRAMBytes
	if !c.VRAMExplicit {
		info, err := os.Stat(c.ModelPath)
		if err != nil {
			return 0, fmt.Errorf("stat model %q: %w", c.ModelPath, err)
		}
		if info.IsDir() {
			return 0, fmt.Errorf("model %q is a directory", c.ModelPath)
		}
		if info.Size() < 0 {
			return 0, errors.New("model size is negative")
		}
		base = uint64(info.Size())
	}
	if c.ContextSize > 0 && c.KVBytesPerToken > 0 {
		ctx := uint64(c.ContextSize)
		if ctx > math.MaxUint64/c.KVBytesPerToken {
			return 0, errors.New("KV cache approximation overflows uint64")
		}
		kv := ctx * c.KVBytesPerToken
		if base > math.MaxUint64-kv {
			return 0, errors.New("simulated VRAM requirement overflows uint64")
		}
		base += kv
	}
	return base, nil
}

// ParseBytes parses raw byte counts and binary/decimal byte suffixes used by
// fake-llama-server resource controls.
func ParseBytes(raw string) (uint64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, errors.New("size is required")
	}
	upper := strings.ToUpper(s)
	units := []struct {
		suffix string
		mult   float64
	}{
		{"GIB", 1024 * 1024 * 1024}, {"GB", 1000 * 1000 * 1000},
		{"MIB", 1024 * 1024}, {"MB", 1000 * 1000},
		{"KIB", 1024}, {"KB", 1000}, {"B", 1},
	}
	mult := float64(1)
	number := upper
	for _, unit := range units {
		if strings.HasSuffix(upper, unit.suffix) {
			number = strings.TrimSpace(strings.TrimSuffix(upper, unit.suffix))
			mult = unit.mult
			break
		}
	}
	value, err := strconv.ParseFloat(number, 64)
	if err != nil || value < 0 || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, fmt.Errorf("invalid size %q", raw)
	}
	bytes := value * mult
	if bytes > math.MaxUint64 {
		return 0, fmt.Errorf("size %q overflows uint64", raw)
	}
	return uint64(math.Round(bytes)), nil
}

func parseTensorSplit(raw string) ([]float64, error) {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' })
	if len(parts) == 0 {
		return nil, errors.New("tensor split is empty")
	}
	weights := make([]float64, len(parts))
	for i, part := range parts {
		value, err := strconv.ParseFloat(part, 64)
		if err != nil || value < 0 || math.IsInf(value, 0) || math.IsNaN(value) {
			return nil, fmt.Errorf("invalid tensor split value %q", part)
		}
		weights[i] = value
	}
	return weights, nil
}

func parsePercent(raw string) (uint32, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 32)
	if err != nil || value > 100 {
		return 0, fmt.Errorf("invalid percentage %q", raw)
	}
	return uint32(value), nil
}

func splitList(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' })
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func parseDurationEnv(getenv func(string) string, key string) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return 0, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid %s %q", key, raw)
	}
	return value, nil
}

func parseBoolEnv(getenv func(string) string, key string) (bool, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid %s %q", key, raw)
	}
	return value, nil
}

func parseOptionalBool(raw string, provided, defaultValue bool) (bool, error) {
	if !provided {
		return defaultValue, nil
	}
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("invalid boolean %q", raw)
	}
	return value, nil
}

func envOr(getenv func(string) string, key, fallback string) string {
	if value := strings.TrimSpace(getenv(key)); value != "" {
		return value
	}
	return fallback
}
