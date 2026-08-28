package pmon

import (
	"fmt"
	"io"
	"sort"
)

// Sample is the subset of per-process GPU utilization rendered by nvidia-smi pmon.
type Sample struct {
	GPUIndex   int
	PID        uint32
	Type       string
	SMUtil     uint32
	MemoryUtil uint32
	EncoderUtil uint32
	DecoderUtil uint32
	Name       string
}

// Matches reports whether args are one of the one-shot pmon forms required by
// LlamaCPP-Manager. Other nvidia-smi arguments must be delegated to the real
// NVIDIA binary unchanged.
func Matches(args []string) bool {
	if len(args) < 3 || args[0] != "pmon" {
		return false
	}

	countSeen := false
	selectorSeen := false
	for i := 1; i < len(args); {
		if i+1 >= len(args) {
			return false
		}
		switch args[i] {
		case "-c":
			if countSeen || args[i+1] != "1" {
				return false
			}
			countSeen = true
		case "-s":
			if selectorSeen || args[i+1] != "u" {
				return false
			}
			selectorSeen = true
		default:
			return false
		}
		i += 2
	}
	return countSeen
}

// Render writes parser-compatible one-shot nvidia-smi pmon utilization output.
func Render(w io.Writer, samples []Sample) error {
	ordered := append([]Sample(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].GPUIndex != ordered[j].GPUIndex {
			return ordered[i].GPUIndex < ordered[j].GPUIndex
		}
		return ordered[i].PID < ordered[j].PID
	})

	if _, err := fmt.Fprintln(w, "# gpu         pid  type    sm   mem   enc   dec   command"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# Idx           #   C/G     %     %     %     %   name"); err != nil {
		return err
	}
	for _, sample := range ordered {
		processType := sample.Type
		if processType == "" {
			processType = "C"
		}
		name := sample.Name
		if name == "" {
			name = "-"
		}
		if _, err := fmt.Fprintf(w, "%5d %11d %5s %5d %5d %5d %5d   %s\n",
			sample.GPUIndex,
			sample.PID,
			processType,
			sample.SMUtil,
			sample.MemoryUtil,
			sample.EncoderUtil,
			sample.DecoderUtil,
			name,
		); err != nil {
			return err
		}
	}
	return nil
}
