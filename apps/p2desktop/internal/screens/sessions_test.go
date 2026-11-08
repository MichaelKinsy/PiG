package screens_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/screens"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/next/host"

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

func open(t *testing.T) (*screens.Sessions, *atomic.Int32) {
	t.Helper()
	fake, zero := true, 0
	server, err := hosts.StartServer(hosts.ServerOptions{DataDir: t.TempDir(), Port: &zero, Fake: &fake, Facets: []host.HostFacet{surfacehost.ServerFacet(nil)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(p2test.Background) })
	h, err := surfacehost.Open(surfacehost.Options{Server: server.Host})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	var redraws atomic.Int32
	scope := h.Scope(func() { redraws.Add(1) })
	t.Cleanup(scope.Dispose)
	s, err := screens.OpenSessions(h, scope)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the catalog", s.Ready)
	return s, &redraws
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

func titles(items []screens.Item) (groups, rows []string) {
	for _, item := range items {
		if item.Group != nil {
			groups = append(groups, item.Group.Label)
		} else {
			rows = append(rows, item.Row.Title)
		}
	}
	return
}

func TestSessionsLifecycleAgainstTheServer(t *testing.T) {
	s, redraws := open(t)
	if got := s.Items("", false); len(got) != 0 {
		t.Fatalf("a new data directory lists %d items", len(got))
	}
	cwd := t.TempDir()
	a, err := s.Create(cwd, "alpha")
	if err != nil || a == "" {
		t.Fatalf("create: %q %v", a, err)
	}
	b, err := s.Create(cwd, "beta")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both rows", func() bool { return s.Count() == 2 })
	groups, rows := titles(s.Items("", false))
	if len(groups) != 1 || len(rows) != 2 {
		t.Fatalf("groups %v rows %v", groups, rows)
	}
	// Pinning moves a row to the top of its group.
	if err := s.Pin(a, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "alpha first", func() bool { _, r := titles(s.Items("", false)); return len(r) == 2 && r[0] == "alpha" })
	if err := s.Rename(b, "gamma"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "renamed", func() bool { _, r := titles(s.Items("", false)); return len(r) == 2 && r[1] == "gamma" })
	if _, r := titles(s.Items("gam", false)); len(r) != 1 || r[0] != "gamma" {
		t.Fatalf("query gam: %v", r)
	}
	// Archive moves a row out of the list and into the archive; Restore brings it back.
	if err := s.Archive(b); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "archived", func() bool {
		_, live := titles(s.Items("", false))
		_, kept := titles(s.Items("", true))
		return len(live) == 1 && len(kept) == 1 && kept[0] == "gamma"
	})
	if err := s.Restore(b); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "restored", func() bool { _, live := titles(s.Items("", false)); return len(live) == 2 })
	// A refused action is recorded and shown: a title longer than the contract allows.
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'x'
	}
	if err := s.Rename(a, string(long)); err == nil || s.LastError() == nil {
		t.Fatalf("an over-long title was accepted (err %v)", err)
	}
	// The refused guess is rolled back.
	waitFor(t, "rolled back", func() bool {
		for _, item := range s.Items("", false) {
			if item.Row != nil && len(item.Row.Title) > 100 {
				return false
			}
		}
		return true
	})
	if redraws.Load() == 0 {
		t.Fatal("the scope never asked for a redraw")
	}
}
