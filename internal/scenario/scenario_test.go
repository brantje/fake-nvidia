package scenario

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/brantje/fake-nvidia/profiles"
)

type fakeExecutor struct{ calls [][]string }

func (f *fakeExecutor) Execute(_ context.Context, args []string) error {
	f.calls = append(f.calls, append([]string(nil), args...))
	return nil
}

type fakeClock struct{ waits []time.Duration }

func (f *fakeClock) Wait(_ context.Context, d time.Duration) error {
	f.waits = append(f.waits, d)
	return nil
}

type fakeEvents struct{ names []string }

func (f *fakeEvents) Wait(_ context.Context, name string) error {
	f.names = append(f.names, name)
	return nil
}

func TestRunnerExecutesDeterministicTriggersAndCleanup(t *testing.T) {
	executor := &fakeExecutor{}
	clock := &fakeClock{}
	events := &fakeEvents{}
	doc := Document{
		Version: 1,
		Initial: []Operation{{Args: []string{"reset"}}},
		Steps: []Step{
			{Name: "pressure", After: "250ms", Do: Operation{Args: []string{"gpu", "0", "memory", "reserve", "1GiB"}}},
			{Name: "ready", Event: "model-ready", Do: Operation{Args: []string{"gpu", "0", "utilization", "90"}}},
		},
		Cleanup: []Operation{{Args: []string{"reset"}}},
	}
	if err := (Runner{Executor: executor, Clock: clock, Events: events}).Run(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	wantCalls := [][]string{
		{"reset"},
		{"gpu", "0", "memory", "reserve", "1GiB"},
		{"gpu", "0", "utilization", "90"},
		{"reset"},
	}
	if !reflect.DeepEqual(executor.calls, wantCalls) {
		t.Fatalf("calls=%v want=%v", executor.calls, wantCalls)
	}
	if !reflect.DeepEqual(clock.waits, []time.Duration{250 * time.Millisecond}) {
		t.Fatalf("waits=%v", clock.waits)
	}
	if !reflect.DeepEqual(events.names, []string{"model-ready"}) {
		t.Fatalf("events=%v", events.names)
	}
}

func TestLoadRejectsAmbiguousTrigger(t *testing.T) {
	_, err := Load(strings.NewReader(`{"version":1,"steps":[{"after":"1s","event":"ready","do":{"args":["reset"]}}]}`))
	if err == nil || !strings.Contains(err.Error(), "both after and event") {
		t.Fatalf("err=%v", err)
	}
}

func TestRenderBaseProfile(t *testing.T) {
	catalog, err := profiles.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	doc := Document{Version: 1, Base: &Base{Profile: "rtx4060ti-16gb", Count: 2}}
	data, err := RenderBase(catalog, doc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "  - index:") != 2 {
		t.Fatalf("rendered base:\n%s", data)
	}
}

type contextExecutor struct {
	cleanupErr error
}

func (f *contextExecutor) Execute(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "cleanup" {
		f.cleanupErr = ctx.Err()
	}
	return nil
}

func TestRunnerCleanupSurvivesCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	executor := &contextExecutor{}
	doc := Document{
		Version: 1,
		Steps:   []Step{{After: "1s", Do: Operation{Args: []string{"reset"}}}},
		Cleanup: []Operation{{Args: []string{"cleanup"}}},
	}
	if err := (Runner{Executor: executor, Clock: RealClock{}}).Run(ctx, doc); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("err=%v", err)
	}
	if executor.cleanupErr != nil {
		t.Fatalf("cleanup context err=%v", executor.cleanupErr)
	}
}
