package coding

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A runner retires when Close, dispose or session replacement invalidates it, and when a reload that loaded replaces it. Pi's runner has no check on its emit methods
// (runner.ts:1135-1560), but no Pi path emits to a runner after dispose() (agent-session.ts:1477-1496) or after _buildRuntime dropped it (:3844), so pig refuses an emit of
// its own goroutines to such a runner with the stale error. A reload that failed leaves the runner stale and in service (TestAFailedReloadLeavesTheSessionDeliveringToTheOldInstance); the
// reload that follows it, and Close, must still retire it: the stale message is the first one, but retiring is not part of the once that keeps it.

// requireRetired fails unless an emit on runner returns the stale-context error without reaching the instance's tool_call handler.
func requireRetired(t *testing.T, state string, runner interface {
	EmitToolCall(ctx context.Context, event extension.ToolCallEvent) (*extension.ToolCallEventResult, error)
}, instance *emitCounter) {
	t.Helper()
	before := instance.count("tool_call")
	if _, err := runner.EmitToolCall(t.Context(), extension.CustomToolCallEvent{}); !errors.Is(err, extension.ErrStaleContext) {
		t.Errorf("%s: an emit on the retired runner = %v, want the stale-context error", state, err)
	}
	if got := instance.count("tool_call"); got != before {
		t.Errorf("%s: the retired runner's tool_call handler ran %d times, want %d", state, got, before)
	}
}

func TestACloseOfTheSessionRetiresItsRunner(t *testing.T) {
	old := newEmitCounter()
	session := newReloadableEmitSession(t, old, &swapLoader{ResourceLoader: NoResources})
	previous := session.ExtensionRunner()
	if _, err := previous.EmitToolCall(t.Context(), extension.CustomToolCallEvent{}); err != nil {
		t.Fatalf("an emit on the live runner: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	requireRetired(t, "closed", previous, old)
}

func TestACloseAfterAFailedReloadRetiresTheOldRunner(t *testing.T) {
	old := newEmitCounter()
	loader := &swapLoader{ResourceLoader: NoResources, err: errors.New("resource reload failed")}
	session := newReloadableEmitSession(t, old, loader)
	previous := session.ExtensionRunner()
	if err := session.Reload(t.Context()); !errors.Is(err, loader.err) {
		t.Fatalf("Reload error = %v, want %v", err, loader.err)
	}
	driveEveryEmitOfTheSession(t, session)
	if got := old.count("tool_call"); got != 1 {
		t.Fatalf("the old instance's tool_call handler ran %d times after the failed reload, want 1", got)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	requireRetired(t, "failed reload, then Close", previous, old)
}

func TestAGoodReloadAfterAFailedOneRetiresTheOldRunner(t *testing.T) {
	old, fresh := newEmitCounter(), newEmitCounter()
	loader := &swapLoader{ResourceLoader: NoResources, err: errors.New("resource reload failed")}
	session := newReloadableEmitSession(t, old, loader)
	previous := session.ExtensionRunner()
	if err := session.Reload(t.Context()); !errors.Is(err, loader.err) {
		t.Fatalf("Reload error = %v, want %v", err, loader.err)
	}
	loader.err, loader.extensions = nil, []extension.Extension{fresh.extension("<new>")}
	if err := session.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if session.ExtensionRunner() == previous {
		t.Fatal("the reload that loaded must replace the runner")
	}
	requireRetired(t, "failed reload, then a reload that loaded", previous, old)
	driveEveryEmitOfTheSession(t, session)
	if got := fresh.count("tool_call"); got != 1 {
		t.Errorf("the new instance's tool_call handler ran %d times, want 1", got)
	}
}

func TestAReloadThatLoadedRetiresTheOldRunner(t *testing.T) {
	old, fresh := newEmitCounter(), newEmitCounter()
	loader := &swapLoader{ResourceLoader: NoResources, extensions: nil}
	session := newReloadableEmitSession(t, old, loader)
	previous := session.ExtensionRunner()
	loader.extensions = []extension.Extension{fresh.extension("<new>")}
	if err := session.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireRetired(t, "superseded", previous, old)
}

// Exactly one instance receives an event at every point of a good reload that follows a failed one: the Session reads its current runner once per emit, the old runner (stale, after the
// failed reload) stays current until the swap, the new runner is current from the swap, and the old one is retired by the same step. A hook that runs inside the reload (where an extension
// factory runs) reaches the old instance alone; every emit after it reaches the new instance alone.
func TestAnEventAroundAReloadThatFollowsAFailedOneReachesExactlyOneInstance(t *testing.T) {
	old, fresh := newEmitCounter(), newEmitCounter()
	loader := &swapLoader{ResourceLoader: NoResources, err: errors.New("resource reload failed")}
	session := newReloadableEmitSession(t, old, loader)
	if err := session.Reload(t.Context()); !errors.Is(err, loader.err) {
		t.Fatalf("Reload error = %v, want %v", err, loader.err)
	}
	driveEveryEmitOfTheSession(t, session)
	inWindow := map[string][2]int{}
	loader.err, loader.extensions = nil, []extension.Extension{fresh.extension("<new>")}
	loader.during = func() {
		driveEveryEmitOfTheSession(t, session)
		for _, event := range emitCounterEvents {
			inWindow[event] = [2]int{old.count(event), fresh.count(event)}
		}
	}
	if err := session.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, event := range emitCounterEvents {
		if got := inWindow[event]; got != [2]int{2, 0} {
			t.Errorf("%s inside the reload: old and new instance counts = %v, want the old instance alone ([2 0])", event, got)
		}
	}
	driveEveryEmitOfTheSession(t, session)
	for _, event := range emitCounterEvents {
		if got := old.count(event); got != 2 {
			t.Errorf("%s: the old instance ran %d times in all, want 2 (the failed reload's prompt and the window)", event, got)
		}
		if got := fresh.count(event); got != 1 {
			t.Errorf("%s: the new instance ran %d times, want 1", event, got)
		}
	}
}
