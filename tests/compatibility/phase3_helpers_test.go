//go:build integration

package compatibility

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/brantje/fake-nvidia/internal/runtimebundle"
)

type runtimeBundle = runtimebundle.Bundle

func runBinary(binary string, bundle runtimebundle.Bundle, configPath, overridesPath string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = bundle.Environment(os.Environ(), configPath, overridesPath)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("%s timed out: %w", binary, ctx.Err())
	}
	if err != nil {
		return string(out), fmt.Errorf("%s %v: %w", binary, args, err)
	}
	return string(out), nil
}
