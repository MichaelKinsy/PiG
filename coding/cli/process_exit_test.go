package cli

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const processExitHelperEnv = "PIG_TEST_PROCESS_EXIT_HELPER"

// TestProcessExitHelper is the child entry point kept as fallback; TestMain handles processExitHelperEnv directly before package setup.
func TestProcessExitHelper(t *testing.T) {
	runProcessExitHelper(os.Getenv(processExitHelperEnv))
}

func runProcessExitHelper(mode string) {
	switch mode {
	case "exit":
		os.Exit(0)
	case "block":
		if _, err := os.Stdout.WriteString("ready\n"); err != nil {
			os.Exit(2)
		}
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
}

type processExitHelper struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdoutDone chan struct{}
	stderrDone chan struct{}
	stderr     bytes.Buffer
}

func startProcessExitHelper(t *testing.T, mode string) *processExitHelper {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessExitHelper$")
	cmd.Env = append(os.Environ(), processExitHelperEnv+"="+mode)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &processExitHelper{
		cmd:        cmd,
		stdin:      stdin,
		stdoutDone: make(chan struct{}),
		stderrDone: make(chan struct{}),
	}
	ready := make(chan string, 1)
	go func() {
		defer close(h.stdoutDone)
		reader := bufio.NewReader(stdout)
		if line, readErr := reader.ReadString('\n'); readErr == nil {
			ready <- line
		}
		_, _ = io.Copy(io.Discard, reader)
	}()
	go func() {
		defer close(h.stderrDone)
		_, _ = io.Copy(&h.stderr, stderrPipe)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-h.stdoutDone
		<-h.stderrDone
		_ = cmd.Wait()
	})
	if mode == "block" {
		select {
		case line := <-ready:
			if line != "ready\n" {
				t.Fatalf("process exit helper printed %q, want ready", line)
			}
		case <-h.stdoutDone:
			<-h.stderrDone
			waitErr := cmd.Wait()
			t.Fatalf("process exit helper exited before ready (wait=%v stderr=%q)", waitErr, h.stderr.String())
		case <-time.After(testbudget.Wait(t)):
			_ = cmd.Process.Kill()
			<-h.stdoutDone
			<-h.stderrDone
			waitErr := cmd.Wait()
			t.Fatalf("process exit helper did not signal ready (wait=%v stderr=%q)", waitErr, h.stderr.String())
		}
	}
	return h
}

func TestWaitProcessExitWaitsForARunningProcess(t *testing.T) {
	helper := startProcessExitHelper(t, "block")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := waitProcessExit(ctx, helper.cmd.Process.Pid); err == nil {
		select {
		case <-helper.stdoutDone:
			<-helper.stderrDone
			waitErr := helper.cmd.Wait()
			t.Fatalf("waitProcessExit returned because helper exited early (wait=%v stderr=%q)", waitErr, helper.stderr.String())
		default:
			t.Fatal("waitProcessExit returned for a process that is still running")
		}
	}
	if err := helper.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := waitProcessExit(testbudget.Context(t), helper.cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
}
