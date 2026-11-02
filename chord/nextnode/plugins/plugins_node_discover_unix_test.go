//go:build !windows

package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeHungNode is a `node` that never answers `--version` and leaves a child holding the output pipes when it is killed, as a
// shim that waits on a lock or the network does.
func writeHungNode(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "node")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n/bin/sleep 8\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// A candidate that does not answer is a problem of that candidate: the probe stops at its limit, also when the killed script's
// child holds the pipes (Cmd.WaitDelay), and the search goes on to the next candidate.
func TestDiscoverNodeStopsAProbeThatDoesNotAnswerAndTriesTheNextCandidate(t *testing.T) {
	isolateNodeSearch(t)
	hung := writeHungNode(t, t.TempDir())
	t.Setenv("PATH", filepath.Dir(hung))
	places := nodePlaces()
	want := writeFakeNode(t, places[len(places)-1].setup(t, t.TempDir()), "v22.19.0", "", 0)
	start := time.Now()
	got, err := discoverNodeProbing(background, nil, nodeProbe{timeout: 300 * time.Millisecond, killDelay: 200 * time.Millisecond})
	if err != nil || got != want {
		t.Fatalf("discoverNode = %q, %v; want %q", got, err, want)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the search took %v: the hung probe was not bounded", elapsed)
	}
}

func TestDiscoverNodeWithOnlyAHungCandidateSaysItDidNotAnswer(t *testing.T) {
	isolateNodeSearch(t)
	t.Setenv("PATH", filepath.Dir(writeHungNode(t, t.TempDir())))
	start := time.Now()
	_, err := discoverNodeProbing(background, nil, nodeProbe{timeout: 300 * time.Millisecond, killDelay: 200 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "node --version did not answer within 300ms") {
		t.Fatalf("error = %v, want that the version probe did not answer", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the search took %v: the hung probe was not bounded", elapsed)
	}
}

// NodeOptions.ReadyTimeout bounds finding Node: a start whose discovery has not finished by then fails with that, instead of
// holding the caller (a KeepWarm spare held its goroutine until Close).
func TestStartNodeBoundsFindingNodeByTheReadyTimeout(t *testing.T) {
	isolateNodeSearch(t)
	t.Setenv("PATH", filepath.Dir(writeHungNode(t, t.TempDir())))
	ready := 300 * time.Millisecond
	start := time.Now()
	host, err := startNode(NodeOptions{ReadyTimeout: &ready, CacheDir: t.TempDir()}, background)
	if err == nil {
		_ = host.Close(background)
		t.Fatal("startNode started with a Node that never answers")
	}
	if !strings.Contains(err.Error(), "finding a Node.js executable did not finish within 300ms") {
		t.Fatalf("error = %v, want that finding Node did not finish within the ready timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the start took %v", elapsed)
	}
}

// Volta's and mise's shims are not run: a shim downloads the Node a project pins. Their install directories are the candidates,
// and a shim in the old place is never started.
func TestDiscoverNodeNeverRunsVoltaOrMiseShimsAndFindsTheirInstalls(t *testing.T) {
	for _, manager := range []struct {
		name, variable string
		shims          []string
		install        []string
	}{
		{"volta", "VOLTA_HOME", []string{"bin"}, []string{"tools", "image", "node", "22.19.0", "bin"}},
		{"mise", "MISE_DATA_DIR", []string{"shims"}, []string{"installs", "node", "22.19.0", "bin"}},
	} {
		t.Run(manager.name, func(t *testing.T) {
			isolateNodeSearch(t)
			t.Setenv("PATH", t.TempDir())
			root := t.TempDir()
			t.Setenv(manager.variable, root)
			marker := filepath.Join(t.TempDir(), "shim-ran")
			shimDir := filepath.Join(append([]string{root}, manager.shims...)...)
			if err := os.MkdirAll(shimDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(shimDir, "node"), []byte("#!/bin/sh\ntouch "+shellQuote(marker)+"\necho v24.0.0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			want := writeFakeNode(t, filepath.Join(append([]string{root}, manager.install...)...), "v22.19.0", "", 0)
			if got, err := discoverFake(t); err != nil || got != want {
				t.Fatalf("discoverNode = %q, %v; want the install %q", got, err, want)
			}
			if _, err := os.Stat(marker); err == nil {
				t.Fatal("the shim was run")
			}
		})
	}
}
