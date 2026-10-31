package plugins

import (
	"errors"
	"reflect"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// Ports the cases of packages/chord/test/next-node/plugins/build.test.ts that are about the data `buildFacet` hands the
// component, not about esbuild (see the rows file for the others, which the Builder seam replaces).

func TestBuildingAFacetReturnsASyntaxErrorAndAMissingFacetAsFailedResultsNeverAThrow(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "syntax", "@test/syntax", map[string]string{"worker.ts": "syntax error: export default {"}, nil)
	discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	plugin := discovery.Plugins[0]
	builder := &referenceBuilder{}
	options := BuildOptions{HostPackages: []string{"@earendil-works/chord/next"}, Platform: PlatformNode}
	// A build error is the builder's failed result, as it made it.
	built, err := BuildFacet(builder, plugin, "worker", options, background)
	if err != nil {
		t.Fatal(err)
	}
	if built.Kind != BuiltFacetFailed || len(built.Errors) != 1 || built.Errors[0] != `worker.ts:1:1: Expected "}"` {
		t.Fatalf("built = %+v", built)
	}
	// A plugin with no facet of the kind fails without a build.
	built, err = BuildFacet(builder, plugin, "server", options, background)
	if err != nil {
		t.Fatal(err)
	}
	want := BuiltFacet{Kind: BuiltFacetFailed, Plugin: "@test/syntax", Facet: "server", Errors: []string{`plugin "@test/syntax" has no server facet`}}
	if !reflect.DeepEqual(built, want) {
		t.Fatalf("built = %#v, want %#v", built, want)
	}
	if n := builder.built["@test/syntax\x00server"]; n != 0 {
		t.Fatalf("the builder built the missing facet %d times", n)
	}
}

func TestBuildingAFacetThrowsOnlyWhenItsContextAborts(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "p", "@test/p", map[string]string{"worker.ts": "export default {};"}, nil)
	discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	aborted, cancel := chordctx.WithCancel(background)
	stop := errors.New("stop")
	cancel(stop)
	builder := &referenceBuilder{}
	_, err = BuildFacet(builder, discovery.Plugins[0], "worker", BuildOptions{Platform: PlatformNode}, aborted)
	if !errors.Is(err, stop) {
		t.Fatalf("error = %v, want the abort's reason", err)
	}
	if len(builder.built) != 0 {
		t.Fatalf("the builder ran: %v", builder.built)
	}
	// Not aborted, a build of the plugin's own facet is the builder's.
	built, err := BuildFacet(builder, discovery.Plugins[0], "worker", BuildOptions{Platform: PlatformNode}, background)
	if err != nil || built.Kind != BuiltFacetScript || built.Digest != sha256Hex([]byte(built.Source)) {
		t.Fatalf("built = %+v, error = %v", built, err)
	}
}
