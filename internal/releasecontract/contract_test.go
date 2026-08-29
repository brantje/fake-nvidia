package releasecontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/brantje/fake-nvidia/profiles"
)

type compatibilityContract struct {
	FakeNvidiaVersion string `json:"fake_nvidia_version"`
	RequiredGoVersion string `json:"required_go_version"`
	MockNVML          struct {
		Revision string `json:"revision"`
	} `json:"mock_nvml"`
	MockCUDA struct {
		Revision string `json:"revision"`
	} `json:"mock_cuda"`
	NvidiaSMI struct {
		Version string `json:"version"`
	} `json:"nvidia_smi"`
	Architectures   []string `json:"architectures"`
	LlamaCPPManager struct {
		Revision string `json:"revision"`
		Image    string `json:"image"`
	} `json:"llamacpp_manager"`
	Profiles   []string `json:"profiles"`
	Topologies []string `json:"topologies"`
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func readContract(t *testing.T, root string) compatibilityContract {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "release", "compatibility.template.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract compatibilityContract
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	return contract
}

func readPins(t *testing.T, root string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "runtime", "pins.env"))
	if err != nil {
		t.Fatal(err)
	}
	pins := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("invalid pins.env line %q", line)
		}
		pins[key] = value
	}
	return pins
}

func TestCompatibilityContractMatchesRepositoryPins(t *testing.T) {
	root := repoRoot(t)
	contract := readContract(t, root)
	pins := readPins(t, root)

	if contract.FakeNvidiaVersion != "${FAKE_NVIDIA_VERSION}" {
		t.Fatalf("fake_nvidia_version=%q, want release placeholder", contract.FakeNvidiaVersion)
	}

	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	wantGoLine := "go " + contract.RequiredGoVersion
	if !strings.Contains(string(goMod), "\n"+wantGoLine+"\n") {
		t.Fatalf("go.mod does not contain %q", wantGoLine)
	}

	if got := pins["UPSTREAM_REVISION"]; got != contract.MockNVML.Revision {
		t.Fatalf("Mock NVML revision=%q, runtime pin=%q", contract.MockNVML.Revision, got)
	}
	if got := pins["UPSTREAM_REVISION"]; got != contract.MockCUDA.Revision {
		t.Fatalf("Mock CUDA reference revision=%q, runtime pin=%q", contract.MockCUDA.Revision, got)
	}
	if got := pins["NVIDIA_UTILS_VERSION"]; got != contract.NvidiaSMI.Version {
		t.Fatalf("nvidia-smi version=%q, runtime pin=%q", contract.NvidiaSMI.Version, got)
	}

	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "phase8.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{contract.LlamaCPPManager.Revision, contract.LlamaCPPManager.Image} {
		if value == "" || !strings.Contains(string(workflow), value) {
			t.Fatalf("Phase 8 workflow does not contain compatibility pin %q", value)
		}
	}
}

func TestCompatibilityContractProfilesAndArchitectures(t *testing.T) {
	contract := readContract(t, repoRoot(t))
	catalog, err := profiles.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}

	gotProfiles := append([]string(nil), contract.Profiles...)
	gotTopologies := append([]string(nil), contract.Topologies...)
	sort.Strings(gotProfiles)
	sort.Strings(gotTopologies)
	if want := catalog.ProfileIDs(); strings.Join(gotProfiles, "\n") != strings.Join(want, "\n") {
		t.Fatalf("release profiles=%v, catalog profiles=%v", gotProfiles, want)
	}
	if want := catalog.TopologyIDs(); strings.Join(gotTopologies, "\n") != strings.Join(want, "\n") {
		t.Fatalf("release topologies=%v, catalog topologies=%v", gotTopologies, want)
	}

	wantArchitectures := []string{"linux/amd64", "linux/arm64"}
	if strings.Join(contract.Architectures, "\n") != strings.Join(wantArchitectures, "\n") {
		t.Fatalf("architectures=%v, want %v", contract.Architectures, wantArchitectures)
	}
}
