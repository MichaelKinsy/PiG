package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	next "github.com/MichaelKinsy/PiG/chord/next/surfaces"
	"github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports packages/chord/test/node/surfaces/surface-build.test.ts (Pi 284e63d9): a surface's build (surface-build.ts; owner decision
// "A" on startup cost): one build shared by every waiter, the scripts object's empty set before it, a rollout step's wait bounded by
// its context while the build goes on, and a failed build answering 503 and failing its step.

// buildFixture is a loaded server with the web surface's build of an entry (its scripts object served).
type buildFixture struct {
	t        *testing.T
	server   host.Host
	plugins  *plugins.Plugins
	building *surfaceBuild
}

func newBuildFixture(t *testing.T, entry string) *buildFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "plugins")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	kind, err := contracts.NewHostKind("server")
	if err != nil {
		t.Fatal(err)
	}
	address := kind.Address(nil)
	server, err := host.CreateHost(host.HostOptions{Address: &address, Principal: delta.JsonObjectOf("kind", "server"), OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	created := plugins.CreatePlugins(plugins.PluginsOptions{
		Roots: []plugins.PluginRoot{{Path: root}}, Kinds: []plugins.KindDefinition{{Name: "web", Platform: plugins.PlatformBrowser}},
	}, chordctx.Background())
	building := newSurfaceBuild(Web, PlatformBrowser, WebPrefix, surfaceBuildOptions{kind: "web", entry: entry, plugins: created}, server)
	report, err := server.Load([]host.HostFacet{{Name: "chord.web", Facet: ScriptsFacet(Web, building.set)}}, chordctx.Background())
	if err != nil || len(report.Failed) != 0 {
		t.Fatalf("load = %+v, %v", report.Failed, err)
	}
	t.Cleanup(func() {
		_ = server.Close(chordctx.Background())
		created.Close()
	})
	return &buildFixture{t: t, server: server, plugins: created, building: building}
}

// entryIn writes main.ts with text in a new directory: its path.
func entryIn(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.ts")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOneSharedBuildBuildsOnceForEveryWaiterTheScriptsObjectShowsAnEmptySetBeforeTheFirstSetAfter(t *testing.T) {
	f := newBuildFixture(t, entryIn(t, "globalThis.started = true;\n"))
	scripts, err := f.server.Get(chordctx.Background(), next.WebScripts, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer scripts.Release()
	current := func() next.ScriptSet {
		value, err := scripts.Member("set").Value()
		if err != nil {
			t.Fatal(err)
		}
		return next.ScriptSetOf(value)
	}
	if set := current(); set.Base != "" || set.Combined != "" || len(set.Plugins) != 0 {
		t.Fatalf("set before the build = %+v, want empty", set)
	}
	if f.building.built() != nil {
		t.Fatal("built before anything built it")
	}
	var wg sync.WaitGroup
	var first, second SurfaceBuilt
	wg.Go(func() { first = f.building.build() })
	wg.Go(func() { second = f.building.build() })
	wg.Wait()
	if first.Err != nil || second.Base != first.Base || len(second.Inputs) != len(first.Inputs) {
		t.Fatalf("builds = %+v, %+v, want one", first, second)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(first.Base) {
		t.Fatalf("base = %q, want 16 hex characters", first.Base)
	}
	if got := f.building.built(); got == nil || got.Base != first.Base {
		t.Fatalf("built = %+v, want the build's outcome", got)
	}
	deadline := time.Now().Add(10 * time.Second)
	for current().Base != first.Base {
		if time.Now().After(deadline) {
			t.Fatalf("the scripts object shows %q, want the base %q", current().Base, first.Base)
		}
		time.Sleep(10 * time.Millisecond)
	}
	response, err := f.building.file(WebPrefix+"base."+first.Base+".js", context.Background())
	if err != nil || response.Kind == http.KindError {
		t.Fatalf("file = %+v, %v", response, err)
	}
}

func TestAFailedBuildAnswers503ForTheSurfacesFilesAndFailsItsStepsHost(t *testing.T) {
	f := newBuildFixture(t, entryIn(t, "export const broken = ;\n"))
	response, err := f.building.file(WebPrefix+"base.0.js", context.Background())
	if err != nil || response.Kind != http.KindError || response.Status != 503 {
		t.Fatalf("file = %+v, %v, want 503", response, err)
	}
	if outcome := f.building.build(); outcome.Err == nil || !strings.Contains(outcome.Err.Error(), `Unexpected ";"`) {
		t.Fatalf("build = %+v, want esbuild's error", outcome)
	}
	built, err := f.plugins.Build(nil, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.building.step("web").Reload(built, map[string][]plugins.PluginScript{}, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hosts) != 1 || result.Hosts[0].Host != "web" || result.Hosts[0].Error == nil || !strings.Contains(result.Hosts[0].Error.Error(), `Unexpected ";"`) {
		t.Fatalf("hosts = %+v, want the web host failed with esbuild's error", result.Hosts)
	}
}
