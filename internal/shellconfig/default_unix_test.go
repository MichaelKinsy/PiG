//go:build !windows

package shellconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnixShellConfigResolutionOrder(t *testing.T) {
	none := func(string) bool { return false }
	notFound := func(string) string { return "" }
	onPath := func(name string) string { return "/opt/tools/" + name }

	if got := unixDefault(func(p string) bool { return p == "/bin/bash" }, onPath); got.Path != "/bin/bash" {
		t.Fatalf("with /bin/bash: %+v", got)
	}
	if got := unixDefault(none, onPath); got.Path != "/opt/tools/bash" || got.Args[0] != "-c" {
		t.Fatalf("bash on PATH: %+v", got)
	}
	if got := unixDefault(none, notFound); got.Path != "sh" || got.Args[0] != "-c" {
		t.Fatalf("sh fallback: %+v", got)
	}
}

// Termux has no /bin: bash lives in $PREFIX/bin, found through PATH as upstream's `which bash` finds it.
func TestUnixShellConfigFindsTermuxBashWithoutBinBash(t *testing.T) {
	const termuxBash = "/data/data/com.termux/files/usr/bin/bash"
	exists := func(path string) bool { return path == termuxBash }
	lookPath := func(name string) string {
		if name == "bash" {
			return termuxBash
		}
		return ""
	}
	got := unixDefault(exists, lookPath)
	if got.Path != termuxBash || len(got.Args) != 1 || got.Args[0] != "-c" {
		t.Fatalf("Termux shell: %+v", got)
	}
}

// Upstream's findExecutableOnPath runs `which` and trusts its first line, so a path that exec.LookPath rejects (here a
// file without the execute bit, as on Termux and other special file systems) is still the shell. A silent or failing
// `which` finds nothing.
func TestFindExecutableOnPathTrustsTheFirstLineOfWhich(t *testing.T) {
	dir := t.TempDir()
	write := func(script string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "which"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	write("#!/bin/sh\nprintf '/not/executable/bash\\n/second/bash\\n'\n")
	if got := FindExecutableOnPath("bash"); got != "/not/executable/bash" {
		t.Fatalf("which output: got %q, want its first line", got)
	}
	write("#!/bin/sh\nexit 1\n")
	if got := FindExecutableOnPath("bash"); got != "" {
		t.Fatalf("failing which: got %q, want none", got)
	}
	// The command receives the executable name as its only argument.
	write("#!/bin/sh\nprintf '%s:%s' \"$#\" \"$1\"\n")
	if got := FindExecutableOnPath("rg"); got != "1:rg" {
		t.Fatalf("which argv: got %q, want 1:rg", got)
	}
}
