//go:build darwin

package daemon

// Ports the macOS branch of packages/env/daemon/src/pty.rs: the foreground leader's name is libproc's proc_name, up to
// 32 characters, not the kernel's 16-character command name.

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessNameIsProcNameNotTheSixteenCharacterCommandName(t *testing.T) {
	source, err := os.Open("/bin/sleep")
	if err != nil {
		t.Skip("no /bin/sleep to copy")
	}
	defer func() { _ = source.Close() }()
	for _, name := range []string{"sleep", "pi-env-name-over-sixteen", "pi-env-name-of-thirty-one-chars-xx"} {
		path := filepath.Join(t.TempDir(), name)
		target, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o755)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := source.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(target, source); err != nil {
			t.Fatal(err)
		}
		if err := target.Close(); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(path, "60")
		if err := command.Start(); err != nil {
			t.Fatalf("starting the copy of sleep as %s: %v", name, err)
		}
		t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
		got, ok := processName(command.Process.Pid)
		want := name[:min(len(name), 2*maxComLen-1)]
		if !ok || got != want {
			t.Errorf("processName(%s) = %q, %v; want %q (%d characters)", name, got, ok, want, len(want))
		}
		if len(name) > maxComLen && !strings.HasPrefix(got, name[:maxComLen+1]) {
			t.Errorf("processName(%s) = %q is cut at the 16-character command name", name, got)
		}
	}
	if _, ok := processName(1 << 30); ok {
		t.Error("a process that does not exist has a name")
	}
}
