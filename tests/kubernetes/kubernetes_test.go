//go:build kubernetes_integration

package kubernetes

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	testNamespace = "fake-nvidia-system"
	consumerName  = "fake-nvidia-consumer"
)

// TestPhase9Kind validates profile reuse, CDI resolution, real device-plugin
// scheduling, live state mutation, and node cleanup on a CPU-only Kind node.
func TestPhase9Kind(t *testing.T) {
	image := strings.TrimSpace(os.Getenv("FAKE_NVIDIA_K8S_IMAGE"))
	if image == "" {
		t.Skip("FAKE_NVIDIA_K8S_IMAGE is required")
	}
	cluster := strings.TrimSpace(os.Getenv("FAKE_NVIDIA_KIND_CLUSTER"))
	if cluster == "" {
		cluster = "fake-nvidia"
	}
	for _, name := range []string{"go", "kubectl", "kind", "docker"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is required: %v", name, err)
		}
	}

	root := repositoryRoot(t)
	node := strings.TrimSpace(runCommand(t, root, nil, 30*time.Second, "kind", "get", "nodes", "--name", cluster))
	if strings.Contains(node, "\n") {
		node = strings.Split(node, "\n")[0]
	}
	if node == "" {
		t.Fatal("Kind cluster has no nodes")
	}
	labeledNodes := runCommand(t, root, nil, 30*time.Second, "kubectl", "get", "nodes", "-l", "fake-nvidia.com/enabled=true", "-o", "name")
	if !strings.Contains(labeledNodes, "node/"+node) {
		t.Fatalf("Kind node %s is missing required fake-nvidia.com/enabled=true label", node)
	}

	manifest := []byte(runCommand(t, root, nil, 2*time.Minute,
		"go", "run", "./cmd/fake-nvidia", "kubernetes",
		"--profile", "rtx4090-24gb",
		"--gpus", "2",
		"--image", image,
		"--cdi-kind", "fake-nvidia.com/gpu",
	))

	defer cleanupPhase9(t, root, node)
	runCommand(t, root, manifest, time.Minute, "kubectl", "apply", "-f", "-")
	runCommand(t, root, nil, 3*time.Minute, "kubectl", "-n", testNamespace, "rollout", "status", "daemonset/fake-nvidia", "--timeout=150s")

	assertCDIRuntime(t, root, node)

	plugin := filepath.Join(root, "kubernetes", "examples", "device-plugin.yaml")
	runCommand(t, root, nil, time.Minute, "kubectl", "apply", "-f", plugin)
	runCommand(t, root, nil, 3*time.Minute, "kubectl", "-n", "kube-system", "rollout", "status", "daemonset/nvidia-device-plugin-daemonset", "--timeout=150s")
	waitFor(t, 90*time.Second, func() bool {
		out, err := commandOutput(root, nil, 20*time.Second, "kubectl", "get", "node", node, "-o", `jsonpath={.status.allocatable.nvidia\.com/gpu}`)
		return err == nil && strings.TrimSpace(out) == "2"
	}, "node to advertise two nvidia.com/gpu resources")

	consumer := filepath.Join(root, "kubernetes", "examples", "consumer.yaml")
	runCommand(t, root, nil, time.Minute, "kubectl", "apply", "-f", consumer)
	runCommand(t, root, nil, 3*time.Minute, "kubectl", "wait", "--for=condition=Ready", "pod/"+consumerName, "--timeout=150s")

	list := runCommand(t, root, nil, time.Minute, "kubectl", "exec", consumerName, "--", "nvidia-smi", "-L")
	if !strings.Contains(list, "GPU 0:") || !strings.Contains(list, "GPU 1:") {
		t.Fatalf("consumer did not see both fake GPUs:\n%s", list)
	}
	runCommand(t, root, nil, time.Minute, "kubectl", "exec", consumerName, "--", "sh", "-ec", "test -c /dev/nvidia0 && test -c /dev/nvidia1 && test -e /opt/fake-nvidia/lib/libnvidia-ml.so.1")

	runCommand(t, root, nil, time.Minute,
		"kubectl", "-n", testNamespace, "exec", "daemonset/fake-nvidia", "--",
		"fake-nvidia", "ctl",
		"--ctl-bin", "/opt/fake-nvidia/runtime/bin/nvml-mock-ctl",
		"--nvidia-smi-bin", "/opt/fake-nvidia/runtime/bin/nvidia-smi",
		"--config", "/host/var/lib/fake-nvidia/state/config.yaml",
		"--overrides", "/host/var/lib/fake-nvidia/state/overrides.yaml",
		"gpu", "0", "utilization", "77",
	)
	waitFor(t, 30*time.Second, func() bool {
		out, err := commandOutput(root, nil, 20*time.Second, "kubectl", "exec", consumerName, "--", "nvidia-smi", "--query-gpu=index,utilization.gpu", "--format=csv,noheader,nounits")
		if err != nil {
			return false
		}
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Split(line, ",")
			if len(fields) == 2 && strings.TrimSpace(fields[0]) == "0" && strings.TrimSpace(fields[1]) == "77" {
				return true
			}
		}
		return false
	}, "live utilization mutation to become visible in the consumer")
}

