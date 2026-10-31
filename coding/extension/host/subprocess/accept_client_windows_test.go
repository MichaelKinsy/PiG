//go:build windows

package subprocess

import (
	"io"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/windows"
)

// holdConnectEvent replaces the event of ln's spare instance with one that
// stays unset until complete runs. The pipe driver still sets the original
// event, which the cleanup waits for, so the instance shows what the driver
// leaves between a client's open returning and the completion that sets the
// connect's event, on the thread that issued the connect: the pipe has a
// client and the event is not yet set.
func holdConnectEvent(t *testing.T, ln *pipeListener) (complete func()) {
	t.Helper()
	held, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	ln.mu.Lock()
	instance := ln.next
	if instance == nil || instance.done {
		ln.mu.Unlock()
		_ = windows.CloseHandle(held)
		t.Fatal("the listener's spare has no connect pending")
	}
	kernelEvent, kernelOverlapped := instance.event, instance.overlapped
	instance.event, instance.overlapped = held, &windows.Overlapped{HEvent: held | 1}
	ln.mu.Unlock()

	var once sync.Once
	complete = func() {
		once.Do(func() {
			if err := windows.SetEvent(held); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(func() {
		complete()
		_ = ln.Close()
		// The driver writes the original connect's result into its overlapped
		// until it sets the event, which a client's open or closing the
		// instance forces.
		if state, err := windows.WaitForSingleObject(kernelEvent, 10_000); err == nil && state == windows.WAIT_OBJECT_0 {
			_ = windows.CloseHandle(kernelEvent)
		}
		runtime.KeepAlive(kernelOverlapped)
	})
	return complete
}

// acceptWaiting reports whether an Accept waits on an instance of ln.
func acceptWaiting(ln *pipeListener) bool {
	ln.mu.Lock()
	defer ln.mu.Unlock()
	return len(ln.accepting) > 0
}

// A Node runner that reported its factory's error and exited has connected
// before the host looks at the pipe. The pipe's state shows that client as soon
// as its open returns, but the connect's event follows when a completion runs
// on the thread that issued the connect. A host whose Accept an exit cut short
// before it started must still take the client, not read the unset event as
// no client and report process_exited with the load error lost.
func TestAwaitConnectionHonorsAClientBeforeItsConnectEventIsSet(t *testing.T) {
	const frame = "load_failed"
	listener, address := extensionListener(t, true)
	ln := listener.(*pipeListener)
	complete := holdConnectEvent(t, ln)
	reportAndExit(t, address, true, frame)

	var settled atomic.Bool
	host := &Host{acceptGate: func(accept func() (net.Conn, error), _ <-chan struct{}) (net.Conn, error) {
		interruptAccept(ln)
		// The completion runs once Accept waits for it. An Accept that gives
		// up has no wait to complete.
		completed := make(chan struct{})
		go func() {
			defer close(completed)
			for !settled.Load() && !acceptWaiting(ln) {
				runtime.Gosched()
			}
			complete()
		}()
		conn, err := accept()
		settled.Store(true)
		<-completed
		return conn, err
	}}
	conn, outcome, err := host.awaitConnection(t.Context(), ln, exitedProcess())
	if outcome != acceptConnected || err != nil {
		t.Fatalf("awaitConnection = %v, %v; want the connection of the client that connected before the exit", outcome, err)
	}
	defer func() { _ = conn.Close() }()
	if got, err := io.ReadAll(conn); string(got) != frame {
		t.Fatalf("connection carried %q, %v; want %q", got, err, frame)
	}
}

// An interrupt closes only the instance of an Accept that no client has
// connected to. A client whose open has returned has connected, whether or not
// its connect's event is set yet, so the waiting Accept still returns it.
func TestPipeListenerInterruptKeepsAClientBeforeItsConnectEventIsSet(t *testing.T) {
	const frame = "load_failed"
	ln, address := listenTestPipe(t)
	complete := holdConnectEvent(t, ln)
	accepted := startAccept(ln)
	waitForAcceptEntry(t, ln, 1)
	reportAndExit(t, address, true, frame)

	ln.interrupt()
	complete()
	got := awaitAccept(t, accepted)
	if got.err != nil {
		t.Fatalf("Accept after the interrupt = %v, %v; want the client that connected before it", got.conn, got.err)
	}
	defer func() { _ = got.conn.Close() }()
	if data, err := io.ReadAll(got.conn); string(data) != frame {
		t.Fatalf("connection carried %q, %v; want %q", data, err, frame)
	}
}
