package tools

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
)

// tools-manager.ts ensureTool (:337) awaits downloadTool and only then reports "<tool> installed to <path>", on the caller's own status callback, before the
// promise resolves with that path. EnsureTool is the awaited path: both statuses arrive in order on the calling goroutine, the second one after the binary is in
// place, and the returned path is the one the status names.
func TestEnsureToolReportsInstalledToAfterTheDownloadSettles(t *testing.T) {
	asset := buildTarGz(t, "fd-v9.0.0-aarch64-apple-darwin/fd", []byte("#!/bin/sh\n"))
	srv := newFakeReleaseServer(t, "sharkdp/fd", "v9.0.0", "fd-v9.0.0-aarch64-apple-darwin.tar.gz", asset)
	tm := makeTestManager(t, srv, "darwin", "arm64")
	t.Setenv("PATH", t.TempDir())

	var statuses []ToolStatus
	installedOnDisk := false
	got := tm.EnsureTool(context.Background(), "fd", func(s ToolStatus) {
		statuses = append(statuses, s)
		if path, ok := strings.CutPrefix(s.Message, "fd installed to "); ok {
			_, err := os.Stat(path)
			installedOnDisk = err == nil
		}
	})
	if got == "" {
		t.Fatal("EnsureTool returned no path")
	}
	want := []ToolStatus{
		{Type: "info", Message: "fd not found. Downloading..."},
		{Type: "info", Message: "fd installed to " + got},
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %+v, want %+v", statuses, want)
	}
	if !installedOnDisk {
		t.Fatal("the installed status arrived before the binary was in place")
	}
}
