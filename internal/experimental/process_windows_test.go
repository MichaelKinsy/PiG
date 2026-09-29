//go:build windows

package experimental

import (
	"testing"

	"golang.org/x/sys/windows"
)

// Pi's spawnInternalProcess (experimental/process.ts) spawns with detached:
// true, stdio "ignore", and windowsHide: true, for which libuv's uv_spawn
// passes DETACHED_PROCESS, CREATE_NEW_PROCESS_GROUP, CREATE_NO_WINDOW, and
// SW_HIDE.
func TestSpawnInternalProcessWindowsFlags(t *testing.T) {
	child, err := SpawnInternalProcess("session-worker", []string{"-test.run=^$"}, InternalProcessSpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := TerminateInternalProcess(child); err != nil {
			t.Error(err)
		}
	})
	attributes := child.cmd.SysProcAttr
	want := uint32(windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS | windows.CREATE_NO_WINDOW)
	if attributes == nil || !attributes.HideWindow || attributes.CreationFlags != want {
		t.Fatalf("spawn attributes = %+v, want HideWindow and CreationFlags %#x", attributes, want)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
}
