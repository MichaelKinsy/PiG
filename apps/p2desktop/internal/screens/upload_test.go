package screens_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/screens"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// A file chosen with the composer's + goes up through the server's upload route and rides on Chat.send as an attachment.
func TestAttachmentsGoWithTheMessage(t *testing.T) {
	fake, zero := true, 0
	dataDir := t.TempDir()
	server, err := hosts.StartServer(hosts.ServerOptions{DataDir: dataDir, Port: &zero, Fake: &fake, Facets: []host.HostFacet{surfacehost.ServerFacet(nil)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(p2test.Background) })
	origin := fmt.Sprintf("http://127.0.0.1:%d", server.Port)
	cookie := p2test.SignInLocalOwner(t, server.Port, dataDir).Cookie
	h, err := surfacehost.Open(surfacehost.Options{Server: server.Host, Origin: origin, Cookie: cookie})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	scope := h.Scope(func() {})
	t.Cleanup(scope.Dispose)
	sessions, err := screens.OpenSessions(h, scope)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the catalog", sessions.Ready)
	id, err := sessions.Create(t.TempDir(), "attach")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("remember the milk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if h.Origin() != origin {
		t.Fatalf("origin = %q", h.Origin())
	}
	up, err := screens.UploadFile(context.Background(), origin, cookie, id, path)
	if err != nil || up.ID == "" || up.Name != "notes.txt" || up.Size != 17 {
		t.Fatalf("upload = %+v, %v", up, err)
	}
	if _, err := screens.UploadFile(context.Background(), origin, cookie, "no-such-session", path); err == nil {
		t.Fatal("an upload to an unknown session was taken")
	}
	if _, err := screens.UploadFile(context.Background(), origin, "", id, path); err == nil {
		t.Fatal("an upload without the owner's session was taken")
	}
	chat, err := screens.OpenChat(h, scope, id, 1, func() {})
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send("read it", up.ID); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send("again", "not-an-upload"); err == nil {
		t.Fatal("an unknown attachment was sent")
	}
	waitFor(t, "the answer", func() bool {
		tr, _ := chat.Transcript()
		return len(tr.Items) >= 2 && !tr.Busy
	})
}
