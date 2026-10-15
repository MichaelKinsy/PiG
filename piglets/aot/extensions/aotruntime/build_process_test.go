//go:build unix

package aotruntime

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A training run executes extension factories, which may start processes of their own. When the run ends on its timeout, those processes end with it.
func TestTrainRunTimeoutEndsTheProcessesTheRunStarted(t *testing.T) {
	work := t.TempDir()
	pidFile := filepath.Join(work, "child.pid")
	host := filepath.Join(work, "host")
	script := "#!/bin/sh\nsleep 300 &\necho $! > " + pidFile + "\nwait\n"
	if err := os.WriteFile(host, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	opts := buildOptions{WorkDir: work}
	err := trainRun(context.Background(), opts, host, filepath.Join(work, "order"), []string{"--version"}, 500*time.Millisecond, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("trainRun = %v, want the timeout", err)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("process %d that the training run started outlived the run", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
