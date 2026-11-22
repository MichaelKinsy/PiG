//go:build windows

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestWin_RPCInputEndKillsRunningBashTree verifies on native Windows that
// closing RPC stdin while an RPC `bash` command with a descendant is running
// kills both the shell and its descendant before pig exits 0 (Pi 31b407dc63, #11889).
func TestWin_RPCInputEndKillsRunningBashTree(t *testing.T) {
	dir := t.TempDir()
	p := startRPCProcessAt(t, dir, []string{"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_HOME=" + t.TempDir()}, "--model", "test-faux/faux-1", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session")
	awaitReady(p)
	pidFile := filepath.Join(t.TempDir(), "pids.txt")
	q := "'" + strings.ReplaceAll(filepath.ToSlash(pidFile), "'", `'\''`) + "'"
	qTmp := "'" + strings.ReplaceAll(filepath.ToSlash(pidFile+".tmp"), "'", `'\''`) + "'"
	// Write Win32 PIDs (not MSYS2 virtual PIDs) via PowerShell/cat /proc or $$; in Git Bash
	// /proc/$$/winpid holds the real Windows PID that OpenProcess / waitProcessExit takes.
	cmd := fmt.Sprintf("sleep 300 & child=$!; wp1=$(cat /proc/$$/winpid 2>/dev/null || echo $$); wp2=$(cat /proc/$child/winpid 2>/dev/null || echo $child); printf '%%s %%s\\n' \"$wp1\" \"$wp2\" > %s && mv %s %s; wait", qTmp, qTmp, q)
	p.sendJSON(map[string]any{"id": "b1", "type": "bash", "command": cmd})

	var shellPID, childPID int
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			fields := strings.Fields(string(data))
			if len(fields) == 2 {
				p1, err1 := strconv.Atoi(fields[0])
				p2, err2 := strconv.Atoi(fields[1])
				if err1 == nil && err2 == nil && p1 > 0 && p2 > 0 {
					shellPID, childPID = p1, p2
					break
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid file %s did not appear within %s", pidFile, testbudget.Wait(t))
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Cleanup(func() {
		if proc, err := os.FindProcess(childPID); err == nil {
			_ = proc.Kill()
		}
		if proc, err := os.FindProcess(shellPID); err == nil {
			_ = proc.Kill()
		}
	})

	p.closeInput()
	drainRPCOutput(p)
	p.waitForExit("while RPC bash command is running on Windows")

	ctx := testbudget.Context(t)
	for _, pid := range []int{shellPID, childPID} {
		if err := waitProcessExit(ctx, pid); err != nil {
			t.Errorf("Windows RPC stdin EOF: pid %d still alive after pig exited: %v", pid, err)
		}
	}
}
