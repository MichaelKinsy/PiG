package coding

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi agent-session.ts:_cycleAvailableModel applies model/thinking/transcript state before awaiting _emitModelSelect. The Go blocking API must still wait for that notification.
func TestModelChangePrefixAndAwaitedNotification(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Path: "/ext/model", Handlers: map[string][]extension.HandlerFn{"model_select": {func(args ...any) (any, error) {
		event := args[0].(extension.ModelSelectEvent)
		if event.Source != extension.ModelSelectSourceCycle {
			t.Errorf("source=%q", event.Source)
		}
		close(entered)
		select {
		case <-release:
		case <-args[1].(context.Context).Done():
		}
		return nil, nil
	}}}}})
	next := *h.session.Model()
	next.ID = "next"
	done := make(chan error, 1)
	go func() { done <- h.session.CycleToModel(&next) }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("model change returned before listener entry: %v", err)
	}
	if h.session.Model().ID != "next" {
		t.Fatal("model prefix did not run before listener")
	}
	select {
	case err := <-done:
		t.Fatalf("blocking CycleToModel returned before notification: %v", err)
	default:
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
