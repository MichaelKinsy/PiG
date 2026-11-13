package host

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// pi: packages/chord/src/remote/remote-object.ts

// Ports the describe "cancellation (accepted difference 4)" of 'references sharing one wire hold (decision "References
// share one wire hold")' of packages/chord/test/remote/remote-object.test.ts (Pi 47e9d74f9 + 90ed3e3b1).

// rsWaiterVariants are the variants of Pi's `Waiter` and `DeepWaiter`: `wait(tag)`.
func rsWaiterVariants() []contracts.VariantEntry {
	return []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
		State:   []contracts.Property{contracts.Prop("n", contracts.Int())},
		Methods: []contracts.MethodEntry{contracts.Meth("wait", contracts.NewMethod(contracts.Str()))},
	})}
}

// rsWaiter is Pi's `Waiter`: a waiter placed on a session's worker: `wait(tag)` records its tag, resolves when its
// context aborts.
var rsWaiter = mustContract(contracts.Remote("app.waiter", contracts.RemoteDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
		session, _ := params.Value("session").(string)
		return Session.Address(jsonObject("id", session)), nil
	},
	Variants: rsWaiterVariants(),
}))

// rsTags are the tags the waiters' calls recorded, in the order their handlers started.
type rsTags struct {
	mu   sync.Mutex
	list []string
}

func (s *rsTags) add(tag string) {
	s.mu.Lock()
	s.list = append(s.list, tag)
	s.mu.Unlock()
}

func (s *rsTags) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.list)
}

// rsServeWaiter is Pi's `serveWaiter`: it serves typ (a waiter), recording each call's tag in seen.
func rsServeWaiter(h FacetHost, typ *contracts.Contract, seen *rsTags) error {
	return h.Serve(typ, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
		return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"n": rhConst(0.0)},
			Methods: map[string]contracts.MethodFunction{
				"wait": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					seen.add(args.(string))
					// Pi: `await new Promise((resolve) => ctx.abortSignal?.addEventListener("abort", resolve))`.
					_, _ = chordctx.Receive(ctx, make(chan struct{}))
					return nil, nil
				},
			},
		}}, nil
	}, ServeOptions{})
}

// rsRejectedWith fails the test unless call rejected with code.
func rsRejectedWith(t testing.TB, what string, call *parkedCallResult, code data.ChordErrorCode) {
	t.Helper()
	_, err := call.wait(background)
	if err == nil {
		t.Fatalf("%s resolved, want %s", what, code)
	}
	if got := chordOf(t, err).Code; got != code {
		t.Fatalf("%s rejected %s, want %s", what, got, code)
	}
}

// rsCauseCode is the code of the chord error a handler's context ended with ("" while it runs): Pi's
// `ctx.abortSignal.reason.code`.
func rsCauseCode(ctx context.Context) data.ChordErrorCode {
	cause := context.Cause(ctx)
	if cause == nil {
		return ""
	}
	if chordErr, is := data.IsChordError(cause); is {
		return chordErr.Code
	}
	return data.ChordErrorCode(cause.Error())
}

