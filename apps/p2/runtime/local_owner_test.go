package runtime_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// apps/p2/src/runtime/local-owner.ts is tested upstream through devices.test.ts ("local sign-in ...") and tui.test.ts; the
// client side against a stub server is here, the server side in e2e/devices_test.go.

func TestLocalOwnerProofIsTheHMACOfThePortAndTheChallengeUnderTheKey(t *testing.T) {
	// HMAC-SHA256(key="a"*64, "p2-local-owner:8788:abc"), computed independently.
	const want = "f781ea635b873bc5f49ad4fa1a2a141e19138513461d1321c8f17fa30407806b"
	if got := p2runtime.LocalOwnerProof(strings.Repeat("a", 64), 8788, "abc"); got != want {
		t.Fatalf("proof = %s, want %s", got, want)
	}
	if p2runtime.LocalOwnerProof(strings.Repeat("a", 64), 8789, "abc") == want {
		t.Fatal("the port is not part of the proof")
	}
}

func TestDataDirectoryIsP2DataDirOrTheHomeDefault(t *testing.T) {
	if dir, err := p2runtime.DataDirectory(func(name string) string {
		if name == "P2_DATA_DIR" {
			return "/data/p2"
		}
		return ""
	}); err != nil || dir != "/data/p2" {
		t.Fatalf("env = %q, %v", dir, err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if dir, err := p2runtime.DataDirectory(func(string) string { return "" }); err != nil || dir != filepath.Join(home, ".pi", "p2") {
		t.Fatalf("default = %q, %v", dir, err)
	}
}

// stubServer answers the two local sign-in routes; claim decides the second answer.
func stubServer(t *testing.T, challenge func(http.ResponseWriter), claim func(http.ResponseWriter, map[string]string)) *url.URL {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+p2runtime.LocalChallengePath, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "http://"+r.Host || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "bad request", http.StatusForbidden)
			return
		}
		challenge(w)
	})
	mux.HandleFunc("POST "+p2runtime.LocalClaimPath, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		claim(w, body)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func writeKey(t *testing.T, key string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "owner-key"), []byte(key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLocalOwnerSignInAnswersTheChallengeWithoutSendingTheKey(t *testing.T) {
	key := strings.Repeat("b", 64)
	dir := writeKey(t, key)
	var target *url.URL
	target = stubServer(t,
		func(w http.ResponseWriter) { _ = json.NewEncoder(w).Encode(map[string]string{"challenge": "c1"}) },
		func(w http.ResponseWriter, body map[string]string) {
			port := 0
			for _, c := range target.Port() {
				port = port*10 + int(c-'0')
			}
			if body["challenge"] != "c1" || body["proof"] != p2runtime.LocalOwnerProof(key, port, "c1") {
				http.Error(w, "no", http.StatusForbidden)
				return
			}
			w.Header().Add("Set-Cookie", "p2_owner_1=local.x; HttpOnly; Path=/")
			w.Header().Add("Set-Cookie", "second=2; Path=/")
		})
	got, err := p2runtime.LocalOwnerSignIn(context.Background(), nil, target, dir)
	if err != nil || got.Kind != p2runtime.LocalSignedIn || got.Cookie != "p2_owner_1=local.x; second=2" {
		t.Fatalf("sign-in = %+v, %v", got, err)
	}
}

func TestLocalOwnerSignInSaysWhyItIsUnavailable(t *testing.T) {
	ctx := context.Background()
	noChallenge := stubServer(t, func(w http.ResponseWriter) { http.NotFound(w, nil) }, func(http.ResponseWriter, map[string]string) {})
	refusing := stubServer(t,
		func(w http.ResponseWriter) { _ = json.NewEncoder(w).Encode(map[string]string{"challenge": "c"}) },
		func(w http.ResponseWriter, _ map[string]string) { http.Error(w, "no", http.StatusForbidden) })
	missing := t.TempDir()
	got, err := p2runtime.LocalOwnerSignIn(ctx, nil, refusing, missing)
	if err != nil || got.Kind != p2runtime.LocalUnavailable ||
		got.Message != "Cannot read "+filepath.Join(missing, "owner-key")+": local sign-in needs the user running the p2 server, with its P2_DATA_DIR" {
		t.Fatalf("no key: %+v, %v", got, err)
	}
	dir := writeKey(t, strings.Repeat("c", 64))
	got, err = p2runtime.LocalOwnerSignIn(ctx, nil, noChallenge, dir)
	if err != nil || got.Kind != p2runtime.LocalUnavailable || got.Message != "The server at "+noChallenge.Scheme+"://"+noChallenge.Host+" offers no local sign-in (404)" {
		t.Fatalf("no challenge: %+v, %v", got, err)
	}
	got, err = p2runtime.LocalOwnerSignIn(ctx, nil, refusing, dir)
	if err != nil || got.Kind != p2runtime.LocalUnavailable ||
		got.Message != "The p2 server at "+refusing.Scheme+"://"+refusing.Host+" does not use the data directory "+dir+": set P2_DATA_DIR to its data directory, or sign in with an owner link" {
		t.Fatalf("another data directory: %+v, %v", got, err)
	}
	if _, err := p2runtime.LocalOwnerSignIn(ctx, nil, &url.URL{Scheme: "http", Host: "127.0.0.1:1"}, dir); err == nil || !strings.Contains(err.Error(), "Cannot reach p2 at http://127.0.0.1:1") {
		t.Fatalf("unreachable: %v", err)
	}
}

func TestLocalOwnerSignInRefusesRedirectsAndACookielessAnswer(t *testing.T) {
	dir := writeKey(t, strings.Repeat("d", 64))
	cookieless := stubServer(t,
		func(w http.ResponseWriter) { _ = json.NewEncoder(w).Encode(map[string]string{"challenge": "c"}) },
		func(w http.ResponseWriter, _ map[string]string) {})
	if _, err := p2runtime.LocalOwnerSignIn(context.Background(), nil, cookieless, dir); err == nil || !strings.Contains(err.Error(), "Local sign-in at") {
		t.Fatalf("cookieless: %v", err)
	}
	redirecting := stubServer(t,
		func(w http.ResponseWriter) {
			w.Header().Set("Location", "http://127.0.0.1:1/")
			w.WriteHeader(http.StatusFound)
		},
		func(http.ResponseWriter, map[string]string) {})
	got, err := p2runtime.LocalOwnerSignIn(context.Background(), nil, redirecting, dir)
	if err != nil || got.Kind != p2runtime.LocalUnavailable || !strings.Contains(got.Message, "(302)") {
		t.Fatalf("redirect: %+v, %v", got, err)
	}
}
