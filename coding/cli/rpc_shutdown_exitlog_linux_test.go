//go:build linux

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A single signal during a stdin-end shutdown is ignored. When a failing stdout then ends the mode before the shutdown does, the process exits with the signal's status, 143, and exit.log still names the signal (D102).
func TestRPCIgnoredSignalThenStdoutFailureLeavesAnExitLogLine(t *testing.T) {
	// Every write to /dev/full fails with ENOSPC; a closed pipe would end the process with SIGPIPE instead.
	full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("no /dev/full: %v", err)
	}
	defer func() { _ = full.Close() }()
	fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-shutdown.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	report := filepath.Join(t.TempDir(), "report.txt")
	cmd := exec.Command(buildPigBinaryForSignalTest(t), "--mode", "rpc", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1", "-e", fixture)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic",
		"RPC_SHUTDOWN_REPORT="+report, "RPC_SHUTDOWN_NOTIFY_AFTER=700", "RPC_SHUTDOWN_SLOW=3000")
	cmd.Stdout = full
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	// The first write RPC mode makes is the dispose's notification, 700 ms into the handler.
	_ = stdin.Close()
	awaitRPCReport(t, report, "session_shutdown")
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waited:
		status := cmd.ProcessState.Sys().(syscall.WaitStatus)
		if !status.Exited() || status.ExitStatus() != 143 {
			t.Fatalf("wait = %v, want exit 143", err)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the process did not end after the stdout failure")
	}
	data, err := os.ReadFile(codingagent.ExitLogPath(agentDir))
	if err != nil {
		t.Fatalf("no exit.log: %v", err)
	}
	if !strings.Contains(string(data), "received SIGTERM during shutdown") || strings.Count(strings.TrimSpace(string(data)), "\n") != 0 {
		t.Fatalf("exit.log = %q, want one line naming the ignored SIGTERM", data)
	}
}
