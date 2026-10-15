package coding

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/agent"
)

// newDisposableSession builds a Session whose test keeps no reference to it, so the test can observe what still holds it
// after Dispose. The returned services outlive it, as a Runtime's do across Session replacement.
func newDisposableSession(t *testing.T) (weak.Pointer[Session], <-chan agent.AgentEvent, func(), *AgentSessionServices) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	agentDir := filepath.Join(tmp, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	svcs, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	return weak.Make(sess), sess.Events(), sess.Dispose, svcs
}

// upstream: packages/coding-agent/src/core/agent-session.ts:1389-1416 dispose removes every listener and disconnects from the
// agent, "when completely done with the session"; nothing of the Session stays live afterwards. Dispose alone ends the Session
// event executor, so its Events stream closes, and nothing the services own keeps the Session reachable.
func TestSessionDisposeReleasesTheSession(t *testing.T) {
	session, events, dispose, svcs := newDisposableSession(t)
	dispose()
	select {
	case _, open := <-events:
		for open {
			_, open = <-events
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the Session event executor still runs after Dispose")
	}
	for range 5 {
		runtime.GC()
		if session.Value() == nil {
			break
		}
	}
	if session.Value() != nil {
		t.Fatal("the Session is still reachable after Dispose")
	}
	runtime.KeepAlive(svcs)
}

// After Dispose the Session has no listeners, so an event it still publishes reaches no one, and a flush of its event
// stream has nothing to wait for.
func TestSessionDisposeReleasesEventProducers(t *testing.T) {
	sess := newBashTestSession(t, `{}`)
	sess.Dispose()
	done := make(chan error, 1)
	go func() {
		for range cap(sess.rawEvents) + 2 {
			sess.emitOrderedEvent(agent.AgentEndEvent{})
		}
		sess.emitOrderedEventSync(agent.AgentEndEvent{})
		done <- sess.FlushEvents(context.Background())
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("FlushEvents after Dispose: %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("an event producer is still blocked after Dispose")
	}
}
