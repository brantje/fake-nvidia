package pmon

import (
	"strconv"
	"strings"
	"testing"
)

func TestMatchesLlamaCPPManagerForms(t *testing.T) {
	for _, args := range [][]string{
		{"pmon", "-c", "1", "-s", "u"},
		{"pmon", "-c", "1"},
		{"pmon", "-s", "u", "-c", "1"},
	} {
		if !Matches(args) {
			t.Fatalf("Matches(%v)=false", args)
		}
	}
}

func TestMatchesRejectsUnrelatedNvidiaSMIForms(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"-L"},
		{"pmon"},
		{"pmon", "-c", "2"},
		{"pmon", "-c", "1", "-s", "m"},
		{"pmon", "-c", "1", "--foo"},
	} {
		if Matches(args) {
			t.Fatalf("Matches(%v)=true", args)
		}
	}
}

func TestRenderIsLlamaCPPManagerParserCompatible(t *testing.T) {
	var out strings.Builder
	samples := []Sample{
		{GPUIndex: 1, PID: 44, SMUtil: 71, MemoryUtil: 9, Name: "worker-b"},
		{GPUIndex: 0, PID: 33, SMUtil: 42, MemoryUtil: 11, EncoderUtil: 2, DecoderUtil: 3, Name: "worker-a"},
	}
	if err := Render(&out, samples); err != nil {
		t.Fatal(err)
	}

	got := map[string]float64{}
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			t.Fatalf("short pmon row %q", line)
		}
		gpu, err := strconv.Atoi(fields[0])
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatal(err)
		}
		util, err := strconv.ParseFloat(strings.TrimSuffix(fields[3], "%"), 64)
		if err != nil {
			t.Fatal(err)
		}
		got[strconv.Itoa(pid)+"@CUDA"+strconv.Itoa(gpu)] = util
	}

	if got["33@CUDA0"] != 42 || got["44@CUDA1"] != 71 {
		t.Fatalf("parsed utilization=%v\noutput:\n%s", got, out.String())
	}
}

func TestRenderEmptyProcessListStillSucceeds(t *testing.T) {
	var out strings.Builder
	if err := Render(&out, nil); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "#") || !strings.HasPrefix(lines[1], "#") {
		t.Fatalf("unexpected empty output:\n%s", out.String())
	}
}
