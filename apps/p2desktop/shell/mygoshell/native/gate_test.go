package native

import (
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	brandsprite "github.com/MichaelKinsy/PiG/apps/p2/brand/sprite"
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/activity"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/egoist/mygo/ui"
)

// TestGateShot is the visual gate's native half when no window can be captured (a locked screen; Linux): it draws the native
// screens headless (MyGo's software tester, 1280x812 DIPs like the web capture) over a server that ALSO serves the branded
// web UI with the sprite hook, so smoke/gate/gate.sh can photograph the web side from the same server. It is skipped unless
// P2_GATE_OUT is set; P2_GATE_THEME=light|dark, P2_GATE_SEED=1 creates the sessions of the gate, PIG_HOME holds the sprite
// selection. The test writes the picture to P2_GATE_OUT, its port to P2_GATE_PORT_FILE and waits for P2_GATE_DONE_FILE.
func TestGateShot(t *testing.T) {
	out := os.Getenv("P2_GATE_OUT")
	if out == "" {
		t.Skip("P2_GATE_OUT is not set")
	}
	t.Setenv("P2_DESKTOP_THEME", os.Getenv("P2_GATE_THEME"))
	// P2_GATE_PROVIDERS=1: the real catalog (the Providers sheet needs providers with login methods) and the sheet open.
	fake, zero := os.Getenv("P2_GATE_PROVIDERS") != "1", 0
	options := hosts.ServerOptions{DataDir: t.TempDir(), Port: &zero, Fake: &fake, Facets: []host.HostFacet{surfacehost.ServerFacet(nil)}}
	if home, err := brandsprite.DefaultHome(); err == nil {
		if source, err := brandsprite.Open(home); err == nil {
			options.Sprite = source
		}
	}
	server, err := hosts.StartServer(options)
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
	tt := ui.NewTester(app.Build, 1280, 812)
	want := "No sessions yet."
	if os.Getenv("P2_GATE_SEED") == "1" {
		base := "/tmp"
		for _, s := range [][2]string{
			{"pig", "Fix the flaky sync test"}, {"pig", "Port the markdown renderer"}, {"pig", "Explain the surface host"},
			{"site", "Write the release notes"}, {"site", "Tighten the landing page"}, {"notes", "Plan the week"},
		} {
			dir := filepath.Join(base, "pig-gate", s[0])
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			until(t, tt, "the sessions to be readable", app.sessions.Ready)
			id, err := app.sessions.Create(dir, s[1])
			if err != nil {
				t.Fatal(err)
			}
			if s[1] == "Fix the flaky sync test" {
				if err := app.sessions.Pin(id, true); err != nil {
					t.Fatal(err)
				}
			}
		}
		want = "Plan the week"
	}
	opened := ""
	if os.Getenv("P2_GATE_CHAT") == "1" {
		// The chat screen: the pinned session, one exchange with the faux provider, shown open.
		for _, item := range app.sessions.Items("", false) {
			if item.Row != nil && item.Row.Title == "Fix the flaky sync test" {
				opened = item.Row.ID
			}
		}
		if opened == "" {
			t.Fatal("the gate's session is missing")
		}
		app.setSel(opened)
		until(t, tt, "the chat", func() bool { return app.chat != nil && tt.HasText("Say something to start.") })
		if err := app.chat.Send("Explain what the surface host does, with a short list."); err != nil {
			t.Fatal(err)
		}
		worked := false
		until(t, tt, "the answer", func() bool {
			worked = worked || tt.HasText("Working…") || tt.HasText("Stop")
			return worked && !tt.HasText("Working…") && !tt.HasText("Stop")
		})
		want = "Explain what the surface host does, with a short list."
		if os.Getenv("P2_GATE_TREE") == "1" {
			// `/tree`: branch from the exchange's user message; the drawer lists the two branches, expanded.
			tr, _ := app.chat.Transcript()
			entry := 0
			for _, item := range tr.Items {
				if user, ok := item.(transcript.UserItem); ok && user.EntryID != nil && entry == 0 {
					entry = *user.EntryID
				}
			}
			if _, _, err := app.treeOf(app.host).Navigate(opened, entry); err != nil {
				t.Fatal(err)
			}
			app.expanded = map[string]bool{opened: true}
			until(t, tt, "the branches", func() bool { return tt.HasText("current") && tt.HasText("Say something to start.") })
			want = "current"
		}
		if os.Getenv("P2_GATE_TOOLS") == "1" {
			// The tool cards: the faux provider's scripted calls (write, edit, bash), one turn each, the last turn's row opened.
			for _, prompt := range []string{"write", "edit", "bash"} {
				if err := app.chat.Send(prompt); err != nil {
					t.Fatal(err)
				}
				until(t, tt, "the "+prompt+" turn", func() bool { return !tt.HasText("Stop") && !tt.HasText("Working…") && tt.HasText(prompt) })
			}
			time.Sleep(time.Second)
			tr, _ := app.chat.Transcript()
			items := app.activityItems(&tr)
			for i := len(items) - 1; i >= 0; i-- {
				if act, ok := items[i].(activity.Activity); ok {
					app.sheetKey = act.Key
					break
				}
			}
			want = "bash"
		}
	}
	if os.Getenv("P2_GATE_PROVIDERS") == "1" {
		app.pickProvider = true
		app.Invalidate()
		until(t, tt, "the providers", func() bool { return tt.HasText("Anthropic") && tt.HasText("OpenAI") })
		want = "Anthropic"
	}
	if opened == "" && os.Getenv("P2_GATE_PROVIDERS") != "1" {
		until(t, tt, "the first frame", func() bool { return tt.HasText(want) && tt.HasText("What should PiG do?") })
	} else {
		until(t, tt, "the first frame", func() bool { return tt.HasText(want) })
	}
	time.Sleep(500 * time.Millisecond)
	tt.Advance(3 * time.Second) // the reveal of streamed text and the fades are done
	tt.Frame()
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, tt.Image()); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if portFile := os.Getenv("P2_GATE_PORT_FILE"); portFile != "" {
		if err := os.WriteFile(portFile, []byte(strconv.Itoa(server.Port)+"\n"+opened), 0o600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(120 * time.Second)
		for done := os.Getenv("P2_GATE_DONE_FILE"); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if _, err := os.Stat(done); err == nil {
				break
			}
		}
	}
}
