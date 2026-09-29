//go:build windows

package tools

import (
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsTaskkillSpawnOptions(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6596-taskkill-enoent.test.ts:30 tests the full Windows spawn contract.
	t.Setenv("SystemRoot", `C:\CustomWindows`)
	command := windowsTaskkillCommand(1234)
	wantPath := filepath.Join(`C:\CustomWindows`, "System32", "taskkill.exe")
	if command.Path != wantPath || !slices.Equal(command.Args, []string{wantPath, "/F", "/T", "/PID", "1234"}) {
		t.Fatalf("command = %q %q", command.Path, command.Args)
	}
	if command.Stdin != nil || command.Stdout != nil || command.Stderr != nil {
		t.Fatal("stdio is not ignored")
	}
	// libuv's uv_spawn: detached adds DETACHED_PROCESS and
	// CREATE_NEW_PROCESS_GROUP; windowsHide with no inherited stdio adds
	// SW_HIDE and CREATE_NO_WINDOW.
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow || command.SysProcAttr.CreationFlags != (windows.CREATE_NEW_PROCESS_GROUP|windows.DETACHED_PROCESS|windows.CREATE_NO_WINDOW) {
		t.Fatalf("spawn attributes = %+v", command.SysProcAttr)
	}
	called := false
	runTaskkill(command, func(*exec.Cmd) error { called = true; return errors.New("spawn taskkill ENOENT") })
	if !called {
		t.Fatal("spawn was not attempted")
	}
}
