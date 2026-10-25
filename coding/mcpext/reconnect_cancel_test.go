package mcpext_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
)

type reconnectCloseBarrier struct {
	mcp.Transport
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *reconnectCloseBarrier) Close() error {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.Transport.Close()
}

func TestMCPReconnectDoesNotOpenAfterCancellationDuringCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	servers := &serverSet{}
	t.Cleanup(servers.closeAll)
	barrier := &reconnectCloseBarrier{
		Transport: createFakeTransport(servers, fakeTransportOptions{}),
		entered:   make(chan struct{}),
		release:   make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(barrier.release) })
	var opened atomic.Int32
	connection, err := mcpext.NewConnection(mcpext.ConnectionOptions{
		Entry: stdioEntry(),
		CreateTransport: func(mcpext.McpServerEntry, string, mcp.AuthProvider) (mcp.Transport, error) {
			if opened.Add(1) == 1 {
				return barrier, nil
			}
			return nil, errors.New("unexpected reconnect after cancellation")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	t.Cleanup(release)
	if _, err := connection.GetClient(ctx); err != nil {
		t.Fatal(err)
	}

	reconnect, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	cause := errors.New("reconnect dismissed")
	done := make(chan error, 1)
	go func() { done <- connection.Reconnect(reconnect) }()
	select {
	case <-barrier.entered:
	case <-ctx.Done():
		t.Fatal("reconnect did not enter transport cleanup")
	}
	stop(cause)
	select {
	case err := <-done:
		t.Fatalf("reconnect returned before transport cleanup: %v", err)
	default:
	}
	release()
	select {
	case err := <-done:
		if !errors.Is(err, cause) {
			t.Errorf("reconnect error = %v, want %v", err, cause)
		}
	case <-ctx.Done():
		t.Fatal("reconnect did not finish after transport cleanup")
	}
	// Close joins all accepted connection attempts before checking their side effects.
	connection.Close()
	if got := opened.Load(); got != 1 {
		t.Errorf("opened %d transports, want only the initial connection", got)
	}
}
