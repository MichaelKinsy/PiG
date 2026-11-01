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

// Two Hosts that join one cell registry each keep their own instance of an extension in every SDK and realization: an instance-local counter, and a pi.events emit that only the emitting Host's instances hear (pig additive (D201)). The behavior equals two Hosts without a registry.
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
