//go:build unix

package subprocess

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBuilderBuildContextCancelsAndDrainsNodeRuntimePreflight(t *testing.T) {
	source := filepath.Join(t.TempDir(), "extension.ts")
	if err := os.WriteFile(source, []byte("export default function () {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(testExecutable, filepath.Join(binDir, "node")); err != nil {
		t.Fatal(err)
	}
	fifoPath := filepath.Join(t.TempDir(), "node-started")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(nodePreflightHelperFIFOEnv, fifoPath)
	t.Setenv("PATH", binDir)

	builder := NewBuilderWithConfigRoot(t.TempDir(), t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	buildDone := make(chan error, 1)
	go func() {
		_, err := builder.BuildContext(ctx, "cancelled-node", source)
		buildDone <- err
	}()

	fifo, err := os.Open(fifoPath)
	if err != nil {
		t.Fatal(err)
	}
	pidLine, err := bufio.NewReader(fifo).ReadString('\n')
	_ = fifo.Close()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(pidLine))
	if err != nil {
		t.Fatalf("helper PID %q: %v", pidLine, err)
	}
	helper, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = helper.Kill() })

	cancel()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case err := <-buildDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("BuildContext error = %v, want context.Canceled", err)
		}
	case <-timer.C:
		_ = helper.Kill()
		<-buildDone
		t.Fatal("BuildContext did not cancel the blocked node --version preflight")
	}
	if err := helper.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("node --version helper still exists after BuildContext returned")
	}

	goodBinDir := t.TempDir()
	goodNode := "#!/bin/sh\nprintf 'v22.13.0\\n'\n"
	if err := os.WriteFile(filepath.Join(goodBinDir, "node"), []byte(goodNode), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(nodePreflightHelperFIFOEnv, "")
	t.Setenv("PATH", goodBinDir)
	if _, err := builder.BuildContext(t.Context(), "cancelled-node", source); err != nil {
		t.Fatalf("build after cancellation did not acquire buildMu: %v", err)
	}
}
