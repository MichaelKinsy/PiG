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

// awaitRPCReport waits until the fixture's report lists event. The tests below leave stdout unread, so they cannot wait on a notification.
func awaitRPCReport(t *testing.T, report, event string) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for !slices.Contains(readRPCShutdownReport(t, report), event) {
		if time.Now().After(deadline) {
			t.Fatalf("the extension never reported %q", event)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A single signal during a started shutdown does nothing (Pi main ace560c30), and that includes the hold on the Session's agent events that the signal's termination prelude takes: a run that a session_shutdown handler starts meanwhile must not wait on a hold nobody releases, or the dispose waits for the handler forever. Pi's handler holds nothing.
func TestRPCSingleSignalDuringShutdownDoesNotBlockARunTheHandlerStarts(t *testing.T) {
	p, report := startRPCShutdownFixture(t, "RPC_SHUTDOWN_SLOW=800", "RPC_SHUTDOWN_SEND_AFTER=300", "RPC_SHUTDOWN_RECORD_DONE=1")
	awaitReady(p)
	p.closeInput()
	awaitRPCReport(t, report, "session_shutdown")
	signalRPC(t, p, syscall.SIGTERM)
	requireExited(t, waitForRPCTermination(t, p), 0)
	events := readRPCShutdownReport(t, report)
	if !slices.Contains(events, "send") || !slices.Contains(events, "session_shutdown:done") {
		t.Fatalf("extension events = %v, want the handler to send its message and run to its end", events)
	}
}

// Print mode: a single signal during the final disposal waits for it and exits 143, also when a session_shutdown handler starts a run meanwhile.
func TestPrintModeSignalDuringFinalDisposalDoesNotBlockARunTheHandlerStarts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping signal shutdown test in short mode")
	}
	bin := buildPigBinaryForSignalTest(t)
	fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-shutdown.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "report.txt")
	cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--no-session", "--no-extensions", "-e", fixture, "--print", "What is 20+22?")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic",
		"RPC_SHUTDOWN_REPORT="+report, "RPC_SHUTDOWN_SLOW=800", "RPC_SHUTDOWN_SEND_AFTER=300", "RPC_SHUTDOWN_RECORD_DONE=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	awaitRPCReport(t, report, "session_shutdown")
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waited:
		if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 143 {
			t.Fatalf("wait = %v, want exit status 143", err)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("the process did not end: the run the handler started waits on a hold the signal took; report = %v", readRPCShutdownReportFile(report))
	}
	if events := readRPCShutdownReportFile(report); !slices.Contains(events, "send") || !slices.Contains(events, "session_shutdown:done") {
		t.Fatalf("extension events = %v, want the handler to send its message and run to its end", events)
	}
}

func readRPCShutdownReportFile(report string) []string {
	data, _ := os.ReadFile(report)
	return strings.Fields(string(data))
}

// Pi calls process.exit at a repeated signal (rpc-mode.ts signalCount > 1), so a stdout nobody reads holds neither a repeated SIGHUP, which Pi's first signal would flush for, nor a repeated signal that arrives while another exit already waits for that stdout.
func TestRPCRepeatedSignalExitsWhileStdoutIsStuck(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
		// during is the report line the signals wait for: the handler running, or finished (the flush then waits on stdout).
		during string
		sig    syscall.Signal
		want   int
	}{
		{"SIGHUP during the dispose", []string{"RPC_SHUTDOWN_SLOW=1500", "RPC_SHUTDOWN_RECORD_DONE=1"}, "session_shutdown", syscall.SIGHUP, 129},
		{"SIGTERM during the stdout flush of the stdin-end exit", []string{"RPC_SHUTDOWN_RECORD_DONE=1"}, "session_shutdown:done", syscall.SIGTERM, 143},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, report := startRPCShutdownFixture(t, tc.env...)
			awaitReady(p)
			// Nothing reads p.records from here on: the scanner stops after the buffered records and the pipe fills.
			for range 1500 {
				p.sendJSON(map[string]any{"type": "get_state"})
			}
			p.closeInput()
			awaitRPCReport(t, report, tc.during)
			time.Sleep(300 * time.Millisecond)
			began := time.Now()
			signalRPC(t, p, tc.sig, tc.sig)
			requireExited(t, waitForRPCTermination(t, p), tc.want)
			if took := time.Since(began); took > 2*time.Second {
				t.Fatalf("exit took %s after the repeated signal, want it at once", took)
			}
		})
	}
}
