package plugins

import (
	"strings"
	"testing"
)

// pig additive (D460 is its use): a core facet of the Node host is made from a module the host imports or from a function of Pi's own
// package the host calls (`translationsFacet`); each is made one way, so a facet that names both or neither is refused at the load
// instead of failing in the subprocess.
func TestNodeLoadNeedsOneWayToMakeEachCoreFacet(t *testing.T) {
	for name, core := range map[string]CoreFacet{
		"neither": {Name: "@test/none"},
		"both":    {Name: "@test/both", Module: "/x.mjs", Export: "translationsFacet"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{Kind: "worker", Core: []CoreFacet{core}, Principal: workerPrincipal, Peer: serverPrincipal}, background)
			if want := `core facet "` + core.Name + `" needs a Module or an Export, not both or neither`; err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
}
