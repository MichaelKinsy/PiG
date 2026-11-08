package terminals

import (
	"context"
	"net"
	"os"
	"testing"
	"time"
)

// Ready is what a caller waits for before it reads what the host's hello told it (HostCalls checks the host's version, which its
// frame handler took from the hello): Pi's `ready.resolve()` runs before `onFrame` too, but a waiter there continues in a
// later microtask, after onFrame returned. Ready here returns after the handler returned for the hello.
func TestAnAttachmentIsReadyOnceTheHandlerSawTheHello(t *testing.T) {
	directory := t.TempDir()
	address, err := HostAddress(directory)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(address)
	//portlint:allow unixsocketpath the address is HostAddress's: an abstract socket on Linux, a short hashed name elsewhere
	listener, err := net.Listen("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	closed := make(chan struct{})
	defer close(closed)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		frame, _ := EncodeFrame(TerminalEvent{Type: EventHello, Version: "v", Running: 0, Pid: 1}.JSON())
		_, _ = conn.Write(frame)
		<-closed
	}()
	handled := make(chan struct{})
	attachment := AttachTerminalHost(directory, func(frame Frame) {
		if frame.Raw {
			return
		}
		// A handler that takes a moment: the waiter must not run ahead of it.
		time.Sleep(200 * time.Millisecond)
		select {
		case <-handled:
		default:
			close(handled)
		}
	}, func() {})
	defer attachment.Detach()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := attachment.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handled:
	default:
		t.Fatal("Ready returned before the handler returned for the hello")
	}
}
