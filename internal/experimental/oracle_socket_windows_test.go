//go:build windows

package experimental

import (
	"crypto/rand"
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

// oracleListenPath returns the address the upstream coordinator listens on in
// place of path. coordinator.ts listens on the paths it is given, and Node on
// Windows listens only on named pipes (a socket file path fails with EACCES),
// so the oracle gets a pipe name.
func oracleListenPath(path string) string {
	return `\\.\pipe\pig-test-` + rand.Text() + "-" + filepath.Base(path)
}

func dialTestSocket(path string) (net.Conn, error) {
	if isPipeName(path) {
		timeout := 5 * time.Second
		return winio.DialPipe(path, &timeout)
	}
	return net.Dial("unix", path)
}

func listenTestSocket(path string) (net.Listener, error) {
	if isPipeName(path) {
		return winio.ListenPipe(path, nil)
	}
	return net.Listen("unix", path)
}

// productControlPath returns a path PiG's CoordinatorConnection can dial to
// reach the coordinator control socket at path. PiG dials AF_UNIX sockets, and
// on Windows the upstream coordinator listens on a named pipe, so the test
// bridges a new AF_UNIX socket to the pipe byte for byte. Each bridged
// connection closes both ends when either end closes.
func productControlPath(t *testing.T, path string) string {
	t.Helper()
	if !isPipeName(path) {
		return path
	}
	bridge := filepath.Join(socketDir(t), "b")
	listener, err := net.Listen("unix", bridge)
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu      sync.Mutex
		closed  bool
		open    []net.Conn
		bridges sync.WaitGroup
	)
	bridges.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			timeout := 5 * time.Second
			pipe, err := winio.DialPipe(path, &timeout)
			if err != nil {
				_ = conn.Close()
				continue
			}
			mu.Lock()
			if closed {
				mu.Unlock()
				_ = conn.Close()
				_ = pipe.Close()
				return
			}
			open = append(open, conn, pipe)
			mu.Unlock()
			bridges.Go(func() { bridgeConns(conn, pipe) })
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		closed = true
		for _, conn := range open {
			_ = conn.Close()
		}
		mu.Unlock()
		bridges.Wait()
	})
	return bridge
}

// bridgeConns copies both ways until either side ends, then closes both.
func bridgeConns(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
	_ = a.Close()
	_ = b.Close()
	<-done
}
