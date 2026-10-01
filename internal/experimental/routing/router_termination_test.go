package routing_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

var errStopping = errors.New("Experimental Session worker is stopping")

// retiredHandle models a worker-backed Harness handle: once terminated, attach and release fail as session-worker-manager.ts:#applyDemand does for a worker that is no longer registered. The router's termination watcher parks on its first TerminalError read until resume closes, so the tests observe a signalled termination the watcher has not yet applied.
type retiredHandle struct {
	terminated chan struct{}
	entered    chan struct{}
	resume     chan struct{}
	terminal   error
	parked     atomic.Bool
	closes     atomic.Int32
}

func newRetiredHandle(terminal error) *retiredHandle {
	return &retiredHandle{terminated: make(chan struct{}), entered: make(chan struct{}), resume: make(chan struct{}), terminal: terminal}
}

// terminate signals the termination and waits until the router's watcher has observed it and parked before applying it.
func (handle *retiredHandle) terminate() {
	close(handle.terminated)
	<-handle.entered
}
func (handle *retiredHandle) retired() bool {
	select {
	case <-handle.terminated:
		return true
	default:
		return false
	}
}
func (handle *retiredHandle) AttachClient(context.Context) (routing.RoutedSessionAttachment, error) {
	if handle.retired() {
		return nil, errStopping
	}
	return retiredLease{handle}, nil
}
func (handle *retiredHandle) Terminated() <-chan struct{} { return handle.terminated }
func (handle *retiredHandle) TerminalError() error {
	if handle.parked.CompareAndSwap(false, true) {
		close(handle.entered)
		<-handle.resume
	}
	return handle.terminal
}
func (handle *retiredHandle) Close(context.Context) error {
	handle.closes.Add(1)
	if handle.retired() {
		return errStopping
	}
	return nil
}

type retiredLease struct{ handle *retiredHandle }

func (retiredLease) InvokeService(context.Context, chord.ServiceCall, chord.ServiceUpdatePublisher) (json.RawMessage, error) {
	return nil, nil
}
func (lease retiredLease) Release(context.Context) error {
	if lease.handle.retired() {
		return errStopping
	}
	return nil
}

type routerProbe struct {
	router  *routing.SessionRouter[string]
	closing atomic.Bool
	mu      sync.Mutex
	errors  []error
	opens   atomic.Int32
}

func (probe *routerProbe) reported() []error {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return slices.Clone(probe.errors)
}

// newRouterProbe hosts every Session through open, called with the zero-based OpenSession count.
func newRouterProbe(t *testing.T, resolve func(context.Context, string) (session.SessionMetadata, error), open func(int) routing.RoutedSessionHandle) *routerProbe {
	t.Helper()
	probe := &routerProbe{}
	if resolve == nil {
		resolve = func(_ context.Context, id string) (session.SessionMetadata, error) {
			return session.SessionMetadata{ID: id, CreatedAt: 1, StorageVersion: 1}, nil
		}
	}
	probe.router = routing.NewSessionRouter(routing.SessionRouterOptions[string]{
		Host: routing.ServerHost{
			ServerServices: testServerServices{},
			ResolveSession: resolve,
			OpenSession: func(context.Context, session.SessionMetadata) (routing.RoutedSessionHandle, error) {
				return open(int(probe.opens.Add(1) - 1)), nil
			},
		},
		ServerId:          testServerID,
		IsClosing:         probe.closing.Load,
		PublishAttachment: func(context.Context, string, *protocol.SessionTarget) error { return nil },
		ReportError: func(err error) {
			probe.mu.Lock()
			defer probe.mu.Unlock()
			probe.errors = append(probe.errors, err)
		},
	})
	return probe
}

