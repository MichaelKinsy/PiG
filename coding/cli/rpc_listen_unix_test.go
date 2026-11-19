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

// requireListenRefusesPath starts a server on path and requires the refusal of an existing path: exit 1 and the message, with the server never reaching its listener.
func requireListenRefusesPath(t *testing.T, dir, path string) {
	t.Helper()
	server := startListenServer(t, listenEnv(t, dir), "--mode", "rpc", "--listen", path, "--model", "test-faux/echo", "--no-session")
	if code := server.waitForExit(); code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, server.stderr())
	}
	if want := "pig --listen: " + path + " exists"; !strings.Contains(server.stderr(), want) {
		t.Fatalf("stderr %q does not contain %q", server.stderr(), want)
	}
}

// TestListenRefusesAnyKindOfExistingPathAndRemovesNothing: a mistyped path must never cost the user a file. Whatever exists at the path (a directory, a stale socket, a live socket, a symlink to a file, a dangling symlink) is refused and left as it was; PiG never unlinks a path it did not create (D232).
func TestListenRefusesAnyKindOfExistingPathAndRemovesNothing(t *testing.T) {
	dir := listenTempDir(t)
	keep := filepath.Join(dir, "keep")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	listenOn := func(t *testing.T, path string, unlink bool) *net.UnixListener {
		t.Helper()
		l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		l.SetUnlinkOnClose(unlink)
		return l
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, path string) (check func(t *testing.T))
	}{
		{"directory", func(t *testing.T, path string) func(*testing.T) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			inside := filepath.Join(path, "inside")
			if err := os.WriteFile(inside, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			return func(t *testing.T) {
				if data, err := os.ReadFile(inside); err != nil || string(data) != "keep" {
					t.Fatalf("the directory or its file was removed or changed: %q, %v", data, err)
				}
			}
		}},
		{"stale socket", func(t *testing.T, path string) func(*testing.T) {
			if err := listenOn(t, path, false).Close(); err != nil {
				t.Fatal(err)
			}
			return func(t *testing.T) {
				if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSocket == 0 {
					t.Fatalf("the stale socket was removed or replaced: %v, %v", info, err)
				}
			}
		}},
		{"live socket", func(t *testing.T, path string) func(*testing.T) {
			l := listenOn(t, path, true)
			t.Cleanup(func() { _ = l.Close() })
			return func(t *testing.T) {
				conn, err := net.Dial("unix", path)
				if err != nil {
					t.Fatalf("the live socket no longer accepts connections: %v", err)
				}
				_ = conn.Close()
			}
		}},
		{"symlink to a file", func(t *testing.T, path string) func(*testing.T) {
			if err := os.Symlink(keep, path); err != nil {
				t.Fatal(err)
			}
			return func(t *testing.T) {
				if target, err := os.Readlink(path); err != nil || target != keep {
					t.Fatalf("the symlink was removed or replaced: %q, %v", target, err)
				}
				if data, err := os.ReadFile(keep); err != nil || string(data) != "keep" {
					t.Fatalf("the file behind the symlink was removed or changed: %q, %v", data, err)
				}
			}
		}},
		{"dangling symlink", func(t *testing.T, path string) func(*testing.T) {
			missing := filepath.Join(dir, "missing")
			if err := os.Symlink(missing, path); err != nil {
				t.Fatal(err)
			}
			return func(t *testing.T) {
				if target, err := os.Readlink(path); err != nil || target != missing {
					t.Fatalf("the symlink was removed or replaced: %q, %v", target, err)
				}
				if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("a socket was created behind the dangling symlink: %v", err)
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".sock")
			check := tc.setup(t, path)
			requireListenRefusesPath(t, dir, path)
			check(t)
		})
	}
}

// TestListenFollowsASymlinkedParentAndStaysPrivate: a symlink in a parent component of the path is followed, as the kernel resolves every path (macOS /tmp is one), so the socket appears in the directory the link names, still mode 0600. The doc tells the user to keep the socket in a directory only they can write, because whoever can swap that link can point the socket elsewhere (D232).
func TestListenFollowsASymlinkedParentAndStaysPrivate(t *testing.T) {
	dir := listenTempDir(t)
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(link, "pig.sock")
	server := startListenServer(t, listenEnv(t, dir), "--mode", "rpc", "--listen", path, "--model", "test-faux/echo", "--no-session")
	server.path = path
	client := server.dial()
	defer client.closeInput()
	info, err := os.Lstat(filepath.Join(real, "pig.sock"))
	if err != nil {
		t.Fatalf("the socket is not in the directory the link names: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v, want a socket of mode 0600", info.Mode())
	}
}

// The private bind directory is what keeps the socket unreachable in the window before its chmod: it must be 0700 whatever the umask.
func TestMakePrivateBindDirIsOwnerOnly(t *testing.T) {
	dir, err := makePrivateBindDir(listenTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("the private bind directory has mode %v, want 0700", info.Mode().Perm())
	}
}
