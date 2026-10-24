package tools

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// awkLines is a command whose child process prints the lines 1..n.
func awkLines(n int) string {
	return "awk 'BEGIN{for(i=1;i<=" + strconv.Itoa(n) + ";i++) print i}'"
}

// assertEveryLine fails unless output is exactly the lines 1..n.
func assertEveryLine(t *testing.T, output string, n int) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(output, "\r", ""), "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("got %d lines, want %d; last %q", len(lines), n, lines[len(lines)-1])
	}
	for i, line := range lines {
		if line != strconv.Itoa(i+1) {
			t.Fatalf("line %d = %q, want %d", i+1, line, i+1)
		}
	}
}

// Upstream guarantees the full output of a command once it exits.
func TestLocalShellOperationsCaptureEveryLineOfAChild(t *testing.T) {
	const n = 3000
	var output strings.Builder
	result, err := CreateLocalBashOperations(nil).Exec(t.Context(), awkLines(n), t.TempDir(), BashOperationsExecOptions{OnData: func(data []byte) { output.Write(data) }})
	if err != nil || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("exec: %+v, %v", result, err)
	}
	assertEveryLine(t, output.String(), n)
}

// holdTheReaderUntilTheGraceExpires makes the pipe reader fall behind the shell's exit by more than the post-exit grace, the way a reader that a loaded
// machine does not schedule for 100 ms does: the reader waits before its first read until the grace has expired. A command whose output fits the pipe
// buffer exits while all of it is unread.
func holdTheReaderUntilTheGraceExpires(t *testing.T) {
	t.Helper()
	graceExpired := make(chan struct{})
	var once sync.Once
	testHookBeforeStdioRead = func() { <-graceExpired }
	testHookStdioGraceExpired = func() { once.Do(func() { close(graceExpired) }) }
	t.Cleanup(func() { testHookBeforeStdioRead, testHookStdioGraceExpired = nil, nil })
}

// A reader that falls behind the shell's exit by more than the post-exit grace must still deliver what the exited command wrote: no process holds the
// pipe, so the grace has not measured a quiet writer, only a slow reader (utils/child-process.ts waitForChildProcess resolves on the streams' end).
func TestLocalShellOperationsReadToEOFAfterTheGrace(t *testing.T) {
	const n = 300
	holdTheReaderUntilTheGraceExpires(t)
	var output strings.Builder
	result, err := CreateLocalBashOperations(nil).Exec(t.Context(), awkLines(n), t.TempDir(), BashOperationsExecOptions{OnData: func(data []byte) { output.Write(data) }})
	if err != nil || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("exec: %+v, %v", result, err)
	}
	assertEveryLine(t, output.String(), n)
}

// The bash tool's own result for the same interleaving: "(no output)" for a command that printed, which is what the model saw when the override of
// the bash tool in TestConformance_BuiltinToolOverrideDelegatesAcrossSDKs lost the output of `echo answer-$((6*7))` on a loaded machine.
func TestShellToolReturnsTheOutputOfACommandThatExitedBeforeItsReaderRan(t *testing.T) {
	holdTheReaderUntilTheGraceExpires(t)
	cfg := shellToolConfig{name: "bash", shellName: "bash", tempFilePrefix: "pi-bash", operations: CreateLocalBashOperations(nil)}
	result, err := executeShellTool(t.Context(), t.TempDir(), cfg, json.RawMessage(`{"command":"echo answer-$((6*7))"}`), nil)
	if err != nil || result.IsError {
		t.Fatalf("execute = %+v, %v", result, err)
	}
	if got := result.Text(); got != "answer-42\n" {
		t.Fatalf("output = %q, want %q", got, "answer-42\n")
	}
}
