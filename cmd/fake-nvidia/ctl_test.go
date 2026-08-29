package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCtlAliasExposesPhase4UX(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"ctl", "help"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v stderr=%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "gpu <gpu> memory reserve") {
		t.Fatalf("control help missing Phase 4 commands:\n%s", out.String())
	}
}
