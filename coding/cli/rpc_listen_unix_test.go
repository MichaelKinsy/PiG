//go:build !windows

package cli

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

// TestListenSocketIsPrivateUnderAPermissiveUmask: the --listen socket is 0600 whatever umask the server inherited (D232). The server inherits the test's umask, so a permissive one here would show at the socket without the private bind.
func TestListenSocketIsPrivateUnderAPermissiveUmask(t *testing.T) {
	old := syscall.Umask(0)
	server := newListenServer(t)
	syscall.Umask(old)
	client := server.dial()
	defer client.closeInput()
	info, err := os.Stat(server.path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket mode %o, want 0600", perm)
	}
}

// TestListenPrivateSocketIsPrivateWithoutChangingTheProcessUmask: binding the socket must not change the umask for the rest of the process, not even for the time of the bind: a goroutine creating files meanwhile gets its own mode (D232). Under a umask of 0, a file created with 0666 must stay 0666 on every iteration; a bind that sets umask 0177 around net.Listen turns some of them into 0600.
func TestListenPrivateSocketIsPrivateWithoutChangingTheProcessUmask(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	dir := shortTempDir(t)
	files := t.TempDir()
	var stop atomic.Bool
	var wg sync.WaitGroup
	var wrong atomic.Int64
	var created atomic.Int64
	for w := range 4 {
		wg.Go(func() {
			path := filepath.Join(files, "f"+string(rune('a'+w)))
			for !stop.Load() {
				_ = os.Remove(path)
				f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
				if err != nil {
					continue
				}
				info, err := f.Stat()
				_ = f.Close()
				if err == nil {
					created.Add(1)
					if info.Mode().Perm() != 0o666 {
						wrong.Add(1)
					}
				}
			}
		})
	}
	for i := range 300 {
		path := filepath.Join(dir, "s.sock")
		listener, err := listenPrivateUnixSocket(path)
		if err != nil {
			stop.Store(true)
			wg.Wait()
			t.Fatalf("bind %d: %v", i, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("socket mode %o, want 0600", perm)
		}
		_ = listener.Close()
		_ = os.Remove(path)
	}
	stop.Store(true)
	wg.Wait()
	if created.Load() == 0 {
		t.Fatal("the probe goroutines created no file")
	}
	if n := wrong.Load(); n != 0 {
		t.Fatalf("%d of %d files created during the binds lost permission bits: the bind changed the process umask", n, created.Load())
	}
}

// TestListenPrivateSocketLeavesOnlyTheSocket: the private bind directory is removed, a bound socket accepts at path, and a path that appeared meanwhile is never replaced (D232).
func TestListenPrivateSocketLeavesOnlyTheSocket(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.sock")
	listener, err := listenPrivateUnixSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "s.sock" || entries[0].Type()&os.ModeSocket == 0 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name() + " " + e.Type().String()
		}
		t.Fatalf("directory holds %v, want only the socket", names)
	}
	accepted := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
		accepted <- err
	}()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	if err := <-accepted; err != nil {
		t.Fatalf("accept: %v", err)
	}

	other := filepath.Join(dir, "taken")
	if err := os.WriteFile(other, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := listenPrivateUnixSocket(other); !errors.Is(err, os.ErrExist) {
		t.Fatalf("bind over an existing path: %v, want file exists", err)
	}
	if data, _ := os.ReadFile(other); !strings.HasPrefix(string(data), "mine") {
		t.Fatalf("the existing file was replaced: %q", data)
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("a refused bind left %d entries, want the socket and the existing file", len(entries))
	}
}

// TestListenPrivateSocketRefusesAPathALongerThanASocketAddress: a path the kernel cannot connect to is refused, not linked into place (D232).
func TestListenPrivateSocketRefusesAPathALongerThanASocketAddress(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, strings.Repeat("a", len(syscall.RawSockaddrUnix{}.Path)))
	if _, err := listenPrivateUnixSocket(path); err == nil || !strings.Contains(err.Error(), "longer than the") {
		t.Fatalf("bind of a %d-byte path: %v, want a refusal", len(path), err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a refused bind left %d entries", len(entries))
	}
}
