package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/web"
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
	// Web facets that ship without a Go plugin (web/web-only.txt, each with its reason): Pi's plugin has server or worker parts
	// not ported yet, so the plugin is not registered (it must not look ported), only its web facet is in the bundle.
	onlyListed, err := os.ReadFile("../../web/web-only.txt")
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, plugin := range hosts.ListPlugins() {
		registered[plugin.Name] = true
	}
	for _, line := range strings.Split(string(onlyListed), "\n") {
		name, _, _ := strings.Cut(strings.TrimSpace(line), " ")
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		if !hasWeb[name] {
			t.Errorf("web-only %s has no web facet in Pi", name)
		}
		if registered[name] {
			t.Errorf("web-only %s is a registered plugin: list it in plugins.txt only", name)
		}
		if files, _ := filepath.Glob("../../web/upstream/plugins/" + name + "/web.ts*"); len(files) == 0 {
			t.Errorf("web-only %s is not vendored", name)
		}
		want = append(want, name)
	}
	slices.Sort(want)
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

// `p2 tui` loads the tui facet of every plugin that has a tui.ts in Pi (apps/p2/plugins/*/tui.ts at the pinned commit), and of no other.
func TestTheTerminalClientLoadsTheTUIFacetsOfThePluginsPiGivesOne(t *testing.T) {
	want := strings.Fields(`artifacts chat chat-mode codemode coding-tools devices envs history login logs mcp models ports powershell prompt
		quiet-tools search send-file sessions sharing status subagents terminals views workspace`)
	plugins, err := hosts.SelectPlugins(nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, facet := range hosts.TUIFacets(plugins) {
		got = append(got, facet.Name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("tui facets = %v, want %v", got, want)
	}
}
