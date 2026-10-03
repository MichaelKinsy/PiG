//go:build !windows

package client

// Ports packages/client/test/unix.test.ts.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

// shortDirectory keeps socket paths under the Unix path limit, as upstream's mkdtemp(join("/tmp", "pc-")).
func shortDirectory(t *testing.T, prefix string) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

func discoveryServerId(value int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", value)
}

// startDiscoveryServer is upstream startServer: a runtime Server on <fileServerId>.sock that reports reportedServerId in its hello.
func startDiscoveryServer(t *testing.T, directory, fileServerId, reportedServerId string) {
	t.Helper()
	if reportedServerId == "" {
		reportedServerId = fileServerId
	}
	listener, err := routing.CreateUnixListener(routing.UnixListenerOptions{Path: filepath.Join(directory, fileServerId+".sock")})
	if err != nil {
		t.Fatal(err)
	}
	server, err := routing.NewServer(routing.ServerHost{
		ServerServices: routingtest.CreateTestServerServices(),
		ResolveSession: func(context.Context, string) (routing.SessionMetadata, error) {
			return nil, errors.New("unused")
		},
		OpenSession: func(context.Context, routing.SessionMetadata) (routing.RoutedSessionHandle, error) {
			return nil, errors.New("unused")
		},
	}, routing.ServerOptions{Listeners: []routing.ServerListener{listener}, ServerId: reportedServerId})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
}

// socketConnections counts the silent servers' connections. active is what the servers have observed, as upstream's counter; seen records every value it took, so a check made after discovery sees each value upstream's poll could sample. maximum is the most connections whose peer had not closed at any accept: the server observes a close only when its reader goroutine runs, so an observed count can still include a connection the client closed before it dialed the next one, which upstream's single event loop cannot show.
type socketConnections struct {
	mu                     sync.Mutex
	active, maximum, total int
	open                   []net.Conn
	seen                   map[int]bool
	changed                chan struct{}
}

func (connections *socketConnections) accepted(conn net.Conn) {
	connections.mu.Lock()
	defer connections.mu.Unlock()
	connections.open = slices.DeleteFunc(connections.open, peerClosed)
	connections.open = append(connections.open, conn)
	connections.active++
	connections.total++
	connections.maximum = max(connections.maximum, len(connections.open))
	connections.notifyLocked()
}

func (connections *socketConnections) closed() {
	connections.mu.Lock()
	defer connections.mu.Unlock()
	connections.active--
	connections.notifyLocked()
}

func (connections *socketConnections) notifyLocked() {
	connections.seen[connections.active] = true
	close(connections.changed)
	connections.changed = make(chan struct{})
}

// peerClosed reports whether the peer of a unix connection has closed, discarding any bytes it sent first. Control, unlike Read, does not wait for a goroutine already blocked reading the connection.
func peerClosed(conn net.Conn) bool {
	raw, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return true
	}
	closed := false
	if err := raw.Control(func(fd uintptr) {
		buffer := make([]byte, 512)
		for {
			n, _, err := syscall.Recvfrom(int(fd), buffer, syscall.MSG_DONTWAIT)
			switch {
			case err == syscall.EAGAIN:
				return
			case err != nil || n == 0:
				closed = true
				return
			}
		}
	}); err != nil {
		return true
	}
	return closed
}

// startSilentSocket accepts connections and never answers, counting live connections like upstream's raw net server.
func startSilentSocket(t *testing.T, path string, connections *socketConnections) {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	var mu sync.Mutex
	var open []net.Conn
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		for _, conn := range open {
			_ = conn.Close()
		}
		mu.Unlock()
		group.Wait()
	})
	group.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			open = append(open, conn)
			mu.Unlock()
			if connections != nil {
				connections.accepted(conn)
			}
			group.Go(func() {
				buffer := make([]byte, 1024)
				for {
					if _, err := conn.Read(buffer); err != nil {
						break
					}
				}
				_ = conn.Close()
				if connections != nil {
					connections.closed()
				}
			})
		}
	})
}

func discover(t *testing.T, options DiscoverUnixServersOptions) []UnixServerRoute {
	t.Helper()
	routes, err := DiscoverUnixServers(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	return routes
}

//go:fix inline
func timeoutMs(value float64) *float64 { return new(value) }

func assertSocket(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("lstat %s = %v, %v; want a socket", path, info, err)
	}
}

