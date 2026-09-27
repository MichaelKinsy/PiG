//go:build unix

package main

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestPrintModeSignalExitCodes pins print mode's termination contract.
//
// Upstream's print mode registers SIGTERM (plus SIGHUP off win32) and exits
// 128+signum after disposing its runtime, and leaves SIGINT to Node's default
// handler, which also exits 128+signum:
//
//	packages/coding-agent/src/modes/print-mode.ts
//	  process.exit(signal === "SIGHUP" ? 129 : 143)
//
// The exit code is the only way a caller: a CI timeout, a supervisor, a shell
// pipeline: can tell "something killed the run" from "the run failed". pig
// previously cancelled its root context and returned the resulting error, so
// every signal produced exit 1 and printed "error: context canceled".
func TestPrintModeSignalExitCodes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping signal exit-code test in short mode")
	}
	bin := buildPigBinaryForSignalTest(t)

	for _, tc := range []struct {
		name   string
		signal syscall.Signal
		want   int
	}{
		{"SIGTERM", syscall.SIGTERM, 143},
		{"SIGHUP", syscall.SIGHUP, 129},
		{"SIGINT", syscall.SIGINT, 130},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workDir := t.TempDir()
			cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--no-extensions", "--print", "Run: sleep for a while")
			cmd.Dir = workDir
			cmd.Env = append(os.Environ(),
				"PIG_HOME="+t.TempDir(),
				"PIG_TEST_FAUX=1",
				"PIG_TEST_FAUX_SCENARIO=parity-basic",
			)
			// Own process group so the signal reaches only this run.
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := cmd.Start(); err != nil {
				t.Fatalf("start pig: %v", err)
			}

			// Wait for the faux tool to actually be running, so the signal
			// lands mid-run rather than during startup.
			marker := filepath.Join(workDir, ai.TestFauxToolStartedMarker)
			deadline := time.Now().Add(testbudget.Wait(t))
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatal("faux tool never started; cannot signal a run that is not in flight")
				}
				time.Sleep(20 * time.Millisecond)
			}

			if err := cmd.Process.Signal(tc.signal); err != nil {
				t.Fatalf("signal %s: %v", tc.signal, err)
			}

			err := cmd.Wait()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("wait = %v, want an exit error carrying %d", err, tc.want)
			}
			if got := exitErr.ExitCode(); got != tc.want {
				t.Errorf("exit code = %d, want %d (upstream print-mode contract)", got, tc.want)
			}
		})
	}
}

// TestPrintModeSignalDuringPipedStdinRead pins termination while print mode
// waits for piped stdin. Upstream main.ts reads piped stdin to its end before
// print mode registers any signal handler, so a writer that never closes the
// pipe (a harness that leaves stdin open) keeps it waiting, and SIGTERM ends
// the process through the default action: status 143, nothing printed. pig
// read stdin with a blocking io.ReadAll after its own SIGTERM handler had
// replaced the default action, so the signal only cancelled a context nobody
// was waiting on and the process hung.
func TestPrintModeSignalDuringPipedStdinRead(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping signal exit-code test in short mode")
	}
	bin := buildPigBinaryForSignalTest(t)
	cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--no-extensions", "--print", "/probe")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_TEST_FAUX=1", "PIG_STARTUP_TRACE=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pig: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	// Signal once pig is waiting on stdin, which the startup trace marks.
	reading := make(chan struct{})
	stderrDone := make(chan struct{})
	var trace strings.Builder
	go func() {
		defer close(stderrDone)
		scanner := bufio.NewScanner(stderr)
		signalled := false
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "[startup] ") {
				trace.WriteString(line + "\n")
			}
			if !signalled && strings.Contains(line, "stdin-read-start") {
				signalled = true
				close(reading)
			}
		}
	}()
	select {
	case <-reading:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("pig never started reading stdin")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}

	// stderr reaches EOF when the process exits; Wait must follow the reads.
	select {
	case <-stderrDone:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("pig ignored SIGTERM while waiting for piped stdin")
	}
	err = cmd.Wait()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 143 {
		t.Fatalf("wait = %v, want exit status 143", err)
	}
	if got := trace.String(); got != "" {
		t.Errorf("stderr = %q, want nothing besides the startup trace", got)
	}
}
