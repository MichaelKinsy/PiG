//go:build unix && !pig_strip_mcp

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's stdio transport ends the process group of every running MCP server from a process.once("exit") hook
// (packages/mcp/src/transports/stdio.ts installExitHook), so a termination signal leaves no server behind. Go runs no
// exit hooks: the signal handler calls tools.KillTrackedDetachedChildren, which runs the cleanup that coding/mcpext
// registers. Only a binary that links mcpext (every build that can start a stdio server) has it registered.
//
// The session_shutdown handler below never settles, so the orderly shutdown that closes the transports (and would end
// the server on its own) cannot finish: only the signal handler's kill can end the server.
func TestSIGTERMEndsAnMCPStdioServerOfTheBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "server.pid")
	// The server records its pid, then blocks forever, ignoring stdin, and never answers `initialize`.
	mcpJSON, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"blocking": map[string]any{
		"command": "sh", "args": []string{"-c", `echo $$ > "$1.tmp" && mv "$1.tmp" "$1"; exec tail -f /dev/null`, "sh", pidFile},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(dir, "agent")
	seedFirstRunDone(t, agentDir)
	if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), mcpJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	hang := filepath.Join(dir, "hang-on-shutdown.mjs")
	if err := os.WriteFile(hang, []byte(`export default function (pi) { pi.on("session_shutdown", () => new Promise(() => {})); }`), 0o600); err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--mode", "rpc", "-e", hang, "--session-dir", t.TempDir())
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "HOME="+dir, "PIG_HOME="+filepath.Join(dir, ".pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pig: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
	var pid int
	waitFor := func(what string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(testbudget.Wait(t))
		for !condition() {
			if time.Now().After(deadline) {
				t.Fatalf("%s\nstderr:\n%s", what, stderr.String())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitFor("the MCP server never started", func() bool {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
		return err == nil
	})
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the MCP server is not running: %v", err)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	// pig reaps its server, so a gone process reports ESRCH.
	waitFor("the MCP server survived SIGTERM of pig", func() bool { return syscall.Kill(pid, 0) != nil })
}
