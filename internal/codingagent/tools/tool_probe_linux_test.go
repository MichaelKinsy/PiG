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

// find and grep keep their search in the caller's session and process group,
// so a terminal Ctrl+C in print mode reaches fd and rg with the agent, as in
// Pi. Only the @ autocomplete (raw mode, no ISIG) detaches its fd.
func TestFindAndGrepKeepSearchInCallersSession(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "mark")
	script := strings.ReplaceAll(detachCheck, "MARK", mark)
	fd := writeFakeBin(t, dir, "fdbin", script)
	rg := writeFakeBin(t, dir, "rgbin", script)
	_, _ = (&FindTool{CWD: dir, FdPath: fd}).Execute(context.Background(), "", []byte(`{"pattern":"*.go"}`), nil)
	_, _ = (&GrepTool{CWD: dir, RgPath: rg}).Execute(context.Background(), "", []byte(`{"pattern":"x"}`), nil)
	b, _ := os.ReadFile(mark)
	if got := string(b); got != "shared\nnullin\nshared\nnullin\n" {
		t.Fatalf("find and grep ran their search with %q, want the caller's session", got)
	}
}
