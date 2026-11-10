//go:build unix

package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi main ace560c30 (#11752, print-mode.ts): the signal handlers stay until the final disposal is done, a signal during it waits for that disposal, and a repeated signal exits at once with the code of the signal that repeated (SIGTERM 143, SIGHUP 129: each handler closes over its own exitCode). Pi 1.5.0 removed the handlers before the final disposeRuntime(), so a signal during it killed the process mid-dispose.
func TestPrintModeSignalDuringFinalDisposalWaitsForIt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping signal shutdown test in short mode")
	}
	bin := buildPigBinaryForSignalTest(t)
	fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-shutdown.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		signals []syscall.Signal
		want    int
	}{
		{"one signal", []syscall.Signal{syscall.SIGTERM}, 143},
		{"one SIGHUP", []syscall.Signal{syscall.SIGHUP}, 129},
		{"repeated signal", []syscall.Signal{syscall.SIGTERM, syscall.SIGTERM}, 143},
		{"repeated SIGHUP", []syscall.Signal{syscall.SIGHUP, syscall.SIGHUP}, 129},
		{"SIGTERM then SIGHUP exits with the second's code", []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP}, 129},
		{"SIGHUP then SIGTERM exits with the second's code", []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM}, 143},
	} {
		signals, want := tc.signals, tc.want
		t.Run(tc.name, func(t *testing.T) {
			report := filepath.Join(t.TempDir(), "report.txt")
			cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--no-session", "--no-extensions", "-e", fixture, "--print", "What is 20+22?")
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic",
				"RPC_SHUTDOWN_REPORT="+report, "RPC_SHUTDOWN_SLOW=2500", "RPC_SHUTDOWN_RECORD_DONE=1")
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()
			waited := make(chan error, 1)
			go func() { waited <- cmd.Wait() }()
			deadline := time.Now().Add(testbudget.Wait(t))
			for {
				data, _ := os.ReadFile(report)
				if slices.Contains(strings.Fields(string(data)), "session_shutdown") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the final disposal never started")
				}
				time.Sleep(20 * time.Millisecond)
			}
			began := time.Now()
			for i, sig := range signals {
				if i > 0 {
					// Signals that arrive before the first is taken off the queue coalesce into one.
					time.Sleep(100 * time.Millisecond)
				}
				if err := cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-waited:
				if len(signals) == 1 {
					t.Fatalf("process ended (%v) before the final disposal finished", err)
				}
				if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != want {
					t.Fatalf("wait = %v, want exit status %d", err, want)
				}
				if took := time.Since(began); took > 2*time.Second {
					t.Fatalf("exit took %s, want it before the 2.5 s handler ended", took)
				}
				if data, _ := os.ReadFile(report); slices.Contains(strings.Fields(string(data)), "session_shutdown:done") {
					t.Fatalf("report = %q, want the handler cut off", data)
				}
			case <-time.After(700 * time.Millisecond):
				if len(signals) == 2 {
					t.Fatalf("a repeated %v did not exit at once", signals[1])
				}
				err := <-waited
				if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != want {
					t.Fatalf("wait = %v, want exit status %d after the disposal", err, want)
				}
				if data, _ := os.ReadFile(report); !slices.Contains(strings.Fields(string(data)), "session_shutdown:done") {
					t.Fatalf("report = %q, want the handler to run to its end", data)
				}
			}
		})
	}
}

// A stdout write error cancels the run before the final disposal. The signal handlers must outlive that cancellation: a repeated SIGTERM during the final disposal still exits at once (print-mode.ts keeps its handlers until disposeRuntime() returns).
func TestPrintModeRepeatedSignalAfterStdoutFailureExitsAtOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping signal shutdown test in short mode")
	}
	bin := buildPigBinaryForSignalTest(t)
	fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-shutdown.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "report.txt")
	cmd := exec.Command(bin, "--mode", "json", "--model", "test-faux/faux-1", "--no-session", "--no-extensions", "-e", fixture, "--print", "What is 20+22?")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic",
		"RPC_SHUTDOWN_REPORT="+report, "RPC_SHUTDOWN_SLOW=2500", "RPC_SHUTDOWN_RECORD_DONE=1")
	// Every write to /dev/full fails with ENOSPC; a closed pipe would end the process with SIGPIPE instead.
	full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("no /dev/full: %v", err)
	}
	defer func() { _ = full.Close() }()
	cmd.Stdout = full
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		data, _ := os.ReadFile(report)
		if slices.Contains(strings.Fields(string(data)), "session_shutdown") {
			break
		}
		select {
		case err := <-waited:
			t.Fatalf("process ended (%v) before the final disposal started", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the final disposal never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	began := time.Now()
	for i := range 2 {
		if i > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-waited:
		if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 143 {
			t.Fatalf("wait = %v, want exit status 143", err)
		}
		if took := time.Since(began); took > 2*time.Second {
			t.Fatalf("exit took %s, want it before the 2.5 s handler ended", took)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("a repeated SIGTERM did not exit at once")
	}
}
