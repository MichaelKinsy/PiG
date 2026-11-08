package native

import (
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
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/login"
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
	until(t, tt, "the empty list", func() bool { return tt.HasText("No sessions yet.") && tt.HasText("What should PiG do?") })
	// Create a session through the form.
	if err := tt.Click("New session"); err != nil {
		t.Fatal(err)
	}
	until(t, tt, "the form", func() bool { return tt.HasText("Directory") })
	app.newCwd, app.newTitle = t.TempDir(), "hello native"
	tt.Frame()
	if err := tt.Click("Create"); err != nil {
		t.Fatal(err)
	}
	until(t, tt, "the session in the list and its sheet", func() bool {
		return tt.HasText("hello native") && tt.HasText("Say something to start.") && !tt.HasText("No sessions yet.")
	})
	// The footer's Archived switches to the archive, which has nothing yet; a second click comes back.
	if err := tt.Click("Show archived sessions"); err != nil {
		t.Fatal(err)
	}
	until(t, tt, "the empty archive", func() bool { return tt.HasText("No archived sessions.") })
	if err := tt.Click("Show archived sessions"); err != nil {
		t.Fatal(err)
	}
	until(t, tt, "the list again", func() bool { return tt.HasText("hello native") })
	// Archiving through the actions of the screen moves the row to the archive.
	id := app.sel()
	go func() { _ = app.sessions.Archive(id) }()
	until(t, tt, "the row gone", func() bool { return tt.HasText("No sessions yet.") })
	app.archived = true
	until(t, tt, "the archived row", func() bool { return tt.HasText("hello native") })
	// Selecting an archived row restores it, as the drawer does (another row was selected before).
	app.setSel("")
	tt.Frame()
	if err := tt.Click("hello native"); err != nil {
		t.Fatal(err)
	}
	until(t, tt, "back in the list", func() bool { return !app.archived && tt.HasText("hello native") })
}

func TestViewBeforeTheHostStartsShowsTheStartPage(t *testing.T) {
	app := New()
	tt := ui.NewTester(app.Build, 600, 400)
	tt.Frame()
	if !tt.HasText("Starting PiG…") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

// TestChatRoundTrip types in the prompt bar of a session and reads the faux provider's answer in the transcript.
func TestChatRoundTrip(t *testing.T) {
	app, tt := startApp(t)
	until(t, tt, "the empty list", func() bool { return tt.HasText("No sessions yet.") })
	id, err := app.sessions.Create(t.TempDir(), "chat session")
	if err != nil {
		t.Fatal(err)
	}
	app.setSel(id)
	until(t, tt, "the chat", func() bool { return tt.HasText("Say something to start.") })
	app.draftText = "hello from the test"
	tt.Frame()
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	worked := false
	until(t, tt, "the message and an answer after the run", func() bool {
		worked = worked || tt.HasText("Working…") || tt.HasText("Stop")
		return worked && tt.HasText("hello from the test") && !tt.HasText("Working…") && !tt.HasText("Stop") && !tt.HasText("Say something to start.")
	})
	if chat, ok := app.chat.Transcript(); !ok || len(chat.Items) < 2 {
		t.Fatalf("the transcript holds %d items, want the message and the answer", len(chat.Items))
	}
}

// TestProvidersSheetFromTheEmptyState: without a credential the empty state offers "Sign in to a provider", which opens the
// providers sheet (the server's p2.provider-login).
func TestProvidersSheetFromTheEmptyState(t *testing.T) {
	_, tt := startApp(t)
	until(t, tt, "the empty state", func() bool { return tt.HasText("What should PiG do?") && tt.HasText("Sign in to a provider") })
	if err := tt.Click("Sign in to a provider"); err != nil {
		t.Fatal(err)
	}
	until(t, tt, "the sheet", func() bool { return tt.HasText("Providers") && tt.HasText("Search providers") || tt.HasText("Done") })
	if err := tt.Click("Done"); err != nil {
		t.Fatal(err)
	}
	until(t, tt, "the sheet closed", func() bool { return !tt.HasText("Done") })
}
