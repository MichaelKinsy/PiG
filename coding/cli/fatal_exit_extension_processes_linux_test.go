//go:build linux

package cli

import (
	"bytes"
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

// Pi's handleFatalRuntimeError shows the error, stops the TUI and calls process.exit(1) (.upstream/current/packages/coding-agent/src/modes/interactive/interactive-mode.ts:2245-2258,
// stop() at :7283-7299): no session_shutdown is emitted, and nothing of the extensions outlives the process, because they run inside it. In PiG the Session
// a failed /new replaced is gone, but the replacement Session's extensions are processes of their own (D70), and the exit that follows the fatal report
// (main.go: ErrInteractiveCrashed, then exitProcess(1)) ended only the startup host: the replacement's Node and Python processes outlived pig by the time they took
// to end, and a harness that removed pig's agent directory right after its exit raced the Node process's last writes (parity scenario 18: "directory not empty"
// under cache/node-compile). The two fixtures take 1.5 s to end once their input closes, so a pig that leaves them behind leaves them running when the test looks.
func TestInteractiveFatalReplacementExitLeavesNoExtensionProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and extension processes")
	}
	t.Parallel()
	budget := testbudget.Wait(t)
	node, err := filepath.Abs(filepath.Join("testdata", "fatal-replace.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	python, err := filepath.Abs(filepath.Join("testdata", "fatal-replace-native.py"))
	if err != nil {
		t.Fatal(err)
	}
	flag := filepath.Join(t.TempDir(), "armed")
	p := startInteractivePigOptions(t, node, interactivePigOptions{
		args: []string{"--model", "test-faux/faux-1", "-e", python},
		env:  []string{"PIG_TEST_FATAL_FLAG=" + flag},
	})
	waitReplaceLog(t, p.log, 2, "the startup session_start of both extensions")
	p.command("/arm")
	armed := time.Now().Add(budget)
	for {
		if _, err := os.Stat(flag); err == nil {
			break
		}
		if time.Now().After(armed) {
			t.Fatalf("/arm never created the flag; screen tail: %q", tail(p.output.since(0), 1500))
		}
		time.Sleep(50 * time.Millisecond)
	}

	p.send("\x15/new")
	time.Sleep(200 * time.Millisecond)
	typed := time.Now()
	p.send("\r")
	select {
	case <-p.exited:
	case <-time.After(budget):
		t.Fatalf("pig did not exit after the failed /new; screen tail: %q", tail(p.output.since(0), 1500))
	}
	t.Logf("fatal exit: %s from Enter to the end of pig", time.Since(typed))

	if code := p.cmd.ProcessState.ExitCode(); code != 1 {
		t.Fatalf("pig exited with status %d after the failed /new, want 1; screen tail: %q", code, tail(p.output.since(0), 1500))
	}
	if !bytes.Contains(p.output.since(0), []byte("Failed to create session")) {
		t.Fatalf("the screen does not show the fatal report; screen tail: %q", tail(p.output.since(0), 1500))
	}
	log, err := os.ReadFile(p.log)
	if err != nil {
		t.Fatal(err)
	}
	// The processes that served the replacement Session: one line per extension, "<kind> new <pid>".
	replacement := map[string]int{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(log)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[1] != "new" {
			continue
		}
		pid, err := strconv.Atoi(fields[2])
		if err != nil {
			t.Fatalf("bad pid in log line %q: %v", line, err)
		}
		replacement[fields[0]] = pid
	}
	if len(replacement) != 2 {
		t.Fatalf("the replacement Session started %v, want one node and one python extension process; log:\n%s", replacement, log)
	}
	// A leftover process is ended and waited for before the test returns, so a failing run leaves none behind.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		for _, pid := range replacement {
			if processRunning(pid) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				_ = waitProcessExit(ctx, pid)
			}
		}
	})
	for kind, pid := range replacement {
		if processRunning(pid) {
			t.Errorf("the %s extension process of the replacement Session outlived pig: %s", kind, processState(pid))
		}
	}
}

// processRunning reports whether pid is a process that has not ended: a zombie has ended and only waits to be reaped.
func processRunning(pid int) bool {
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(status), "\n") {
		if state, ok := strings.CutPrefix(line, "State:"); ok {
			state = strings.TrimSpace(state)
			return !strings.HasPrefix(state, "Z") && !strings.HasPrefix(state, "X")
		}
	}
	return true
}
