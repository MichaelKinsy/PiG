package coding

import (
	"context"
	"testing"
)

// packages/coding-agent/src/core/agent-session.ts:dispose invalidates the runner the AgentSession built. A Runtime-owned runner is shared by every Session the Runtime starts, so closing one Session must leave it usable by the next; Runtime.Close invalidates it.
func TestSessionCloseKeepsRuntimeOwnedRunnerActive(t *testing.T) {
	rt, err := NewRuntime(RuntimeOptions{Services: newTestServices(t)})
	if err != nil {
		t.Fatal(err)
	}
	first, err := rt.New(SessionStartOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if rt.NewExtensionRunner().IsStale() {
		t.Fatal("closing one Runtime Session invalidated the Runtime runner")
	}
	second, err := rt.New(SessionStartOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if err := second.BindExtensions(context.Background(), ExtensionBindings{}); err != nil {
		t.Fatalf("second Session could not bind extensions: %v", err)
	}
	if _, err := second.Send(context.Background(), "after first close"); err != nil {
		t.Fatalf("second Session send: %v", err)
	}
	runner := rt.NewExtensionRunner()
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if !runner.IsStale() {
		t.Fatal("Runtime.Close must still invalidate its runner")
	}
}

// Clone shares the source Session's runner. /clone closes the headless clone and must not invalidate the live Session's extensions.
func TestSessionCloneCloseKeepsSourceRunnerActive(t *testing.T) {
	rt, err := NewRuntime(RuntimeOptions{Services: newTestServices(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	sess, err := rt.New(SessionStartOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if _, err := sess.Send(context.Background(), "before clone"); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.DispatchSlash("/clone"); err != nil {
		t.Fatal(err)
	}
	if sess.currentRunner().IsStale() {
		t.Fatal("closing the clone invalidated the live Session runner")
	}
	if _, err := sess.Send(context.Background(), "after clone"); err != nil {
		t.Fatalf("send after clone: %v", err)
	}
}
