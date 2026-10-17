//go:build unix

package aotruntime

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A compiler or training host that leaves a child holding its output pipe must not keep a cancelled build, and with it session shutdown, waiting
// for that child: cancelling the build kills the whole process group.
func TestCancelledCompilerRunKillsItsChildren(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	compiler := filepath.Join(dir, "compiler")
	script := "#!/bin/sh\nsleep 60 &\necho $! > " + pidFile + "\nwait\n"
	if err := os.WriteFile(compiler, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		errs <- runCompiler(ctx, buildOptions{Compiler: compiler, Platform: "linux-x64", WorkDir: dir}, dir, filepath.Join(dir, "tsconfig.json"), "", "", filepath.Join(dir, "out"), nil, io.Discard)
	}()
	var child int
	deadline := time.Now().Add(10 * time.Second)
	for child == 0 && time.Now().Before(deadline) {
		if data, err := os.ReadFile(pidFile); err == nil && strings.HasSuffix(string(data), "\n") {
			child = atoi(strings.TrimSpace(string(data)))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if child == 0 {
		t.Fatal("the fake compiler did not start its child")
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	cancel()
	select {
	case <-errs:
	case <-time.After(10 * time.Second):
		t.Fatal("runCompiler still waits 10 s after its context ended: a child of the compiler holds its output")
	}
	deadline = time.Now().Add(5 * time.Second)
	for syscall.Kill(child, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if syscall.Kill(child, 0) == nil {
		t.Error("the compiler's child outlived the cancelled build")
	}
}

// A training run ends with the host's exit; a process an extension started while it loaded does not outlive the run.
func TestTrainingRunLeavesNoProcessBehind(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	host := filepath.Join(dir, "host")
	script := "#!/bin/sh\nsleep 60 </dev/null >/dev/null 2>&1 &\necho $! > " + pidFile + "\nexit 0\n"
	if err := os.WriteFile(host, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := trainRun(context.Background(), buildOptions{WorkDir: dir}, host, filepath.Join(dir, "order"), []string{"--version"}, 10*time.Second, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	child := atoi(strings.TrimSpace(string(data)))
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(child, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if syscall.Kill(child, 0) == nil {
		t.Error("a process the training host started outlived the run")
	}
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
