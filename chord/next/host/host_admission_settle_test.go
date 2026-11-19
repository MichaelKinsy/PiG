package host

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Go-only (no Pi case): a Pi decision's `settle` promise never settles once the decision is released; a Go task that waits
// for it (`settleWait`, a route's) must end. It ends through a context, the decision's lifetime, which `release` cancels.

// noLong is a `blocked` that reports nothing: a route's decision is no attempt.
type noLong struct{}

func (noLong) blockedBy(*blocker) {}

// releasing is a `blocked` that releases its decision from inside the section that settles it: `settle` names its blocker there,
// before it resolves its verdict, so the release and the verdict are both there when the waiter selects.
type releasing struct{ decision *Decision }

func (r releasing) blockedBy(*blocker) { r.decision.release() }

// namesBlocker is a `blocked` that tells when its waiter named a blocker: the wait it reports has begun.
type namesBlocker struct{ named chan struct{} }

func (n *namesBlocker) blockedBy(found *blocker) {
	if found == nil {
		return
	}
	select {
	case n.named <- struct{}{}:
	default:
	}
}

// unservedDecision declares, in a section of h, a decision that asks for a type nobody serves.
func unservedDecision(t *testing.T, h *chordHost) *Decision {
	t.Helper()
	var decision *Decision
	var err error
	h.enter(func() {
		decision = newDecision(decisionOwner{host: h, label: "test.route", caller: h.own(), moved: func() {}})
		err = decision.declare(func(self AdmitSelf) (any, error) {
			self.Get(admMissing, jsonObject())
			return self.Derive(nil, func([]any) (delta.JsonValue, error) { return Allow("v"), nil }), nil
		}, background)
	})
	must(t, err)
	return decision
}

