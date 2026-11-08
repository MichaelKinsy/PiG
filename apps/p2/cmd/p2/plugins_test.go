package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	locationscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/locations/contract"
	portscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/ports/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/web"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Every plugin package of plugins/ is linked into the executable.
func TestEveryPluginIsLinked(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob("../../plugins/*/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no plugin packages found")
	}
	for _, match := range matches {
		name := filepath.Base(filepath.Dir(match))
		if !strings.Contains(string(source), `_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/`+name+`"`) {
			t.Errorf("plugins/%s is not imported by main.go", name)
		}
	}
}

// The embedded bundle holds exactly the web facets of the linked plugins that have one in Pi (Pi bundles the selected plugins'
// facets: a facet whose plugin has no server here would wait for contracts nothing serves). Which plugins have a web facet
// comes from Pi's own file list (web/pi-web-plugins.txt, written by vendor.sh from the pinned commit), not from what was
// vendored, and each such facet must be vendored. web/build/rebuild.sh rebuilds the bundle from web/plugins.txt.
func TestBundleHoldsTheLinkedPluginsWebFacets(t *testing.T) {
	bundle, err := web.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	listed, err := os.ReadFile("../../web/pi-web-plugins.txt")
	if err != nil {
		t.Fatal(err)
	}
	hasWeb := map[string]bool{}
	for _, name := range strings.Fields(string(listed)) {
		hasWeb[name] = true
	}
	var want []string
	for _, plugin := range hosts.ListPlugins() {
		if !hasWeb[plugin.Name] {
			continue
		}
		want = append(want, plugin.Name)
		if files, _ := filepath.Glob("../../web/upstream/plugins/" + plugin.Name + "/web.ts*"); len(files) == 0 {
			t.Errorf("plugin %s has a web facet in Pi, but it is not vendored: add plugins/%s/web.tsx to web/upstream-files.txt and run vendor.sh", plugin.Name, plugin.Name)
		}
	}
	if !slices.Equal(want, bundle.Plugins) {
		t.Errorf("the bundle holds web facets %v, the linked plugins have %v: edit web/plugins.txt and run web/build/rebuild.sh", bundle.Plugins, want)
	}
	listedPlugins, err := os.ReadFile("../../web/plugins.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(strings.Fields(string(listedPlugins)), want) {
		t.Errorf("web/plugins.txt is %v, want %v", strings.Fields(string(listedPlugins)), want)
	}
}

// The executable's own server, with the plugins main.go links, serves the ports and locations plugins: the owner holds
// `Ports`, and `AgentLocations` (a worker's contract) is served and refuses the owner, as it would not be where no linked
// plugin serves it. A blank import in the e2e tests links a plugin there; this is the executable's set.
func TestTheExecutableServesPortsAndLocations(t *testing.T) {
	registered := map[string]bool{}
	for _, plugin := range hosts.ListPlugins() {
		registered[plugin.Name] = true
	}
	for _, name := range []string{"ports", "locations"} {
		if !registered[name] {
			t.Errorf("plugin %s is not registered in the executable", name)
		}
	}
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	owner := fixture.Browser()
	ports, err := p2test.HoldIn(owner, portscontract.Ports, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatalf("the owner cannot hold Ports: %v", err)
	}
	ports.Release()
	locations, err := owner.Hold(p2test.Background, locationscontract.AgentLocations, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer locations.Release()
	p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, locations), data.CodeForbidden)
}
