package cli

import (
	"syscall"
	"testing"
)

// The tests of this file prove the --listen server's concurrency contracts (D232) with the Node fixture's node_slow tool, a tool call that runs until the host cancels it, so a session sits in a running turn for as long as a test needs.
// Ordering: the turns of different connections overlap, and each connection's records keep their own order. Cancellation: closing a connection during a turn is that session's input end, so the turn is cancelled and only that session's Host and extension processes end. Draining: a termination signal during a turn cancels it and every session's shutdown has run when the server exits.

// startSlowTurn starts a turn whose tool call does not end until it is cancelled, and returns once the call has begun.
func startSlowTurn(p *rpcProcess) {
	p.t.Helper()
	p.sendJSON(map[string]any{"id": "slow", "type": "prompt", "message": "Run: ext tool node_slow"})
	p.await("node_slow started", func(r rpcRecord) bool {
		return r["type"] == "tool_execution_start" && r["toolName"] == "node_slow"
	})
}

// A connection that closes during a turn ends only its own session in the default mode: the turn of the other session runs while it is still running, the closed session's extension processes end, the other session's processes and counters stay, and the server still exits on a signal (D232).
func TestListenCloseDuringATurnByDefaultEndsOnlyThatSessionsProcesses(t *testing.T) {
	s, log := listenExtensionServer(t)
	a, b := s.dial(), s.dial()
	for _, conn := range []*rpcProcess{a, b} {
		if got := callTool(conn, "node_counter"); got != "1" {
			t.Fatalf("node_counter = %q, want 1\n%s", got, s.stderr())
		}
	}
	requireCellCounts(t, s, 2, 2, "two sessions with their own extension processes")
	startSlowTurn(a)
	if got := callTool(b, "node_counter"); got != "2" {
		t.Fatalf("the other session's turn during a's running turn: node_counter = %q, want 2\n%s", got, s.stderr())
	}
	a.closeInput()
	a.waitForEOF()
	requireShutdownLines(t, s, log, 2)
	requireCellCounts(t, s, 1, 1, "after the connection closed during its turn")
	if got := callTool(b, "node_counter"); got != "3" {
		t.Fatalf("the other session's node_counter = %q, want 3 from its own process\n%s", got, s.stderr())
	}
	b.closeInput()
	b.waitForEOF()
	requireShutdownLines(t, s, log, 4)
	requireCellCounts(t, s, 0, 0, "after the last connection closed")
	s.signal(syscall.SIGTERM)
	if code := s.waitForExit(); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGTERM), s.stderr())
	}
}

// A termination signal during a running turn cancels the turn, ends every session and drains: each session's session_shutdown has run when the server exits, the socket is gone and both connections are closed, in both extension modes (D232).
func TestListenSIGTERMDuringATurnCancelsItAndEndsEverySessionBeforeExit(t *testing.T) {
	for _, mode := range []struct {
		name string
		args []string
	}{{"isolated", nil}, {"shared", []string{shareExtensions}}} {
		t.Run(mode.name, func(t *testing.T) {
			s, log := listenExtensionServer(t, mode.args...)
			a, b := s.dial(), s.dial()
			for _, conn := range []*rpcProcess{a, b} {
				if got := callTool(conn, "node_counter"); got == "" {
					t.Fatalf("node_counter answered nothing\n%s", s.stderr())
				}
			}
			startSlowTurn(a)
			s.signal(syscall.SIGTERM)
			if code := s.waitForExit(); code != 128+int(syscall.SIGTERM) {
				t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGTERM), s.stderr())
			}
			if lines := readShutdownLog(log); len(lines) != 4 {
				t.Fatalf("session_shutdown lines = %q at the server's exit, want one per instance (4)", lines)
			}
			s.requireSocketGone()
			a.waitForEOF()
			b.waitForEOF()
		})
	}
}
