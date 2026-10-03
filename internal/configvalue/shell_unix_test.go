//go:build !windows

package configvalue

import "testing"

// Node's child_process runs a shell command through /bin/sh, and through
// /system/bin/sh when process.platform is "android" (Termux has no /bin).
func TestDefaultShellFollowsNodeOnAndroid(t *testing.T) {
	for goos, want := range map[string]string{
		"linux":   "/bin/sh",
		"darwin":  "/bin/sh",
		"freebsd": "/bin/sh",
		"android": "/system/bin/sh",
	} {
		if got := defaultShell(goos); got != want {
			t.Errorf("defaultShell(%q) = %q, want %q", goos, got, want)
		}
	}
}
