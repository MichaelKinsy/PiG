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

// The embedded web bundle holds exactly the web facets of the linked plugins that have one upstream (Pi bundles the selected
// plugins' facets: a facet whose plugin has no server here would wait for contracts nothing serves). web/build/rebuild.sh
// rebuilds it from web/plugins.txt.
func TestBundleHoldsTheLinkedPluginsWebFacets(t *testing.T) {
	bundle, err := web.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := filepath.Glob("../../web/upstream/plugins/*/web.ts*")
	if err != nil {
		t.Fatal(err)
	}
	hasWeb := map[string]bool{}
	for _, file := range upstream {
		hasWeb[filepath.Base(filepath.Dir(file))] = true
	}
	var want []string
	for _, plugin := range hosts.ListPlugins() {
		if hasWeb[plugin.Name] {
			want = append(want, plugin.Name)
		}
	}
	if !slices.Equal(want, bundle.Plugins) {
		t.Errorf("the bundle holds web facets %v, the linked plugins have %v: edit web/plugins.txt and run web/build/rebuild.sh", bundle.Plugins, want)
	}
	listed, err := os.ReadFile("../../web/plugins.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(strings.Fields(string(listed)), want) {
		t.Errorf("web/plugins.txt is %v, want %v", strings.Fields(string(listed)), want)
	}
}
