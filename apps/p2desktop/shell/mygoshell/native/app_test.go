package native

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/egoist/mygo/ui"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/models"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/status"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/web-shell"
)

func TestMain(m *testing.M) { p2test.Main(m) }

// startApp runs the native app on a real in-process server and returns it with a tester drawing its view.
func startApp(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	fake, zero := true, 0
	server, err := hosts.StartServer(hosts.ServerOptions{DataDir: t.TempDir(), Port: &zero, Fake: &fake, Facets: []host.HostFacet{surfacehost.ServerFacet(nil)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(p2test.Background) })
	app := New()
	h, err := surfacehost.Open(surfacehost.Options{Server: server.Host, Changed: app.Invalidate})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	if err := app.Start(h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Detach)
	return app, ui.NewTester(app.Build, 1000, 700)
}

// until draws frames until ok, as the window would at each Invalidate.
func until(t *testing.T, tt *ui.Tester, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		tt.Frame()
		if ok() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; texts %q", what, tt.Texts())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSessionsScreenOverTheRealServer(t *testing.T) {
	app, tt := startApp(t)
	until(t, tt, "the empty list", func() bool { return tt.HasText("No sessions yet.") })
	// Create a session through the form.
	if err := tt.Click("New"); err != nil {
		t.Fatal(err)
	}
	until(t, tt, "the form", func() bool { return tt.HasText("New session") })
	if err := tt.Click("Directory"); err != nil {
		t.Logf("label click: %v", err)
	}
	app.newCwd, app.newTitle = t.TempDir(), "hello native"
	tt.Frame()
	if err := tt.Click("Create"); err != nil {
		t.Fatal(err)
	}
	until(t, tt, "the session in the list and its sheet", func() bool {
		return tt.HasText("hello native") && tt.HasText("Archive") && tt.HasText("Pin")
	})
	if err := tt.Click("Pin"); err != nil {
		t.Fatal(err)
	}
	until(t, tt, "unpin after the pin", func() bool { return tt.HasText("Unpin") })
	if err := tt.Click("Archive"); err != nil {
		t.Fatal(err)
	}
	until(t, tt, "the row gone", func() bool { return tt.HasText("No sessions yet.") })
	// The archive toggle shows it, and restoring brings it back.
	app.archived = true
	until(t, tt, "the archived row", func() bool { return tt.HasText("hello native") })
	if err := tt.Click("hello native"); err != nil {
		t.Fatal(err)
	}
	until(t, tt, "Restore", func() bool { return tt.HasText("Restore") })
	if err := tt.Click("Restore"); err != nil {
		t.Fatal(err)
	}
	app.archived = false
	until(t, tt, "back in the list", func() bool {
		return tt.HasText("hello native") && !strings.Contains(strings.Join(tt.Texts(), " "), "Restore")
	})
}

func TestViewBeforeTheHostStartsShowsTheStartPage(t *testing.T) {
	app := New()
	tt := ui.NewTester(app.Build, 600, 400)
	tt.Frame()
	if !tt.HasText("Starting PiG…") {
		t.Fatalf("texts %q", tt.Texts())
	}
}
