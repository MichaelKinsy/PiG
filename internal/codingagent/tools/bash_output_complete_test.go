package tools

import (
	"strconv"
	"strings"
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
	result, err := NewLocalBashOperations(nil, "").Exec(t.Context(), awkLines(n), t.TempDir(), BashOperationsExecOptions{OnData: func(data []byte) { output.Write(data) }})
	if err != nil || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("exec: %+v, %v", result, err)
	}
	assertEveryLine(t, output.String(), n)
}
