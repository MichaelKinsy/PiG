//go:build unix

package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func writeFakeBin(t testing.TB, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// FDclone installs as /usr/bin/fd on Debian and opens /dev/tty; sharkdp's fd
// is fdfind there. The impostor comes first on PATH.
func TestLookupToolPathSkipsImpostorFdForFdfind(t *testing.T) {
	dir := t.TempDir()
	writeFakeBin(t, dir, "fd", "echo 'FDclone (Japanese file manager) 3.01'\n")
	want := writeFakeBin(t, dir, "fdfind", "echo 'fdfind 10.3.0'\n")
	t.Setenv("PATH", dir)
	if got := LookupToolPath("fd", ""); got != want {
		t.Fatalf("LookupToolPath(fd) = %q, want %q", got, want)
	}
	if got := NewToolsManager(t.TempDir()).GetToolPath("fd"); got != want {
		t.Fatalf("GetToolPath(fd) = %q, want %q", got, want)
	}
}

func TestLookupToolPathAcceptsPlainFd(t *testing.T) {
	dir := t.TempDir()
	want := writeFakeBin(t, dir, "fd", "echo 'fd 10.3.0'\n")
	t.Setenv("PATH", dir)
	if got := LookupToolPath("fd", ""); got != want {
		t.Fatalf("LookupToolPath(fd) = %q, want %q", got, want)
	}
}

func TestLookupToolPathRejectsLoneImpostor(t *testing.T) {
	for name, body := range map[string]string{
		"other output": "echo 'FDclone 3.01'\n",
		"exit status":  "echo 'fd 10.3.0'\nexit 3\n",
		"silent":       "exit 0\n",
		"prefix later": "echo 'not fd 10.3.0'\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFakeBin(t, dir, "fd", body)
			t.Setenv("PATH", dir)
			if got := LookupToolPath("fd", ""); got != "" {
				t.Fatalf("LookupToolPath(fd) = %q, want none", got)
			}
		})
	}
}

func TestLookupToolPathChecksRipgrepIdentity(t *testing.T) {
	dir := t.TempDir()
	rg := writeFakeBin(t, dir, "rg", "echo 'something else 1.0'\n")
	t.Setenv("PATH", dir)
	if got := LookupToolPath("rg", ""); got != "" {
		t.Fatalf("impostor rg accepted: %q", got)
	}
	writeFakeBin(t, dir, "rg", "echo 'ripgrep 14.1.0'\n")
	if got := LookupToolPath("rg", ""); got != rg {
		t.Fatalf("LookupToolPath(rg) = %q, want %q", got, rg)
	}
}

// The managed copy in <agentDir>/bin is PiG's own download and is not probed.
func TestLookupToolPathManagedCopyNeedsNoProbe(t *testing.T) {
	bin := t.TempDir()
	local := writeFakeBin(t, bin, "fd", "echo wrong\n")
	t.Setenv("PATH", t.TempDir())
	if got := LookupToolPath("fd", bin); got != local {
		t.Fatalf("got %q, want %q", got, local)
	}
}

func TestToolProbeCachesPerFileRevision(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	p := writeFakeBin(t, dir, "fd", "echo run >> "+count+"\necho 'fd 10.3.0'\n")
	t.Setenv("PATH", dir)
	runs := func() int {
		b, _ := os.ReadFile(count)
		return strings.Count(string(b), "run")
	}
	for range 5 {
		if LookupToolPath("fd", "") != p {
			t.Fatal("fd not found")
		}
	}
	if n := runs(); n != 1 {
		t.Fatalf("probe ran %d times for an unchanged binary, want 1", n)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	LookupToolPath("fd", "")
	LookupToolPath("fd", "")
	if n := runs(); n != 2 {
		t.Fatalf("probe ran %d times after the binary changed, want 2", n)
	}
}

// A hung --version must not stall lookup forever, the timeout must end the
// wrapper's children too, and the verdict of a cut-short probe must not stick.
func TestProbeToolVersionTimesOutHungProgramAndItsChildren(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out toolProbeTimeout")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	// Not exec: sleep is a grandchild that only a group kill reaches.
	p := writeFakeBin(t, dir, "fd", "sleep 60 &\necho $! > "+pidFile+"\nwait\n")
	t.Setenv("PATH", dir)
	start := time.Now()
	if ok, completed := probeToolVersion(p, []string{"fd "}); ok || completed {
		t.Fatal("hung program accepted or its timeout counted as a verdict")
	}
	if d := time.Since(start); d > toolProbeTimeout+3*time.Second {
		t.Fatalf("probe took %v", d)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for range 50 {
		if syscall.Kill(pid, 0) != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("the probe's child %d outlived the timeout", pid)
	}
	if got := systemToolIdentity(p, "fd", []string{"fd "}); got {
		t.Fatal("hung program accepted")
	}
	if _, cached := toolProbeCache.Load(toolProbeKeyFor(t, p, "fd")); cached {
		t.Fatal("a timed-out probe was cached")
	}
}

func toolProbeKeyFor(t *testing.T, path, tool string) toolProbeKey {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return toolProbeKey{path: path, tool: tool, modTime: info.ModTime(), size: info.Size()}
}

// A failed start is no verdict either: the file may still be being written.
func TestProbeToolVersionDoesNotCacheStartFailure(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fd")
	if err := os.WriteFile(p, []byte("#!/nonexistent-interpreter\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, completed := probeToolVersion(p, []string{"fd "}); ok || completed {
		t.Fatalf("start failure gave ok=%v completed=%v", ok, completed)
	}
	if systemToolIdentity(p, "fd", []string{"fd "}) {
		t.Fatal("unstartable program accepted")
	}
	if _, cached := toolProbeCache.Load(toolProbeKeyFor(t, p, "fd")); cached {
		t.Fatal("a start failure was cached")
	}
}

// Pi's libuv starts a file with no #! line through /bin/sh (glibc's execvp
// fallback), and so do the searches; the probe has to start it the same way.
func TestLookupToolPathAcceptsFdWrapperWithoutShebang(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("posix_spawn on macOS does not run a #!-less file through sh")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "fd")
	if err := os.WriteFile(p, []byte("echo 'fd 10.3.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if got := LookupToolPath("fd", ""); got != p {
		t.Fatalf("LookupToolPath(fd) = %q, want %q", got, p)
	}
}

func BenchmarkLookupToolPathProbed(b *testing.B) {
	dir := b.TempDir()
	writeFakeBin(b, dir, "fd", "echo 'fd 10.3.0'\n")
	b.Setenv("PATH", dir)
	b.ReportAllocs()
	for b.Loop() {
		if LookupToolPath("fd", "") == "" {
			b.Fatal("fd not found")
		}
	}
}

// One uncached probe: the cost LookupToolPath pays once per binary revision.
func BenchmarkProbeToolVersionUncached(b *testing.B) {
	dir := b.TempDir()
	p := writeFakeBin(b, dir, "fd", "echo 'fd 10.3.0'\n")
	b.ReportAllocs()
	for b.Loop() {
		if ok, completed := probeToolVersion(p, []string{"fd "}); !ok || !completed {
			b.Fatal("probe failed")
		}
	}
}
