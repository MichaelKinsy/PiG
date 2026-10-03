//go:build !windows

package shellconfig

import (
	"errors"
	"testing"
)

func TestUnixShellConfigResolutionOrder(t *testing.T) {
	none := func(string) bool { return false }
	notFound := func(string) (string, error) { return "", errors.New("not found") }
	onPath := func(name string) (string, error) { return "/opt/tools/" + name, nil }

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
	lookPath := func(name string) (string, error) {
		if name == "bash" {
			return termuxBash, nil
		}
		return "", errors.New("not found")
	}
	got := unixDefault(exists, lookPath)
	if got.Path != termuxBash || len(got.Args) != 1 || got.Args[0] != "-c" {
		t.Fatalf("Termux shell: %+v", got)
	}
}
