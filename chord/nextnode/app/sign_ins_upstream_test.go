package app

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/jsclock"
)

// Ports packages/chord/test/node/app/sessions.test.ts (Pi main c23b9ec1): tests of sessions (slice 13 §2): issue, revoke and list
// through `Sessions`; tokens kept only as hashes; the authenticator taking a bearer token or the signed session cookie; ephemeral
// sessions dropped when the server starts.
//
// Go forced differences (language mechanics): the server is the kit's facets on a host (kitServer), not the whole app; Pi's fake `Date` is
// jsclock's mock (the cookies' clock follows it); the test of a cookie's signed expiry past 400 days is the cookie component's
// (cookies_test.go in nextnode/http).

func session(t *testing.T, s *kitServer, id string) delta.JsonValue {
	t.Helper()
	sessions, err := s.host.Get(background, Sessions, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer sessions.Release()
	list, err := sessions.Member("list").Value()
	if err != nil {
		t.Fatal(err)
	}
	for _, each := range list.([]any) {
		if each.(*delta.JsonObject).Value("id") == id {
			return each
		}
	}
	return nil
}

func call(t *testing.T, s *kitServer, method string, args delta.JsonValue) delta.JsonValue {
	t.Helper()
	sessions, err := s.host.Get(background, Sessions, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer sessions.Release()
	value, err := sessions.Call(method, args, background)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSessionsIssueATokenAsBearerAndCookieListItWithoutItsHashRevokeIt(t *testing.T) {
	s := newKitServer(t, kitServerOptions{})
	defer s.close()
	ada := delta.JsonObjectOf("kind", "user", "id", "u1", "name", "ada")
	id, token, cookie := s.issue(ada, "ada's laptop", "device")
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{16}\.[A-Za-z0-9_-]{43}$`).MatchString(token) {
		t.Fatalf("token = %q", token)
	}
	viaBearer := s.send("GET", "/whoami", bearer(token), "")
	if viaBearer.status != 200 || string(viaBearer.body) != `{"kind":"user","id":"u1","name":"ada"}` {
		t.Fatalf("bearer: %d %s", viaBearer.status, viaBearer.body)
	}
	if !regexp.MustCompile(`^chord-session=[^;]+; HttpOnly; Path=/; SameSite=Strict; Max-Age=34560000$`).MatchString(cookie) {
		t.Fatalf("cookie = %q", cookie)
	}
	viaCookie := s.send("GET", "/whoami", map[string]string{"cookie": strings.SplitN(cookie, ";", 2)[0]}, "")
	if viaCookie.status != 200 || !strings.Contains(string(viaCookie.body), `"id":"u1"`) {
		t.Fatalf("cookie: %d %s", viaCookie.status, viaCookie.body)
	}
	listed, ok := session(t, s, id).(*delta.JsonObject)
	if !ok {
		t.Fatal("the session is not listed")
	}
	if listed.Value("label") != "ada's laptop" || listed.Value("kind") != "device" || !reflect.DeepEqual(listed.Value("principal"), ada) ||
		listed.Len() != 6 || listed.Has("hash") {
		t.Fatalf("listed = %v", listed)
	}
	call(t, s, "revoke", id)
	if status := s.send("GET", "/whoami", bearer(token), "").status; status != 401 {
		t.Fatalf("a revoked token got %d", status)
	}
}

func TestSessionsRefuseATokenWithAWrongSecretAnUnknownIDAndAForgedCookie(t *testing.T) {
	s := newKitServer(t, kitServerOptions{})
	defer s.close()
	_, token, _ := s.issue(delta.JsonObjectOf("kind", "owner"), "x", "device")
	id, secret, _ := strings.Cut(token, ".")
	for _, forged := range []string{id + ".wrong", "nope." + secret, "garbage"} {
		if status := s.send("GET", "/whoami", bearer(forged), "").status; status != 401 {
			t.Fatalf("%q got %d", forged, status)
		}
	}
	if status := s.send("GET", "/whoami", map[string]string{"cookie": "chord-session=" + token + ".x.y"}, "").status; status != 401 {
		t.Fatalf("a forged cookie got %d", status)
	}
}

func TestSessionsKeepOnlyTheTokensHashInTheStore(t *testing.T) {
	dir := t.TempDir()
	s := newKitServer(t, kitServerOptions{Dir: dir})
	_, token, _ := s.issue(delta.JsonObjectOf("kind", "owner"), "x", "device")
	s.close()
	_, secret, _ := strings.Cut(token, ".")
	var stored []byte
	for _, name := range []string{"store.sqlite", "store.sqlite-wal"} {
		if content, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			stored = append(stored, content...)
		}
	}
	if strings.Contains(string(stored), secret) {
		t.Fatal("the secret is in the store")
	}
	if !strings.Contains(string(stored), HashToken(secret)) {
		t.Fatal("the secret's hash is not in the store")
	}
}

func TestEphemeralSessionsAuthenticateInMemoryAreNotListedAndEndWithTheServerPersistedOnesStay(t *testing.T) {
	dir := t.TempDir()
	first := newKitServer(t, kitServerOptions{Dir: dir})
	ephemeralID, ephemeralToken, _ := first.issue(delta.JsonObjectOf("kind", "user", "id", "e", "name", "e"), "e", "ephemeral")
	deviceID, deviceToken, _ := first.issue(delta.JsonObjectOf("kind", "owner"), "d", "device")
	if session(t, first, deviceID) == nil || session(t, first, ephemeralID) != nil {
		t.Fatal("the device session must be listed and the ephemeral one not")
	}
	if status := first.send("GET", "/whoami", bearer(ephemeralToken), "").status; status != 200 {
		t.Fatalf("an ephemeral token got %d", status)
	}
	first.close()
	second := newKitServer(t, kitServerOptions{Dir: dir})
	defer second.close()
	if status := second.send("GET", "/whoami", bearer(ephemeralToken), "").status; status != 401 {
		t.Fatalf("an ephemeral token after a restart got %d", status)
	}
	if status := second.send("GET", "/whoami", bearer(deviceToken), "").status; status != 200 {
		t.Fatalf("a device token after a restart got %d", status)
	}
}

func TestEphemeralSessionsLiveAtMostTheLimitPastItTheOldestEndsAnIssueCostsTheSameWhateverCameBefore(t *testing.T) {
	s := newKitServer(t, kitServerOptions{Limits: SessionLimits{MaxEphemeral: 3}})
	defer s.close()
	issue := func(index int) string {
		_, token, _ := s.issue(delta.JsonObjectOf("kind", "user", "id", "u"+strconv.Itoa(index), "name", "u"), "u", "ephemeral")
		return token
	}
	var tokens []string
	for index := range 4 {
		tokens = append(tokens, issue(index))
	}
	var statuses []int
	for _, token := range tokens {
		statuses = append(statuses, s.send("GET", "/whoami", bearer(token), "").status)
	}
	if !reflect.DeepEqual(statuses, []int{401, 200, 200, 200}) {
		t.Fatalf("statuses = %v", statuses)
	}
	// Many more: the cap holds them, and nothing is persisted (no document grows).
	for index := 4; index < 2000; index++ {
		issue(index)
	}
	sessions, err := s.host.Get(background, Sessions, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer sessions.Release()
	list, err := sessions.Member("list").Value()
	if err != nil || len(list.([]any)) != 0 {
		t.Fatalf("list = %v, %v", list, err)
	}
}

func TestRollingCookiesRenewABrowsersCookieOnceItIsOlderThanADayWithAFreshSignedExpiry(t *testing.T) {
	s := newKitServer(t, kitServerOptions{})
	defer s.close()
	_, _, cookie := s.issue(delta.JsonObjectOf("kind", "owner"), "b", "browser")
	signed, _, _ := strings.Cut(cookie, ";")
	whoami := func() reply { return s.send("GET", "/whoami", map[string]string{"cookie": signed}, "") }
	// Within the day: nothing set.
	if reply := whoami(); reply.status != 200 || reply.headers.Get("set-cookie") != "" {
		t.Fatalf("within the day: %d %q", reply.status, reply.headers.Get("set-cookie"))
	}
	start := jsclock.DateNow()
	mock, restore := jsclock.MockDateNow(start + 2*24*60*60_000)
	defer restore()
	renewed := whoami()
	if renewed.status != 200 {
		t.Fatalf("renewed status = %d", renewed.status)
	}
	set := renewed.headers.Get("set-cookie")
	if !regexp.MustCompile(`^chord-session=[^;]+; HttpOnly; Path=/; SameSite=Strict; Max-Age=34560000$`).MatchString(set) {
		t.Fatalf("set-cookie = %q", set)
	}
	parts := strings.Split(strings.SplitN(set, ";", 2)[0], ".")
	seconds, err := strconv.ParseFloat(parts[len(parts)-2], 64)
	if err != nil || seconds*1000 <= start+401*24*60*60_000 {
		t.Fatalf("expires %v ms, want after %v", seconds*1000, start+401*24*60*60_000)
	}
	// 401 days on, the old cookie has expired and the renewed one still signs in (the touch of `lastUsed` is awaited first).
	sessionWrites.Wait()
	mock.Set(start + 401*24*60*60_000)
	if status := whoami().status; status != 401 {
		t.Fatalf("the old cookie got %d", status)
	}
	if status := s.send("GET", "/whoami", map[string]string{"cookie": strings.SplitN(set, ";", 2)[0]}, "").status; status != 200 {
		t.Fatalf("the renewed cookie got %d", status)
	}
}

func TestRollingCookiesDoNotRenewAnEphemeralSessionsCookieNorARevokedSessions(t *testing.T) {
	s := newKitServer(t, kitServerOptions{})
	defer s.close()
	_, _, ephemeralCookie := s.issue(delta.JsonObjectOf("kind", "user", "id", "e", "name", "e"), "e", "ephemeral")
	browserID, _, browserCookie := s.issue(delta.JsonObjectOf("kind", "owner"), "b", "browser")
	call(t, s, "revoke", browserID)
	_, restore := jsclock.MockDateNow(jsclock.DateNow() + 2*24*60*60_000)
	defer restore()
	short := s.send("GET", "/whoami", map[string]string{"cookie": strings.SplitN(ephemeralCookie, ";", 2)[0]}, "")
	if short.status != 200 || short.headers.Get("set-cookie") != "" {
		t.Fatalf("ephemeral: %d %q", short.status, short.headers.Get("set-cookie"))
	}
	revoked := s.send("GET", "/whoami", map[string]string{"cookie": strings.SplitN(browserCookie, ";", 2)[0]}, "")
	if revoked.status != 401 || revoked.headers.Get("set-cookie") != "" {
		t.Fatalf("revoked: %d %q", revoked.status, revoked.headers.Get("set-cookie"))
	}
}

func TestSessionsEndAPersistedSessionUnusedFor400DaysAtItsNextUse(t *testing.T) {
	s := newKitServer(t, kitServerOptions{})
	defer s.close()
	id, token, _ := s.issue(delta.JsonObjectOf("kind", "owner"), "d", "device")
	_, restore := jsclock.MockDateNow(jsclock.DateNow() + 400*24*60*60_000 + 1)
	if status := s.send("GET", "/whoami", bearer(token), "").status; status != 401 {
		t.Fatalf("an idle token got %d", status)
	}
	restore()
	sessionWrites.Wait()
	if session(t, s, id) != nil {
		t.Fatal("the idle session is still listed")
	}
}

func TestSessionsAnswerAMalformedTokenOrAPrototypeKeyAsAnIDWithAPlain401ReportingNothing(t *testing.T) {
	s := newKitServer(t, kitServerOptions{})
	defer s.close()
	for _, token := range []string{"constructor.x", "__proto__.x", "toString.x", "hasOwnProperty.x", ".x", "x", "a.b.c"} {
		if status := s.send("GET", "/whoami", bearer(token), "").status; status != 401 {
			t.Fatalf("%q got %d", token, status)
		}
	}
	if status := s.send("GET", "/whoami", map[string]string{"cookie": "chord-session=__proto__.x"}, "").status; status != 401 {
		t.Fatalf("a prototype cookie got %d", status)
	}
	if len(s.errs.all()) != 0 || len(s.errors.all()) != 0 {
		t.Fatalf("reported: %v %v", s.errs.all(), s.errors.all())
	}
}
