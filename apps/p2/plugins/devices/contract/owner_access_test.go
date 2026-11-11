package contract

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// Ports the unit cases of test/p2/devices.test.ts that need no server: owner invitations and signed cookies.

func requestWith(cookie string) *http.Request {
	request, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	request.Header.Set("Cookie", cookie)
	return request
}

func signedIn(access *OwnerAccess, cookie string, port int) bool {
	return access.Session(requestWith(cookie), port) != nil
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func readTrimmed(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(content))
}

func TestOwnerInvitationsArePrivateOneUseBoundedAndExpireAfterTenMinutesCookiesSurviveRestart(t *testing.T) {
	directory := t.TempDir()
	now := int64(10_000)
	clock := func() int64 { return now }
	access, err := OpenOwnerAccess(directory, clock)
	if err != nil {
		t.Fatal(err)
	}
	startup := readTrimmed(t, filepath.Join(directory, "owner-token.txt"))
	if got := mode(t, filepath.Join(directory, "owner-key")); got != 0o600 {
		t.Fatalf("owner-key mode %o", got)
	}
	if got := mode(t, filepath.Join(directory, "owner-token.txt")); got != 0o600 {
		t.Fatalf("owner-token.txt mode %o", got)
	}
	if !access.Claim(startup) {
		t.Fatal("the startup token is not claimable")
	}
	if access.Claim(startup) {
		t.Fatal("a token works once")
	}
	invitation := access.IssueToken()
	if access.Claim("wrong") {
		t.Fatal("a wrong token is claimed")
	}
	if !access.Claim(" " + invitation + "\n") {
		t.Fatal("whitespace around a token is not ignored")
	}
	if access.Claim(invitation) {
		t.Fatal("a claimed token claims again")
	}
	expired := access.IssueToken()
	now += 600_000
	if access.Claim(expired) {
		t.Fatal("the ten-minute boundary is expired")
	}
	evicted := access.IssueToken()
	for count := 0; count < 32; count++ {
		access.IssueToken()
	}
	if access.Claim(evicted) {
		t.Fatal("outstanding invitations are bounded")
	}
	cookie := strings.Split(access.Cookie(8788, true), ";")[0]
	if !signedIn(access, cookie, 8788) {
		t.Fatal("a fresh cookie is not authorized")
	}
	if signedIn(access, cookie, 8789) {
		t.Fatal("cookies are scoped to the server port")
	}
	if signedIn(access, cookie[:len(cookie)-1]+"!", 8788) {
		t.Fatal("a tampered cookie is authorized")
	}
	if signedIn(access, "p2_owner_8788=bad", 8788) {
		t.Fatal("a malformed cookie is authorized")
	}
	unfinished := access.IssueToken()
	access.Close()
	if access.Claim(unfinished) {
		t.Fatal("a closed access keeps its invitations")
	}
	reopened, err := OpenOwnerAccess(directory, clock)
	if err != nil {
		t.Fatal(err)
	}
	if !signedIn(reopened, cookie, 8788) {
		t.Fatal("the installation key persists")
	}
	nextStartup := readTrimmed(t, filepath.Join(directory, "owner-token.txt"))
	if nextStartup == startup {
		t.Fatal("every startup has a fresh headless token")
	}
	if reopened.Claim(unfinished) {
		t.Fatal("in-memory invitations do not survive a restart")
	}
	if !reopened.Claim(nextStartup) {
		t.Fatal("the new startup token is not claimable")
	}
	reopened.Close()
	if err := os.WriteFile(filepath.Join(directory, "owner-key"), []byte("invalid-private-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenOwnerAccess(directory, nil); err == nil || !strings.Contains(err.Error(), "Invalid owner key") {
		t.Fatalf("opening with an invalid key: %v", err)
	}
}

func TestSignedCookiesExpireLogoutPersistsRotationInvalidatesCookiesAndLegacyKeyCookiesCannotForge(t *testing.T) {
	directory := t.TempDir()
	legacy := strings.Repeat("e", 64)
	if err := os.WriteFile(filepath.Join(directory, "owner-key"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	now := int64(1000)
	clock := func() int64 { return now }
	access, err := OpenOwnerAccess(directory, clock)
	if err != nil {
		t.Fatal(err)
	}
	if readTrimmed(t, filepath.Join(directory, "owner-key")) == legacy {
		t.Fatal("the old bearer key must not become a signing key")
	}
	if signedIn(access, "p2_owner_8788="+legacy, 8788) {
		t.Fatal("the legacy key is a cookie")
	}
	one := strings.Split(access.Cookie(8788, true), ";")[0]
	two := strings.Split(access.Cookie(8788, true), ";")[0]
	local := strings.Split(access.LocalCookie(8788, ""), ";")[0]
	if one == two {
		t.Fatal("two devices share a cookie")
	}
	if !signedIn(access, one, 8788) {
		t.Fatal("the first device is not authorized")
	}
	device := access.Session(requestWith(one), 8788)
	if device == nil {
		t.Fatal("no device")
	}
	if err := access.Logout(*device); err != nil {
		t.Fatal(err)
	}
	if signedIn(access, one, 8788) {
		t.Fatal("a logged-out device is authorized")
	}
	if !signedIn(access, two, 8788) {
		t.Fatal("another device was logged out")
	}
	if got := mode(t, filepath.Join(directory, "owner-revoked.json")); got != 0o600 {
		t.Fatalf("owner-revoked.json mode %o", got)
	}
	access.Close()
	access, err = OpenOwnerAccess(directory, clock)
	if err != nil {
		t.Fatal(err)
	}
	if signedIn(access, one, 8788) {
		t.Fatal("individual revocation does not survive a restart")
	}
	if !signedIn(access, two, 8788) {
		t.Fatal("the other device does not survive a restart")
	}
	if session := access.Session(requestWith(local), 8788); session == nil || session.Kind != SessionLocal {
		t.Fatalf("local session = %+v", session)
	}
	if err := access.Rotate(); err != nil {
		t.Fatal(err)
	}
	if signedIn(access, two, 8788) {
		t.Fatal("rotation keeps a cookie")
	}
	if signedIn(access, local, 8788) {
		t.Fatal("rotation ends local sessions too")
	}
	fresh := access.Cookie(8788, false)
	if !strings.Contains(fresh, "SameSite=Lax") || strings.Contains(fresh, "Secure") {
		t.Fatalf("cookie %q", fresh)
	}
	freshCookie := strings.Split(fresh, ";")[0]
	if !signedIn(access, freshCookie, 8788) {
		t.Fatal("a cookie of the rotated key is not authorized")
	}
	now += 30 * 24 * 60 * 60_000
	if signedIn(access, freshCookie, 8788) {
		t.Fatal("server-side cookie expiry is not enforced")
	}
	access.Close()
}

func TestAnInvalidRevocationFileFailsOpening(t *testing.T) {
	for _, content := range []string{"[]", "null", "{", `{"x":1}`, `{"` + strings.Repeat("a", 32) + `":"x"}`, `{"` + strings.Repeat("a", 32) + `":1.5}`} {
		directory := t.TempDir()
		access, err := OpenOwnerAccess(directory, nil)
		if err != nil {
			t.Fatal(err)
		}
		access.Close()
		if err := os.WriteFile(filepath.Join(directory, "owner-revoked.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenOwnerAccess(directory, nil); err == nil || !strings.Contains(err.Error(), "Cannot read owner revocations") {
			t.Fatalf("%s: %v", content, err)
		}
	}
}

// Ports devices.test.ts "local sign-in": challenges are one-time, bound to the port and the key, and expire after a minute.
func TestLocalChallengesAreOneTimePortBoundKeyBoundAndExpireAfterAMinute(t *testing.T) {
	directory := t.TempDir()
	now := int64(1000)
	access, err := OpenOwnerAccess(directory, func() int64 { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer access.Close()
	key := readTrimmed(t, filepath.Join(directory, "owner-key"))
	answer := func(challenge string, key string, port int) bool {
		return access.ProveLocal(challenge, p2runtime.LocalOwnerProof(key, port, challenge), port)
	}
	if !answer(access.IssueChallenge(), key, 8788) {
		t.Fatal("the key's answer for the port is refused")
	}
	used := access.IssueChallenge()
	if answer(used, strings.Repeat("f", 64), 8788) {
		t.Fatal("another key's answer is accepted")
	}
	if answer(used, key, 8788) {
		t.Fatal("a challenge works once, even after a wrong answer")
	}
	other := access.IssueChallenge()
	if access.ProveLocal(other, p2runtime.LocalOwnerProof(key, 8789, other), 8788) {
		t.Fatal("an answer for another port is accepted")
	}
	late := access.IssueChallenge()
	now += 60_000
	if answer(late, key, 8788) {
		t.Fatal("a challenge expires after a minute")
	}
	if access.ProveLocal("unknown", p2runtime.LocalOwnerProof(key, 8788, "unknown"), 8788) {
		t.Fatal("an unissued challenge is accepted")
	}
	malformed := access.IssueChallenge()
	if access.ProveLocal(malformed, "short", 8788) {
		t.Fatal("a malformed proof is accepted")
	}
	if !answer(malformed, key, 8788) {
		t.Fatal("a malformed proof must not use the challenge up")
	}
	// The key rotates: challenges of the old key are forgotten, and local sessions end.
	pending := access.IssueChallenge()
	local := strings.Split(access.LocalCookie(8788, ""), ";")[0]
	if session := access.Session(requestWith(local), 8788); session == nil || session.Kind != SessionLocal {
		t.Fatalf("local session = %+v", session)
	}
	if err := access.Rotate(); err != nil {
		t.Fatal(err)
	}
	if answer(pending, readTrimmed(t, filepath.Join(directory, "owner-key")), 8788) {
		t.Fatal("rotation keeps a challenge")
	}
	if access.Session(requestWith(local), 8788) != nil {
		t.Fatal("rotation keeps a local session")
	}
}

func TestLocalSessionCookiesAreRenewedWithTheirIDAndNeverSecure(t *testing.T) {
	now := int64(5000)
	access, err := OpenOwnerAccess(t.TempDir(), func() int64 { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer access.Close()
	first := access.LocalCookie(8788, "")
	if !strings.HasPrefix(first, "p2_owner_8788=local.") || strings.Contains(first, "Secure") || !strings.Contains(first, "; HttpOnly; SameSite=Lax; Path=/; Max-Age=2592000") {
		t.Fatalf("cookie %q", first)
	}
	session := access.Session(requestWith(strings.Split(first, ";")[0]), 8788)
	if session == nil || session.Kind != SessionLocal {
		t.Fatalf("session = %+v", session)
	}
	now += 1000
	renewed := access.LocalCookie(8788, session.ID)
	again := access.Session(requestWith(strings.Split(renewed, ";")[0]), 8788)
	if again == nil || again.ID != session.ID || again.Expires != session.Expires+1000 {
		t.Fatalf("renewed = %+v (was %+v)", again, session)
	}
	// A device cookie's payload cannot be passed off as a local one, nor the reverse (the prefix is signed).
	device := strings.Split(access.Cookie(8788, false), ";")[0]
	forged := strings.Replace(device, "p2_owner_8788=", "p2_owner_8788=local.", 1)
	if access.Session(requestWith(forged), 8788) != nil {
		t.Fatal("a device cookie with a local prefix is a session")
	}
	if one := access.Session(requestWith(device), 8788); one == nil || one.Kind != SessionDevice {
		t.Fatalf("device session = %+v", one)
	}
}
