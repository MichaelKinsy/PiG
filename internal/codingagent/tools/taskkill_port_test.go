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
	// .upstream/v1.3.0+0ed1b043/packages/coding-agent/test/suite/regressions/6596-taskkill-enoent.test.ts:30
	// Path/argv/error ownership is shared Go code; Windows console attributes
	// are asserted by TestWindowsTaskkillSpawnOptions on the native target.
	// #9490: a missing or empty SystemRoot must retain an absolute taskkill fallback, as upstream's `||` does.
	unset := (*string)(nil)
	for _, tc := range []struct {
		systemRoot *string
		wantRoot   string
	}{
		{new(`D:\CustomWindows`), `D:\CustomWindows`},
		{unset, `C:\Windows`},
		{new(""), `C:\Windows`},
	} {
		name := "undefined"
		if tc.systemRoot != nil {
			name = fmt.Sprintf("%q", *tc.systemRoot)
		}
		t.Run("uses System32 taskkill and consumes its spawn error with SystemRoot="+name, func(t *testing.T) {
			t.Setenv("SystemRoot", "")
			if tc.systemRoot == nil {
				if err := os.Unsetenv("SystemRoot"); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv("SystemRoot", *tc.systemRoot)
			}
			command := newTaskkillCommand(1234)
			wantPath := filepath.Join(tc.wantRoot, "System32", "taskkill.exe")
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
		})
	}
}
