package plugins

// pi: packages/chord/src/next-node/plugins/evaluate.ts

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Ports evaluate.ts `pluginFile`: imported anywhere but a facet script it throws, naming the path it was given.
func TestPluginFileOutsideAFacetScriptThrows(t *testing.T) {
	got, err := PluginFile("data/x.json")
	if got != "" {
		t.Fatalf("path = %q, want none", got)
	}
	want := `pluginFile("data/x.json"): only a plugin's facet script, evaluated by the plugins component, has one`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// Ports runtime.ts `new PluginError(required, failed, problems = [])`: the message names each required plugin, then each problem, and
// the lists are the ones given.
func TestNewPluginErrorNamesRequiredPluginsThenProblems(t *testing.T) {
	err := NewPluginError(nil, nil, model.DiscoveryProblem{Path: "/roots/a", Error: "missing"})
	if got, want := err.Error(), "required plugin failed at load: /roots/a: missing"; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
	if len(err.Problems) != 1 || len(err.Required) != 0 || len(err.Failed) != 0 {
		t.Fatalf("lists = %+v", err)
	}
	if empty := NewPluginError(nil, nil); len(empty.Problems) != 0 {
		t.Fatalf("problems default = %v, want none", empty.Problems)
	}
}

// plugins.ts RolloutHost: `stopped: true` is the host that stopped cleanly during the step, and only it.
func TestRolloutHostStoppedIsTheHostThatStoppedCleanly(t *testing.T) {
	for kind, want := range map[RolloutHostKind]bool{RolloutStopped: true, RolloutReported: false, RolloutErrored: false} {
		if got := (RolloutHost{Kind: kind, Host: "h"}).Stopped(); got != want {
			t.Errorf("%s: Stopped() = %v, want %v", kind, got, want)
		}
	}
}
