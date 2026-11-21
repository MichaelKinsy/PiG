package subprocess

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const (
	// harnessTestHelperEnv switches the test binary into the harness stub mode
	// handled at the top of TestMain. Running os.Args[0] directly gives Windows
	// and Unix a real native executable without compiling a helper binary or
	// wrapping a shell script.
	harnessTestHelperEnv     = "PIG_TEST_HARNESS_HELPER_MODE"
	harnessTestHelperPIDEnv  = "PIG_TEST_HARNESS_HELPER_PID_FILE"
	harnessTestHelperArgsEnv = "PIG_TEST_HARNESS_HELPER_ARGS_FILE"
	harnessTestHelperExitEnv = "PIG_TEST_HARNESS_HELPER_EXIT_CODE"
)

// maybeRunHarnessTestHelper runs the native stub binary inside TestMain when
// harnessTestHelperEnv is set. Modes:
//   - "wait": write PID atomically, record args if requested, then block until killed.
//   - "exit": write PID atomically, record args if requested, echo stdin to stdout/stderr
//     when mode is "stdio-exit", and exit with harnessTestHelperExitEnv (default 0).
func maybeRunHarnessTestHelper() {
	mode := os.Getenv(harnessTestHelperEnv)
	if mode == "" {
		return
	}
	// Clear helper vars so any nested process does not re-enter helper mode.
	_ = os.Unsetenv(harnessTestHelperEnv)

	if argsFile := os.Getenv(harnessTestHelperArgsEnv); argsFile != "" {
		payload, err := json.Marshal(os.Args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "marshal harness helper args:", err)
			os.Exit(2)
		}
		tmp := argsFile + ".tmp"
		if err := os.WriteFile(tmp, payload, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "write harness helper args:", err)
			os.Exit(2)
		}
		if err := os.Rename(tmp, argsFile); err != nil {
			fmt.Fprintln(os.Stderr, "rename harness helper args:", err)
			os.Exit(2)
		}
	}

	if pidFile := os.Getenv(harnessTestHelperPIDEnv); pidFile != "" {
		tmp := pidFile + ".tmp"
		if err := os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "write harness helper pid:", err)
			os.Exit(2)
		}
		if err := os.Rename(tmp, pidFile); err != nil {
			fmt.Fprintln(os.Stderr, "rename harness helper pid:", err)
			os.Exit(2)
		}
	}

	switch mode {
	case "wait":
		for {
			time.Sleep(time.Hour)
		}
	case "stdio-exit":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read harness helper stdin:", err)
			os.Exit(2)
		}
		_, _ = fmt.Fprintf(os.Stdout, "out:%s|args:%s\n", strings.TrimSpace(string(data)), strings.Join(os.Args[1:], ","))
		_, _ = fmt.Fprintf(os.Stderr, "err:%s\n", strings.TrimSpace(string(data)))
		os.Exit(parseHarnessHelperExitCode())
	case "exit":
		os.Exit(parseHarnessHelperExitCode())
	default:
		fmt.Fprintf(os.Stderr, "unknown harness helper mode %q\n", mode)
		os.Exit(2)
	}
}

func parseHarnessHelperExitCode() int {
	raw := os.Getenv(harnessTestHelperExitEnv)
	if raw == "" {
		return 0
	}
	code, err := strconv.Atoi(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid harness helper exit code %q: %v\n", raw, err)
		os.Exit(2)
	}
	return code
}

func startHarnessEntry(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
}

// harnessPublishedPID is the pid in what a stub wrote to its pid file with echo, accepted only on a complete line: echo creates the file and then writes "pid\n", so a reader can find it empty, or with the digits and not yet the newline, and must try again.
func harnessPublishedPID(raw []byte) (int, bool) {
	line, complete := bytes.CutSuffix(raw, []byte("\n"))
	if !complete {
		return 0, false
	}
	line = bytes.TrimSuffix(line, []byte("\r"))
	pid, err := strconv.Atoi(string(line))
	return pid, err == nil
}

// waitHarnessPID waits until parse accepts what pidFile holds. The bound only fails a hang: the file's appearance is the event.
func waitHarnessPID(t *testing.T, pidFile string, parse func([]byte) (int, bool)) int {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, ok := parse(raw); ok {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never held a pid", pidFile)
	return 0
}

// waitHarnessChildPID waits for the pid a process published in pidFile by a rename, which makes the file appear whole.
func waitHarnessChildPID(t *testing.T, pidFile string) int {
	t.Helper()
	return waitHarnessPID(t, pidFile, func(raw []byte) (int, bool) {
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		return pid, err == nil
	})
}

// harnessEntryEnds starts waiting for cmd in the background: the channel receives the entry's wait status once it ended.
func harnessEntryEnds(cmd *exec.Cmd) <-chan error {
	ended := make(chan error, 1)
	go func() { ended <- cmd.Wait() }()
	return ended
}

// harnessSelfBinaryPath returns the current test binary's absolute path for use
// as PIG_HARNESS_BINARY with harnessTestHelperEnv.
func harnessSelfBinaryPath(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		t.Fatalf("abs test executable %q: %v", exe, err)
	}
	return abs
}

// harnessProcessSnapshotEntry is a pure representation of a process table row,
// used to render diagnostic trees and verify parent-child relationships on
// Linux as well as Windows.
type harnessProcessSnapshotEntry struct {
	PID  int
	PPID int
	Name string
}

// harnessChildrenByParent returns all snapshot entries whose PPID is in parents,
// ordered by PID.
func harnessChildrenByParent(entries []harnessProcessSnapshotEntry, parents ...int) []harnessProcessSnapshotEntry {
	want := make(map[int]struct{}, len(parents))
	for _, p := range parents {
		if p > 0 {
			want[p] = struct{}{}
		}
	}
	var matched []harnessProcessSnapshotEntry
	for _, e := range entries {
		if _, ok := want[e.PPID]; ok {
			matched = append(matched, e)
		}
	}
	return matched
}

// harnessParentFromSnapshot returns the PPID of pid from entries, or 0 if absent.
func harnessParentFromSnapshot(entries []harnessProcessSnapshotEntry, pid int) int {
	for _, e := range entries {
		if e.PID == pid {
			return e.PPID
		}
	}
	return 0
}

// formatHarnessProcessSnapshot formats entries matching any of focusPIDs (as PID
// or PPID) for diagnostic failure messages.
func formatHarnessProcessSnapshot(entries []harnessProcessSnapshotEntry, focusPIDs ...int) string {
	want := make(map[int]struct{}, len(focusPIDs))
	for _, p := range focusPIDs {
		if p > 0 {
			want[p] = struct{}{}
		}
	}
	var lines []string
	for _, e := range entries {
		_, matchPID := want[e.PID]
		_, matchPPID := want[e.PPID]
		if matchPID || matchPPID {
			lines = append(lines, fmt.Sprintf("pid=%d ppid=%d exe=%s", e.PID, e.PPID, e.Name))
		}
	}
	if len(lines) == 0 {
		return "(none matching focus pids)"
	}
	return strings.Join(lines, "; ")
}
