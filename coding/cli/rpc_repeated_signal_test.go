//go:build unix

package cli

import (
	"slices"
	"syscall"
	"testing"
	"time"
)

// Pi main ace560c30 (#11752): once a shutdown has started, from stdin end or from a signal, a stdin end or a single signal does not interrupt it, and a second signal exits at once. Pi 1.5.0 removed its signal handlers when shutdown() started, so the next signal killed the process mid-dispose, and a second shutdown() call exited at once. Paseo closes stdin and sends SIGTERM together; systemd sends SIGTERM and stdin closes after.
//
// The fixture's session_shutdown handler waits a second and then records that it ran to its end.
const rpcSlowShutdown = "RPC_SHUTDOWN_SLOW=1500"

func startSlowShutdownFixture(t *testing.T) (*rpcProcess, string) {
	t.Helper()
	p, report := startRPCShutdownFixture(t, rpcSlowShutdown, "RPC_SHUTDOWN_RECORD_DONE=1")
	awaitReady(p)
	return p, report
}

func signalRPC(t *testing.T, p *rpcProcess, signals ...syscall.Signal) {
	t.Helper()
	for i, sig := range signals {
		if i > 0 {
			// Signals that arrive before the first is taken off the queue coalesce into one.
			time.Sleep(100 * time.Millisecond)
		}
		if err := p.cmd.Process.Signal(sig); err != nil {
			t.Fatal(err)
		}
	}
}

func requireExited(t *testing.T, status syscall.WaitStatus, code int) {
	t.Helper()
	if !status.Exited() || status.ExitStatus() != code {
		t.Fatalf("wait status = %v, want exit %d", status, code)
	}
}

func TestRPCSingleSignalDuringInputEndShutdownLetsItFinish(t *testing.T) {
	p, report := startSlowShutdownFixture(t)
	p.closeInput()
	awaitRPCShutdownStarted(p)
	signalRPC(t, p, syscall.SIGTERM)
	requireExited(t, waitForRPCTermination(t, p), 0)
	if events := readRPCShutdownReport(t, report); !slices.Equal(events, []string{"session_shutdown", "session_shutdown:done"}) {
		t.Fatalf("extension events = %v, want the handler to run to its end", events)
	}
}

func TestRPCInputEndDuringSignalShutdownLetsItFinish(t *testing.T) {
	p, report := startSlowShutdownFixture(t)
	signalRPC(t, p, syscall.SIGTERM)
	awaitRPCShutdownStarted(p)
	p.closeInput()
	requireExited(t, waitForRPCTermination(t, p), 143)
	if events := readRPCShutdownReport(t, report); !slices.Equal(events, []string{"session_shutdown", "session_shutdown:done"}) {
		t.Fatalf("extension events = %v, want the handler to run to its end", events)
	}
}

func TestRPCRepeatedSignalDuringShutdownExitsAtOnce(t *testing.T) {
	for name, start := range map[string]func(*rpcProcess){
		"after input end": func(p *rpcProcess) {
			p.closeInput()
			awaitRPCShutdownStarted(p)
			signalRPC(t, p, syscall.SIGTERM, syscall.SIGTERM)
		},
		"after the first signal": func(p *rpcProcess) {
			signalRPC(t, p, syscall.SIGTERM)
			awaitRPCShutdownStarted(p)
			signalRPC(t, p, syscall.SIGTERM)
		},
	} {
		t.Run(name, func(t *testing.T) {
			p, report := startSlowShutdownFixture(t)
			began := time.Now()
			start(p)
			requireExited(t, waitForRPCTermination(t, p), 143)
			if took := time.Since(began); took > 1200*time.Millisecond {
				t.Fatalf("exit took %s, want it before the 1.5 s handler ended", took)
			}
			if events := readRPCShutdownReport(t, report); slices.Contains(events, "session_shutdown:done") {
				t.Fatalf("extension events = %v, want the handler cut off", events)
			}
		})
	}
}
