package kubernetes

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const DefaultCDIKind = "fake-nvidia.com/gpu"

type cdiSpec struct {
	CDIVersion     string         `json:"cdiVersion"`
	Kind           string         `json:"kind"`
	ContainerEdits containerEdits `json:"containerEdits"`
	Devices        []cdiDevice    `json:"devices"`
}

type containerEdits struct {
	Env         []string     `json:"env,omitempty"`
	Mounts      []cdiMount   `json:"mounts,omitempty"`
	DeviceNodes []deviceNode `json:"deviceNodes,omitempty"`
}

type cdiMount struct {
	HostPath      string   `json:"hostPath"`
	ContainerPath string   `json:"containerPath"`
	Options       []string `json:"options,omitempty"`
}

type deviceNode struct {
	Path        string `json:"path"`
	HostPath    string `json:"hostPath"`
	Permissions string `json:"permissions,omitempty"`
}

type cdiDevice struct {
	Name           string         `json:"name"`
	ContainerEdits containerEdits `json:"containerEdits"`
}

// GenerateCDISpec renders the node-local CDI specification used by fake-nvidia.
// The node root is deliberately outside /dev so fake device nodes cannot replace
// real NVIDIA device nodes on hosts that also contain physical GPUs.
func GenerateCDISpec(kind, nodeRoot string, deviceCount int) ([]byte, error) {
	if !validCDIKind(kind) {
		return nil, fmt.Errorf("invalid CDI kind %q", kind)
	}
	if deviceCount <= 0 || deviceCount > 8 {
		return nil, fmt.Errorf("device count must be between 1 and 8, got %d", deviceCount)
	}
	if !filepath.IsAbs(nodeRoot) {
		return nil, errors.New("node root must be absolute")
	}
	nodeRoot = filepath.Clean(nodeRoot)
	if nodeRoot == "/" {
		return nil, errors.New("node root must not be filesystem root")
	}

	common := containerEdits{
		Env: []string{
			"PATH=/opt/fake-nvidia/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"LD_LIBRARY_PATH=/opt/fake-nvidia/lib",
			"MOCK_NVML_CONFIG=/opt/fake-nvidia/state/config.yaml",
			"MOCK_NVML_OVERRIDES=/opt/fake-nvidia/state/overrides.yaml",
		},
		Mounts: []cdiMount{
			{HostPath: filepath.Join(nodeRoot, "runtime", "bin"), ContainerPath: "/opt/fake-nvidia/bin", Options: []string{"ro", "nosuid", "nodev", "bind"}},
			{HostPath: filepath.Join(nodeRoot, "runtime", "lib"), ContainerPath: "/opt/fake-nvidia/lib", Options: []string{"ro", "nosuid", "nodev", "bind"}},
			{HostPath: filepath.Join(nodeRoot, "state"), ContainerPath: "/opt/fake-nvidia/state", Options: []string{"ro", "nosuid", "nodev", "bind"}},
		},
		DeviceNodes: []deviceNode{
			{Path: "/dev/nvidiactl", HostPath: filepath.Join(nodeRoot, "dev", "nvidiactl"), Permissions: "rw"},
			{Path: "/dev/nvidia-uvm", HostPath: filepath.Join(nodeRoot, "dev", "nvidia-uvm"), Permissions: "rw"},
			{Path: "/dev/nvidia-uvm-tools", HostPath: filepath.Join(nodeRoot, "dev", "nvidia-uvm-tools"), Permissions: "rw"},
		},
	}

	devices := make([]cdiDevice, 0, deviceCount+1)
	allNodes := make([]deviceNode, 0, deviceCount)
	for i := 0; i < deviceCount; i++ {
		node := deviceNode{
			Path:        fmt.Sprintf("/dev/nvidia%d", i),
			HostPath:    filepath.Join(nodeRoot, "dev", fmt.Sprintf("nvidia%d", i)),
			Permissions: "rw",
		}
		devices = append(devices, cdiDevice{Name: fmt.Sprintf("%d", i), ContainerEdits: containerEdits{DeviceNodes: []deviceNode{node}}})
		allNodes = append(allNodes, node)
	}
	devices = append(devices, cdiDevice{Name: "all", ContainerEdits: containerEdits{DeviceNodes: allNodes}})

	data, err := json.MarshalIndent(cdiSpec{CDIVersion: "0.6.0", Kind: kind, ContainerEdits: common, Devices: devices}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// validCDIKind validates the vendor DNS subdomain and single CDI name segment.
func validCDIKind(kind string) bool {
	if kind == "" || strings.TrimSpace(kind) != kind {
		return false
	}
	parts := strings.Split(kind, "/")
	return len(parts) == 2 && validDNSSubdomain(parts[0]) && validCDIName(parts[1])
}

// validDNSSubdomain validates the DNS-subdomain prefix required by CDI kinds.
func validDNSSubdomain(value string) bool {
	if len(value) == 0 || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		for i, r := range label {
			ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-'
			if !ok || (r == '-' && (i == 0 || i == len(label)-1)) {
				return false
			}
		}
	}
	return true
}

// validCDIName validates the CDI kind name segment for spec version 0.6.0.
func validCDIName(value string) bool {
	if len(value) == 0 || len(value) > 63 || !isASCIIAlphanumeric(value[0]) || !isASCIIAlphanumeric(value[len(value)-1]) {
		return false
	}
	for i := 1; i < len(value)-1; i++ {
		c := value[i]
		if !isASCIIAlphanumeric(c) && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

// isASCIIAlphanumeric reports whether c is an ASCII letter or digit.
func isASCIIAlphanumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
