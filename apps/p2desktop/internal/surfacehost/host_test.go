package surfacehost_test

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/models"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/status"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/tree"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/web-shell"
)

func TestMain(m *testing.M) { p2test.Main(m) }

func startServer(t *testing.T, facets ...host.HostFacet) *hosts.P2Server {
	t.Helper()
	fake, zero := true, 0
	server, err := hosts.StartServer(hosts.ServerOptions{DataDir: t.TempDir(), Port: &zero, Fake: &fake, Facets: facets})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(p2test.Background) })
	return server
}

func object(value any) *delta.JsonObject { o, _ := value.(*delta.JsonObject); return o }

// themePlugin is a registered script of a client of this kind: its default export is a facet contributing a theme.
func themePlugin(name string, theme surfaces.Theme) surfaces.RegisteredScript {
	return surfaces.RegisteredScript{Plugin: name, Digest: "d-" + name, Run: func(_ func(string) (any, error), module *surfaces.Module) error {
		module.Exports.Default = host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
			return self.Contribute(registries.Registry[any, any](surfaces.Themes), theme)
		})
		return nil
	}}
}

func TestHostFollowsTheActiveThemeAndLoadsPlugins(t *testing.T) {
	failing := surfaces.RegisteredScript{Plugin: "broken", Digest: "x", Run: func(func(string) (any, error), *surfaces.Module) error {
		return errors.New("boom")
	}}
	extra := surfaces.Theme{ID: "x.light", Name: "X", Mode: "light", Colors: map[string]string{"bg": "#fafafa"}}
	plugins := []surfaces.RegisteredScript{themePlugin("a-theme", extra), failing}
	server := startServer(t, surfacehost.ServerFacet(plugins))
	var changes atomic.Int32
	var errs []error
	h, err := surfacehost.Open(surfacehost.Options{
		Server: server.Host, Mode: "dark", Preferred: []string{"en"},
		Plugins: plugins,
		Changed: func() { changes.Add(1) },
		OnError: func(err error) { errs = append(errs, err) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	waitFor(t, "PiG dark theme", func() bool { return h.Theme().ID == "pig.dark" })
	if failed := h.Failed(); len(failed) != 1 || failed[0].Plugin != "broken" {
		t.Fatalf("failed plugins = %+v, want broken only", failed)
	}
	if len(errs) == 0 {
		t.Fatal("the failed plugin was not reported")
	}
	// A plugin's own theme wins its mode by name order (the registry's rule), the PiG light theme otherwise.
	h.SetMode("light")
	waitFor(t, "plugin light theme", func() bool { return h.Theme().ID == "x.light" })
	h.SetMode("dark")
	waitFor(t, "dark again", func() bool { return h.Theme().ID == "pig.dark" })
	if changes.Load() == 0 {
		t.Fatal("Changed was never called")
	}
}

func TestScopeFollowsLiveSessionsAndDisposes(t *testing.T) {
	server := startServer(t, surfacehost.ServerFacet(nil))
	h, err := surfacehost.Open(surfacehost.Options{Server: server.Host})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	var redraws atomic.Int32
	scope := h.Scope(func() { redraws.Add(1) })
	ref, err := scope.Hold(sessionscontract.Sessions, delta.NewJsonObject(0))
	if err != nil {
		t.Fatal(err)
	}
	sessions := watchSessions(scope, ref)
	waitFor(t, "an empty list", func() bool { rows, ok := sessions(); return ok && len(rows) == 0 })
	if _, err := ref.Call("create", delta.JsonObjectOf("cwd", t.TempDir(), "title", "first"), h.Context()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the new session in state", func() bool { rows, _ := sessions(); return len(rows) == 1 })
	if redraws.Load() == 0 {
		t.Fatal("the scope never asked for a redraw")
	}
	scope.Dispose()
	before := redraws.Load()
	if _, err := h.Client.Hold(h.Context(), sessionscontract.Sessions, delta.NewJsonObject(0), host.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if after := redraws.Load(); after != before {
		t.Fatalf("a disposed scope redrew: %d -> %d", before, after)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOutdatedWhenTheServerPublishesAnotherBase(t *testing.T) {
	plugins := []surfaces.RegisteredScript{themePlugin("a-theme", surfaces.Theme{ID: "x.light", Mode: "light", Colors: map[string]string{"bg": "#fafafa"}})}
	server := startServer(t, surfacehost.ServerFacet(plugins))
	var outdated atomic.Bool
	h, err := surfacehost.Open(surfacehost.Options{Server: server.Host, Plugins: plugins, Outdated: func() { outdated.Store(true) }})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	waitFor(t, "connected", func() bool { return h.Connection() == surfaces.Connected })
	set := surfacehost.SetOf(plugins)
	set.Base = "another-base"
	if err := surfacehost.Publish(server.Host, set); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the outdated callback", outdated.Load)
}

func TestSetOfThisBuildChangesNothingAndAnUnknownScriptIsReportedAndRetried(t *testing.T) {
	plugins := []surfaces.RegisteredScript{themePlugin("a-theme", surfaces.Theme{ID: "x.light", Mode: "light", Colors: map[string]string{"bg": "#fafafa"}})}
	server := startServer(t, surfacehost.ServerFacet(plugins))
	var mu sync.Mutex
	var errs []error
	var outdated atomic.Bool
	report := func(err error) { mu.Lock(); errs = append(errs, err); mu.Unlock() }
	h, err := surfacehost.Open(surfacehost.Options{Server: server.Host, Plugins: plugins, Outdated: func() { outdated.Store(true) }, OnError: report})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	waitFor(t, "connected", func() bool { return h.Connection() == surfaces.Connected })
	// The server expects the script this build holds: nothing to fetch, nothing reported, not outdated.
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	n := len(errs)
	mu.Unlock()
	if n != 0 || outdated.Load() {
		t.Fatalf("a matching set reported %v (outdated %v)", errs, outdated.Load())
	}
	// A server expecting a script another build has: the update fails and is reported (and retried).
	published := surfacehost.SetOf(plugins)
	published.Plugins[0].Digest = "newer"
	if err := surfacehost.Publish(server.Host, published); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "update_failed", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, err := range errs {
			if strings.Contains(err.Error(), "update_failed") {
				return true
			}
		}
		return false
	})
}
