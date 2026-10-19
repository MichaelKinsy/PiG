package subprocess

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// extensionListener listens the way a host does for one extension process and
// returns the address its runner connects to.
func extensionListener(t *testing.T, node bool) (net.Listener, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "pl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	ln, address, err := ListenExtension(filepath.Join(dir, "e.sock"), node)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln, address
}

// reportAndExit is what a Node runner whose factory failed does to the host:
// it connects, writes one frame, and closes the connection. Its process then
// exits.
func reportAndExit(t *testing.T, address string, node bool, frame string) {
	t.Helper()
	var client io.WriteCloser
	var err error
	if node && runtime.GOOS == "windows" {
		client, err = os.OpenFile(address, os.O_RDWR, 0)
	} else {
		client, err = net.Dial("unix", address)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte(frame)); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

// exitedProcess is the exit channel of a process that has ended.
func exitedProcess() <-chan struct{} {
	exited := make(chan struct{})
	close(exited)
	return exited
}

func eachListenerKind(t *testing.T, run func(t *testing.T, node bool)) {
	t.Helper()
	for _, node := range []bool{false, true} {
		t.Run(fmt.Sprintf("node=%t", node), func(t *testing.T) { run(t, node) })
	}
}

// A runner that reported its factory's error and exited has already connected,
// and the host looks only after the exit. The connection outranks the exit,
// whether the host's Accept has started, has been cut short by the exit, or has
// yet to notice the client.
func TestAwaitConnectionHonorsAConnectionMadeBeforeTheExit(t *testing.T) {
	t.Parallel()
	const frame = "load_failed"
	for name, gate := range map[string]func(ln net.Listener) acceptGateFunc{
		"accept not gated": func(net.Listener) acceptGateFunc { return nil },
		"accept cut short by the exit before it starts": func(ln net.Listener) acceptGateFunc {
			return func(accept func() (net.Conn, error), _ <-chan struct{}) (net.Conn, error) {
				interruptAccept(ln)
				return accept()
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			eachListenerKind(t, func(t *testing.T, node bool) {
				if !node && runtime.GOOS == "windows" {
					// acceptQueued (accept_client_windows.go): a Windows AF_UNIX listener has no readiness query, so
					// a Go, Rust or Python runner that connected and then exited reads as process_exited. Those
					// runners report no load error over the socket.
					t.Skip("a Windows AF_UNIX listener cannot take a client queued before an interrupted Accept")
				}
				// Repeats cover both orders of the host's Accept and the exit watcher.
				for range 50 {
					ln, address := extensionListener(t, node)
					reportAndExit(t, address, node, frame)
					host := &Host{acceptGate: gate(ln)}
					conn, outcome, err := host.awaitConnection(t.Context(), ln, exitedProcess())
					if outcome != acceptConnected || err != nil {
						t.Fatalf("awaitConnection = %v, %v; want the connection the process made before it exited", outcome, err)
					}
					got, _ := io.ReadAll(conn)
					_ = conn.Close()
					if runtime.GOOS != "windows" && string(got) != frame {
						t.Fatalf("connection carried %q, want %q", got, frame)
					}
				}
			})
		})
	}
}

// A process that exits without ever connecting is process_exited, at once.
func TestAwaitConnectionReportsAProcessThatExitedWithoutConnecting(t *testing.T) {
	t.Parallel()
	eachListenerKind(t, func(t *testing.T, node bool) {
		for range 20 {
			ln, _ := extensionListener(t, node)
			conn, outcome, err := (&Host{}).awaitConnection(t.Context(), ln, exitedProcess())
			if outcome != acceptProcessExited || err != nil || conn != nil {
				t.Fatalf("awaitConnection = %v, %v, %v; want the process exit", conn, outcome, err)
			}
		}
	})
}

// An exit that ends the wait for a client that is still to come is
// process_exited: the exit interrupts the Accept the host already blocks in.
func TestAwaitConnectionEndsWhenTheProcessExitsWhileWaiting(t *testing.T) {
	t.Parallel()
	eachListenerKind(t, func(t *testing.T, node bool) {
		ln, _ := extensionListener(t, node)
		exited := make(chan struct{})
		type result struct {
			conn    net.Conn
			outcome acceptOutcome
			err     error
		}
		done := make(chan result, 1)
		go func() {
			conn, outcome, err := (&Host{}).awaitConnection(t.Context(), ln, exited)
			done <- result{conn, outcome, err}
		}()
		close(exited)
		if got := <-done; got.outcome != acceptProcessExited || got.err != nil || got.conn != nil {
			t.Fatalf("awaitConnection = %+v; want the process exit", got)
		}
	})
}

func TestAwaitConnectionReturnsTheConnectionOfALiveProcess(t *testing.T) {
	t.Parallel()
	eachListenerKind(t, func(t *testing.T, node bool) {
		ln, address := extensionListener(t, node)
		type result struct {
			conn    net.Conn
			outcome acceptOutcome
			err     error
		}
		done := make(chan result, 1)
		go func() {
			conn, outcome, err := (&Host{}).awaitConnection(t.Context(), ln, make(chan struct{}))
			done <- result{conn, outcome, err}
		}()
		reportAndExit(t, address, node, "register")
		got := <-done
		if got.outcome != acceptConnected || got.err != nil {
			t.Fatalf("awaitConnection = %+v; want the connection", got)
		}
		_ = got.conn.Close()
	})
}

func TestAwaitConnectionEndsWhenTheCallerCancels(t *testing.T) {
	t.Parallel()
	eachListenerKind(t, func(t *testing.T, node bool) {
		ln, _ := extensionListener(t, node)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			conn, outcome, err := (&Host{}).awaitConnection(ctx, ln, make(chan struct{}))
			if outcome != acceptCancelled || conn != nil {
				err = fmt.Errorf("outcome = %v, conn = %v: %w", outcome, conn, err)
			}
			done <- err
		}()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("awaitConnection error = %v; want the caller's cancellation", err)
		}
	})
}

// A listener that fails while the process is alive is accept_failed, not an exit.
func TestAwaitConnectionReportsAListenerThatFailed(t *testing.T) {
	t.Parallel()
	eachListenerKind(t, func(t *testing.T, node bool) {
		ln, _ := extensionListener(t, node)
		done := make(chan acceptOutcome, 1)
		go func() {
			_, outcome, _ := (&Host{}).awaitConnection(t.Context(), ln, make(chan struct{}))
			done <- outcome
		}()
		// Closing before Accept starts fails it the same way as closing a blocked Accept.
		_ = ln.Close()
		if outcome := <-done; outcome != acceptFailed {
			t.Fatalf("outcome = %v, want accept failure", outcome)
		}
	})
}
