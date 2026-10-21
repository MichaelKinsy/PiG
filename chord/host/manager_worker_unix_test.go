//go:build unix

package host

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// workerPipe is the pipe the relay (testdata/manager_relay.mjs) gave the worker on descriptor 3: a socket pair.
func workerPipe() (io.ReadWriteCloser, error) { return net.FileConn(os.NewFile(3, "ipc")) }

// alive is manager.test.ts alive(pid): process.kill(pid, 0).
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// manager.ts (Pi cd6b2a55) lockSocketPath, on every Unix (Linux and Android hold their lock in the abstract namespace,
// so only this test reaches it there): a socket path that fits in sun_path stays in the temporary directory; a longer
// one moves to /tmp/chord2-<uid>, a directory only its owner can access, where a socket can listen.
func TestLockSocketPathKeepsTheSocketPathWithinSunPath(t *testing.T) {
	// The deep path falls back to /tmp/chord2-<uid>, which every process of the user shares: the name carries the pid, so
	// processes that run this test at once (parallel test binaries, a stress harness) do not bind the same socket.
	file := "chord2-" + strconv.Itoa(os.Getpid()) + "-0123456789abcdef.sock"
	short, err := os.MkdirTemp("/tmp", "lk") //portlint:allow hardtmp the test needs a short real path under sun_path, as Pi's /tmp/chord2-<uid> lock dir
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	t.Setenv("TMPDIR", short)
	if path, err := lockSocketPath(file); err != nil || path != filepath.Join(short, file) {
		t.Fatalf("short TMPDIR: %q, %v", path, err)
	}
	deep := filepath.Join(short, strings.Repeat("a", 60), strings.Repeat("b", 60))
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", deep)
	path, err := lockSocketPath(file)
	if err != nil {
		t.Fatal(err)
	}
	directory := "/tmp/chord2-" + strconv.Itoa(os.Getuid())
	if path != filepath.Join(directory, file) || len(path) > maxSocketPathBytes {
		t.Fatalf("deep TMPDIR: %q", path)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("lock directory %v, %v", info, err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
}
