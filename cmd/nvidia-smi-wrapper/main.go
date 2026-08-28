package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/brantje/fake-nvidia/internal/nvmlpmon"
	"github.com/brantje/fake-nvidia/internal/pmon"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if pmon.Matches(args) {
		samples, err := nvmlpmon.Collect()
		if err != nil {
			fmt.Fprintf(stderr, "nvidia-smi pmon compatibility: %v\n", err)
			return 1
		}
		if err := pmon.Render(stdout, samples); err != nil {
			fmt.Fprintf(stderr, "nvidia-smi pmon compatibility: %v\n", err)
			return 1
		}
		return 0
	}
	return delegate(args, stdin, stdout, stderr)
}

func delegate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "resolve nvidia-smi wrapper path: %v\n", err)
		return 1
	}
	realBinary := filepath.Join(filepath.Dir(executable), "nvidia-smi.real")
	cmd := exec.Command(realBinary, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "run %s: %v\n", realBinary, err)
		return 1
	}
	return 0
}
