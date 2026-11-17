//go:build !pig_strip_node_extensions

package subprocess

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A real generation's socket file lives from its listener's bind to the listener's Close, which the Host runs once the generation connected or failed to start; a stop removes the file of its own generation when the generation never connected. This test asserts the directory's contents through a real isolated process and a real packed cell: no file outlives a load or a reload, an extension a reload removes leaves none, a process that exits before it connects leaves none, and Shutdown removes the directory.
func TestSocketFilesOfRealGenerationsEndWithTheirListeners(t *testing.T) {
	eachSubprocessMode(t, func(t *testing.T, packed bool) {
		var member ExtConfig
		if packed {
			root := writePackedFactoryModule(t, "example.com/sockets/member", "member", "tool_member")
			member = packedFactoryConfig("member", root, "example.com/sockets/member", "hm")
		} else {
			// The fixture registers itself as "fixture".
			member = ExtConfig{Name: "fixture", Path: buildFixtureExt(t), Enabled: true}
		}
		current := []ExtConfig{member}
		h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		h.SetConfigLoader(func() ([]ExtConfig, error) { return current, nil })
		t.Cleanup(func() { h.Shutdown("test done") })
		ctx, cancel := buildContext(t)
		defer cancel()

		reloadAndRequireNoSocketFile := func(step string, wantLoaded bool) {
			t.Helper()
			if _, err := h.Reload(ctx); err != nil {
				t.Fatalf("%s: reload: %v", step, err)
			}
			h.mu.Lock()
			_, loaded := h.exts[member.Name]
			h.mu.Unlock()
			if loaded != wantLoaded {
				t.Fatalf("%s: member loaded = %v, want %v; reload issues: %q", step, loaded, wantLoaded, h.LastReloadReport().Issues)
			}
			if got := socketFiles(t, h); len(got) != 0 {
				t.Fatalf("%s: socket directory holds %q", step, got)
			}
		}

		reloadAndRequireNoSocketFile("first load", true)
		for round := range 5 {
			reloadAndRequireNoSocketFile("reload "+string(rune('1'+round)), true)
		}

		// A reload that removes the extension.
		current = nil
		reloadAndRequireNoSocketFile("reload removing the extension", false)

		if !packed && runtime.GOOS != "windows" {
			// A process that exits before it connects: the start fails and its listener closes with it. A POSIX shell script cannot start on native Windows.
			dies := filepath.Join(t.TempDir(), "dies")
			if err := os.WriteFile(dies, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			current = []ExtConfig{{Name: "dies", Path: dies, Enabled: true}}
			if _, err := h.Reload(ctx); err != nil {
				t.Fatalf("reload with a process that exits at once: %v", err)
			}
			if got := socketFiles(t, h); len(got) != 0 {
				t.Fatalf("socket directory holds %q after a process exited before it connected", got)
			}
		}

		// The session ends with a generation loaded: Shutdown removes the directory.
		current = []ExtConfig{member}
		reloadAndRequireNoSocketFile("reload after the removal", true)
		dir := h.sockRuntimeDir
		h.Shutdown("session ended")
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("socket directory survived Shutdown: %v", err)
		}
	})
}

// A pig that is killed leaves its socket directory: nothing runs to remove it. The next pig, in the same socket directory, makes a runtime directory of its own (os.MkdirTemp), so the file the first one left at <count>.s is not at any path the second binds, though the second's count starts again at the same number; and it removes nothing but its own directory.
func TestAKilledHostsSocketFileIsNeitherUsedNorRemovedByTheNextHost(t *testing.T) {
	socketDir := t.TempDir()
	killed := NewHost(socketDir)
	left, err := killed.newSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(left); got != socketLeaf(1) {
		t.Fatalf("the killed Host's first socket file is %q, want %q", got, socketLeaf(1))
	}
	leftSocketFile(t, left)
	t.Cleanup(func() { _ = os.RemoveAll(killed.sockRuntimeDir) })

	next := NewHost(socketDir)
	// The assertions below can fail the test before the explicit Shutdown; Shutdown is safe to call twice.
	t.Cleanup(func() { next.Shutdown("test done") })
	ctx, cancel := buildContext(t)
	defer cancel()
	loaded, errs := next.LoadAll(ctx, []ExtConfig{{Name: "fixture", Path: buildFixtureExt(t), Enabled: true}})
	if len(errs) != 0 || len(loaded) != 1 {
		t.Fatalf("loaded=%v errors=%v", loaded, errs)
	}
	if next.sockRuntimeDir == killed.sockRuntimeDir || filepath.Dir(next.sockRuntimeDir) != filepath.Dir(killed.sockRuntimeDir) {
		t.Fatalf("the Hosts' socket directories are %q and %q, want two directories in one", killed.sockRuntimeDir, next.sockRuntimeDir)
	}
	if got := next.sockSeq.Load(); got != 1 {
		t.Fatalf("the next Host made %d socket paths for one extension, want 1", got)
	}
	next.Shutdown("session ended")
	if _, err := os.Stat(left); err != nil {
		t.Fatalf("the killed Host's socket file did not survive the next Host: %v", err)
	}
}
