//go:build unix

package main

import (
	"errors"
	"os/exec"
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

// Pi 0.87.1 removes its signal handlers when shutdown() starts
// (rpc-mode.ts:733-735), so SIGTERM during a stdin-end dispose whose
// session_shutdown handler keeps the event loop alive takes the default
// action: the process is terminated by the signal.
func TestRPCSignalDuringInputEndShutdownDiesBySignal(t *testing.T) {
	p, _ := startRPCShutdownFixture(t, "RPC_SHUTDOWN_HOLD=1")
	awaitReady(p)
	p.closeInput()
	awaitRPCShutdownStarted(p)
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if status := waitForRPCTermination(t, p); !status.Signaled() || status.Signal() != syscall.SIGTERM {
		t.Fatalf("wait status = %v, want termination by SIGTERM", status)
	}
}

// A second SIGTERM during a signal-triggered dispose takes the default action
// too.
func TestRPCSecondSignalDiesBySignal(t *testing.T) {
	p, _ := startRPCShutdownFixture(t, "RPC_SHUTDOWN_HOLD=1")
	awaitReady(p)
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	awaitRPCShutdownStarted(p)
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if status := waitForRPCTermination(t, p); !status.Signaled() || status.Signal() != syscall.SIGTERM {
		t.Fatalf("wait status = %v, want termination by SIGTERM", status)
	}
}

// Pi 0.87.1 rpc-mode.ts:728-731: stdin end while a signal-triggered dispose
// is still running re-enters shutdown(), which exits 0 at once.
func TestRPCInputEndDuringSignalShutdownExitsZero(t *testing.T) {
	p, _ := startRPCShutdownFixture(t, "RPC_SHUTDOWN_HOLD=1")
	awaitReady(p)
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	awaitRPCShutdownStarted(p)
	p.closeInput()
	if status := waitForRPCTermination(t, p); !status.Exited() || status.ExitStatus() != 0 {
		t.Fatalf("wait status = %v, want exit 0", status)
	}
}
