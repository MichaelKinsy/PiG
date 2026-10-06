package release

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

func TestFetchIndexVerifiesTheSignedIndexAndInstallsNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
	key := newKey(t)
	server, indexURL := releaseServer(t, key, releaseSpec{piglet: "porter", version: "1.2.3", target: testTarget, pigVersion: "pig-test", binary: []byte("binary")})
	defer server.Close()

	verified, err := FetchIndex(context.Background(), indexURL, Options{Version: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if verified.Index.Piglet != "porter" || verified.Index.Version != "1.2.3" || verified.Index.Signer.KeyID != signature.KeyID(key.Public().(ed25519.PublicKey)) {
		t.Fatalf("index = %+v", verified.Index)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("fetching an index wrote managed state: %v", entries)
	}
}

func TestFetchIndexRefusesWhatPullWouldRefuse(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
	key := newKey(t)
	for name, tc := range map[string]struct {
		spec    releaseSpec
		version string
		want    string
	}{
		"bad signature":    {releaseSpec{piglet: "porter", version: "1.2.3", target: testTarget, pigVersion: "p", binary: []byte("b"), badIndexSignature: true}, "1.2.3", "Piglet release index"},
		"version mismatch": {releaseSpec{piglet: "porter", version: "1.2.3", target: testTarget, pigVersion: "p", binary: []byte("b")}, "2.0.0", "version 1.2.3, requested 2.0.0"},
	} {
		t.Run(name, func(t *testing.T) {
			server, indexURL := releaseServer(t, key, tc.spec)
			defer server.Close()
			if _, err := FetchIndex(context.Background(), indexURL, Options{Version: tc.version}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFetchIndexReportsAMissingReleaseAndBadReferences(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
	if _, err := FetchIndex(context.Background(), server.URL+"/piglet-release.json", Options{}); err == nil || !strings.Contains(err.Error(), "fetch Piglet release index: 404 Not Found") {
		t.Fatalf("missing release: %v", err)
	}
	if _, err := FetchIndex(context.Background(), "ftp://example.test/x", Options{}); err == nil {
		t.Fatal("accepted a reference that is neither a GitHub ref nor an HTTPS URL")
	}
	if _, err := FetchIndex(context.Background(), "github:acme/porter", Options{}); err == nil {
		t.Fatal("accepted a GitHub reference without a version")
	}
}
