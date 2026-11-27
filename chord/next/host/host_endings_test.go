package host

import (
	"context"
	"sync"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Pi's #tearDown (host/object.ts:1623-1640) is `await teardown.previous; await attempt.close(reason); ...`: an ending
// waiting for its predecessor is a promise reaction, a closure, not a thread of control. The cases below pin that for the
// Go form: the work of an ending starts, as a counted task, in the section that settles its predecessor; until then it is
// no goroutine and no task (awaitAfter), and the endings of one identity run in the order they were made, one at a time.

// serveGatedNotes serves Notes whose attempts open a native that closes only once gate is closed: a teardown parked on a
// real wait (Pi: `await native.close()`, attempt.ts:1375-1390).
func serveGatedNotes(h FacetHost, gate <-chan struct{}) error {
	return h.Serve(Notes, func(_ *delta.JsonObject, _ data.Principal, _ AdmittedValue, self Self) (contracts.MembersImplementation, error) {
		self.Open(func(context.Context) (any, error) { return nil, nil },
			func(_ any, _ NativeCloseReason, ctx context.Context) error {
				_, _ = chordctx.Receive(context.WithoutCancel(ctx), gate)
				return nil
			})
		stub := func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }
		return contracts.MembersImplementation{
			State:   map[string]chord.Watchable[delta.JsonValue]{"notes": constant([]any{}), "count": constant(float64(0))},
			Methods: map[string]contracts.MethodFunction{"add": stub, "fail": stub, "wait": stub, "echo": stub},
		}, nil
	}, ServeOptions{Admit: func(*delta.JsonObject, data.Principal, AdmitSelf) (chord.Watchable[delta.JsonValue], error) {
		return constant(Allow(delta.JsonObjectOf("granted", []delta.JsonValue{"add", "fail", "echo"}))), nil
	}})
}

// tasksAlive is how many tasks the host's loop runs or has parked on a wait.
func tasksAlive(loop *Loop) int {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	return len(loop.inflight)
}

// 1,000 holds of one identity, each released at once, queue behind a first ending that waits on a native's close. Each of
// the 999 endings behind it is a reaction of its predecessor: the only task alive is the head's (a Go goroutine per pending
// ending made 999 parked tasks of them, 5.7 KiB each: 14,626 at 20,000 holds).
func TestEndingsBehindARunningTeardownAreNoTasks(t *testing.T) {
	const behind = 999
	gate := make(chan struct{})
	var open sync.Once
	letGo := func() { open.Do(func() { close(gate) }) }
	// Also on a failed assertion: the host's close, a cleanup that runs after this function, waits for the head's native.
	defer letGo()
	s := rawLinked(t, func(h FacetHost) error { return serveGatedNotes(h, gate) }, rawLinkedOptions{Parent: true})
	h := s.Server.(*chordHost)
	loop := h.loop().(*Loop)
	spec := specOf(Notes)
	hold := func(origin int) *link.HoldMessage {
		return &link.HoldMessage{
			Origin: link.Some(origin), Hold: 0, Type: spec.Name, Version: spec.Version, Fingerprint: spec.Fingerprint,
			Params: mustJSONPart(t, jsonObject("session", "S")), Address: serverWireAddress,
		}
	}
	s.Peer.Send(hold(1))
	s.Peer.Next("bound")
	s.Peer.Send(&link.ReleaseMessage{Origin: link.Some(1), Hold: 0})
	rhSync(t, s.Peer)
	loop.quiesce()
	// The head's ending, parked on the close of its one native, which runs as a task of its own (runParallel).
	const head = 2
	if got := tasksAlive(loop); got != head {
		t.Fatalf("tasks alive with the head's close parked = %d, want %d (its ending and its native's close)", got, head)
	}
	for origin := 2; origin <= 1+behind; origin++ {
		s.Peer.Send(hold(origin))
		s.Peer.Send(&link.ReleaseMessage{Origin: link.Some(origin), Hold: 0})
	}
	rhSync(t, s.Peer)
	loop.quiesce()
	// Still the head's two: the endings behind it wait for it as reactions, not as tasks.
	if got := tasksAlive(loop); got != head {
		t.Fatalf("tasks alive with %d endings pending behind the head = %d, want %d (the head's)", behind, got, head)
	}
	letGo()
	rhSync(t, s.Peer)
	loop.quiesce()
	if got := tasksAlive(loop); got != 0 {
		t.Fatalf("tasks alive once the head closed = %d, want 0", got)
	}
	var chain int
	h.enter(func() { chain = len(h.endings) })
	if chain != 0 {
		t.Fatalf("the host still records %d endings once every teardown finished", chain)
	}
}

// endedWork stands for what an ending's work closes over: the sealed attempt and its teardown.
type endedWork struct{ _ [16]byte }

// returnedHandlerFinishedInline is a section that ends with a handler that returned inside it to finish: the call that the
// section settling an ending admits, as the section of a link's frame does (Loop.finish adopts the coroutine's goroutine for
// the task of the goroutine that ended the section: parking.Adopt).
func returnedHandlerFinishedInline(loop *Loop) { handlerSettlingAfterItsSection(loop, func() {}) }

// Nothing of an ending is kept once it ran. In Pi a settled promise holds no reaction, the async `#tearDown` frame is gone with
// its work (host/object.ts:1623-1640), and `host.ending` drops the identity's entry once its chain emptied (host.ts:1690-1696).
// Here the host's `endings` has no entry for the identity, the pacer has no record of any paced task, and what the endings
// closed over is collected: the pacer used to index its tasks by goroutine ID and delete the entry by the task's ID as it is at
// the task's end, which a coroutine adopted since (parking.Adopt) had changed, so a task whose section finished a handler
// inline stayed in the index with its work, and the attempt the work captured (the reload cycle's `referent 0 was not
// collected`). It keeps no such index now: the task is the identity.
func TestEndingsOnceRanLeaveNothingOfThemInTheHostNorThePacer(t *testing.T) {
	const endings = 3
	const identity = "endings/ran"
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	h := host.(*chordHost)
	loop := h.loop().(*Loop)
	refs := make([]weak.Pointer[endedWork], 0, endings)
	h.enter(func() {
		for range endings {
			work := &endedWork{}
			refs = append(refs, weak.Make(work))
			torn := awaitAfter(h, h.endingOf(identity), func() (struct{}, error) {
				*work = endedWork{}
				returnedHandlerFinishedInline(loop)
				return struct{}{}, nil
			})
			h.ending(identity, torn)
		}
	})
	loop.quiesce()
	var chain int
	h.enter(func() { chain = len(h.endings) })
	if chain != 0 {
		t.Fatalf("the host still records %d endings once every teardown finished, want none", chain)
	}
	if tasks := pacerHolds(loop); tasks != 0 {
		t.Fatalf("the pacer keeps %d paced tasks once every ending ran, want none", tasks)
	}
	nexttest.ExpectCollected(t, refs...)
}
