//go:build windows

package winpipe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

func pipeName(t *testing.T) string {
	return fmt.Sprintf(`\\.\pipe\winpipe-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
}

// A Close with an Accept waiting ends the Accept with net.ErrClosed and returns: the instance the Accept waited on is
// closed, so the name is free again.
func TestCloseEndsAWaitingAcceptAndFreesTheName(t *testing.T) {
	name := pipeName(t)
	l, err := Listen(name, &winio.PipeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		accepted <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-accepted; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept = %v, want net.ErrClosed", err)
	}
	again, err := Listen(name, &winio.PipeConfig{})
	if err != nil {
		t.Fatalf("the name is not free after Close: %v", err)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("a second Close = %v", err)
	}
}

// A Close that races a client dialing and giving up returns, every time: the lock's waiter that aborts while its holder
// releases (go-winio's own Close waited forever for one in a few hundred of these).
func TestCloseReturnsWhileAClientDialsAndGivesUp(t *testing.T) {
	const rounds = 500
	for round := range rounds {
		name := pipeName(t)
		l, err := Listen(name, &winio.PipeConfig{})
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				_ = conn.Close()
			}
		}()
		ctx, cancel := context.WithCancel(context.Background())
		dialed := make(chan struct{})
		go func() {
			defer close(dialed)
			if conn, err := winio.DialPipeContext(ctx, name); err == nil {
				_ = conn.Close()
			}
		}()
		cancel()
		closed := make(chan struct{})
		go func() {
			_ = l.Close()
			close(closed)
		}()
		select {
		case <-closed:
		case <-time.After(10 * time.Second):
			t.Fatalf("round %d: Close did not return", round)
		}
		<-dialed
	}
}
