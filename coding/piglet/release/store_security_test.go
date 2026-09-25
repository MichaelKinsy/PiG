package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestPullProcess(t *testing.T) {
	if ref := os.Getenv("PIG_TEST_PULL_URL"); ref != "" {
		if _, err := Pull(t.Context(), ref, Options{Target: testTarget}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentProcessesRevalidateSignerBeforePublication(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
	keyA, keyB := newKey(t), newKey(t)
	binaryA := signedBinary(t, keyA, "porter", "1.0.0", testTarget, "pig-test")
	sum := sha256.Sum256(binaryA)
	indexA, err := Sign(Index{Piglet: "porter", Version: "1.0.0", PigVersion: "pig-test", SourceRef: "npm:porter@1.0.0", Binaries: map[string]Binary{testTarget: {URL: "binary", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(binaryA))}}}, keyA)
	if err != nil {
		t.Fatal(err)
	}
	arrived, resume := make(chan struct{}), make(chan struct{})
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
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPullProcess$")
	command.Env = append(os.Environ(), "PIG_TEST_PULL_URL="+serverA.URL+"/index")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Go(func() { done <- command.Wait() })
	defer func() { cancel(); workers.Wait() }()
	select {
	case <-arrived:
	case err := <-done:
		t.Fatalf("first process: %v %s", err, &output)
	}
	serverB, urlB := releaseServer(t, keyB, releaseSpec{piglet: "porter", version: "2.0.0", target: testTarget, pigVersion: "pig-test", binary: signedBinary(t, keyB, "porter", "2.0.0", testTarget, "pig-test")})
	defer serverB.Close()
	second := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPullProcess$")
	second.Env = append(os.Environ(), "PIG_TEST_PULL_URL="+urlB)
	data, secondErr := second.CombinedOutput()
	before := snapshotTree(t, os.Getenv("PIG_HOME"))
	close(resume)
	err = <-done
	if secondErr != nil {
		t.Fatalf("second process: %v %s", secondErr, data)
	}
	if err == nil || !strings.Contains(output.String(), "pinned to signer") {
		t.Errorf("unauthorized process committed: %v %s", err, &output)
	}
	if after := snapshotTree(t, os.Getenv("PIG_HOME")); !maps.Equal(before, after) {
		t.Error("losing process changed installed state")
	}
}

func TestPullRejectsManagedSymlinkAncestors(t *testing.T) {
	for _, relative := range []string{"artifacts", "artifacts/piglets", "artifacts/piglets/porter", "artifacts/piglets/porter/1.0.0", "artifacts/piglets/porter/1.0.0/testos/testarch", "receipts", "receipts/piglets", "receipts/piglets/porter", "receipts/piglets/porter/1.0.0/testos"} {
		t.Run(relative, func(t *testing.T) {
			home, outside := t.TempDir(), t.TempDir()
			t.Setenv("PIG_HOME", home)
			t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
			path := filepath.Join(home, filepath.FromSlash(relative))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			testenv.Symlink(t, outside, path)
			key := newKey(t)
			server, ref := releaseServer(t, key, releaseSpec{piglet: "porter", version: "1.0.0", target: testTarget, pigVersion: "pig-test", binary: signedBinary(t, key, "porter", "1.0.0", testTarget, "pig-test")})
			defer server.Close()
			if _, err := Pull(t.Context(), ref, Options{Target: testTarget}); err == nil {
				t.Error("accepted symlinked managed path")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("external writes: %v %v", entries, err)
			}
		})
	}
}

func TestInventoryRejectsManagedSymlinkAncestors(t *testing.T) {
	for _, relative := range []string{"artifacts", "artifacts/piglets", "artifacts/piglets/porter", "artifacts/piglets/porter/1.0.0/testos/testarch/pig-porter", "receipts", "receipts/piglets", "receipts/piglets/porter"} {
		t.Run(relative, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("PIG_HOME", home)
			t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
			key := newKey(t)
			server, ref := releaseServer(t, key, releaseSpec{piglet: "porter", version: "1.0.0", target: testTarget, pigVersion: "pig-test", binary: signedBinary(t, key, "porter", "1.0.0", testTarget, "pig-test")})
			defer server.Close()
			if _, err := Pull(t.Context(), ref, Options{Target: testTarget}); err != nil {
				t.Fatal(err)
			}
			path, moved := filepath.Join(home, filepath.FromSlash(relative)), filepath.Join(t.TempDir(), "moved")
			if err := os.Rename(path, moved); err != nil {
				t.Fatal(err)
			}
			testenv.Symlink(t, moved, path)
			if installed, errs := ListInstalled(); len(installed) != 0 || len(errs) == 0 {
				t.Fatalf("accepted symlink: installed=%v errors=%v", installed, errs)
			}
		})
	}
}

func TestEarendilAssetAndRedirectAliasesNeverReachTransport(t *testing.T) {
	for _, host := range []string{"pi.dev.", "API.PI.DEV.:443", "pi。dev", "ｐｉ.dev"} {
		for _, route := range []string{"asset", "redirect"} {
			t.Run(host+"/"+route, func(t *testing.T) {
				t.Setenv("PIG_HOME", t.TempDir())
				t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
				key := newKey(t)
				index, err := Sign(Index{Piglet: "porter", Version: "1.0.0", PigVersion: "pig-test", SourceRef: "npm:porter@1.0.0", Binaries: map[string]Binary{testTarget: {URL: "https://" + host + "/binary", SHA256: strings.Repeat("a", 64), Size: 1}}}, key)
				if err != nil {
					t.Fatal(err)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if route == "redirect" {
						http.Redirect(w, r, "https://"+host+"/index", http.StatusFound)
						return
					}
					_, _ = w.Write(index)
				}))
				defer server.Close()
				transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					if address != strings.TrimPrefix(server.URL, "http://") {
						t.Errorf("alias reached transport: %s", address)
						return nil, fmt.Errorf("external network refused")
					}
					return (&net.Dialer{}).DialContext(ctx, network, address)
				}}
				defer transport.CloseIdleConnections()
				if _, err := Pull(t.Context(), server.URL+"/index", Options{Client: &http.Client{Transport: transport}, Target: testTarget}); err == nil || !strings.Contains(err.Error(), "Earendil-operated") {
					t.Fatalf("error = %v", err)
				}
			})
		}
	}
}
