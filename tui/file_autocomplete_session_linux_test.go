//go:build linux

package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A search program that opens /dev/tty (FDclone) must find no controlling
// terminal: the autocomplete runs fd as a session leader.
func TestWalkDirectoryWithFdRunsInANewSession(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "mark")
	fd := filepath.Join(dir, "fdbin")
	script := "#!/bin/sh\nsid=$(cut -d' ' -f6 /proc/$$/stat)\n[ \"$sid\" = \"$$\" ] && echo leader > " + mark + " || echo shared > " + mark + "\n"
	if err := os.WriteFile(fd, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	p := NewCombinedProvider(nil, dir, fd)
	p.walkDirectoryWithFd(context.Background(), dir, "x", 0)
	b, _ := os.ReadFile(mark)
	if string(b) != "leader\n" {
		t.Fatalf("fd ran with %q, want a new session", b)
	}
}
