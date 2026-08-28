package profiles

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed *.json topologies/*.json
var catalogFS embed.FS

type ComputeCapability struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

type Profile struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Architecture      string            `json:"architecture"`
	ComputeCapability ComputeCapability `json:"compute_capability"`
	TotalMemoryMiB    uint64            `json:"total_memory_mib"`
	ReservedMemoryMiB uint64            `json:"reserved_memory_mib"`
	TemperatureC      int               `json:"temperature_c"`
	PowerDrawMW       uint32            `json:"power_draw_mw"`
	Source            string            `json:"source"`
	UpstreamConfig    string            `json:"upstream_config,omitempty"`
}

type TopologyDevice struct {
	Profile    string `json:"profile"`
	VRAMMiB    uint64 `json:"vram_mib,omitempty"`
	UsedMiB    uint64 `json:"used_mib,omitempty"`
	GPUUtil    uint32 `json:"gpu_util,omitempty"`
	MemoryUtil uint32 `json:"memory_util,omitempty"`
}

type Topology struct {
	ID      string           `json:"id"`
	Devices []TopologyDevice `json:"devices"`
}

type Catalog struct {
	profiles   map[string]Profile
	topologies map[string]Topology
}

// LoadCatalog implements the corresponding fake-nvidia operation.
func LoadCatalog() (*Catalog, error) {
	c := &Catalog{profiles: map[string]Profile{}, topologies: map[string]Topology{}}
	profileFiles, err := fs.Glob(catalogFS, "*.json")
	if err != nil {
		return nil, err
	}
	for _, name := range profileFiles {
		var p Profile
		if err := decodeFile(name, &p); err != nil {
			return nil, err
		}
		if err := validateProfile(p); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if _, exists := c.profiles[p.ID]; exists {
			return nil, fmt.Errorf("duplicate profile %q", p.ID)
		}
		c.profiles[p.ID] = p
	}

	topologyFiles, err := fs.Glob(catalogFS, "topologies/*.json")
	if err != nil {
		return nil, err
	}
	for _, name := range topologyFiles {
		var topology Topology
		if err := decodeFile(name, &topology); err != nil {
			return nil, err
		}
		if err := c.validateTopology(topology); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if _, exists := c.topologies[topology.ID]; exists {
			return nil, fmt.Errorf("duplicate topology %q", topology.ID)
		}
		c.topologies[topology.ID] = topology
	}
	return c, nil
}

// decodeFile implements the corresponding fake-nvidia operation.
func decodeFile(name string, dst any) error {
	f, err := catalogFS.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decode %s: %w", name, err)
	}
	return nil
}

// validateProfile implements the corresponding fake-nvidia operation.
func validateProfile(p Profile) error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("profile id is required")
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("profile name is required")
	}
	if strings.TrimSpace(p.Architecture) == "" {
		return errors.New("architecture is required")
	}
	if p.ComputeCapability.Major <= 0 {
		return errors.New("compute capability major must be positive")
	}
	if p.TotalMemoryMiB == 0 {
		return errors.New("total memory must be positive")
	}
	if p.ReservedMemoryMiB >= p.TotalMemoryMiB {
		return errors.New("reserved memory must be smaller than total memory")
	}
	if p.TemperatureC < 0 || p.TemperatureC > 200 {
		return errors.New("temperature must be between 0 and 200 C")
	}
	if strings.TrimSpace(p.Source) == "" {
		return errors.New("source is required")
	}
	if p.Source == "upstream" && p.UpstreamConfig == "" {
		return errors.New("upstream profiles must declare upstream_config")
	}
	return nil
}

// validateTopology implements the corresponding fake-nvidia operation.
func (c *Catalog) validateTopology(topology Topology) error {
	if strings.TrimSpace(topology.ID) == "" {
		return errors.New("topology id is required")
	}
	if len(topology.Devices) == 0 {
		return errors.New("topology must contain at least one device")
	}
	for i, d := range topology.Devices {
		p, ok := c.profiles[d.Profile]
		if !ok {
			return fmt.Errorf("device %d references unknown profile %q", i, d.Profile)
		}
		total := p.TotalMemoryMiB
		if d.VRAMMiB != 0 {
			total = d.VRAMMiB
		}
		if total <= p.ReservedMemoryMiB {
			return fmt.Errorf("device %d total VRAM must exceed reserved VRAM", i)
		}
		if d.UsedMiB > total-p.ReservedMemoryMiB {
			return fmt.Errorf("device %d used+reserved VRAM exceeds total", i)
		}
		if d.GPUUtil > 100 || d.MemoryUtil > 100 {
			return fmt.Errorf("device %d utilization must be <= 100", i)
		}
	}
	return nil
}

// Profile implements the corresponding fake-nvidia operation.
func (c *Catalog) Profile(id string) (Profile, bool) {
	p, ok := c.profiles[id]
	return p, ok
}

// Topology implements the corresponding fake-nvidia operation.
func (c *Catalog) Topology(id string) (Topology, bool) {
	t, ok := c.topologies[id]
	return t, ok
}

// ProfileIDs implements the corresponding fake-nvidia operation.
func (c *Catalog) ProfileIDs() []string {
	ids := make([]string, 0, len(c.profiles))
	for id := range c.profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// TopologyIDs implements the corresponding fake-nvidia operation.
func (c *Catalog) TopologyIDs() []string {
	ids := make([]string, 0, len(c.topologies))
	for id := range c.topologies {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