func TestDecisionSettleWait(t *testing.T) {
	made := func(t *testing.T) (*chordHost, *Decision) {
		s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{Mode: "write"})
		h := s.h.(*chordHost)
		var decision *Decision
		var err error
		h.enter(func() {
			decision = newDecision(decisionOwner{host: h, label: "test.route", caller: h.own(), moved: func() {}})
			err = decision.declare(func(self AdmitSelf) (any, error) {
				return self.Derive(nil, func([]any) (delta.JsonValue, error) { return Allow("v"), nil }), nil
			}, background)
		})
		must(t, err)
		return h, decision
	}

	t.Run("a settled decision's waiter gets its allow", func(t *testing.T) {
		_, decision := made(t)
		found := decision.settleWait(noLong{}, background)
		if found.kind != verdictAllow || found.value != "v" {
			t.Fatalf("verdict = %+v, want allow v", found)
		}
	})

	t.Run("a released decision's waiter returns, and what it returns is neither an allow nor a denial", func(t *testing.T) {
		h, decision := made(t)
		h.enter(decision.release)
		found := decision.settleWait(noLong{}, background)
		if found.kind != verdictBroken || found.err == nil || found.err.Code != data.CodeReleased || !errors.Is(found.err, ErrDecisionReleased) {
			t.Fatalf("verdict = %+v, want broken released (errors.Is ErrDecisionReleased)", found)
		}
		// The same through `settle`, the form an attempt's parked check takes: its callers act on allow and deny only.
		var promise *keyedPromise[*verdict]
		h.enter(func() { promise = decision.settle(noLong{}, background) })
		got, err := promise.await(background)
		if err != nil || got.kind != verdictBroken || got.err.Code != data.CodeReleased || !errors.Is(got.err, ErrDecisionReleased) {
			t.Fatalf("settle = %+v, %v, want broken released (errors.Is ErrDecisionReleased)", got, err)
		}
	})

	t.Run("a waiter on a request nobody serves gets the parked verdict at once: it has nothing to wait for", func(t *testing.T) {
		// Pi's `settle` returns the unsettled verdict of a check that is not live without waiting (admission.ts:238-258): a type
		// nobody serves parks the attempt, it does not park the waiter.
		s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{Mode: "write"})
		h := s.h.(*chordHost)
		decision := unservedDecision(t, h)
		found := decision.settleWait(noLong{}, background)
		if found.kind != verdictParked || !found.firstPass || found.cause == nil {
			t.Fatalf("verdict = %+v, want parked at the first pass with its cause", found)
		}
		h.enter(decision.release)
	})

	t.Run("a waiter that waits on a live query's first run returns once the decision is released", func(t *testing.T) {
		// The query's first run waits behind a commit that is held, so the waiter is parked in `settle` when the release comes: it
		// does not depend on which of the two goroutines reaches the host first. The probe says when the waiter named its blocker,
		// and the release is the test's next step.
		chordStore, _, holdNext := faultyStore(t)
		address := Server.Address(nil)
		h := newHost(t, HostOptions{Address: &address, Principal: serverPrincipal(), Store: chordStore, OnError: func(HostError) {}}).(*chordHost)
		mustLoad(t)(h.Load([]HostFacet{{Name: "@test/shelf", Facet: admShelfFacet(&admFlag{})}}, background))
		shelf := mustGet(t, h, admShelf, jsonObject())
		_, err := shelf.Call("put", "a", background)
		must(t, err)
		var decision *Decision
		h.enter(func() {
			decision = newDecision(decisionOwner{host: h, label: "test.route", caller: h.own(), moved: func() {}})
			err = decision.declare(func(self AdmitSelf) (any, error) {
				items := self.LiveQuery(self.Get(admShelf, jsonObject()).Queries("items")["all"], jsonObject(), 3)
				return self.Derive([]any{items}, func([]any) (delta.JsonValue, error) { return Allow("v"), nil }), nil
			}, background)
		})
		must(t, err)
		held := holdNext()
		// Released at the test's end whatever failed: the host's close waits for the held commit.
		t.Cleanup(held.Release)
		writing := make(chan error, 1)
		go func() { _, err := shelf.Call("put", "b", background); writing <- err }()
		receive(t, held.Entered())
		naming := &namesBlocker{named: make(chan struct{}, 8)}
		waiter := make(chan *verdict, 1)
		go func() { waiter <- decision.settleWait(naming, background) }()
		receive(t, naming.named)
		h.enter(decision.release)
		// Returned while the commit is still held: the release ended the wait, not the query's first run.
		found := receive(t, waiter)
		if found.kind != verdictBroken || !errors.Is(found.err, ErrDecisionReleased) {
			t.Fatalf("verdict = %+v, want broken, ErrDecisionReleased", found)
		}
		held.Release()
		must(t, receive(t, writing))
		closeHosts(t, h)
	})

	t.Run("a release in the turn of the verdict wins: the waiter gets released, never the verdict", func(t *testing.T) {
		s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{Mode: "write"})
		h := s.h.(*chordHost)
		// The release and the verdict are both ready when the wait selects, and select picks one at random: each decision is a
		// toss, and half of them gave the waiter the parked verdict before the release was preferred.
		for i := range 200 {
			decision := unservedDecision(t, h)
			found := decision.settleWait(releasing{decision}, background)
			if found.kind != verdictBroken || !errors.Is(found.err, ErrDecisionReleased) {
				t.Fatalf("decision %d: verdict = %+v, want broken, ErrDecisionReleased", i, found)
			}
		}
	})

	t.Run("a genuinely broken admit is no release: its cause is not ErrDecisionReleased", func(t *testing.T) {
		s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{Mode: "write"})
		h := s.h.(*chordHost)
		var decision *Decision
		var err error
		h.enter(func() {
			decision = newDecision(decisionOwner{host: h, label: "test.route", caller: h.own(), moved: func() {}})
			err = decision.declare(func(self AdmitSelf) (any, error) {
				// Neither allow(value) nor deny(reason).
				return self.Derive(nil, func([]any) (delta.JsonValue, error) { return jsonObject("kind", "maybe"), nil }), nil
			}, background)
		})
		must(t, err)
		found := decision.settleWait(noLong{}, background)
		if found.kind != verdictBroken || found.err == nil || errors.Is(found.err, ErrDecisionReleased) {
			t.Fatalf("verdict = %+v, want broken and not ErrDecisionReleased", found)
		}
	})

	t.Run("an attempt whose decision is released while it waits neither admits nor shows a denial by it", func(t *testing.T) {
		built := &admLog{}
		waits := func(params *delta.JsonObject, principal data.Principal, self AdmitSelf) (chord.Watchable[delta.JsonValue], error) {
			self.Get(admMissing, jsonObject())
			return admitBy(false)(params, principal, self)
		}
		s := admServed(t, admDocCode{Built: built, Admit: waits}, admOptions{Mode: "write"})
		h := s.h.(*chordHost)
		doc := mustHold(t, s.h, admDoc, jsonObject("id", "a"))
		s.delivered()
		admWaiting(t, doc)
		var first *attempt
		h.enter(func() { first = reloadObjectOf(doc).phase.attempt })
		// The reload's attempt binds its own requests: the sealed first attempt's decision, still waiting on the type nobody
		// serves, is released.
		_, err := s.reload(map[string]Facet{"doc": admDocFacet(admDocCode{Built: built, Admit: waits}, admDoc, nil)})
		must(t, err)
		s.delivered()
		var released bool
		h.enter(func() { released = first.decision.released })
		if !released {
			t.Fatalf("the first attempt's decision was not released by its successor's admission")
		}
		// The object shows its successor's wait: not the release (broken), not a denial, and nothing was admitted or built.
		if message := admWaiting(t, doc).Cause.Error.Message; !strings.Contains(message, "test.missing") {
			t.Fatalf("message = %q, want it to name test.missing", message)
		}
		expectStrings(t, built.Items())
		expectNoErrors(t, s.errors)
		doc.Release()
		must(t, s.h.Close(background))
	})
}
