//go:build unix

package codingagent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// emergencyExitHelperEnv makes the test binary run the dead-terminal exit with a bash call in flight.
const emergencyExitHelperEnv = "PIG_TEST_EMERGENCY_EXIT_DIR"

// TestEmergencyTerminalExitHelper is the helper process of TestEmergencyTerminalExitKillsTheShellOfARunningBashCall: it
// starts a bash call that leaves a background sleep, then takes the dead-terminal exit.
func TestEmergencyTerminalExitHelper(t *testing.T) {
	dir := os.Getenv(emergencyExitHelperEnv)
	if dir == "" {
		t.Skip("helper process of another test")
	}
	operations := tools.CreateLocalBashOperations(&tools.LocalBashOptions{BinDir: dir})
	go func() {
		_, _ = operations.Exec(context.Background(), "sleep 3600 & echo $! > "+filepath.Join(dir, "sleep.pid")+"; wait", dir, tools.BashOperationsExecOptions{})
	}()
	for {
		// The shell creates the file before it writes the pid.
		if data, err := os.ReadFile(filepath.Join(dir, "sleep.pid")); err == nil && strings.HasSuffix(string(data), "\n") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	(&InteractiveMode{opts: InteractiveModeOptions{AgentDir: dir}}).emergencyTerminalExit("terminal gone: test")
}

// Pi's emergencyTerminalExit calls killTrackedDetachedChildren before process.exit(129): a bash call's shell runs in its
// own process group, so without it a command such as a dev server outlives a PiG whose terminal is gone.
func TestEmergencyTerminalExitKillsTheShellOfARunningBashCall(t *testing.T) {
	dir := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestEmergencyTerminalExitHelper$")
	command.Env = append(os.Environ(), emergencyExitHelperEnv+"="+dir)
	err := command.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 129 {
		t.Fatalf("helper = %v, want exit code 129", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "sleep.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	// The kill is synchronous and precedes the exit, so only reaping remains.
	deadline := time.Now().Add(5 * time.Second)
	for processRuns(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the background sleep %d of the bash call survived the emergency exit", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Pi's uncaughtCrash calls killTrackedDetachedChildren before it reports the crash and exits: the shell of a running
// bash call is in its own process group and would otherwise outlive the crashed process.
func TestUncaughtCrashKillsTheShellOfARunningBashCall(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "shell.pid")
	operations := tools.CreateLocalBashOperations(&tools.LocalBashOptions{BinDir: dir})
	done := make(chan tools.BashOperationsResult, 1)
	go func() {
		result, _ := operations.Exec(context.Background(), "echo $$ > "+pidFile+"; sleep 60", dir, tools.BashOperationsExecOptions{})
		done <- result
	}()
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the bash call did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work", AgentDir: t.TempDir(), AppVersion: "9.9.9"})
	var stderr strings.Builder
	m.uncaughtCrash(errors.New("boom"), []byte("stack\n"), &stderr)
	select {
	case result := <-done:
		if result.ExitCode == nil || *result.ExitCode != 128+int(syscall.SIGKILL) {
			t.Fatalf("exit code = %v, want %d", result.ExitCode, 128+int(syscall.SIGKILL))
		}
	case <-time.After(testbudget.Wait(t)):
		data, _ := os.ReadFile(pidFile)
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
		t.Fatal("the shell of the running bash call survived the crash")
	}
}

// processRuns reports a live process: a killed process that init has not reaped yet is a zombie and does not run.
func processRuns(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	fields := strings.Fields(string(stat)[strings.LastIndex(string(stat), ")")+1:])
	return len(fields) == 0 || fields[0] != "Z"
}
