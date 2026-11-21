//go:build unix

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// readTwoPIDs waits for pidFile to contain two decimal PIDs (the shell and its
// descendant, written atomically via rename so a partial line is never read)
// and registers cleanup SIGKILLs for both.
func readTwoPIDs(t *testing.T, pidFile string) (shellPID, childPID int) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			fields := strings.Fields(string(data))
			if len(fields) == 2 {
				p1, err1 := strconv.Atoi(fields[0])
				p2, err2 := strconv.Atoi(fields[1])
				if err1 == nil && err2 == nil && p1 > 0 && p2 > 0 {
					t.Cleanup(func() {
						_ = syscall.Kill(p2, syscall.SIGKILL)
						_ = syscall.Kill(p1, syscall.SIGKILL)
					})
					return p1, p2
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid file %s did not appear with two pids within %s", pidFile, testbudget.Wait(t))
		}
		time.Sleep(15 * time.Millisecond)
	}
}

// shellSingleQuote quotes s for POSIX sh.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// longBashCommandWithChild starts a long-lived descendant (`sleep 300`) under
// the shell, writes "<shell_pid> <child_pid>" atomically to pidFile, and waits.
func longBashCommandWithChild(pidFile string) string {
	q := shellSingleQuote(pidFile)
	qTmp := shellSingleQuote(pidFile + ".tmp")
	return fmt.Sprintf("sleep 300 & child=$!; printf '%%s %%s\\n' \"$$\" \"$child\" > %s && mv %s %s; wait", qTmp, qTmp, q)
}

func assertProcessesGone(t *testing.T, what string, pids ...int) {
	t.Helper()
	ctx := testbudget.Context(t)
	for _, pid := range pids {
		if err := waitProcessExit(ctx, pid); err != nil {
			t.Errorf("%s: pid %d still alive after exit: %v", what, pid, err)
		}
	}
}

// TestRPCInputEndKillsRunningBashTree verifies that closing RPC stdin while an
// RPC `bash` command is running ends both the shell and its descendant before
// the RPC process exits 0. Runs against both pig and the oracle Pi.
func TestRPCInputEndKillsRunningBashTree(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, _ := impl.start(t, "RPC_SHUTDOWN_NO_HANDLER=1")
			awaitReady(p)
			pidFile := filepath.Join(t.TempDir(), "pids.txt")
			p.sendJSON(map[string]any{"id": "b1", "type": "bash", "command": longBashCommandWithChild(pidFile)})
			shellPID, childPID := readTwoPIDs(t, pidFile)
			p.closeInput()
			drainRPCOutput(p)
			p.waitForExit("while RPC bash command is running")
			assertProcessesGone(t, impl.name+" RPC stdin EOF", shellPID, childPID)
		})
	}
}

