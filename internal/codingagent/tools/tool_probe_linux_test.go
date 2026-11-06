//go:build linux

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The script records whether it is a session leader (no controlling terminal
// is possible for one) and whether stdin is the null device.
const detachCheck = `sid=$(cut -d' ' -f6 /proc/$$/stat)
[ "$sid" = "$$" ] && echo leader >> MARK || echo shared >> MARK
[ "$(readlink /proc/$$/fd/0)" = /dev/null ] && echo nullin >> MARK || echo ttyin >> MARK
`

func TestVersionProbeCannotReachTheTerminal(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "mark")
	writeFakeBin(t, dir, "fd", strings.ReplaceAll(detachCheck, "MARK", mark)+"echo 'fd 10.3.0'\n")
	// The script needs cut and readlink; the impostor-free fd in dir still wins.
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	if LookupToolPath("fd", "") != filepath.Join(dir, "fd") {
		t.Fatal("fd not found")
	}
	b, _ := os.ReadFile(mark)
	if got := string(b); got != "leader\nnullin\n" {
		t.Fatalf("probe ran with %q, want a new session and /dev/null stdin", got)
	}
}

func TestFindRunsFdInANewSession(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "mark")
	fd := writeFakeBin(t, dir, "fdbin", strings.ReplaceAll(detachCheck, "MARK", mark))
	_, _ = (&FindTool{CWD: dir, FdPath: fd}).Execute(context.Background(), "", []byte(`{"pattern":"*.go"}`), nil)
	b, _ := os.ReadFile(mark)
	if got := string(b); !strings.HasPrefix(got, "leader\n") {
		t.Fatalf("find ran fd with %q, want a new session", got)
	}
}
