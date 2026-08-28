package control

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// EnvExecRunner runs commands with a deterministic environment overlay.
type EnvExecRunner struct {
	Values map[string]string
}

// Run implements Runner with Values replacing matching inherited variables.
func (r EnvExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = overlayEnvironment(os.Environ(), r.Values)
	return cmd.CombinedOutput()
}

func overlayEnvironment(base []string, values map[string]string) []string {
	out := make([]string, 0, len(base)+len(values))
	seen := make(map[string]bool, len(values))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		if value, replace := values[key]; replace {
			if !seen[key] {
				out = append(out, key+"="+value)
				seen[key] = true
			}
			continue
		}
		out = append(out, entry)
	}
	for key, value := range values {
		if !seen[key] {
			out = append(out, key+"="+value)
		}
	}
	return out
}
