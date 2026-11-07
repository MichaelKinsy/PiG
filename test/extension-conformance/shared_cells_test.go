package extensionconformance

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// sharedToolText runs the named tool of h's extension and returns its text.
func sharedToolText(t *testing.T, h *harness, tool, params string) string {
	t.Helper()
	registered, ok := findTool(h.runner, tool)
	if !ok {
		t.Fatalf("tool %q is not registered", tool)
	}
	result, err := registered.Definition.Execute(t.Context(), "shared-cells-"+tool, json.RawMessage(params), nil)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return result.Text()
}

// Two Hosts that join one cell registry each keep their own instance of an extension in every SDK and realization: an instance-local counter, and a pi.events emit that only the emitting Host's instances hear (pig additive (D232)). The behavior equals two Hosts without a registry.
// isolatedByDesign names the cases whose member runs alone in its own process per Host (an exact standalone is always isolated; fused-go and inproc-go have no process): they prove instance-local state only. Every other case admits both Hosts' members into one process.
var isolatedByDesign = map[string]bool{"subprocess-go": true, "subprocess-node": true, "subprocess-rust": true, "subprocess-python": true, "fused-go": true}

func TestConformance_SharedCellsIsolatePerHostInstances(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := append(allHarnessCases(), packedSDKHarnessCases()...)
	cases = append(cases, harnessCase{name: "subprocess-node-lone-cell", make: makeSubprocessNodeLoneCellHarness, extName: "node-sdk-fixture"})
	for _, tc := range cases {
		if tc.name == "inproc-go" {
			continue // no Host: nothing is shared
		}
		t.Run(tc.name, func(t *testing.T) {
			registry := subprocess.NewCellRegistry()
			conformanceCellRegistry, conformanceCellCwd = registry, t.TempDir()
			t.Cleanup(func() { conformanceCellRegistry, conformanceCellCwd = nil, ""; registry.Close() })
			var hosts []*harness
			for range 2 {
				h := tc.make(t)
				t.Cleanup(func() {
					if h.cleanup != nil {
						h.cleanup()
					}
					h.host.Shutdown("test done")
				})
				hosts = append(hosts, h)
			}
			a, b := hosts[0], hosts[1]
			if a.host.Group() == "" || a.host.Group() == b.host.Group() {
				t.Fatalf("groups = %q and %q, want two distinct non-empty ids", a.host.Group(), b.host.Group())
			}
			pa, pb := a.host.MemberProcessID(tc.extName), b.host.MemberProcessID(tc.extName)
			switch {
			case isolatedByDesign[tc.name]:
				if pa != 0 && pa == pb {
					t.Errorf("isolated case shares process %d", pa)
				}
			case pa == 0 || pa != pb:
				t.Errorf("member pids = %d and %d, want one shared process", pa, pb)
			}
			got := []string{sharedToolText(t, a, "counter", `{}`), sharedToolText(t, a, "counter", `{}`), sharedToolText(t, b, "counter", `{}`)}
			if strings.Join(got, ",") != "1,2,1" {
				t.Errorf("counters = %v, want 1,2,1 (instance-local)", got)
			}
			sharedToolText(t, a, "ping", `{"name":"A"}`)
			if got := sharedToolText(t, a, "received", `{}`); got != "received=A" {
				t.Errorf("Host A heard %q, want its own emit", got)
			}
			if got := sharedToolText(t, b, "received", `{}`); got != "received=" {
				t.Errorf("Host B heard %q, want nothing from Host A", got)
			}
			sharedToolText(t, b, "ping", `{"name":"B"}`)
			if got := sharedToolText(t, b, "received", `{}`); got != "received=B" {
				t.Errorf("Host B heard %q, want its own emit", got)
			}
			if got := sharedToolText(t, a, "received", `{}`); got != "received=A" {
				t.Errorf("Host A heard %q after Host B's emit, want only its own", got)
			}
		})
	}
}

// Hosts that join one cell registry share an extension's process, so the module-level state of a Node extension and the package-level state of a Go extension is shared between their instances: the second Host's instance reads the first's factory increment (D232). Only state kept in the factory closure or the instance is per Host. An exact standalone is isolated by design and never shares: it reads 1 in each Host.
func TestConformance_SharedCellsShareModuleLevelState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Setenv("PIG_FIXTURE_MODULE_STATE", "1")
	want := map[string][2]string{
		"subprocess-node-packed": {"factoryCalls=1", "factoryCalls=2"},
		"subprocess-go-packed":   {"factoryCalls=1", "factoryCalls=2"},
		"subprocess-node":        {"factoryCalls=1", "factoryCalls=1"},
		"subprocess-go":          {"factoryCalls=1", "factoryCalls=1"},
	}
	for _, tc := range append(allHarnessCases(), packedSDKHarnessCases()...) {
		reads, ok := want[tc.name]
		if !ok {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			registry := subprocess.NewCellRegistry()
			conformanceCellRegistry, conformanceCellCwd = registry, t.TempDir()
			t.Cleanup(func() { conformanceCellRegistry, conformanceCellCwd = nil, ""; registry.Close() })
			load := func() *harness {
				h := tc.make(t)
				t.Cleanup(func() {
					if h.cleanup != nil {
						h.cleanup()
					}
					h.host.Shutdown("test done")
				})
				return h
			}
			a := load()
			if got := sharedToolText(t, a, "module_state", `{}`); got != reads[0] {
				t.Fatalf("Host A module state = %q, want %q", got, reads[0])
			}
			b := load()
			if got := sharedToolText(t, b, "module_state", `{}`); got != reads[1] {
				t.Errorf("Host B module state = %q, want %q", got, reads[1])
			}
			if got := sharedToolText(t, a, "module_state", `{}`); got != reads[1] {
				t.Errorf("Host A module state after Host B joined = %q, want %q", got, reads[1])
			}
		})
	}
}
