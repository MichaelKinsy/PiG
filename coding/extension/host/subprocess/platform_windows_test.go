//go:build windows

package subprocess

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestWindowsUnixSocketPathsAreShortIsolatedAndCleaned(t *testing.T) {
	// A short directory whose name has spaces is the temporary directory, so <TEMP>\pig keeps every socket path the Host
	// can produce (<TEMP>\pig\h<uint32>\e-9999.sock) within the AF_UNIX limit whatever length TEMP has on this machine.
	base := testenv.ShortTempDir(t, "pig ext ")
	t.Setenv("TEMP", base)
	t.Setenv("TMP", base)
	t.Setenv("TMPDIR", base)
	if socketDir := filepath.Join(base, "pig"); len(socketDir)+worstRuntimeSocketSuffix > unixSocketPathLimit("windows") {
		t.Fatalf("test temp directory %q is too deep to keep its socket directory", socketDir)
	}

	firstCwd, secondCwd, configRoot := t.TempDir(), t.TempDir(), t.TempDir()
	first := NewHostWithConfigRoot(firstCwd, filepath.Join(configRoot, "config with spaces"))
	second := NewHostWithConfigRoot(secondCwd, filepath.Join(configRoot, "other config"))
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
		if !strings.Contains(path, "pig ext ") {
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

// A TEMP too deep for the socket paths below it makes the Host use %LOCALAPPDATA%\pig\s, so the extension still starts.
func TestWindowsUnixSocketPathsFallBackWhenTempIsTooLong(t *testing.T) {
	cwd, configRoot := t.TempDir(), t.TempDir()
	// Both ShortTempDir calls precede the LOCALAPPDATA override: under a long TEMP ShortTempDir falls back to
	// %LOCALAPPDATA%\pig\s, and below the overridden directory the fallback exceeds its own length bound.
	deep := filepath.Join(testenv.ShortTempDir(t, "pig deep "), strings.Repeat("d", unixSocketPathLimit("windows")))
	localAppData := testenv.ShortTempDir(t, "pig local ")
	t.Setenv("LOCALAPPDATA", localAppData)
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEMP", deep)
	t.Setenv("TMP", deep)
	t.Setenv("TMPDIR", deep)

	host := NewHostWithConfigRoot(cwd, filepath.Join(configRoot, "config with spaces"))
	path, err := host.sockPathFor("deep")
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir := host.sockRuntimeDir
	if want := filepath.Join(localAppData, "pig", "s"); filepath.Dir(runtimeDir) != want {
		t.Fatalf("runtime directory %q is not below the fallback %q", runtimeDir, want)
	}
	if len([]byte(path)) > unixSocketPathLimit("windows") {
		t.Fatalf("Windows AF_UNIX path has %d bytes: %q", len([]byte(path)), path)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on %q: %v", path, err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	host.Shutdown("test")
	if _, err := os.Stat(runtimeDir); !os.IsNotExist(err) {
		t.Fatalf("socket runtime directory remains after shutdown: %q (%v)", runtimeDir, err)
	}
}
