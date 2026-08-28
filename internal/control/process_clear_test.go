package control

import (
	"context"
	"strings"
	"testing"
)

func TestSetProcessesNilClearsWithEmptyArray(t *testing.T) {
	f := &fakeRunner{}
	c := clientWithFake(f)
	if err := c.SetProcesses(context.Background(), "0", nil); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls=%v", f.calls)
	}
	args := strings.Join(f.calls[0].args, " ")
	if !strings.Contains(args, "processes=[]") {
		t.Fatalf("process clear was not explicit: %s", args)
	}
	if strings.Contains(args, "processes=null") {
		t.Fatalf("process clear used null merge semantics: %s", args)
	}
}
