//go:build windows

package tools

import (
	"strings"
	"testing"
)

// A reader that falls behind the shell's exit by more than the post-exit grace
// must still deliver what the exited command wrote: its job is empty, so no
// process can still hold the pipe. The reader is held before its first read
// until the grace has expired; the output fits the pipe buffer, so the
// command exits while all of it is unread.
func TestWin_LocalShellOperationsReadToEOFAfterTheGrace(t *testing.T) {
	const n = 300
	graceExpired := make(chan struct{})
	testHookBeforeStdioRead = func() { <-graceExpired }
	testHookStdioGraceExpired = func() { close(graceExpired) }
	t.Cleanup(func() { testHookBeforeStdioRead, testHookStdioGraceExpired = nil, nil })
	var output strings.Builder
	result, err := NewLocalBashOperations(nil, "").Exec(t.Context(), awkLines(n), t.TempDir(), BashOperationsExecOptions{OnData: func(data []byte) { output.Write(data) }})
	if err != nil || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("exec: %+v, %v", result, err)
	}
	assertEveryLine(t, output.String(), n)
}
