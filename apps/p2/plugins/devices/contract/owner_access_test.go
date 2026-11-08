package contract

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ports the unit cases of test/p2/devices.test.ts that need no server: owner invitations and signed cookies.

func requestWith(cookie string) *http.Request {
	request, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	request.Header.Set("Cookie", cookie)
	return request
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
	if !access.Authorized(requestWith(cookie), 8788) {
		t.Fatal("a fresh cookie is not authorized")
	}
	if access.Authorized(requestWith(cookie), 8789) {
		t.Fatal("cookies are scoped to the server port")
	}
	if access.Authorized(requestWith(cookie[:len(cookie)-1]+"!"), 8788) {
		t.Fatal("a tampered cookie is authorized")
	}
	if access.Authorized(requestWith("p2_owner_8788=bad"), 8788) {
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
	if !reopened.Authorized(requestWith(cookie), 8788) {
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
	if access.Authorized(requestWith("p2_owner_8788="+legacy), 8788) {
		t.Fatal("the legacy key is a cookie")
	}
	one := strings.Split(access.Cookie(8788, true), ";")[0]
	two := strings.Split(access.Cookie(8788, true), ";")[0]
	if one == two {
		t.Fatal("two devices share a cookie")
	}
	if !access.Authorized(requestWith(one), 8788) {
		t.Fatal("the first device is not authorized")
	}
	device := access.Device(requestWith(one), 8788)
	if device == nil {
		t.Fatal("no device")
	}
	if err := access.Logout(*device); err != nil {
		t.Fatal(err)
	}
	if access.Authorized(requestWith(one), 8788) {
		t.Fatal("a logged-out device is authorized")
	}
	if !access.Authorized(requestWith(two), 8788) {
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
	if access.Authorized(requestWith(one), 8788) {
		t.Fatal("individual revocation does not survive a restart")
	}
	if !access.Authorized(requestWith(two), 8788) {
		t.Fatal("the other device does not survive a restart")
	}
	if err := access.Rotate(); err != nil {
		t.Fatal(err)
	}
	if access.Authorized(requestWith(two), 8788) {
		t.Fatal("rotation keeps a cookie")
	}
	fresh := access.Cookie(8788, false)
	if !strings.Contains(fresh, "SameSite=Lax") || strings.Contains(fresh, "Secure") {
		t.Fatalf("cookie %q", fresh)
	}
	freshCookie := strings.Split(fresh, ";")[0]
	if !access.Authorized(requestWith(freshCookie), 8788) {
		t.Fatal("a cookie of the rotated key is not authorized")
	}
	now += 30 * 24 * 60 * 60_000
	if access.Authorized(requestWith(freshCookie), 8788) {
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
