//go:build docker_integration

package dockerintegration

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const consumerImage = "debian:bookworm-20260824-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171"

func TestCPUOnlyConsumerInjection(t *testing.T) {
	runtimeRoot := os.Getenv("FAKE_NVIDIA_RUNTIME_DIR")
	if runtimeRoot == "" {
		t.Fatal("FAKE_NVIDIA_RUNTIME_DIR is required")
	}
	runtimeRoot, err := filepath.Abs(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	before := runtimeFingerprint(t, runtimeRoot)

	repoRoot := repositoryRoot(t)
	root := filepath.Join(t.TempDir(), "fake-nvidia")
	run(t, repoRoot, nil, "docker", "version", "--format", "{{.Server.Version}}")
	run(t, repoRoot, nil, "go", "run", "./cmd/fake-nvidia", "up",
		"--runtime-dir", runtimeRoot,
		"--root", root,
		"--profile", "rtx4060ti-16gb",
		"--gpus", "2")

	composeEnv := []string{"FAKE_NVIDIA_ROOT=" + root}
	list := run(t, repoRoot, composeEnv, "docker", "compose",
		"-f", "examples/docker/compose.yaml",
		"-f", "examples/docker/compose.override.yaml",
		"run", "--rm", "consumer")
	if strings.Count(strings.TrimSpace(list), "\n") != 1 {
		t.Fatalf("nvidia-smi -L output = %q, want two GPUs", list)
	}
	if !strings.Contains(list, "GPU 0:") || !strings.Contains(list, "GPU 1:") {
		t.Fatalf("nvidia-smi -L output = %q, missing GPU indices", list)
	}

	mounts := dockerInjectionArgs(root)
	queryArgs := append([]string{"run", "--rm"}, mounts...)
	queryArgs = append(queryArgs, consumerImage,
		"nvidia-smi",
		"--query-gpu=index,uuid,name,memory.total,memory.used,utilization.gpu",
		"--format=csv,noheader,nounits")
	query := run(t, repoRoot, nil, "docker", queryArgs...)
	lines := nonEmptyLines(query)
	if len(lines) != 2 {
		t.Fatalf("manager discovery returned %d lines: %q", len(lines), query)
	}
	for index, line := range lines {
		parts := strings.Split(line, ",")
		if len(parts) != 6 {
			t.Fatalf("manager discovery line %q has %d fields", line, len(parts))
		}
		if strings.TrimSpace(parts[0]) != fmt.Sprint(index) {
			t.Fatalf("manager discovery line %q has wrong index", line)
		}
		if !strings.Contains(parts[2], "4060 Ti") {
			t.Fatalf("manager discovery line %q has wrong profile name", line)
		}
	}

	run(t, repoRoot, nil, "go", "run", "./cmd/fake-nvidia", "down", "--root", root)
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("injection root still exists after down: %v", err)
	}
	after := runtimeFingerprint(t, runtimeRoot)
	if before != after {
		t.Fatalf("source runtime changed across prepare/use/teardown: before=%x after=%x", before, after)
	}
}

func dockerInjectionArgs(root string) []string {
	return []string{
		"--mount", "type=bind,src=" + filepath.Join(root, "runtime", "bin", "nvidia-smi") + ",dst=/usr/local/bin/nvidia-smi,readonly",
		"--mount", "type=bind,src=" + filepath.Join(root, "runtime", "bin", "nvidia-smi.real") + ",dst=/usr/local/bin/nvidia-smi.real,readonly",
		"--mount", "type=bind,src=" + filepath.Join(root, "runtime", "bin", "nvml-mock-ctl") + ",dst=/usr/local/bin/nvml-mock-ctl,readonly",
		"--mount", "type=bind,src=" + filepath.Join(root, "runtime", "lib") + ",dst=/opt/fake-nvidia/lib,readonly",
		"--mount", "type=bind,src=" + filepath.Join(root, "state") + ",dst=/var/lib/fake-nvidia",
		"--env", "LD_LIBRARY_PATH=/opt/fake-nvidia/lib",
		"--env", "MOCK_NVML_CONFIG=/var/lib/fake-nvidia/config.yaml",
		"--env", "MOCK_NVML_OVERRIDES=/var/lib/fake-nvidia/overrides.yaml",
	}
}

func runtimeFingerprint(t *testing.T, root string) [32]byte {
	t.Helper()
	h := sha256.New()
	for _, rel := range []string{
		"bin/nvidia-smi",
		"bin/nvidia-smi.real",
		"bin/nvml-mock-ctl",
		"lib/libnvidia-ml.so.1",
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read runtime artifact %s: %v", rel, err)
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write(data)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func run(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func nonEmptyLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
