package routing_test

// pi: packages/server/src/session-router.ts

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

// publishRecorder keeps every attachment the router publishes to each client, in order.
type publishRecorder struct {
	mu      sync.Mutex
	history map[string][]*protocol.SessionTarget
}

func (recorder *publishRecorder) publish(_ context.Context, client string, attachment *protocol.SessionTarget) error {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.history[client] = append(recorder.history[client], attachment)
	return nil
}

func (recorder *publishRecorder) of(client string) []*protocol.SessionTarget {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]*protocol.SessionTarget(nil), recorder.history[client]...)
}

func newLifecycleRouter(t *testing.T, sessions ...string) (*routing.SessionRouter[string], *routingtest.TestServerHost, *publishRecorder) {
	t.Helper()
	host := routingtest.NewTestServerHost()
	for _, id := range sessions {
		host.Seed(&id)
	}
	recorder := &publishRecorder{history: map[string][]*protocol.SessionTarget{}}
	router := routing.NewSessionRouter(routing.SessionRouterOptions[string]{
		Host: host.ServerHost(), ServerId: testServerID, IsClosing: func() bool { return false }, ReportError: func(error) {}, PublishAttachment: recorder.publish,
	})
	t.Cleanup(func() { _ = router.Close(context.Background()) })
	return router, host, recorder
}

func requireHarnessCounts(t *testing.T, label string, harness *routingtest.TestHarness, attached, released, closed int) {
	t.Helper()
	if harness.AttachedClients() != attached || harness.AttachmentReleaseCount() != released || harness.CloseCount() != closed {
		t.Fatalf("%s: attached=%d released=%d closed=%d, want %d %d %d", label, harness.AttachedClients(), harness.AttachmentReleaseCount(), harness.CloseCount(), attached, released, closed)
	}
}

// Pi: packages/server/src/session-router.ts attachment lifecycle. attachClientNow (:160-195) keeps the current attachment for the same Session and releases the previous one without publishing when switching; removeSession (:72-89) releases every attachment and closes the Harness; disconnect (:91-101) releases without publishing; closeInternal (:108-140) releases attachments before closing each Harness.
// Pi source: packages/server/src/session-router.ts:160-190 (attachClientNow) and :234-250 (releaseAttachment).
// mutation-checked: negating the condition `value := r.hosted[id]; value != nil` at session_router.go:246 fails it.
func TestSessionRouterAttachmentLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("re-attaching the current Session keeps its attachment", func(t *testing.T) {
		t.Parallel()
		router, host, recorder := newLifecycleRouter(t, "session-1")
		for range 2 {
			if err := router.AttachClient(ctx, "c1", "session-1"); err != nil {
				t.Fatal(err)
			}
		}
		history := recorder.of("c1")
		if len(history) != 1 || history[0] == nil || history[0].SessionId != "session-1" {
			t.Fatalf("published=%v, want one session-1 attachment", history)
		}
		if host.OpenSessionCount() != 1 {
			t.Fatalf("open count=%d", host.OpenSessionCount())
		}
		requireHarnessCounts(t, "after re-attach", latestHarness(t, host, "session-1"), 1, 0, 0)
		if _, err := router.ExecuteServiceCall(ctx, routerCall, *history[0], "c1", nil); err != nil {
			t.Fatalf("the first attachment is no longer current: %v", err)
		}
	})
	t.Run("switching Sessions releases the previous attachment without publishing a detach", func(t *testing.T) {
		t.Parallel()
		router, host, recorder := newLifecycleRouter(t, "session-1", "session-2")
		if err := router.AttachClient(ctx, "c1", "session-1"); err != nil {
			t.Fatal(err)
		}
		if err := router.AttachClient(ctx, "c1", "session-2"); err != nil {
			t.Fatal(err)
		}
		history := recorder.of("c1")
		if len(history) != 2 || history[0] == nil || history[0].SessionId != "session-1" || history[1] == nil || history[1].SessionId != "session-2" {
			t.Fatalf("published=%v, want session-1 then session-2", history)
		}
		requireHarnessCounts(t, "previous Session", latestHarness(t, host, "session-1"), 0, 1, 0)
		requireHarnessCounts(t, "current Session", latestHarness(t, host, "session-2"), 1, 0, 0)
	})
	t.Run("removing a Session releases its attachments and closes the Harness", func(t *testing.T) {
		t.Parallel()
		router, host, recorder := newLifecycleRouter(t, "session-1")
		if err := router.AttachClient(ctx, "c1", "session-1"); err != nil {
			t.Fatal(err)
		}
		if err := router.AttachClient(ctx, "c2", "session-1"); err != nil {
			t.Fatal(err)
		}
		removed := latestHarness(t, host, "session-1")
		stale := *recorder.of("c1")[0]
		if err := router.RemoveSession(ctx, "session-1"); err != nil {
			t.Fatal(err)
		}
		requireHarnessCounts(t, "removed Session", removed, 0, 2, 1)
		for _, client := range []string{"c1", "c2"} {
			if history := recorder.of(client); len(history) != 2 || history[1] != nil {
				t.Fatalf("%s published=%v, want the attachment then a detach", client, history)
			}
		}
		_, err := router.ExecuteServiceCall(ctx, routerCall, stale, "c1", nil)
		requireServerCode(t, err, "session_not_attached", "Session is not attached to this client")
		if err := router.AttachClient(ctx, "c1", "session-1"); err != nil {
			t.Fatal(err)
		}
		if harnesses := host.Harnesses()["session-1"]; len(harnesses) != 2 {
			t.Fatalf("harnesses=%d, want a fresh Harness after removal", len(harnesses))
		}
	})
	t.Run("disconnect releases the attachment without publishing", func(t *testing.T) {
		t.Parallel()
		router, host, recorder := newLifecycleRouter(t, "session-1")
		if err := router.AttachClient(ctx, "c1", "session-1"); err != nil {
			t.Fatal(err)
		}
		if err := router.Disconnect(ctx, "c1"); err != nil {
			t.Fatal(err)
		}
		requireHarnessCounts(t, "after disconnect", latestHarness(t, host, "session-1"), 0, 1, 0)
		if history := recorder.of("c1"); len(history) != 1 || history[0] == nil {
			t.Fatalf("published=%v, want only the attachment", history)
		}
	})
	t.Run("close releases attachments and closes the Harness", func(t *testing.T) {
		t.Parallel()
		router, host, recorder := newLifecycleRouter(t, "session-1")
		if err := router.AttachClient(ctx, "c1", "session-1"); err != nil {
			t.Fatal(err)
		}
		if err := router.Close(ctx); err != nil {
			t.Fatal(err)
		}
		requireHarnessCounts(t, "after close", latestHarness(t, host, "session-1"), 0, 1, 1)
		if history := recorder.of("c1"); len(history) != 2 || history[1] != nil {
			t.Fatalf("published=%v, want the attachment then a detach", history)
		}
	})
}
