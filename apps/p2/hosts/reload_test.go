package hosts_test

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// brokenBuild makes the reload-probe plugin's facet fail to start: a build whose setup throws.
var brokenBuild atomic.Bool

var reloadProbe = host.DefineFacet(func(host.FacetHost, host.FacetSelf) error {
	if brokenBuild.Load() {
		return errors.New("the probe's new build does not start")
	}
	return nil
})

func init() { hosts.RegisterPlugin(hosts.Plugin{Name: "reload-probe", Server: &reloadProbe}) }

// A plugin whose new build fails to start keeps its old code, and the revision the reload rolled back to is the old
// one: reloading the same broken build again tries it again (and fails again), and the fixed build then rolls out. A
// baseline that kept the failed revision would make the repeat a no-op that hid the failure.
func TestReloadOfABuildThatFailsToStartRollsTheRevisionBack(t *testing.T) {
	brokenBuild.Store(false)
	t.Cleanup(func() { brokenBuild.Store(false) })
	rebuilds := &p2test.Rebuilds{}
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Plugins: []string{"reload-probe"}, BuildRevision: rebuilds.Revision})
	reload := func() hosts.ReloadResult {
		t.Helper()
		result, err := fixture.Server.Reload([]string{"reload-probe"}, context.Background())
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		return result
	}
	failed := func(result hosts.ReloadResult) bool {
		return len(result.Server.Failed) == 1 && result.Server.Failed[0].Plugin == "reload-probe" && len(result.Server.Plugins.Changed) == 0
	}

	brokenBuild.Store(true)
	rebuilds.Rebuild("reload-probe")
	if result := reload(); !failed(result) {
		t.Fatalf("the broken build: %+v, want reload-probe failed", result.Server)
	}
	// Nothing was rebuilt since, but the plugin is not on the new revision: the reload tries the build again.
	if result := reload(); !failed(result) {
		t.Fatalf("the repeat of the broken build: %+v, want reload-probe failed again", result.Server)
	}
	brokenBuild.Store(false)
	result := reload()
	if len(result.Server.Failed) != 0 || !slices.Contains(result.Server.Plugins.Changed, "reload-probe") {
		t.Fatalf("the fixed build: %+v, want reload-probe changed", result.Server)
	}
	// Now it is on the revision: a repeat is a no-op.
	if result := reload(); len(result.Server.Failed) != 0 || len(result.Server.Plugins.Changed) != 0 {
		t.Fatalf("the repeat of the fixed build: %+v, want a no-op", result.Server)
	}
}
