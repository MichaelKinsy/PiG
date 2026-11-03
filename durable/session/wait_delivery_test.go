package session_test

// pig divergence (D224), round 4 review B1: every blocking point of the Session a listener can reach is a wait of the Session, so a
// listener that calls it has entered (chord.DeliveryEntered) and the hop that waits for its synchronous part publishes the next
// part of the call. The cases of TestGroupCommitAListenerThatUnloadsDocumentsDoesNotStallTheHop (onLine, awaitDrained, the writer's
// part wait) are the same shape; these are the other calls of a listener: Close, a ticket's Release.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// listenerAtPartTwo runs a call of four parts behind a held storage call, with a watch listener that calls act at the first
// publication it sees after part 1 (part 2); with parked, a job holds the line until the last part is published and the listener's
// ticket queues behind it. It fails when act does not return.
func listenerAtPartTwo(t *testing.T, parked bool, act func(g *gc, ticket *session.LineTicket) error) error {
	t.Helper()
	g := openGC(t, 4)
	a, b, c, d := g.ids[0], g.ids[1], g.ids[2], g.ids[3]
	var published atomic.Int64
	allPublished := make(chan struct{})
	var publishedOnce sync.Once
	publishAll := func() { publishedOnce.Do(func() { close(allPublished) }) }
	acted := make(chan error, 1)
	var ticket *session.LineTicket
	var once sync.Once
	watch := session.NewSessionWatch(g.session, 0, func() {})
	watch.Start(func(context.Context, durable.WatchCall[int]) error {
		once.Do(func() { acted <- act(g, ticket) })
		return nil
	})
	g.session.FollowCommits(func(callCtx context.Context, commit session.ObservedCommit) {
		n := published.Add(1)
		if n > 1 {
			watch.Advance(callCtx, durable.WatchCall[int]{Kind: durable.WatchCallValue, Value: int(commit.Seq)})
		}
		if n == 4 {
			publishAll()
		}
	})
	held := g.storage.hold()
	first := g.bump(a)
	<-held.Entered()
	second := g.bump(b)
	third := g.bump(c)
	fourth := g.bump(d)
	calls := []*call{first, second, third, fourth}
	if parked {
		calls = append(calls, g.readAsync(func(durable.Storage) (any, error) { <-allPublished; return nil, nil }))
		ticket = g.session.TakeLineTicket()
	}
	g.awaitQueued(3)
	held.Release()
	var err error
	select {
	case err = <-acted:
	case <-time.After(10 * time.Second):
		// Unstick the job that holds the line, so the Session can still end.
		publishAll()
		t.Error("the listener's call never returned: the hop waits for the listener, which waits for the parts behind it")
	}
	for _, call := range calls[:4] {
		mustBump(t, call)
	}
	return err
}

func TestGroupCommitAListenerThatClosesTheSessionDoesNotStallTheHop(t *testing.T) {
	err := listenerAtPartTwo(t, false, func(g *gc, _ *session.LineTicket) error {
		closeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return g.session.Close(closeCtx)
	})
	if err != nil {
		t.Fatalf("Close from a listener = %v, want nil", err)
	}
}

func TestGroupCommitAListenerThatReleasesATicketBehindAJobDoesNotStallTheHop(t *testing.T) {
	err := listenerAtPartTwo(t, true, func(_ *gc, ticket *session.LineTicket) error {
		ticket.Release()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