// upstream: packages/server/src/session-router.ts:#open registers `handle.terminated?.then(invalidate)` and #acquire reads hostedSessions. The invalidation microtask runs before a later attach request, so an attach after a Harness termination opens a fresh Harness instead of attaching to the retired one.
func TestRouterAttachAfterSignalledTerminationOpensAFreshHarness(t *testing.T) {
	retired, fresh := newRetiredHandle(nil), newRetiredHandle(nil)
	resumeRetired, resumeFresh := sync.OnceFunc(func() { close(retired.resume) }), sync.OnceFunc(func() { close(fresh.resume) })
	t.Cleanup(resumeRetired)
	t.Cleanup(resumeFresh)
	probe := newRouterProbe(t, nil, func(index int) routing.RoutedSessionHandle {
		return []*retiredHandle{retired, fresh}[index]
	})
	ctx := context.Background()
	if err := probe.router.AttachClient(ctx, "client-1", "session-1"); err != nil {
		t.Fatal(err)
	}
	retired.terminate()
	err := probe.router.AttachClient(ctx, "client-2", "session-1")
	resumeRetired()
	resumeFresh()
	if err != nil {
		t.Fatalf("attach after termination = %v, want a fresh Harness", err)
	}
	if got := probe.opens.Load(); got != 2 {
		t.Fatalf("OpenSession calls = %d, want 2", got)
	}
	probe.closing.Store(true)
	if err := probe.router.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// upstream: packages/server/src/session-router.ts:removeSession returns when hostedSessions no longer holds the Session. The termination microtask has already removed a terminated Harness, so removal neither releases through nor closes the retired handle.
func TestRouterRemoveSessionAfterSignalledTerminationIsANoOp(t *testing.T) {
	retired := newRetiredHandle(nil)
	resumeRetired := sync.OnceFunc(func() { close(retired.resume) })
	t.Cleanup(resumeRetired)
	probe := newRouterProbe(t, nil, func(int) routing.RoutedSessionHandle { return retired })
	ctx := context.Background()
	if err := probe.router.AttachClient(ctx, "client-1", "session-1"); err != nil {
		t.Fatal(err)
	}
	retired.terminate()
	err := probe.router.RemoveSession(ctx, "session-1")
	resumeRetired()
	if err != nil {
		t.Fatalf("RemoveSession after termination = %v, want nil", err)
	}
	if got := retired.closes.Load(); got != 0 {
		t.Fatalf("retired handle closes = %d, want 0", got)
	}
	probe.closing.Store(true)
	if err := probe.router.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// upstream: packages/server/src/session-router.ts:#open's termination microtask reports the Harness terminal error as soon as the termination settles, before closeInternal (:111-120) resumes after Promise.all and reports its opening failures.
func TestRouterCloseReportsSignalledTerminationBeforeOpeningFailures(t *testing.T) {
	errTerminal := errors.New("worker crashed")
	errOpen := errors.New("resolve session-2 failed")
	retired := newRetiredHandle(errTerminal)
	resolving, failResolve := make(chan struct{}), make(chan struct{})
	probe := newRouterProbe(t, func(_ context.Context, id string) (session.SessionMetadata, error) {
		if id == "session-2" {
			close(resolving)
			<-failResolve
			return session.SessionMetadata{}, errOpen
		}
		return session.SessionMetadata{ID: id, CreatedAt: 1, StorageVersion: 1}, nil
	}, func(int) routing.RoutedSessionHandle { return retired })
	// Settle the parked watcher and resolution on failure too, so a failed run does not leave Close parked for the rest of the package.
	resume, resolve := sync.OnceFunc(func() { close(retired.resume) }), sync.OnceFunc(func() { close(failResolve) })
	t.Cleanup(resume)
	t.Cleanup(resolve)
	ctx := context.Background()
	if err := probe.router.AttachClient(ctx, "client-1", "session-1"); err != nil {
		t.Fatal(err)
	}
	attaching := make(chan error, 1)
	go func() { attaching <- probe.router.AttachClient(ctx, "client-2", "session-2") }()
	<-resolving
	retired.terminate()
	probe.closing.Store(true)
	closing := make(chan error, 1)
	go func() { closing <- probe.router.Close(ctx) }()
	pollUntil(t, "router draining in-flight acquisitions", routerIsDraining)
	resolve()
	// The terminal error, the opening failure and the background release failure through the retired lease are reported before Close joins the parked watcher.
	pollUntil(t, "close reports", func() bool { return len(probe.reported()) == 3 })
	resume()
	if err := <-closing; err != nil {
		t.Fatalf("Close = %v", err)
	}
	if err := <-attaching; !errors.Is(err, errOpen) {
		t.Fatalf("attach = %v, want %v", err, errOpen)
	}
	reported := probe.reported()
	terminal, opening := slices.Index(reported, errTerminal), slices.Index(reported, errOpen)
	if terminal < 0 || opening < 0 || terminal > opening {
		t.Fatalf("reported %v, want the terminal error before the opening failure", reported)
	}
}
