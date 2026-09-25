//go:build windows

package subprocess

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsUnixSocketPathsAreShortIsolatedAndCleaned(t *testing.T) {
	base, err := os.MkdirTemp(os.TempDir(), "pig ext sockets ")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	t.Setenv("TEMP", base)
	t.Setenv("TMP", base)
	t.Setenv("TMPDIR", base)

	first := NewHostWithConfigRoot(t.TempDir(), filepath.Join(t.TempDir(), "config with spaces"))
	second := NewHostWithConfigRoot(t.TempDir(), filepath.Join(t.TempDir(), "other config"))
	firstPath, err := first.sockPathFor("first")
	if err != nil {
		t.Fatal(err)
	}
	secondPath, err := second.sockPathFor("second")
	if err != nil {
		t.Fatal(err)
	}
	if firstPath == secondPath {
		t.Fatalf("independent hosts reused socket path %q", firstPath)
	}
	for _, path := range []string{firstPath, secondPath} {
		if len([]byte(path)) > unixSocketPathLimit("windows") {
			t.Fatalf("Windows AF_UNIX path has %d bytes: %q", len([]byte(path)), path)
		}
		if !strings.Contains(path, "pig ext sockets ") {
			t.Fatalf("socket path did not preserve spaces: %q", path)
		}
		listener, listenErr := net.Listen("unix", path)
		if listenErr != nil {
			t.Fatalf("listen on %q: %v", path, listenErr)
		}
		if closeErr := listener.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}

	firstRuntimeDir := first.sockRuntimeDir
	secondRuntimeDir := second.sockRuntimeDir
	first.Shutdown("test")
	second.Shutdown("test")
	for _, dir := range []string{firstRuntimeDir, secondRuntimeDir} {
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			t.Fatalf("socket runtime directory remains after shutdown: %q (%v)", dir, statErr)
		}
	}
}
