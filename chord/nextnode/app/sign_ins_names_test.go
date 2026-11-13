package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
)

// Not upstream's: the names Pi ff7414d51 ("Sign-ins, not sessions") changed that a running kit shows to someone else: the
// cookie a browser holds and the document the server's store keeps. Each test runs on the kit's own helpers, which the
// rename left alone, so each fails on the code before the rename by a run, not by a name that does not compile. Pi keeps
// no alias and reads nothing under the old names, and neither does the kit here.

// upgradeOldCookie is the `name=value` a browser holds from the kit before the rename: the cookie `chord-session`
// carrying token, signed with the server's cookie secret, 400 days.
func upgradeOldCookie(token string) string {
	request := provenanceRequest{provenance: chordhttp.Provenance{Kind: chordhttp.ProvenanceLocal}}
	set := chordhttp.Cookie("chord-session", token, chordhttp.CookieOptions{Secret: cookieSecret, Request: request, MaxAge: new(float64(400 * 24 * 60 * 60))})
	pair, _, _ := strings.Cut(set, ";")
	return pair
}

func TestTheKitsCookieIsChordSignInAndTheNameItHadBeforeSignsNobodyIn(t *testing.T) {
	s := newKitServer(t, kitServerOptions{})
	defer s.close()
	_, token, set := s.issue(ownerPrincipal(), "b", "browser")
	pair, _, _ := strings.Cut(set, ";")
	if name, _, _ := strings.Cut(pair, "="); name != "chord-sign-in" {
		t.Fatalf("the kit's cookie is named %q, want chord-sign-in", name)
	}
	if got := whoamiOf(t, s, map[string]string{"cookie": pair}); got != `{"kind":"owner"}` {
		t.Fatalf("whoami with the cookie = %s", got)
	}
	// The same live token in a well-signed cookie of the old name: the kit does not read that name.
	if status := s.send("GET", "/whoami", map[string]string{"cookie": upgradeOldCookie(token)}, "").status; status != 401 {
		t.Fatalf("a signed cookie named chord-session = %d, want 401", status)
	}
}

func TestTheKitsStoredDocumentIsChordAppSignInsWithTheFieldSignIns(t *testing.T) {
	dir := t.TempDir()
	s := newKitServer(t, kitServerOptions{Dir: dir})
	s.issue(ownerPrincipal(), "x", "device")
	s.close()
	var stored []byte
	for _, name := range []string{"store.sqlite", "store.sqlite-wal"} {
		if content, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			stored = append(stored, content...)
		}
	}
	for _, want := range []string{"chord.app.sign-ins", `"signIns"`} {
		if !strings.Contains(string(stored), want) {
			t.Errorf("the store does not hold %s", want)
		}
	}
	for _, gone := range []string{"chord.app.sessions", `"sessions"`} {
		if strings.Contains(string(stored), gone) {
			t.Errorf("the store holds %s", gone)
		}
	}
}
