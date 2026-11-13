package sdk

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// OnViewAbilities delivers each change of the replicated client abilities, in order, after the replica took it. The
// state an extension starts in is not a change, an equal snapshot is not one, and "no client" (nil) differs from a
// client that draws no slot.
func TestOnViewAbilitiesDeliversEachChangeAfterTheReplica(t *testing.T) {
	ext := New("test")
	t.Cleanup(ext.viewAbilitiesFeed.stop)
	got := make(chan string, 8)
	describe := func(a *ViewAbilities) string {
		if a == nil {
			return "none"
		}
		slots := make([]string, len(a.Slots))
		for i, s := range a.Slots {
			slots[i] = string(s)
		}
		return fmt.Sprintf("slots=%s,mounts=%d", strings.Join(slots, "+"), a.Limits.Mounts)
	}
	ext.OnViewAbilities(func(ctx Context, a *ViewAbilities) {
		// The handler's value is the replica's: ViewAbilities() was updated before the handler ran.
		got <- describe(a) + "|" + describe(ctx.ViewAbilities())
	})
	ext.OnViewAbilities(func(_ Context, a *ViewAbilities) { got <- "second:" + describe(a) })
	update := func(state string) {
		t.Helper()
		ext.handleNotify(envelope{Notify: &notifyMsg{Method: "state_update", Args: []byte(`{"state":` + state + `}`)}})
	}
	next := func(step, want string) {
		t.Helper()
		select {
		case have := <-got:
			if have != want {
				t.Fatalf("%s: handler got %q, want %q", step, have, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: no handler ran, want %q", step, want)
		}
	}
	quiet := func(step string) {
		t.Helper()
		// A change that follows proves nothing was queued for this step: deliveries are in order.
		update(`{"uiAbilities":{"slots":["status"],"limits":{"mounts":99}}}`)
		next(step, "slots=status,mounts=99|slots=status,mounts=99")
		next(step, "second:slots=status,mounts=99")
	}

	const client = `{"uiAbilities":{"slots":["panel","status"],"nodes":["text"],"limits":{"mounts":7}}}`
	// The first state is where the extension starts: no event, whether or not a client is attached.
	update(client)
	quiet("the first state")
	update(client)
	next("a client's abilities", "slots=panel+status,mounts=7|slots=panel+status,mounts=7")
	next("registration order", "second:slots=panel+status,mounts=7")
	// An equal snapshot (any other state change) is not a change of the abilities.
	update(client)
	quiet("an equal snapshot")
	// A client that draws no slot is a record; no client is nil.
	update(`{"uiAbilities":{"slots":[],"limits":{"mounts":7}}}`)
	next("a client with no slot", "slots=,mounts=7|slots=,mounts=7")
	next("a client with no slot", "second:slots=,mounts=7")
	update(`{"hasUI":true}`)
	next("the last client left", "none|none")
	next("the last client left", "second:none")
}

// A session no client attaches to runs no handler: every snapshot lacks uiAbilities.
func TestOnViewAbilitiesNeverFiresWithoutAClient(t *testing.T) {
	ext := New("test")
	t.Cleanup(ext.viewAbilitiesFeed.stop)
	fired := make(chan struct{}, 4)
	ext.OnViewAbilities(func(Context, *ViewAbilities) { fired <- struct{}{} })
	for _, state := range []string{`{"hasUI":true}`, `{"toolsExpanded":true}`, `{"hasUI":false}`, `{"uiAbilities":null}`} {
		ext.handleNotify(envelope{Notify: &notifyMsg{Method: "state_update", Args: []byte(`{"state":` + state + `}`)}})
	}
	ext.viewAbilitiesFeed.mu.Lock()
	queued, running := len(ext.viewAbilitiesFeed.queue), ext.viewAbilitiesFeed.running
	ext.viewAbilitiesFeed.mu.Unlock()
	if queued != 0 || running || len(fired) != 0 {
		t.Fatalf("a session without a client queued %d deliveries (worker running: %v, handler ran %d times), want none", queued, running, len(fired))
	}
}

// A handler that blocks holds back only later abilities events: the message loop goes on applying state.
func TestBlockedViewAbilitiesHandlerDoesNotBlockTheLoop(t *testing.T) {
	ext := New("test")
	release := make(chan struct{})
	t.Cleanup(func() {
		close(release)
		ext.viewAbilitiesFeed.stop()
	})
	entered := make(chan struct{}, 1)
	ext.OnViewAbilities(func(Context, *ViewAbilities) {
		entered <- struct{}{}
		<-release
	})
	update := func(state string) {
		ext.handleNotify(envelope{Notify: &notifyMsg{Method: "state_update", Args: []byte(`{"state":` + state + `}`)}})
	}
	update(`{}`)
	update(`{"uiAbilities":{"slots":["panel"],"limits":{"mounts":3}}}`)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never ran")
	}
	// The loop applies the next state while the handler is still blocked.
	done := make(chan struct{})
	go func() {
		update(`{"uiAbilities":{"slots":["panel"],"limits":{"mounts":4}}}`)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a blocked handler held the message loop")
	}
	if a := (Context{ext: ext}).ViewAbilities(); a == nil || a.Limits.Mounts != 4 {
		t.Fatalf("the replica = %+v, want the state applied while the handler blocked", a)
	}
}