// TestRPCInputEndKillsRunningBashTreeNoExt runs without extensions so
// Session.Dispose() -> AbortBash() -> os.Exit(0) has no extension IPC delay
// to mask the race between os.Exit(0) and bash_operations.go's ctx.Done()
// goroutine calling killProcessGroup.
func TestRPCInputEndKillsRunningBashTreeNoExt(t *testing.T) {
	dir := t.TempDir()
	p := startRPCProcessAt(t, dir, []string{"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_HOME=" + t.TempDir()}, "--model", "test-faux/faux-1", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session")
	awaitReady(p)
	pidFile := filepath.Join(t.TempDir(), "pids.txt")
	p.sendJSON(map[string]any{"id": "b1", "type": "bash", "command": longBashCommandWithChild(pidFile)})
	shellPID, childPID := readTwoPIDs(t, pidFile)
	p.closeInput()
	drainRPCOutput(p)
	p.waitForExit("while RPC bash command is running without extensions")
	assertProcessesGone(t, "pig RPC stdin EOF (no extensions)", shellPID, childPID)
}

// TestRPCExtensionShutdownWhileBashRuns pins that ctx.shutdown() does not end
// RPC mode while an RPC `bash` command is active (Pi rpc-mode.ts:763-766,801-808
// holds checkShutdownRequested while activeCommands > 0), and that ending stdin
// afterwards kills the running shell and its descendant cleanly.
func TestRPCExtensionShutdownWhileBashRuns(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			gate := filepath.Join(t.TempDir(), "gate")
			p, _ := impl.start(t, "RPC_SHUTDOWN_NO_HANDLER=1", "RPC_SHUTDOWN_IDLE_GATE="+gate)
			awaitReady(p)
			pidFile := filepath.Join(t.TempDir(), "pids.txt")
			p.sendJSON(map[string]any{"id": "b1", "type": "bash", "command": longBashCommandWithChild(pidFile)})
			shellPID, childPID := readTwoPIDs(t, pidFile)
			if err := os.WriteFile(gate, []byte("go"), 0o600); err != nil {
				t.Fatal(err)
			}
			// Give the idle-gate timer time to fire ctx.shutdown() and verify
			// the process stays alive while the bash command is active.
			exited := make(chan error, 1)
			go func() { exited <- p.cmd.Wait() }()
			select {
			case err := <-exited:
				p.exited = true
				t.Fatalf("%s exited while bash command was active (err=%v); expected activeCommands > 0 to hold ctx.shutdown()\n%s", impl.name, err, p.stderr.String())
			case <-time.After(500 * time.Millisecond):
			}
			p.closeInput()
			drainRPCOutput(p)
			select {
			case err := <-exited:
				p.exited = true
				if err != nil {
					t.Fatalf("%s exited after EOF with error: %v\n%s", impl.name, err, p.stderr.String())
				}
			case <-time.After(p.budget):
				t.Fatalf("%s did not exit after EOF", impl.name)
			}
			assertProcessesGone(t, impl.name+" ctx.shutdown()+EOF", shellPID, childPID)
		})
	}
}

// TestListenConnectionEOFDoesNotKillSiblingConnectionBash verifies that
// exitRPC does not call tools.KillTrackedDetachedChildren() when the transport
// is not processOwned (--listen): closing one client connection must not kill a
// bash command running on another client's connection.
func TestListenConnectionEOFDoesNotKillSiblingConnectionBash(t *testing.T) {
	dir := listenTempDir(t)
	path := filepath.Join(dir, "pig.sock")
	server := startListenServer(t, listenEnv(t, dir), "--mode", "rpc", "--listen", path, "--model", "test-faux/faux-1", "--no-session", "--no-extensions", "--no-skills", "--no-prompt-templates")
	server.path = path
	conn1 := server.dial()
	conn2 := server.dial()
	requestResponse(conn1, "r1", "get_state")
	requestResponse(conn2, "r2", "get_state")

	pidFile := filepath.Join(t.TempDir(), "pids.txt")
	conn2.sendJSON(map[string]any{"id": "b2", "type": "bash", "command": longBashCommandWithChild(pidFile)})
	shellPID, childPID := readTwoPIDs(t, pidFile)

	// Closing conn1 runs exitRPC with transport.processOwned == false.
	conn1.closeInput()
	conn1.waitForEOF()

	// Issue a round-trip on conn2 so any kill triggered by conn1's teardown has
	// already executed, then verify conn2's bash shell and child are still alive
	// (not dead, not zombie).
	if state := requestResponse(conn2, "after-conn1", "get_state"); state["success"] != true {
		t.Fatalf("conn2 get_state after conn1 EOF: %v", state)
	}
	checkCtx, cancelCheck := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancelCheck()
	if err := waitProcessExit(checkCtx, shellPID); err == nil {
		t.Fatalf("conn1 EOF killed sibling conn2 shell %d", shellPID)
	}
	if err := waitProcessExit(checkCtx, childPID); err == nil {
		t.Fatalf("conn1 EOF killed sibling conn2 descendant %d", childPID)
	}

	// Clean up by aborting conn2's bash and shutting down the server.
	conn2.sendJSON(map[string]any{"id": "ab", "type": "abort_bash"})
	conn2.await("abort response", func(r rpcRecord) bool { return isSuccessResponse(r, "ab") })
	assertProcessesGone(t, "conn2 abort_bash", shellPID, childPID)
	conn2.closeInput()
	conn2.waitForEOF()
	server.signal(syscall.SIGTERM)
	server.waitForExit()
}
