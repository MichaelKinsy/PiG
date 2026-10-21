package cli

import (
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestProcessExitHelper is the child of the waitProcessExit tests: "exit" ends at once, "block" runs until its stdin closes.
func TestProcessExitHelper(t *testing.T) {
	switch os.Getenv("PIG_TEST_PROCESS_EXIT_HELPER") {
	case "exit":
		os.Exit(0)
	case "block":
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
}

func startProcessExitHelper(t *testing.T, mode string) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessExitHelper$")
	cmd.Env = append(os.Environ(), "PIG_TEST_PROCESS_EXIT_HELPER="+mode)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd, stdin
}

func TestWaitProcessExitWaitsForARunningProcess(t *testing.T) {
	cmd, stdin := startProcessExitHelper(t, "block")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := waitProcessExit(ctx, cmd.Process.Pid); err == nil {
		t.Fatal("waitProcessExit returned for a process that is still running")
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := waitProcessExit(testbudget.Context(t), cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
}
