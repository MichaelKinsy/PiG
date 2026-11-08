package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/jsclock"
)

// Ports packages/chord/test/node/app/owner.test.ts (Pi main c23b9ec1): the owner link, the owner token file, an owner the app
// chooses. The tests that start the whole app (`testApp`, a restart of it) run on a kit server and a second kit server over the
// same directory; the owner-only pairing route of the last test is pairing_upstream_test.go's.

var (
	ownerPrincipal = func() *delta.JsonObject { return delta.JsonObjectOf("kind", "owner") }
	adaUser        = func() *delta.JsonObject { return delta.JsonObjectOf("kind", "user", "id", "u1") }
	bobUser        = func() *delta.JsonObject { return delta.JsonObjectOf("kind", "user", "id", "u2") }
)

// ownerServer is a kit server with the owner facet for owner and its first link's token.
func ownerServer(t *testing.T, dir string, owner *delta.JsonObject) (*kitServer, string) {
	t.Helper()
	link := &OwnerLink{}
	token := link.Renew()
	return newKitServer(t, kitServerOptions{Dir: dir, Facets: []host.HostFacet{{Name: "chord.app.owner", Facet: OwnerFacet(link, owner)}}}), token
}

func postToken(s *kitServer, token string) reply {
	body, _ := json.Marshal(map[string]string{"token": token})
	return s.send("POST", OwnerPath, nil, string(body))
}

func cookieOf(r reply) string { return strings.SplitN(r.headers.Get("set-cookie"), ";", 2)[0] }

func whoamiOf(t *testing.T, s *kitServer, headers map[string]string) string {
	t.Helper()
	return string(s.send("GET", "/whoami", headers, "").body)
}

func readToken(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}

func mustMode0600(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode of %s = %v, want 0600", path, info.Mode().Perm())
	}
}

