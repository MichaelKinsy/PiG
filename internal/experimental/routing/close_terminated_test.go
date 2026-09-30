package routing_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

// terminatedLease models the worker manager's attachment: once its worker is retired, a release is rejected (session-worker-manager.ts:#applyDemand throws "Experimental Session worker is stopping" when the worker is no longer registered).
type terminatedLease struct {
	terminated <-chan struct{}
	// settle parks Release, so a release begun by the connection's disconnect stays in flight until Close has decided what to join.
	settle <-chan struct{}
}

func (terminatedLease) InvokeService(context.Context, chord.ServiceCall, chord.ServiceUpdatePublisher) (json.RawMessage, error) {
	return nil, nil
}
func (lease terminatedLease) Release(context.Context) error {
	<-lease.settle
	select {
	case <-lease.terminated:
		return errors.New("Experimental Session worker is stopping")
	default:
		return nil
	}
}

// gatedTerminationHandle parks the router's termination watcher, after it observed Terminated and before it invalidates the Session, on its first TerminalError read. Later reads return immediately. release also settles every lease release.
type gatedTerminationHandle struct {
	terminated chan struct{}
	entered    chan struct{}
	release    chan struct{}
	parked     atomic.Bool
}

func (handle *gatedTerminationHandle) AttachClient(context.Context) (routing.RoutedSessionAttachment, error) {
	return terminatedLease{handle.terminated, handle.release}, nil
}
func (handle *gatedTerminationHandle) Terminated() <-chan struct{} { return handle.terminated }
func (handle *gatedTerminationHandle) TerminalError() error {
	if handle.parked.CompareAndSwap(false, true) {
		close(handle.entered)
		<-handle.release
	}
	return nil
}
func (*gatedTerminationHandle) Close(context.Context) error { return nil }

// upstream: packages/server/src/session-router.ts:#open registers `handle.terminated?.then(invalidate)`. Upstream runs that continuation as a microtask, before any later event such as a Close, so a Close that follows a Harness termination never releases through the retired handle. Go runs the watcher on its own goroutine, so closeInternal must apply an already-signalled termination before it snapshots attachments.
func TestRouterCloseAppliesTerminationSignalledBeforeClose(t *testing.T) {
	handle := &gatedTerminationHandle{terminated: make(chan struct{}), entered: make(chan struct{}), release: make(chan struct{})}
	host := routing.ServerHost{
		ServerServices: testServerServices{},
		ResolveSession: func(_ context.Context, id string) (session.SessionMetadata, error) {
			return session.SessionMetadata{ID: id, CreatedAt: 1, StorageVersion: 1}, nil
		},
		OpenSession: func(context.Context, session.SessionMetadata) (routing.RoutedSessionHandle, error) {
			return handle, nil
		},
	}
	server := createServer(t, host)
	client := connect(t, server)
	client.hello(protocol.ProtocolVersion)
	expectOK(t, client.attach(testServerID, "session-1"))
	close(handle.terminated)
	<-handle.entered
	closing := make(chan error, 1)
	go func() { closing <- server.Close() }()
	// Every path that reaches the retired lease is in flight before it settles: closeInternal has taken its attachment snapshot and blocks joining the parked release or router work, which includes the parked watcher.
	pollUntil(t, "router close blocked after its attachment snapshot", func() bool {
		return goroutinesMatching(func(stack string) bool {
			header, _, _ := strings.Cut(stack, "\n")
			return strings.Contains(stack, "SessionRouter[...]).closeInternal(") && !strings.Contains(header, "runnable") && !strings.Contains(header, "running")
		}) == 1
	})
	close(handle.release)
	if err := <-closing; err != nil {
		t.Fatalf("Close = %v, want the terminated Session invalidated before shutdown", err)
	}
}
