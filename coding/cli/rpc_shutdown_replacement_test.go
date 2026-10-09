package cli

import (
	"runtime"
	"slices"
	"testing"
	"time"
)

// endInputAfterReplacement writes new_session and a prompt for an extension command in one write, then ends stdin. Pi reads both lines before the replacement runs, and exits before new_session answers; the command and the quit shutdown run on the replaced Session. Pig's queued replacement builds a Session after stdin ended.
func endInputAfterReplacement(p *rpcProcess, command string) []rpcRecord {
	p.t.Helper()
	p.send(`{"id":"n","type":"new_session"}` + "\n" + `{"id":"c","type":"prompt","message":"/` + command + `"}`)
	p.closeInput()
	return drainRPCOutput(p)
}

// Pi 0.87.1: a command that finishes only after the process is decided to exit answers nothing, also when a new_session is queued before it. Pig runs the command on the Session that the replacement built, so its stdin end must reach that Session's extension host, or the response hold is lost.
func TestRPCInputEndAfterQueuedReplacementHoldsCommandResponse(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			// As for the immediate2 rows of TestRPCInputEndAfterExtensionCommandComparedWithPi (piRacy): on Windows libuv reads pipe stdin on a reader thread, so whether Pi's two nested setImmediate ticks beat the end of input varies, and only pig's side is asserted there.
			if impl.name == "pi" && runtime.GOOS == "windows" {
				t.Skip("Pi's answer to a nested setImmediate at the end of input is not fixed on Windows (piRacy)")
			}
			p, report := impl.start(t)
			afterEOF := endInputAfterReplacement(p, "immediate2")
			p.waitForExit("after the replacement")
			for _, r := range afterEOF {
				if r["id"] == "c" {
					t.Fatalf("stdout after stdin ended = %v, want no response to the command", afterEOF)
				}
			}
			events := readRPCShutdownReport(t, report)
			// Pi records nothing for a command that runs after its exit. Pig's runtime is a separate process that its host stops after the exit decision, so the continuation may record its event first.
			if impl.name == "pig" {
				events = slices.DeleteFunc(events, func(event string) bool { return event == "immediate2" })
			}
			if !slices.Equal(events, []string{"session_shutdown", "session_shutdown"}) {
				t.Fatalf("extension events = %v, want two session_shutdown", events)
			}
		})
	}
}

// Pi 0.87.1: a quit session_shutdown handler that never settles keeps nothing alive, so the process exits 0 when stdin ends, also when a new_session is queued before it. Pi delivers the quit shutdown to the replaced Session's stale instance; pig delivers it to the Session that the replacement built, and its stdin end must reach that host, or the shutdown waits forever.
func TestRPCInputEndAfterQueuedReplacementWithPendingShutdownExitsZero(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, report := impl.start(t, "RPC_SHUTDOWN_BLOCK_QUIT=1")
			endInputAfterReplacement(p, "micro")
			p.waitForExit("with a quit session_shutdown handler that never settles")
			events := readRPCShutdownReport(t, report)
			if !slices.Equal(events, []string{"session_shutdown", "micro", "session_shutdown"}) {
				t.Fatalf("extension events = %v, want a shutdown for the replaced Session, micro and a quit shutdown", events)
			}
		})
	}
}

// Pi 0.87.1: a command that replaces the Session and then waits on a timer never answers when stdin ends after the replacement, and the process exits 0 (3 of 3). Each Session's host keeps its own suspended count, so the replacement host's report does not hold the flush for the replaced host's suspended command.
func TestRPCInputEndAfterCommandReplacementAndTimerExitsWithoutResponse(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, report := impl.start(t)
			p.sendJSON(map[string]any{"id": "c", "type": "prompt", "message": "/nstimer"})
			deadline := time.Now().Add(p.budget)
			for !slices.Contains(readRPCShutdownReport(t, report), "ns-done") {
				if time.Now().After(deadline) {
					t.Fatalf("the command did not replace the Session\n%s", p.stderr.String())
				}
				time.Sleep(20 * time.Millisecond)
			}
			p.closeInput()
			afterEOF := drainRPCOutput(p)
			p.waitForExit("after the replacement with a timer pending")
			for _, r := range afterEOF {
				if r["id"] == "c" {
					t.Fatalf("stdout after stdin ended = %v, want no response to the command", afterEOF)
				}
			}
		})
	}
}