func tokenFile(t *testing.T, s *kitServer, path string, owner *delta.JsonObject) string {
	t.Helper()
	token, err := OwnerTokenFile(path, owner, s.host, background)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestOwnerLinkServesAPageReadingTheFragmentSignsTheOwnerInOnceAndRefusesTheTokenASecondTime(t *testing.T) {
	s, token := ownerServer(t, "", ownerPrincipal())
	defer s.close()
	if status := s.send("GET", "/whoami", nil, "").status; status != 401 {
		t.Fatalf("anonymous whoami = %d, want 401", status)
	}
	page := s.send("GET", OwnerPath, nil, "")
	if page.status != 200 || !strings.Contains(string(page.body), "location.hash") {
		t.Fatalf("page = %d %q", page.status, page.body)
	}
	signed := postToken(s, token)
	if signed.status != 200 {
		t.Fatalf("post = %d", signed.status)
	}
	if got := whoamiOf(t, s, map[string]string{"cookie": cookieOf(signed)}); got != `{"kind":"owner"}` {
		t.Fatalf("whoami = %s", got)
	}
	if status := postToken(s, token).status; status != 403 {
		t.Fatalf("second post = %d, want 403", status)
	}
	sessions, err := s.host.Get(background, Sessions, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer sessions.Release()
	list, err := sessions.Member("list").Value()
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, each := range list.([]any) {
		if object := each.(*delta.JsonObject); object.Value("kind") == "browser" {
			labels = append(labels, object.Value("label").(string))
		}
	}
	if len(labels) != 1 || labels[0] != "owner browser" {
		t.Fatalf("browser sessions = %v", labels)
	}
}

func TestOwnerLinkRefusesAnExpiredTokenAWrongOneAndThePreviousStarts(t *testing.T) {
	start := jsclock.DateNow()
	mock, restore := jsclock.MockDateNow(start)
	defer restore()
	link := &OwnerLink{}
	expiring := link.Renew()
	mock.Set(start + 10*60_000)
	if link.Consume(expiring) {
		t.Fatal("an expired token was consumed")
	}
	previous := link.Renew()
	current := link.Renew()
	if link.Consume(previous) {
		t.Fatal("the previous start's token was consumed")
	}
	if link.Consume(strings.Repeat("x", 43)) {
		t.Fatal("a wrong token was consumed")
	}
	mock.Set(start + 10*60_000 + 10*60_000 - 1)
	if !link.Consume(current) {
		t.Fatal("the current token was refused")
	}
	if link.Consume(current) {
		t.Fatal("a used token was consumed twice")
	}
}

func TestOwnerLinkGivesANewLinkAtEachStartTheOldOneNoLongerSignsIn(t *testing.T) {
	dir := t.TempDir()
	first, old := ownerServer(t, dir, ownerPrincipal())
	first.close()
	second, fresh := ownerServer(t, dir, ownerPrincipal())
	defer second.close()
	if fresh == old {
		t.Fatal("the link did not change")
	}
	if status := postToken(second, old).status; status != 403 {
		t.Fatalf("old token post = %d, want 403", status)
	}
}

func TestOwnerTokenFileIsWrittenAtFirstStartWithMode0600SignsInAsTheOwnerAndIsKeptAcrossStarts(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "owner-token")
	first, _ := ownerServer(t, dir, ownerPrincipal())
	written := tokenFile(t, first, file, ownerPrincipal())
	mustMode0600(t, file)
	token := readToken(t, file)
	if token != written {
		t.Fatalf("returned %q, file %q", written, token)
	}
	if got := whoamiOf(t, first, bearer(token)); got != `{"kind":"owner"}` {
		t.Fatalf("whoami = %s", got)
	}
	first.close()
	second, _ := ownerServer(t, dir, ownerPrincipal())
	defer second.close()
	if kept := tokenFile(t, second, file, ownerPrincipal()); kept != token || readToken(t, file) != token {
		t.Fatal("the token file changed across starts")
	}
}

func TestOwnerTokenFileIsReplacedAtTheNextStartWhenItsSecretNoLongerMatchesItsSession(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "owner-token")
	first, _ := ownerServer(t, dir, ownerPrincipal())
	tokenFile(t, first, file, ownerPrincipal())
	token := readToken(t, file)
	first.close()
	damaged := token[:strings.IndexByte(token, '.')] + "." + strings.Repeat("x", 43)
	if err := os.WriteFile(file, []byte(damaged+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, _ := ownerServer(t, dir, ownerPrincipal())
	defer second.close()
	tokenFile(t, second, file, ownerPrincipal())
	if replaced := readToken(t, file); replaced == damaged || replaced == token {
		t.Fatalf("the file was not replaced: %q", replaced)
	}
}

func TestOwnerTokenFileIsReplacedAtTheNextStartOnceItsSessionWasRevoked(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "owner-token")
	first, _ := ownerServer(t, dir, ownerPrincipal())
	tokenFile(t, first, file, ownerPrincipal())
	token := readToken(t, file)
	call(t, first, "revoke", token[:strings.IndexByte(token, '.')])
	first.close()
	second, _ := ownerServer(t, dir, ownerPrincipal())
	defer second.close()
	tokenFile(t, second, file, ownerPrincipal())
	if replaced := readToken(t, file); replaced == token {
		t.Fatal("the revoked token stayed")
	}
	mustMode0600(t, file)
}

func TestAnOwnerTheAppChoosesSignsTheOwnerLinkAndTheOwnerTokenFileInAsThatPrincipal(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "owner-token")
	s, link := ownerServer(t, dir, adaUser())
	defer s.close()
	signed := postToken(s, link)
	if got := whoamiOf(t, s, map[string]string{"cookie": cookieOf(signed)}); got != `{"kind":"user","id":"u1"}` {
		t.Fatalf("cookie whoami = %s", got)
	}
	tokenFile(t, s, file, adaUser())
	if got := whoamiOf(t, s, bearer(readToken(t, file))); got != `{"kind":"user","id":"u1"}` {
		t.Fatalf("token whoami = %s", got)
	}
}

func TestAUserOwnersTokenFileIsKeptAcrossStartsAlsoWithTheOwnersFieldsInAnotherOrder(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "owner-token")
	first, _ := ownerServer(t, dir, adaUser())
	tokenFile(t, first, file, adaUser())
	token := readToken(t, file)
	first.close()
	reordered := delta.JsonObjectOf("id", "u1", "kind", "user")
	second, _ := ownerServer(t, dir, reordered)
	defer second.close()
	if kept := tokenFile(t, second, file, reordered); kept != token || readToken(t, file) != token {
		t.Fatal("the token file changed")
	}
}

func TestTheOwnerTokenFileIsReplacedWhenTheOwnerChangesItsSessionRevokedTheOldOwnersOthersStay(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "owner-token")
	first, _ := ownerServer(t, dir, adaUser())
	tokenFile(t, first, file, adaUser())
	old := readToken(t, file)
	_, other, _ := first.issue(adaUser(), "other", "device")
	first.close()
	second, _ := ownerServer(t, dir, bobUser())
	defer second.close()
	tokenFile(t, second, file, bobUser())
	replaced := readToken(t, file)
	if replaced == old {
		t.Fatal("the token file was not replaced")
	}
	mustMode0600(t, file)
	if got := whoamiOf(t, second, bearer(replaced)); got != `{"kind":"user","id":"u2"}` {
		t.Fatalf("new owner whoami = %s", got)
	}
	if status := second.send("GET", "/whoami", bearer(old), "").status; status != 401 {
		t.Fatalf("old owner token = %d, want 401", status)
	}
	if got := whoamiOf(t, second, bearer(other)); got != `{"kind":"user","id":"u1"}` {
		t.Fatalf("other session whoami = %s", got)
	}
}
