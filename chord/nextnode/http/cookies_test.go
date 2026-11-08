package http

// pi: packages/chord/src/next-node/http/cookies.ts

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Tests of cookies (security baseline (5)): the safe defaults, `Secure` on a remote origin, a signature checked when
// read back (a changed value, another name or secret reads nothing), and an expiry signed with it. Ports
// test/next-node/http/cookies.test.ts.

// cookiesRequest is a request carrying a `Cookie` header, from a provenance.
type cookiesRequest struct {
	HttpRequest
	header     http.Header
	provenance Provenance
}

func (r cookiesRequest) Headers() http.Header   { return r.header }
func (r cookiesRequest) Provenance() Provenance { return r.provenance }

func cookiesRequestWith(header string) HttpRequest {
	return cookiesRequestFrom(header, Provenance{Kind: ProvenanceLocal})
}

func cookiesRequestFrom(header string, provenance Provenance) HttpRequest {
	return cookiesRequest{header: http.Header{"Cookie": []string{header}}, provenance: provenance}
}

func cookiesRead(t *testing.T, request HttpRequest, name, secret string) (string, bool) {
	t.Helper()
	return ReadCookie(request, name, secret)
}

func cookiesExpectNone(t *testing.T, request HttpRequest, name, secret string) {
	t.Helper()
	if value, ok := ReadCookie(request, name, secret); ok {
		t.Fatalf("ReadCookie(%q) = %q, want none", name, value)
	}
}

func TestCookiesAreHttpOnlyPathSameSiteStrictByDefaultSecureOnARemoteOriginLaxAndMaxAgeWhenAsked(t *testing.T) {
	local := cookiesRequestWith("")
	got := Cookie("session", "a b", CookieOptions{Secret: "s", Request: local})
	if !regexp.MustCompile(`^session=a%20b\.\.[A-Za-z0-9_-]{43}; HttpOnly; Path=/; SameSite=Strict$`).MatchString(got) {
		t.Fatalf("cookie = %q", got)
	}
	remote := cookiesRequestFrom("", Provenance{Kind: ProvenanceRemote, Host: "notes.example.com"})
	maxAge := 60.5
	got = Cookie("session", "x", CookieOptions{Secret: "s", Request: remote, SameSite: SameSiteLax, MaxAge: &maxAge})
	if !regexp.MustCompile(`; HttpOnly; Path=/; SameSite=Lax; Secure; Max-Age=60$`).MatchString(got) {
		t.Fatalf("cookie = %q", got)
	}
}

func TestCookiesReadBackOnlyWithTheirSignatureNameAndSecret(t *testing.T) {
	set := Cookie("session", "owner:1", CookieOptions{Secret: "s", Request: cookiesRequestWith("")})
	value, _, _ := strings.Cut(set, ";")
	if got, ok := cookiesRead(t, cookiesRequestWith("other=1; "+value), "session", "s"); !ok || got != "owner:1" {
		t.Fatalf("ReadCookie = %q, %v, want owner:1", got, ok)
	}
	cookiesExpectNone(t, cookiesRequestWith(value), "session", "t")
	cookiesExpectNone(t, cookiesRequestWith(strings.Replace(value, "owner", "admin", 1)), "session", "s")
	cookiesExpectNone(t, cookiesRequestWith(strings.Replace(value, "session", "sess", 1)), "sess", "s")
	cookiesExpectNone(t, cookiesRequestWith("session=unsigned"), "session", "s")
	cookiesExpectNone(t, cookiesRequestWith(""), "session", "s")
}

// Opus's recheck R3: a copied cookie stayed valid past its Max-Age.
func TestCookiesRefuseACookiePastItsSignedExpiryAndOneWhoseExpiryWasChanged(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	saved := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = saved })

	maxAge := 60.0
	set := Cookie("session", "a.b", CookieOptions{Secret: "s", Request: cookiesRequestWith(""), MaxAge: &maxAge})
	value, _, _ := strings.Cut(set, ";")
	if got, ok := cookiesRead(t, cookiesRequestWith(value), "session", "s"); !ok || got != "a.b" {
		t.Fatalf("ReadCookie = %q, %v, want a.b", got, ok)
	}
	last := strings.LastIndex(value, ".")
	payload := value[:strings.LastIndex(value[:last], ".")]
	parts := strings.Split(value, ".")
	expires, signature := parts[len(parts)-2], parts[len(parts)-1]
	seconds, err := strconv.ParseFloat(expires, 64)
	if err != nil {
		t.Fatal(err)
	}
	extended := payload + "." + strconv.FormatFloat(seconds+3600, 'f', -1, 64) + "." + signature
	cookiesExpectNone(t, cookiesRequestWith(extended), "session", "s")
	clock = clock.Add(61 * time.Second)
	cookiesExpectNone(t, cookiesRequestWith(value), "session", "s")
}

// TestReadSignedCookieGivesTheSignedExpiry is `readSignedCookie` of packages/chord/src/node/http/cookies.ts (Pi main
// c23b9ec1): the value as ReadCookie reads it, with its signed expiry (milliseconds since the epoch); none for a cookie
// without MaxAge; nothing for a cookie ReadCookie refuses.
func TestReadSignedCookieGivesTheSignedExpiry(t *testing.T) {
	local := cookiesRequestWith("")
	maxAge := 3600.0
	with := Cookie("session", "a.b", CookieOptions{Secret: "s", Request: local, MaxAge: &maxAge})
	signed, ok := ReadSignedCookie(cookiesRequestWith(strings.SplitN(with, ";", 2)[0]), "session", "s")
	if !ok || signed.Value != "a.b" || signed.Expires == nil {
		t.Fatalf("signed = %+v, %v", signed, ok)
	}
	if remaining := *signed.Expires - float64(time.Now().UnixMilli()); remaining < 3_590_000 || remaining > 3_601_000 {
		t.Fatalf("expires in %v ms, want about an hour", remaining)
	}
	without := Cookie("session", "x", CookieOptions{Secret: "s", Request: local})
	signed, ok = ReadSignedCookie(cookiesRequestWith(strings.SplitN(without, ";", 2)[0]), "session", "s")
	if !ok || signed.Value != "x" || signed.Expires != nil {
		t.Fatalf("session cookie = %+v, %v", signed, ok)
	}
	if _, ok := ReadSignedCookie(cookiesRequestWith(strings.SplitN(with, ";", 2)[0]), "session", "other"); ok {
		t.Fatal("a cookie signed with another secret was read")
	}
}
