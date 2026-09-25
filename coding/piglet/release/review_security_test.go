package release

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The first successful commit must pin the signer even when another download started with no current pointer.
func TestReviewConcurrentPullCannotReplaceNewSignerPin(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
	keyA, keyB := newKey(t), newKey(t)
	binaryA := signedBinary(t, keyA, "porter", "1.0.0", testTarget, "pig-test")
	binaryB := signedBinary(t, keyB, "porter", "2.0.0", testTarget, "pig-test")
	arrived, resume := make(chan struct{}), make(chan struct{})
	sum := sha256.Sum256(binaryA)
	indexA, err := Sign(Index{
		Piglet: "porter", Version: "1.0.0", PigVersion: "pig-test", SourceRef: "npm:porter@1.0.0",
		Binaries: map[string]Binary{testTarget: {URL: "binary", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(binaryA))}},
	}, keyA)
	if err != nil {
		t.Fatal(err)
	}
	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index" {
			_, _ = w.Write(indexA)
			return
		}
		close(arrived)
		select {
		case <-resume:
			_, _ = w.Write(binaryA)
		case <-r.Context().Done():
		}
	}))
	defer serverA.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Go(func() {
		_, err := Pull(ctx, serverA.URL+"/index", Options{Target: testTarget})
		done <- err
	})
	defer func() { cancel(); workers.Wait() }()
	select {
	case <-arrived:
	case err := <-done:
		t.Fatalf("first pull did not reach its asset request: %v", err)
	}
	serverB, urlB := releaseServer(t, keyB, releaseSpec{
		piglet: "porter", version: "2.0.0", target: testTarget, pigVersion: "pig-test", binary: binaryB,
	})
	defer serverB.Close()
	if _, err := Pull(t.Context(), urlB, Options{Target: testTarget}); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, os.Getenv("PIG_HOME"))
	close(resume)
	err = <-done
	if err == nil || !strings.Contains(err.Error(), "pinned to signer") {
		current, _ := readCurrent("porter")
		want := signature.KeyID(keyB.Public().(ed25519.PublicKey))
		t.Errorf("second commit changed an established signer pin without --accept-signer: err=%v current=%s want=%s", err, current.SignerKeyID, want)
	}
	if after := snapshotTree(t, os.Getenv("PIG_HOME")); !maps.Equal(after, before) {
		t.Error("refused concurrent signer rotation must not publish files or change current")
	}
}

func TestReviewPullRejectsSymlinkedArtifactParent(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
	parent := filepath.Join(home, "artifacts", "piglets")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, outside, filepath.Join(parent, "porter"))
	key := newKey(t)
	server, url := releaseServer(t, key, releaseSpec{
		piglet: "porter", version: "1.0.0", target: testTarget, pigVersion: "pig-test",
		binary: signedBinary(t, key, "porter", "1.0.0", testTarget, "pig-test"),
	})
	defer server.Close()
	result, err := Pull(t.Context(), url, Options{Target: testTarget})
	if err == nil {
		t.Errorf("pull followed a symlink outside its managed artifact root: %#v", result)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("pull wrote outside managed artifact root: %v", entries)
	}
}

// The transport records its canonical destination instead of performing DNS or opening a network connection.
func TestReviewPullRejectsEquivalentEarendilHostsBeforeDial(t *testing.T) {
	for _, host := range []string{"pi.dev.", "API.PI.DEV.:443", "pi。dev", "ｐｉ.dev"} {
		t.Run(host, func(t *testing.T) {
			t.Setenv("PIG_HOME", t.TempDir())
			var mu sync.Mutex
			var dialed string
			transport := &http.Transport{DialContext: func(_ context.Context, _, address string) (net.Conn, error) {
				mu.Lock()
				dialed = address
				mu.Unlock()
				return nil, fmt.Errorf("probe prevents all network: %s", address)
			}}
			defer transport.CloseIdleConnections()
			_, err := Pull(t.Context(), "https://"+host+"/piglet-release.json", Options{Client: &http.Client{Transport: transport}})
			mu.Lock()
			defer mu.Unlock()
			if err == nil || !strings.Contains(err.Error(), "Earendil-operated") || dialed != "" {
				t.Fatalf("Earendil alias reached transport: dialed=%q error=%v", dialed, err)
			}
		})
	}
}
