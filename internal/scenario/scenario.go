package scenario

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/brantje/fake-nvidia/internal/config"
	"github.com/brantje/fake-nvidia/profiles"
)

// Document is a deterministic fake-nvidia control scenario.
type Document struct {
	Version int         `json:"version"`
	Base    *Base       `json:"base,omitempty"`
	Initial []Operation `json:"initial,omitempty"`
	Steps   []Step      `json:"steps,omitempty"`
	Cleanup []Operation `json:"cleanup,omitempty"`
}

// Base declares the base profile/topology that should be rendered before the
// consumer runtime starts. Scenario execution itself mutates only overrides.
type Base struct {
	System   config.System          `json:"system,omitempty"`
	Profile  string                 `json:"profile,omitempty"`
	Count    int                    `json:"count,omitempty"`
	VRAMMiB  uint64                 `json:"vram_mib,omitempty"`
	Topology string                 `json:"topology,omitempty"`
	Devices  []config.DeviceRequest `json:"devices,omitempty"`
}

// Operation reuses the control CLI grammar so scenarios and shell tests express
// mutations identically.
type Operation struct {
	Args []string `json:"args"`
}

// Step executes one operation after either a duration or a named event.
type Step struct {
	Name  string    `json:"name,omitempty"`
	After string    `json:"after,omitempty"`
	Event string    `json:"event,omitempty"`
	Do    Operation `json:"do"`
}

// Executor executes one control operation.
type Executor interface {
	Execute(context.Context, []string) error
}

// Clock provides controllable timing for scenario tests.
type Clock interface {
	Wait(context.Context, time.Duration) error
}

// EventSource provides deterministic named triggers.
type EventSource interface {
	Wait(context.Context, string) error
}

// RealClock is the wall-clock implementation used by the CLI.
type RealClock struct{}

// Wait blocks for d or until the context is cancelled.
func (RealClock) Wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// LineEvents reads newline-delimited event names, useful for deterministic CI
// orchestration without introducing a control server.
type LineEvents struct {
	scanner *bufio.Scanner
}

// NewLineEvents constructs an event source over r.
func NewLineEvents(r io.Reader) *LineEvents {
	return &LineEvents{scanner: bufio.NewScanner(r)}
}

// Wait consumes events until the requested name is observed.
func (l *LineEvents) Wait(ctx context.Context, name string) error {
	if l == nil || l.scanner == nil {
		return errors.New("event source is required")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if !l.scanner.Scan() {
			if err := l.scanner.Err(); err != nil {
				return err
			}
			return fmt.Errorf("event %q not received before input closed", name)
		}
		if strings.TrimSpace(l.scanner.Text()) == name {
			return nil
		}
	}
}

// Runner executes validated scenarios with injectable time/event sources.
type Runner struct {
	Executor Executor
	Clock    Clock
	Events   EventSource
}

// Run executes initial mutations, triggered/timed steps, then cleanup. Cleanup
// is attempted even when a scenario step fails.
func (r Runner) Run(ctx context.Context, doc Document) (err error) {
	if err := Validate(doc); err != nil {
		return err
	}
	if r.Executor == nil {
		return errors.New("scenario executor is required")
	}
	if r.Clock == nil {
		r.Clock = RealClock{}
	}
	defer func() {
		for _, op := range doc.Cleanup {
			if cleanupErr := r.Executor.Execute(ctx, op.Args); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("cleanup %q: %w", strings.Join(op.Args, " "), cleanupErr))
			}
		}
	}()

	for _, op := range doc.Initial {
		if err := r.Executor.Execute(ctx, op.Args); err != nil {
			return fmt.Errorf("initial %q: %w", strings.Join(op.Args, " "), err)
		}
	}
	for i, step := range doc.Steps {
		if step.After != "" {
			d, parseErr := time.ParseDuration(step.After)
			if parseErr != nil {
				return fmt.Errorf("step %d: invalid after duration: %w", i, parseErr)
			}
			if waitErr := r.Clock.Wait(ctx, d); waitErr != nil {
				return fmt.Errorf("step %d wait: %w", i, waitErr)
			}
		}
		if step.Event != "" {
			if r.Events == nil {
				return fmt.Errorf("step %d requires event source for %q", i, step.Event)
			}
			if waitErr := r.Events.Wait(ctx, step.Event); waitErr != nil {
				return fmt.Errorf("step %d event %q: %w", i, step.Event, waitErr)
			}
		}
		if err := r.Executor.Execute(ctx, step.Do.Args); err != nil {
			return fmt.Errorf("step %d %q: %w", i, step.Name, err)
		}
	}
	return nil
}

// Load decodes a strict scenario JSON document.
func Load(r io.Reader) (Document, error) {
	var doc Document
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return Document{}, fmt.Errorf("decode scenario: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Document{}, errors.New("decode scenario: multiple JSON documents")
		}
		return Document{}, fmt.Errorf("decode scenario: %w", err)
	}
	if err := Validate(doc); err != nil {
		return Document{}, err
	}
	return doc, nil
}

// Validate checks deterministic trigger and operation constraints.
func Validate(doc Document) error {
	if doc.Version != 1 {
		return fmt.Errorf("unsupported scenario version %d", doc.Version)
	}
	if doc.Base != nil {
		selected := 0
		if doc.Base.Profile != "" {
			selected++
		}
		if doc.Base.Topology != "" {
			selected++
		}
		if len(doc.Base.Devices) > 0 {
			selected++
		}
		if selected != 1 {
			return errors.New("scenario base must choose exactly one of profile, topology, or devices")
		}
		if doc.Base.Profile != "" && doc.Base.Count < 0 {
			return errors.New("scenario base count cannot be negative")
		}
	}
	for i, op := range doc.Initial {
		if len(op.Args) == 0 {
			return fmt.Errorf("initial operation %d has no args", i)
		}
	}
	for i, step := range doc.Steps {
		if step.After != "" && step.Event != "" {
			return fmt.Errorf("step %d cannot set both after and event", i)
		}
		if step.After != "" {
			d, err := time.ParseDuration(step.After)
			if err != nil || d < 0 {
				return fmt.Errorf("step %d has invalid after duration %q", i, step.After)
			}
		}
		if len(step.Do.Args) == 0 {
			return fmt.Errorf("step %d has no operation args", i)
		}
	}
	for i, op := range doc.Cleanup {
		if len(op.Args) == 0 {
			return fmt.Errorf("cleanup operation %d has no args", i)
		}
	}
	return nil
}

// RenderBase renders the declared base profile/topology as upstream Mock NVML YAML.
func RenderBase(catalog *profiles.Catalog, doc Document) ([]byte, error) {
	if err := Validate(doc); err != nil {
		return nil, err
	}
	if doc.Base == nil {
		return nil, errors.New("scenario has no base profile/topology")
	}
	base := doc.Base
	var cfg config.MockConfig
	var err error
	switch {
	case base.Topology != "":
		cfg, err = config.ComposeTopology(catalog, base.System, base.Topology)
	case base.Profile != "":
		count := base.Count
		if count == 0 {
			count = 1
		}
		var devices []config.DeviceRequest
		devices, err = config.Repeated(base.Profile, count, base.VRAMMiB)
		if err == nil {
			cfg, err = config.Compose(catalog, config.Spec{System: base.System, Devices: devices})
		}
	default:
		cfg, err = config.Compose(catalog, config.Spec{System: base.System, Devices: base.Devices})
	}
	if err != nil {
		return nil, err
	}
	return config.RenderYAML(cfg)
}
