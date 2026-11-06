//go:build unix

package tools

import (
	"os"
	"path/filepath"
	"strings"
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

// A hung --version must not stall lookup forever; this checks the probe is
// bounded without waiting out the production timeout.
func TestProbeToolVersionTimesOutHungProgram(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out toolProbeTimeout")
	}
	dir := t.TempDir()
	p := writeFakeBin(t, dir, "fd", "exec sleep 60\n")
	start := time.Now()
	if probeToolVersion(p, []string{"fd "}) {
		t.Fatal("hung program accepted")
	}
	if d := time.Since(start); d > toolProbeTimeout+3*time.Second {
		t.Fatalf("probe took %v", d)
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
		if !probeToolVersion(p, []string{"fd "}) {
			b.Fatal("probe failed")
		}
	}
}
