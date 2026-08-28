package upstream

const (
	Repository = "NVIDIA/k8s-test-infra"
	Revision   = "f7bbbf025110c63c04567cf42e357af32fa2f62d"

	MockNVMLPath       = "pkg/gpu/mocknvml"
	ConfigurationPath  = "docs/configuration.md"
	ControlPath        = "docs/nvml-mock-ctl.md"
	DefaultOverrideTTL = "1s"
)

// OverrideLayers documents the merge order implemented by the pinned
// nvml-mock runtime. fake-nvidia delegates the merge itself to upstream.
var OverrideLayers = []string{"base", "all", "device"}

// RuntimeMutable summarizes the fields fake-nvidia may safely mutate through
// the upstream override layer without reconstructing a device.
var RuntimeMutable = []string{
	"memory", "utilization", "processes", "temperature", "power", "failure",
}

// RestartRequired summarizes identity/topology fields that the pinned upstream
// implementation constructs once and does not hot-reload.
var RestartRequired = []string{
	"name", "architecture", "brand", "compute_capability", "uuid", "pci.bus_id", "bar1_memory",
}
