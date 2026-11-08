//go:build unix

package cli

import (
	"errors"
	"os/exec"
	"slices"
	"syscall"
	"testing"
	"time"
)

// awaitRPCShutdownStarted waits for the notification the fixture's
// session_shutdown handler sends before it waits.
func awaitRPCShutdownStarted(p *rpcProcess) {
	p.t.Helper()
	p.await("session_shutdown notification", func(r rpcRecord) bool {
		return r["type"] == "extension_ui_request" && r["method"] == "notify" && r["message"] == "session_shutdown started"
	})
}

// waitForRPCTermination waits for the process and returns its wait status.
func waitForRPCTermination(t *testing.T, p *rpcProcess) syscall.WaitStatus {
	t.Helper()
	waited := make(chan error, 1)
	go func() { waited <- p.cmd.Wait() }()
	select {
	case err := <-waited:
		p.exited = true
		if err == nil {
			return p.cmd.ProcessState.Sys().(syscall.WaitStatus)
		}
		exit, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			t.Fatal(err)
		}
		return exit.Sys().(syscall.WaitStatus)
	case <-time.After(p.budget):
		_ = p.cmd.Process.Kill()
		<-waited
		p.exited = true
		t.Fatalf("RPC process did not end within %s\n%s", p.budget, p.stderr.String())
		return 0
	}
}

// A pending Promise alone does not keep Node alive, but stdin still does until the dispose resolves (rpc-mode.ts:738-740). Signals must not pretend that input ended.
func TestRPCSignalPendingPromiseKeepsReadingInput(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, _ := impl.start(t, "RPC_SHUTDOWN_BLOCK=1")
			awaitReady(p)
			if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			awaitRPCShutdownStarted(p)
			awaitReady(p)
			p.closeInput()
			if status := waitForRPCTermination(t, p); !status.Exited() || status.ExitStatus() != 0 {
				t.Fatalf("wait status = %v, want exit 0", status)
			}
		})
	}
}

// A termination signal during a tool call runs the same dispose as stdin end: Pi awaits session.abort() before
// session_shutdown (agent-session-runtime.ts:409-417), so the aborted run's turn boundaries, agent_end and agent_settled
// reach the extension first. The signal's handler kills the tracked children (rpc-mode.ts:383-391) and, for SIGTERM, exits
// 143 without flushing stdout, so only the events are compared, and no Session event reaches stdout: its forwarder was
// removed when shutdown started.
func TestRPCSignalMidPromptDeliversPiShutdownSequence(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, report := impl.start(t)
			startInFlightPrompt(p)
			if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			afterSignal := drainRPCOutput(p)
			requireExited(t, waitForRPCTermination(t, p), 143)
			for _, r := range afterSignal {
				if r["type"] != "extension_ui_request" {
					t.Fatalf("stdout after the signal has %v; only the shutdown handler's notification may follow", r)
				}
			}
			if events := readRPCShutdownReport(t, report); !slices.Equal(events, rpcMidPromptInputEndExtensionEvents) {
				t.Fatalf("extension events = %v, want %v", events, rpcMidPromptInputEndExtensionEvents)
			}
		})
	}
}
