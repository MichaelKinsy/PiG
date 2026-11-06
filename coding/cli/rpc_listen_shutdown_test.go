//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// listenShutdownServer starts a --listen server whose sessions load the rpc-shutdown fixture with the given RPC_SHUTDOWN_* settings, and returns it with the file the fixture reports to and the agent directory whose exit.log is the server's (D102).
func listenShutdownServer(t *testing.T, extraEnv ...string) (server *listenServer, report, agentDir string) {
	t.Helper()
	dir := listenTempDir(t)
	fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-shutdown.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	report = filepath.Join(dir, "report.txt")
	path := filepath.Join(dir, "pig.sock")
	env := append(listenEnv(t, dir), "PIG_TEST_FAUX_SCENARIO=parity-basic", "RPC_SHUTDOWN_REPORT="+report)
	server = startListenServer(t, append(env, extraEnv...), "--mode", "rpc", "--listen", path, "--model", "test-faux/faux-1", "--no-session",
		"--no-extensions", "--no-skills", "--no-prompt-templates", "-e", fixture)
	server.path = path
	return server, report, filepath.Join(dir, "agent")
}

func exitLogLines(t *testing.T, agentDir string) []string {
	t.Helper()
	data, err := os.ReadFile(codingagent.ExitLogPath(agentDir))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// A connection's session follows D234: once its shutdown has started, a single signal does not interrupt it. The server's signal reaches the session, finds the shutdown running, and the server waits for that dispose to finish before it exits 128+signum (D232).
func TestListenSingleSignalDuringAConnectionsInputEndShutdownLetsItFinish(t *testing.T) {
	server, report, agentDir := listenShutdownServer(t, "RPC_SHUTDOWN_SLOW=1500", "RPC_SHUTDOWN_RECORD_DONE=1")
	conn := server.dial()
	requestResponse(conn, "ready", "get_state")
	conn.closeInput()
	awaitRPCReport(t, report, "session_shutdown")
	server.signal(syscall.SIGTERM)
	if code := server.waitForExit(); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGTERM), server.stderr())
	}
	if !slices.Contains(readRPCShutdownReportFile(report), "session_shutdown:done") {
		t.Fatalf("the signal interrupted the connection's shutdown: report %q\n%s", readRPCShutdownReportFile(report), server.stderr())
	}
	server.requireSocketGone()
	if lines := exitLogLines(t, agentDir); len(lines) != 1 || !strings.Contains(lines[0], "received SIGTERM") {
		t.Fatalf("exit.log = %q, want the server's one line naming SIGTERM", lines)
	}
}

// The signal's hold on the Session's agent events (the termination prelude) is released when the connection's shutdown was already running: a run that a session_shutdown handler starts meanwhile must not wait on a hold nobody releases, or the dispose waits for the handler forever (D234, as TestRPCSingleSignalDuringShutdownDoesNotBlockARunTheHandlerStarts).
func TestListenSingleSignalDuringAConnectionsShutdownDoesNotBlockARunTheHandlerStarts(t *testing.T) {
	server, report, _ := listenShutdownServer(t, "RPC_SHUTDOWN_SLOW=800", "RPC_SHUTDOWN_SEND_AFTER=300", "RPC_SHUTDOWN_RECORD_DONE=1")
	conn := server.dial()
	requestResponse(conn, "ready", "get_state")
	conn.closeInput()
	awaitRPCReport(t, report, "session_shutdown")
	server.signal(syscall.SIGTERM)
	if code := server.waitForExit(); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGTERM), server.stderr())
	}
	if !slices.Contains(readRPCShutdownReportFile(report), "session_shutdown:done") {
		t.Fatalf("the handler never finished: report %q\n%s", readRPCShutdownReportFile(report), server.stderr())
	}
}

// A repeated signal exits the server at once, without waiting for a connection's dispose, with 128 plus its own number (D234).
func TestListenRepeatedSignalDuringAConnectionsShutdownExitsAtOnce(t *testing.T) {
	server, report, _ := listenShutdownServer(t, "RPC_SHUTDOWN_SLOW=600000", "RPC_SHUTDOWN_RECORD_DONE=1")
	conn := server.dial()
	requestResponse(conn, "ready", "get_state")
	conn.closeInput()
	awaitRPCReport(t, report, "session_shutdown")
	start := time.Now()
	server.signal(syscall.SIGTERM)
	time.Sleep(200 * time.Millisecond)
	select {
	case <-server.exited:
		t.Fatalf("one signal ended the server during a connection's dispose: exit %d\n%s", server.code, server.stderr())
	default:
	}
	server.signal(syscall.SIGHUP)
	if code := server.waitForExit(); code != 128+int(syscall.SIGHUP) {
		t.Fatalf("exit %d, want %d (the repeated signal's own number)\n%s", code, 128+int(syscall.SIGHUP), server.stderr())
	}
	if took := time.Since(start); took >= server.budget {
		t.Fatalf("the repeated signal took %s", took)
	}
	if slices.Contains(readRPCShutdownReportFile(report), "session_shutdown:done") {
		t.Fatal("the dispose finished: the repeated signal waited for it")
	}
	server.requireSocketGone()
}

// A connection's end is not the process's end: it writes no exit.log line, and the server's own ending writes the one line (D102, D232).
func TestListenConnectionEndsWriteNoExitLogLine(t *testing.T) {
	server, _, agentDir := listenShutdownServer(t)
	for range 2 {
		conn := server.dial()
		requestResponse(conn, "ready", "get_state")
		conn.closeInput()
		conn.waitForEOF()
	}
	if lines := exitLogLines(t, agentDir); len(lines) != 0 {
		t.Fatalf("exit.log after two connection ends = %q, want none", lines)
	}
	server.signal(syscall.SIGTERM)
	if code := server.waitForExit(); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit %d, want %d\n%s", code, 128+int(syscall.SIGTERM), server.stderr())
	}
	if lines := exitLogLines(t, agentDir); len(lines) != 1 || !strings.Contains(lines[0], "received SIGTERM") {
		t.Fatalf("exit.log = %q, want one line naming SIGTERM", lines)
	}
}

// An extension's ctx.shutdown() on a connection ends that session and releases it: the connection closes, its extension process goes, the other sessions are served, and one signal then ends the server (D232; D234 makes a later trigger of a started shutdown a no-op, so the connection's own exit has to end its mode).
func TestListenExtensionShutdownEndsOnlyThatSession(t *testing.T) {
	server, report, _ := listenShutdownServer(t)
	first := server.dial()
	requestResponse(first, "ready", "get_state")
	second := server.dial()
	requestResponse(second, "ready", "get_state")
	requireCellCounts(t, server, 2, 0, "with two sessions open")
	first.sendJSON(map[string]any{"id": "quit", "type": "prompt", "message": "/quit"})
	first.waitForEOF()
	if !slices.Contains(readRPCShutdownReportFile(report), "session_shutdown") {
		t.Fatalf("the quit command did not shut the session down: %q\n%s", readRPCShutdownReportFile(report), server.stderr())
	}
	requireCellCounts(t, server, 1, 0, "after the first session quit: its extension process must be gone")
	requestResponse(second, "again", "get_state")
	third := server.dial()
	requestResponse(third, "ready", "get_state")
	second.closeInput()
	second.waitForEOF()
	third.closeInput()
	third.waitForEOF()
	requireCellCounts(t, server, 0, 0, "after every session ended")
	server.signal(syscall.SIGTERM)
	if code := server.waitForExit(); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit %d, want %d: one signal must end a server whose session quit by itself\n%s", code, 128+int(syscall.SIGTERM), server.stderr())
	}
}
