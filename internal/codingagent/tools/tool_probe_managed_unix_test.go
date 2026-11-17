//go:build unix

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// D269. A system fd or rg whose `--version` does not name it is not used, and the search goes on to the managed download: EnsureTool downloads the tool into <agentDir>/bin and returns that copy,
// where Pi's ensureTool (tools-manager.ts:337-341) finds the impostor with commandExists and downloads nothing. The release server is a fixture.
func TestImpostorOnPathFallsThroughToTheManagedDownload(t *testing.T) {
	for _, tc := range []struct {
		tool, repo, tag, asset string
		archive                func(t *testing.T) []byte
		impostors              []string
	}{
		{"fd", "sharkdp/fd", "v9.0.0", "fd-v9.0.0-aarch64-apple-darwin.tar.gz", func(t *testing.T) []byte {
			return buildTarGz(t, "fd-v9.0.0-aarch64-apple-darwin/fd", []byte("#!/bin/sh\necho 'fd 9.0.0'\n"))
		}, []string{"fd", "fdfind"}},
		{"rg", "BurntSushi/ripgrep", "14.0.0", "ripgrep-14.0.0-aarch64-apple-darwin.tar.gz", func(t *testing.T) []byte {
			return buildTarGz(t, "rg", []byte("#!/bin/sh\necho 'ripgrep 14.0.0'\n"))
		}, []string{"rg"}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			pathDir := t.TempDir()
			for _, name := range tc.impostors {
				writeFakeBin(t, pathDir, name, "echo 'FDclone (a file manager) 3.01'\n")
			}
			t.Setenv("PATH", pathDir)
			t.Setenv("PI_OFFLINE", "")
			srv := newFakeReleaseServer(t, tc.repo, tc.tag, tc.asset, tc.archive(t))
			tm := makeTestManager(t, srv, "darwin", "arm64")
			if got := tm.GetToolPath(tc.tool); got != "" {
				t.Fatalf("GetToolPath(%s) = %q, want none: the impostor on PATH is not the tool", tc.tool, got)
			}
			var statuses []string
			path := tm.EnsureTool(context.Background(), tc.tool, func(s ToolStatus) { statuses = append(statuses, s.Message) })
			want := filepath.Join(tm.BinDir(), tc.tool)
			if path != want {
				t.Fatalf("EnsureTool(%s) = %q, want the managed download %q (statuses %q)", tc.tool, path, want, statuses)
			}
			if srv.dlHits != 1 {
				t.Errorf("the release server served %d downloads, want 1", srv.dlHits)
			}
			if len(statuses) == 0 || !strings.Contains(statuses[0], "not found. Downloading...") {
				t.Errorf("statuses = %q, want the download announced", statuses)
			}
		})
	}
}

// D269. The managed copy in <agentDir>/bin is PiG's own download and is not probed: its `--version` is never run, whatever it prints, and a system impostor on PATH is not probed either once
// the managed copy exists.
func TestLookupToolPathNeverRunsTheManagedCopy(t *testing.T) {
	dir := t.TempDir()
	ran := filepath.Join(dir, "ran")
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"fd", "rg"} {
		managed := writeFakeBin(t, bin, tool, "echo managed-"+tool+" >> "+ran+"\necho 'not named at all'\n")
		path := t.TempDir()
		writeFakeBin(t, path, tool, "echo system-"+tool+" >> "+ran+"\necho 'not named at all'\n")
		t.Setenv("PATH", path)
		if got := LookupToolPath(tool, bin); got != managed {
			t.Fatalf("LookupToolPath(%s) = %q, want the managed copy %q", tool, got, managed)
		}
		if got := NewToolsManager(dir).GetToolPath(tool); got != managed {
			t.Fatalf("GetToolPath(%s) = %q, want the managed copy %q", tool, got, managed)
		}
	}
	if data, err := os.ReadFile(ran); err == nil {
		t.Errorf("a probe ran %q: neither the managed copy nor the system program is probed once the managed copy exists", data)
	}
}
