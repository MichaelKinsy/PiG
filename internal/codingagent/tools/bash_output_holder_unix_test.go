//go:build unix

package tools

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// A descendant that outlives the shell keeps the write end of the output pipe open, so the grace applies to it (utils/child-process.ts
// waitForChildProcess: the idle timer is the only thing that ends the wait). The grace measures the writer's silence: what the shell wrote before it
// exited is output the readers have not taken yet when they fall behind by more than the grace, and the wait must not release the pipe on it.
func TestLocalShellOperationsKeepTheOutputOfAnExitedShellWhileADescendantHoldsThePipe(t *testing.T) {
	holdTheReaderUntilTheGraceExpires(t)
	pidFile := filepath.Join(t.TempDir(), "holder.pid")
	t.Cleanup(func() {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	var output strings.Builder
	command := "sleep 600 &\necho $! > " + bashSingleQuotedArg(pidFile) + "\necho kept"
	result, err := CreateLocalBashOperations(nil).Exec(t.Context(), command, t.TempDir(), BashOperationsExecOptions{OnData: func(data []byte) { output.Write(data) }})
	if err != nil || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("exec: %+v, %v", result, err)
	}
	if got := output.String(); got != "kept\n" {
		t.Fatalf("output = %q, want %q", got, "kept\n")
	}
}