// assertCDIRuntime invokes containerd directly on the Kind node so the test
// proves the generated CDI spec is resolved independently of device-plugin use.
func assertCDIRuntime(t *testing.T, root, node string) {
	t.Helper()
	runCommand(t, root, nil, time.Minute, "docker", "exec", node, "test", "-f", "/var/run/cdi/fake-nvidia.json")
	const image = "docker.io/library/debian:bookworm-20260824-slim"
	runCommand(t, root, nil, 3*time.Minute, "docker", "exec", node, "ctr", "-n", "k8s.io", "images", "pull", image)
	out := runCommand(t, root, nil, time.Minute,
		"docker", "exec", node, "ctr", "-n", "k8s.io", "run", "--rm",
		"--device", "fake-nvidia.com/gpu=all", image, "fake-nvidia-cdi-smoke",
		"/opt/fake-nvidia/bin/nvidia-smi", "-L",
	)
	if !strings.Contains(out, "GPU 0:") || !strings.Contains(out, "GPU 1:") {
		t.Fatalf("CDI container did not see both fake GPUs:\n%s", out)
	}
}

// cleanupPhase9 removes test workloads and verifies the installer cleaned its
// node-local state and CDI specification.
func cleanupPhase9(t *testing.T, root, node string) {
	t.Helper()
	_, _ = commandOutput(root, nil, time.Minute, "kubectl", "delete", "pod", consumerName, "--ignore-not-found=true", "--wait=true")
	_, _ = commandOutput(root, nil, time.Minute, "kubectl", "-n", "kube-system", "delete", "daemonset", "nvidia-device-plugin-daemonset", "--ignore-not-found=true", "--wait=true")
	_, _ = commandOutput(root, nil, 2*time.Minute, "kubectl", "delete", "namespace", testNamespace, "--ignore-not-found=true", "--wait=true")
	waitFor(t, 45*time.Second, func() bool {
		_, err := commandOutput(root, nil, 10*time.Second, "docker", "exec", node, "sh", "-ec", "test ! -e /var/run/cdi/fake-nvidia.json && test ! -e /var/lib/fake-nvidia/.fake-nvidia-injection")
		return err == nil
	}, "fake-nvidia node-local teardown")
}

// repositoryRoot resolves the repository root from this test source file.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// runCommand executes a command or fails the current test with its output.
func runCommand(t *testing.T, dir string, stdin []byte, timeout time.Duration, name string, args ...string) string {
	t.Helper()
	out, err := commandOutput(dir, stdin, timeout, name, args...)
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

// commandOutput runs a bounded command and returns combined stdout/stderr.
func commandOutput(dir string, stdin []byte, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("command timed out after %s: %w", timeout, ctx.Err())
	}
	return string(out), err
}

// waitFor polls a condition until it succeeds or the deadline expires.
func waitFor(t *testing.T, timeout time.Duration, check func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("timed out waiting for %s", description)
}
