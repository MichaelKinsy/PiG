//go:build linux

package tui

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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

// Cancelling the search ends the whole group, so no fd child outlives it.
func TestWalkDirectoryWithFdCancelKillsTheGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	fd := filepath.Join(dir, "fdbin")
	script := "#!/bin/sh\nsleep 60 &\necho $! > " + pidFile + "\nwait\n"
	if err := os.WriteFile(fd, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	p := NewCombinedProvider(nil, dir, fd)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.walkDirectoryWithFd(ctx, dir, "x", 0)
		close(done)
	}()
	var pid int
	for range 100 {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				pid = n
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if pid == 0 {
		cancel()
		t.Fatal("fd stub never started its child")
	}
	cancel()
	// Without the group kill the stub's sleep keeps the output pipe open and
	// the search returns only when it exits.
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("the search did not return after cancel")
	}
	for range 50 {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("child %d of the cancelled fd outlived it", pid)
}
