package plugins

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports the cases of packages/chord/test/next-node/plugins/evaluate.test.ts that are about the identity and the plugin
// directory an evaluation gets, not about `vm.Script` and `require` (see the rows file for the others, which the Evaluator
// seam replaces).

func TestEvaluatingAFacetRunsAScriptUnderAStableFilenamePerPluginKindAndDigest(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "p", "@test/p", map[string]string{"worker.ts": "export default {};"}, nil)
	discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	plugin := discovery.Plugins[0]
	built, err := BuildFacet(&referenceBuilder{}, plugin, "worker", BuildOptions{Platform: PlatformNode}, background)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ScriptFilename(built, plugin), "/chord-plugins/@test/p/worker.ts?"+built.Digest; got != want {
		t.Fatalf("filename = %q, want %q", got, want)
	}
	// A plugin the discovery has no file of the kind for names it `<kind>.js`.
	if got, want := ScriptFilename(built, Plugin{Name: "@test/p", Facets: map[string]string{}}), "/chord-plugins/@test/p/worker.js?"+built.Digest; got != want {
		t.Fatalf("filename = %q, want %q", got, want)
	}
	// What the evaluator gets is the build's identity: the same plugin, kind and digest give the same facet.
	setup := func(host.FacetHost, host.FacetSelf) error { return nil }
	evaluator := referenceEvaluator{facets: map[string]host.Facet{ScriptFilename(built, plugin): host.DefineFacet(setup)}}
	facet, err := evaluator.EvaluateFacet(built, EvaluateOptions{HostPackages: HostPackages{}, Plugin: plugin})
	if err != nil || facet.Setup == nil {
		t.Fatalf("facet = %+v, error = %v", facet, err)
	}
	other := built
	other.Digest = "0"
	_, err = evaluator.EvaluateFacet(other, EvaluateOptions{HostPackages: HostPackages{}, Plugin: plugin})
	if err == nil || err.Error() != `plugin "@test/p" (worker): its default export is not a facet (defineFacet)` {
		t.Fatalf("error = %v", err)
	}
}

func TestEvaluatingAFacetGivesEachFacetPluginFileForItsOwnPluginsDirectoryAndRefusesDotDotOutOfIt(t *testing.T) {
	root := tempRoot(t, "contracts")
	writePlugin(t, root, "files", "@test/files", map[string]string{"worker.ts": "", "assets/data.txt": "a file of the plugin"}, nil)
	writePlugin(t, root, "escape", "@test/escape", map[string]string{"worker.ts": ""}, nil)
	discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	var files, escape Plugin
	for _, plugin := range discovery.Plugins {
		switch plugin.Name {
		case "@test/files":
			files = plugin
		case "@test/escape":
			escape = plugin
		}
	}
	got, err := PluginFile(files, "assets/data.txt")
	if err != nil || got != filepath.Join(realOf(t, root), "files", "assets", "data.txt") {
		t.Fatalf("pluginFile = %q, %v", got, err)
	}
	_, err = PluginFile(escape, "../files/assets/data.txt")
	if want := `pluginFile("../files/assets/data.txt"): outside the directory of plugin "@test/escape"`; err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}
