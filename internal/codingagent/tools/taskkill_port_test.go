package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
)

func TestTaskkillSpawnFailurePort(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6596-taskkill-enoent.test.ts:30
	// Path/argv/error ownership is shared Go code; Windows console attributes
	// are asserted by TestWindowsTaskkillSpawnOptions on the native target.
	t.Setenv("SystemRoot", `C:\CustomWindows`)
	command := newTaskkillCommand(1234)
	wantPath := filepath.Join(`C:\CustomWindows`, "System32", "taskkill.exe")
	if command.Path != wantPath || !slices.Equal(command.Args, []string{wantPath, "/F", "/T", "/PID", "1234"}) {
		t.Fatalf("command = %q %q", command.Path, command.Args)
	}
	if command.Stdin != nil || command.Stdout != nil || command.Stderr != nil {
		t.Fatal("taskkill does not ignore stdio")
	}
	calls := 0
	runTaskkill(command, func(got *exec.Cmd) error {
		calls++
		if got != command {
			t.Error("spawn command changed")
		}
		return &os.PathError{Op: "fork/exec", Path: got.Path, Err: syscall.ENOENT}
	})
	if calls != 1 {
		t.Fatalf("spawn calls = %d, want one", calls)
	}
	data, err := json.Marshal([]any{command.Path, command.Args[1:], calls})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("TASKKILL %s\n", data)
}

func TestTaskkillSystemRootUsesNullishDefault(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/src/utils/shell.ts:221 uses ??, not ||.
	t.Setenv("SystemRoot", "")
	if got := newTaskkillCommand(1).Path; got != filepath.Join("System32", "taskkill.exe") {
		t.Fatal(got)
	}
	if err := os.Unsetenv("SystemRoot"); err != nil {
		t.Fatal(err)
	}
	if got := newTaskkillCommand(1).Path; got != filepath.Join(`C:\Windows`, "System32", "taskkill.exe") {
		t.Fatal(got)
	}
}