func TestUnixUpstream(t *testing.T) {
	t.Parallel()
	t.Run("discoverUnixServers › returns no routes when the server directory is missing", func(t *testing.T) {
		// upstream: packages/client/test/unix.test.ts:94
		t.Parallel()
		directory := filepath.Join(shortDirectory(t, "pc-"), "missing")
		if routes := discover(t, DiscoverUnixServersOptions{Directory: directory}); routes == nil || len(routes) != 0 {
			t.Fatalf("routes = %#v; want []", routes)
		}
	})
	t.Run("discoverUnixServers › discovers reachable servers in server ID order", func(t *testing.T) {
		// upstream: packages/client/test/unix.test.ts:99
		t.Parallel()
		directory := shortDirectory(t, "pc-")
		first, second := discoveryServerId(1), discoveryServerId(2)
		startDiscoveryServer(t, directory, second, "")
		startDiscoveryServer(t, directory, first, "")

		want := []UnixServerRoute{{ServerId: first, Path: filepath.Join(directory, first+".sock")}, {ServerId: second, Path: filepath.Join(directory, second+".sock")}}
		if routes := discover(t, DiscoverUnixServersOptions{Directory: directory}); !slices.Equal(routes, want) {
			t.Fatalf("routes = %v; want %v", routes, want)
		}
	})
	t.Run("discoverUnixServers › ignores malformed entries, non-sockets, and mismatched servers", func(t *testing.T) {
		// upstream: packages/client/test/unix.test.ts:112
		t.Parallel()
		directory := shortDirectory(t, "pc-")
		for name, content := range map[string]string{discoveryServerId(1) + ".sock": "not a socket", "not-a-server.sock": "ignored"} {
			if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Mkdir(filepath.Join(directory, discoveryServerId(2)+".sock"), 0o700); err != nil {
			t.Fatal(err)
		}
		startDiscoveryServer(t, directory, discoveryServerId(3), discoveryServerId(4))

		if routes := discover(t, DiscoverUnixServersOptions{Directory: directory}); len(routes) != 0 {
			t.Fatalf("routes = %v; want []", routes)
		}
	})
	t.Run("discoverUnixServers › ignores stale sockets without deleting them", func(t *testing.T) {
		// upstream: packages/client/test/unix.test.ts:122
		t.Parallel()
		directory := shortDirectory(t, "pc-")
		path := filepath.Join(directory, discoveryServerId(1)+".sock")
		// Upstream SIGKILLs a child that listens on the path; the socket file stays and connections are refused. A listener closed without unlinking leaves the same state.
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		listener.SetUnlinkOnClose(false)
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}

		if routes := discover(t, DiscoverUnixServersOptions{Directory: directory}); len(routes) != 0 {
			t.Fatalf("routes = %v; want []", routes)
		}
		assertSocket(t, path)
	})
	t.Run("discoverUnixServers › times out an unresponsive socket without deleting it", func(t *testing.T) {
		// upstream: packages/client/test/unix.test.ts:139
		t.Parallel()
		directory := shortDirectory(t, "pc-")
		path := filepath.Join(directory, discoveryServerId(1)+".sock")
		startSilentSocket(t, path, nil)

		if routes := discover(t, DiscoverUnixServersOptions{Directory: directory, TimeoutMs: new(float64(20))}); len(routes) != 0 {
			t.Fatalf("routes = %v; want []", routes)
		}
		assertSocket(t, path)
	})
	t.Run("discoverUnixServers › limits concurrent probes to 16", func(t *testing.T) {
		// upstream: packages/client/test/unix.test.ts:149
		t.Parallel()
		directory := shortDirectory(t, "pc-")
		connections := &socketConnections{seen: map[int]bool{}, changed: make(chan struct{})}
		for index := 1; index <= 20; index++ {
			startSilentSocket(t, filepath.Join(directory, discoveryServerId(index)+".sock"), connections)
		}

		type outcome struct {
			routes []UnixServerRoute
			err    error
		}
		discovery := make(chan outcome, 1)
		go func() {
			routes, err := DiscoverUnixServers(t.Context(), DiscoverUnixServersOptions{Directory: directory, TimeoutMs: new(float64(100))})
			discovery <- outcome{routes, err}
		}()
		result := <-discovery
		if result.err != nil || len(result.routes) != 0 {
			t.Fatalf("discovery = %v, %v; want []", result.routes, result.err)
		}
		// A dial completes from the listen backlog, so the server's accept loop may still be counting the last connections when discovery returns.
		for {
			connections.mu.Lock()
			total, changed := connections.total, connections.changed
			connections.mu.Unlock()
			if total > 20 {
				t.Fatalf("total probe connections = %d; want 20", total)
			}
			if total == 20 {
				break
			}
			select {
			case <-changed:
			case <-t.Context().Done():
				t.Fatalf("total probe connections = %d; want 20", total)
			}
		}
		// Upstream polls active to 16 and then reads the maximum. Checked once every probe is counted, a limit that skips 16 fails instead of waiting for a value that never comes, and the maximum also covers every later probe, which a close seen late by the server must not inflate (see socketConnections).
		connections.mu.Lock()
		reached, active, maximum := connections.seen[16], connections.active, connections.maximum
		connections.mu.Unlock()
		if !reached {
			t.Fatalf("active probes never reached 16 (now %d, maximum %d)", active, maximum)
		}
		if maximum != 16 {
			t.Fatalf("maximum concurrent probes = %d; want 16", maximum)
		}
	})
	t.Run("discoverUnixServers › ignores an endpoint that closes before its handshake", func(t *testing.T) {
		// upstream: packages/client/test/unix.test.ts:163
		t.Parallel()
		directory := shortDirectory(t, "pc-")
		listener, err := net.Listen("unix", filepath.Join(directory, discoveryServerId(1)+".sock"))
		if err != nil {
			t.Fatal(err)
		}
		var group sync.WaitGroup
		t.Cleanup(func() { _ = listener.Close(); group.Wait() })
		group.Go(func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				_ = conn.Close()
			}
		})

		if routes := discover(t, DiscoverUnixServersOptions{Directory: directory}); len(routes) != 0 {
			t.Fatalf("routes = %v; want []", routes)
		}
	})
	t.Run("discoverUnixServers › propagates unexpected filesystem errors", func(t *testing.T) {
		// upstream: packages/client/test/unix.test.ts:177
		t.Parallel()
		file := filepath.Join(shortDirectory(t, "pc-"), "not-a-directory")
		if err := os.WriteFile(file, []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}

		if _, err := DiscoverUnixServers(t.Context(), DiscoverUnixServersOptions{Directory: file}); !errors.Is(err, syscall.ENOTDIR) {
			t.Fatalf("discover error = %v; want ENOTDIR", err)
		}
	})
}
