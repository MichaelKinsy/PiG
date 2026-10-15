package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A pinned download is accepted only with exactly its pinned size and sha256, from the network or an offline copy,
// and a refused one leaves no file behind.
func TestFetchPinned(t *testing.T) {
	body := []byte("pinned archive bytes")
	sum := sha256.Sum256(body)
	pin := pinnedArchive{size: int64(len(body)), sha256: hex.EncodeToString(sum[:])}
	serve := func(b []byte, status int) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write(b)
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, "a.tar.gz")
	ok := pin
	ok.url = serve(body, http.StatusOK).URL
	if err := fetchPinned(context.Background(), http.DefaultClient, ok, "", dst); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dst); !bytes.Equal(got, body) {
		t.Errorf("downloaded %q", got)
	}
	local := filepath.Join(dir, "offline.tar.gz")
	if err := os.WriteFile(local, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fetchPinned(context.Background(), http.DefaultClient, pin, local, filepath.Join(dir, "b.tar.gz")); err != nil {
		t.Errorf("an offline copy: %v", err)
	}

	changed := append([]byte{}, body...)
	changed[0] ^= 1
	for name, c := range map[string]struct {
		body   []byte
		status int
		want   string
	}{
		"changed":   {changed, http.StatusOK, "sha256"},
		"short":     {body[:len(body)-1], http.StatusOK, "bytes, want the pinned"},
		"long":      {append(append([]byte{}, body...), 'x'), http.StatusOK, "bytes, want the pinned"},
		"not found": {nil, http.StatusNotFound, "404"},
	} {
		p := pin
		p.url = serve(c.body, c.status).URL
		bad := filepath.Join(dir, name+".tar.gz")
		if err := fetchPinned(context.Background(), http.DefaultClient, p, "", bad); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := os.Stat(bad); !os.IsNotExist(err) {
			t.Errorf("%s: a refused download leaves %s", name, bad)
		}
	}
}

// An endless body is refused after one byte over the pinned size, not read to the end.
func TestFetchPinnedStopsAtTheSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 64<<10)
		for r.Context().Err() == nil {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := pinnedArchive{url: srv.URL, size: 1 << 20, sha256: strings.Repeat("0", 64)}
	if err := fetchPinned(ctx, http.DefaultClient, p, "", filepath.Join(t.TempDir(), "a")); err == nil || !strings.Contains(err.Error(), "1048577 bytes, want the pinned 1048576") {
		t.Errorf("an endless body: %v", err)
	}
}

// Every architecture a bundle builds for has a pinned CPython.
func TestCPythonIsPinnedForEveryArch(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		a, ok := cpythonArchives[arch]
		if !ok || a.size <= 0 || len(a.sha256) != 64 || !strings.Contains(a.url, strings.ReplaceAll(cpythonRelease, "+", "%2B")) {
			t.Errorf("%s: %+v", arch, a)
		}
	}
	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "image", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	version, release, _ := strings.Cut(cpythonRelease, "+")
	for _, want := range []string{"CPYTHON_RELEASE=" + release, "CPYTHON_VERSION=" + version, "CPYTHON_SHA256_AMD64=" + cpythonArchives["amd64"].sha256, "CPYTHON_SHA256_ARM64=" + cpythonArchives["arm64"].sha256, cpythonPython} {
		if !strings.Contains(string(dockerfile), want) {
			t.Errorf("bench/k8s/image/Dockerfile does not carry the bundle's CPython: no %s", want)
		}
	}
}
