//go:build unix

package routing_test

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

func makeUnixServer(t *testing.T, path string) *routing.Server {
	t.Helper()
	server, err := routing.CreateUnixServer(newTestServerHost(), routing.UnixServerOptions{Path: path, ServerId: testServerID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

func expectHello(t *testing.T, client *unixTestClient, serverID string) {
	t.Helper()
	message := client.hello(t)
	hello, isHello := message.(protocol.ServerHello)
	if !isHello || (serverID != "" && hello.ServerId != serverID) {
		t.Fatalf("hello reply = %#v, want hello from %q", message, serverID)
	}
}

func socketPath(t *testing.T, nested bool) string {
	t.Helper()
	directory := shortDirectory(t, "ps-")
	if nested {
		return filepath.Join(directory, "p", "n", "server.sock")
	}
	return filepath.Join(directory, "server.sock")
}

// upstream: packages/server/test/unix.test.ts:41 "creates an in-memory server ID and derives its explicit Unix socket path"
func TestUnixServerDerivesItsExplicitSocketPathAndRestartsOnIt(t *testing.T) {
	directory := shortDirectory(t, "pi-server-")
	path, err := routing.GetUnixSocketPath(testServerID, directory)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(directory, testServerID+".sock"); path != want {
		t.Fatalf("socket path = %q, want %q", path, want)
	}
	first := makeUnixServer(t, path)
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	firstClient := connectUnixTestClient(t, path)
	expectHello(t, firstClient, testServerID)
	if err := firstClient.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	replacement := makeUnixServer(t, path)
	if err := replacement.Start(); err != nil {
		t.Fatal(err)
	}
	expectHello(t, connectUnixTestClient(t, path), testServerID)
	for _, invalid := range []string{"", "invalid-server"} {
		if _, err := routing.GetUnixSocketPath(invalid, directory); err == nil {
			t.Fatalf("GetUnixSocketPath accepted %q", invalid)
		}
	}
}

func TestUnixListenerFilesystemLifecycle(t *testing.T) {
	// upstream: packages/server/test/unix.test.ts:68 "rejects a live listener without unlinking it"
	t.Run("rejects a live listener without unlinking it", func(t *testing.T) {
		path := socketPath(t, false)
		first := makeUnixServer(t, path)
		if err := first.Start(); err != nil {
			t.Fatal(err)
		}
		firstIdentity, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		expectError(t, makeUnixServer(t, path).Start(), "already running")
		current, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if current.Mode()&os.ModeSocket == 0 || !os.SameFile(firstIdentity, current) {
			t.Fatal("the live listener's socket inode was replaced or unlinked")
		}
		expectHello(t, connectUnixTestClient(t, path), "")
	})

	// upstream: packages/server/test/unix.test.ts:88 "never unlinks a regular file at the configured path"
	t.Run("never unlinks a regular file at the configured path", func(t *testing.T) {
		path := socketPath(t, false)
		if err := os.WriteFile(path, []byte("do not remove"), 0o640); err != nil {
			t.Fatal(err)
		}
		expectError(t, makeUnixServer(t, path).Start(), "non-socket")
		if content, err := os.ReadFile(path); err != nil || string(content) != "do not remove" {
			t.Fatalf("regular file = %q, %v", content, err)
		}
	})

	// upstream: packages/server/test/unix.test.ts:96 "creates nested temp parents, restricts permissions, and removes its own socket"
	t.Run("creates nested temp parents, restricts permissions, and removes its own socket", func(t *testing.T) {
		path := socketPath(t, true)
		server := makeUnixServer(t, path)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSocket == 0 {
			t.Fatal("configured path is not a socket")
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("socket mode = %o, want 600", info.Mode().Perm())
		}
		entries, err := os.ReadDir(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if names := entryNames(entries); !slices.Equal(names, []string{"server.sock"}) {
			t.Fatalf("socket directory = %v, want only server.sock", names)
		}
		if err := server.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("socket path after close = %v, want ENOENT", err)
		}
	})

	// upstream: packages/server/test/unix.test.ts:109 "does not remove a replacement inode during shutdown"
	t.Run("does not remove a replacement inode during shutdown", func(t *testing.T) {
		path := socketPath(t, false)
		server := makeUnixServer(t, path)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := server.Close(); err != nil {
			t.Fatal(err)
		}
		if content, err := os.ReadFile(path); err != nil || string(content) != "replacement" {
			t.Fatalf("replacement = %q, %v", content, err)
		}
	})

	// upstream: packages/server/test/unix.test.ts:122 "removes a genuinely stale socket before binding"; the fixture is a killed process that owned the listening socket (fixtures/stale-socket-server.mjs).
	t.Run("removes a genuinely stale socket before binding", func(t *testing.T) {
		path := socketPath(t, false)
		child := exec.Command(os.Args[0], "-test.run=^TestStaleSocketOwnerProcess$", "--", path)
		child.Env = append(os.Environ(), "PIG_ROUTING_STALE_SOCKET_OWNER=1")
		stdout, err := child.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
		listening := make([]byte, 1)
		if _, err := stdout.Read(listening); err != nil {
			t.Fatalf("stale socket owner never reported listening: %v", err)
		}
		staleIdentity, err := os.Lstat(path)
		if err != nil || staleIdentity.Mode()&os.ModeSocket == 0 {
			t.Fatalf("stale fixture path = %v, %v", staleIdentity, err)
		}
		if err := child.Process.Signal(syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		_ = child.Wait()
		server := makeUnixServer(t, path)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		live, err := os.Lstat(path)
		if err != nil || live.Mode()&os.ModeSocket == 0 {
			t.Fatalf("live socket = %v, %v; want a socket", live, err)
		}
		expectHello(t, connectUnixTestClient(t, path), "")
	})
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}

// TestStaleSocketOwnerProcess is the child process for the stale-socket case (fixtures/stale-socket-server.mjs): it listens on argv[path], reports one byte, and waits to be killed.
func TestStaleSocketOwnerProcess(t *testing.T) {
	if os.Getenv("PIG_ROUTING_STALE_SOCKET_OWNER") != "1" {
		t.Skip("child process of the stale-socket case")
	}
	path := os.Args[len(os.Args)-1]
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	if _, err := os.Stdout.Write([]byte("1")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Minute)
}
