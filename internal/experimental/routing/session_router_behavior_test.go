package routing_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

type routerFixture struct {
	router   *routing.SessionRouter[string]
	host     *routingtest.TestServerHost
	closing  atomic.Bool
	mu       sync.Mutex
	attached map[string]*protocol.SessionTarget
}

func newRouterFixture(t *testing.T) *routerFixture {
	t.Helper()
	fixture := &routerFixture{host: routingtest.NewTestServerHost(), attached: map[string]*protocol.SessionTarget{}}
	fixture.host.Seed(nil)
	fixture.router = routing.NewSessionRouter(routing.SessionRouterOptions[string]{
		Host: fixture.host.ServerHost(), ServerId: testServerID, IsClosing: fixture.closing.Load, ReportError: func(error) {},
		PublishAttachment: func(_ context.Context, client string, attachment *protocol.SessionTarget) error {
			fixture.mu.Lock()
			fixture.attached[client] = attachment
			fixture.mu.Unlock()
			return nil
		},
	})
	t.Cleanup(func() { _ = fixture.router.Close(context.Background()) })
	return fixture
}

func (fixture *routerFixture) target(client string) protocol.SessionTarget {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return *fixture.attached[client]
}

func requireServerCode(t *testing.T, err error, code routing.ServerOperationErrorCode, message string) {
	t.Helper()
	failure, ok := errors.AsType[*routing.ServerError](err)
	if !ok || failure.Code != code || failure.Message != message {
		t.Fatalf("error=%T %v, want %s %q", err, err, code, message)
	}
}

var routerCall = chord.ServiceCall{ServiceId: "test.session", Member: "echo"}

// Pi: packages/server/src/session-router.ts:54-110 admission: a call needs a session target that names the client's current session and attachment; a closing server drains.
// mutation-checked: negating the condition `_, ok := h.sessions[metadata.SessionID()]; !ok` at host.go:440 fails it.
func TestSessionRouterAdmitsOnlyTheCurrentAttachment(t *testing.T) {
	t.Parallel()
	fixture := newRouterFixture(t)
	ctx := context.Background()
	if err := fixture.router.AttachClient(ctx, "c1", "session-1"); err != nil {
		t.Fatal(err)
	}
	current := fixture.target("c1")
	if _, err := fixture.router.ExecuteServiceCall(ctx, routerCall, current, "c1", nil); err != nil {
		t.Fatalf("current attachment rejected: %v", err)
	}
	const notAttached = "Session is not attached to this client"
	for name, target := range map[string]protocol.RpcTarget{
		"server target":      protocol.ServerTarget{ServerId: testServerID},
		"another session":    protocol.SessionTarget{ServerId: testServerID, SessionId: "session-2", AttachmentId: current.AttachmentId},
		"another attachment": protocol.SessionTarget{ServerId: testServerID, SessionId: "session-1", AttachmentId: "stale"},
	} {
		_, err := fixture.router.ExecuteServiceCall(ctx, routerCall, target, "c1", nil)
		if failure, ok := errors.AsType[*routing.ServerError](err); !ok || failure.Code != "session_not_attached" || failure.Message != notAttached {
			t.Fatalf("%s: error=%v", name, err)
		}
	}
	// A client that never attached has no attachment.
	_, err := fixture.router.ExecuteServiceCall(ctx, routerCall, current, "stranger", nil)
	requireServerCode(t, err, "session_not_attached", notAttached)
	// Detaching retires the attachment id.
	if err := fixture.router.DetachClient(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.router.ExecuteServiceCall(ctx, routerCall, current, "c1", nil)
	requireServerCode(t, err, "session_not_attached", notAttached)
}

// Pi source: packages/server/src/session-router.ts:276-290 (open while draining).
// mutation-checked: negating the condition `id != nil` at host.go:459 fails it.
func TestSessionRouterDrainsWhileTheServerIsClosing(t *testing.T) {
	t.Parallel()
	fixture := newRouterFixture(t)
	ctx := context.Background()
	if err := fixture.router.AttachClient(ctx, "c1", "session-1"); err != nil {
		t.Fatal(err)
	}
	current := fixture.target("c1")
	fixture.closing.Store(true)
	const draining = "Server is draining"
	_, err := fixture.router.ExecuteServiceCall(ctx, routerCall, current, "c1", nil)
	requireServerCode(t, err, "server_draining", draining)
	requireServerCode(t, fixture.router.AttachClient(ctx, "c2", "session-1"), "server_draining", draining)
	requireServerCode(t, fixture.router.RemoveSession(ctx, "session-1"), "server_draining", draining)
}