func TestReferencesSharingOneWireHoldCancellation(t *testing.T) {
	// remote-object.test.ts: references sharing one wire hold › cancellation (accepted difference 4) › a non-final release: the caller gets released, the provider's handler aborted, a sibling's call survives; the last release: released
	//
	// Pi names the two handlers by the order they started (`probe.waits[0]` is A's). Go starts handlers off the loop, so
	// the case makes that order its own: A's call is running at the provider before B's is made.
	t.Run("a non-final release: the caller gets released, the provider's handler aborted, a sibling's call survives; the last release: released", func(t *testing.T) {
		var probe *notesProbe
		hosts := linked(t, func(h FacetHost) error {
			made, err := serveNotes(h, nil)
			probe = made
			return err
		}, linkedOptions{})
		get := func() *Reference {
			ref, err := hosts.Client.Get(background, Notes, rhNotes("S1"), GetOptions{})
			must(t, err)
			return ref
		}
		a := get()
		b := get()
		aCall := startCall(t, a, "wait", nil, background)
		probe.AwaitWaits(t, 1)
		bCall := startCall(t, b, "wait", nil, background)
		waits := probe.AwaitWaits(t, 2)
		settleLinked(hosts)
		if len(waits) != 2 {
			t.Fatalf("waits = %d, want 2", len(waits))
		}
		a.Release()
		rsRejectedWith(t, "a's call", aCall, data.CodeReleased)
		rhWaitFor(t, func() bool { return waits[0].Err() != nil })
		settleLinked(hosts)
		if codes := []data.ChordErrorCode{rsCauseCode(waits[0]), rsCauseCode(waits[1])}; codes[0] != data.CodeAborted || codes[1] != "" {
			t.Fatalf("the handlers' reasons = %q, want [aborted, none]", codes)
		}
		b.Release()
		rsRejectedWith(t, "b's call", bCall, data.CodeReleased)
		rhWaitFor(t, func() bool { return waits[1].Err() != nil })
		if code := rsCauseCode(waits[1]); code != data.CodeReleased {
			t.Fatalf("the sibling's handler's reason = %q, want released", code)
		}
	})

	// remote-object.test.ts: references sharing one wire hold › cancellation (accepted difference 4) › through a relay with an upstream window of 1 (A1 running, A2 and B1 queued there): the worker sees A1 and B1, never A2
	t.Run("through a relay with an upstream window of 1 (A1 running, A2 and B1 queued there): the worker sees A1 and B1, never A2", func(t *testing.T) {
		seen := &rsTags{}
		window := 1
		rel := relay(t, relayedOptions{WorkerCallWindow: &window, Worker: func(h FacetHost) error { return rsServeWaiter(h, rsWaiter, seen) }})
		get := func() *Reference {
			ref, err := rel.Tab.Get(background, rsWaiter, rhNotes("S1"), GetOptions{})
			must(t, err)
			return ref
		}
		a := get()
		b := get()
		calls := []*parkedCallResult{startCall(t, a, "wait", "A1", background), startCall(t, a, "wait", "A2", background)}
		sibling := startCall(t, b, "wait", "B1", background)
		rhWaitFor(t, func() bool { return len(seen.all()) >= 1 })
		rel.Transported()
		if got := seen.all(); !slices.Equal(got, []string{"A1"}) {
			t.Fatalf("seen = %v, want [A1]", got)
		}
		a.Release()
		for index, call := range calls {
			if _, err := call.wait(background); err == nil {
				t.Fatalf("call %d of the released reference resolved, want a rejection", index)
			}
		}
		rhWaitFor(t, func() bool { return len(seen.all()) >= 2 })
		rel.Transported()
		if got := seen.all(); !slices.Equal(got, []string{"A1", "B1"}) {
			t.Fatalf("seen = %v, want [A1 B1]", got)
		}
		b.Release()
		rsRejectedWith(t, "the sibling's call", sibling, data.CodeReleased)
	})

	// remote-object.test.ts: references sharing one wire hold › cancellation (accepted difference 4) › through two relays with upstream windows 2 and 1, owners interleaved: the provider never sees the released owner's queued call
	t.Run("through two relays with upstream windows 2 and 1, owners interleaved: the provider never sees the released owner's queued call", func(t *testing.T) {
		deep := mustKind(contracts.NewHostKind("deep"))
		deepWaiter := mustContract(contracts.Remote("app.deep-waiter", contracts.RemoteDeclaration{
			Version: 1, Params: contracts.Params{},
			HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return deep.Address(nil), nil },
			Variants:    rsWaiterVariants(),
		}))
		seen := &rsTags{}
		var mu sync.Mutex
		var links []*link.MemoryLink
		pair := func() (*link.MemoryLink, *link.MemoryLink) {
			near, far := link.MemoryLinkPair()
			mu.Lock()
			links = append(links, near, far)
			mu.Unlock()
			return near, far
		}
		// settled is Pi's `for (let i = 0; i < 6; i++) await transported()`: what is in flight on every hop arrived.
		settled := func() {
			mu.Lock()
			all := slices.Clone(links)
			mu.Unlock()
			for range 6 {
				transported(all...)
			}
		}
		one := 1
		provider := newHost(t, HostOptions{Address: new(deep.Address(nil)), Principal: serverPrincipal(), CallWindow: &one, OnError: func(HostError) {}})
		mustLoad(t)(provider.Load([]HostFacet{{Name: "@test/deep", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return rsServeWaiter(h, deepWaiter, seen)
		})}}, background))
		// relayTo is a relay host routing `deep` to upstream, with callWindow on the links it accepts.
		relayTo := func(upstream Host, callWindow int) Host {
			host := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), CallWindow: &callWindow, OnError: func(HostError) {}})
			mustRoute(t)(host.Route(deep, RouteOptions{
				Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
				Resolve: func(contracts.Address, context.Context) (ResolvedLink, error) {
					if t.Context().Err() != nil {
						return ResolvedLink{}, errors.New("the test is ending")
					}
					near, far := pair()
					// The upstream takes the relay as its parent: a child link carries no origins.
					if err := upstream.Connect(far, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background); err != nil {
						return ResolvedLink{}, err
					}
					return resolved{Link: near, Principal: serverPrincipal(), Codec: testCodec()}.link(), nil
				},
			}, background))
			mustLoad(t)(host.Load(nil, background))
			return host
		}
		inner := relayTo(provider, 2)
		outer := relayTo(inner, 2)
		tab := newHost(t, HostOptions{OnError: func(HostError) {}})
		mustLoad(t)(tab.Load(nil, background))
		near, far := pair()
		must(t, outer.Accept(far, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
		must(t, tab.Connect(near, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
		get := func() *Reference {
			ref, err := tab.Get(background, deepWaiter, jsonObject(), GetOptions{})
			must(t, err)
			return ref
		}
		a := get()
		b := get()
		// The tab's window (2) sends A1 and A2, the outer relay forwards both (2), the inner one runs A1 (1) and queues
		// A2; B1 and B2 wait in the tab.
		calls := []*parkedCallResult{
			startCall(t, a, "wait", "A1", background), startCall(t, a, "wait", "A2", background),
			startCall(t, b, "wait", "B1", background), startCall(t, b, "wait", "B2", background),
		}
		rhWaitFor(t, func() bool { return len(seen.all()) >= 1 })
		settled()
		if got := seen.all(); !slices.Equal(got, []string{"A1"}) {
			t.Fatalf("seen = %v, want [A1]", got)
		}
		// abort(A2) first drops A2 at the inner relay; abort(A1) then frees the provider's slot for B1. Oldest first,
		// that slot would send A2.
		a.Release()
		rhWaitFor(t, func() bool { return len(seen.all()) >= 2 })
		settled()
		if got := seen.all(); !slices.Equal(got, []string{"A1", "B1"}) {
			t.Fatalf("seen = %v, want [A1 B1]", got)
		}
		b.Release()
		for index, call := range calls {
			rsRejectedWith(t, []string{"A1", "A2", "B1", "B2"}[index], call, data.CodeReleased)
		}
		settled()
		if got := seen.all(); !slices.Equal(got, []string{"A1", "B1"}) {
			t.Fatalf("seen at the end = %v, want [A1 B1]", got)
		}
		// Downstream first: a relay closing before its consumer would resolve its route again for it.
		for _, host := range []Host{tab, outer, inner, provider} {
			must(t, host.Close(background))
		}
	})
}
