package host

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/next/host/ctx.test.ts. The cases that build a host (`loaded`, `linked`) run on a real host: a
// handler gets its `ctx` as the *callContext the host made for the call (every method's context.Context is a Ctx). The
// `linked abort signals` cases need no host.

var (
	ctxItem = mustContract(declareLocal("app.item", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("id", contracts.Int())},
	}))
	ctxDesk = mustContract(declareLocal("app.desk", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1,
		Members: []contracts.LocalEntry{
			contracts.Mem("use", contracts.NewMethod(contracts.TSchema{}, contracts.Int())),
			contracts.Mem("leak", contracts.NewMethod()),
			contracts.Mem("late", contracts.NewMethod()),
			contracts.Mem("who", contracts.NewMethod(contracts.TSchema{}, contracts.Str())),
		},
	}))
)

// scopeItem is a local type like `app.item`: one param `id`, one member `id`.
func scopeItem(t testing.TB, name string) *contracts.Contract {
	t.Helper()
	contract, err := declareLocal(name, contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("id", contracts.Int())},
	})
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

// wantScopeError fails unless err is a ChordError of code, reason and message.
func wantScopeError(t testing.TB, err error, code data.ChordErrorCode, reason data.ChordErrorReason, message string) {
	t.Helper()
	chordErr, ok := data.IsChordError(err)
	if !ok {
		t.Fatalf("err = %v (%T), want a ChordError", err, err)
	}
	if chordErr.Code != code || chordErr.Reason != reason || chordErr.Message != message {
		t.Fatalf("err = {%s %s %q}, want {%s %s %q}", chordErr.Code, chordErr.Reason, chordErr.Message, code, reason, message)
	}
}

// kindOrNone is `ctx.principal?.kind ?? "none"`.
func kindOrNone(principal data.Principal) string {
	if principal == nil {
		return "none"
	}
	return principalKind(principal)
}

// whoIs is the file's `who(ctx)`: the principal's kind and the incarnation of the ctx.
func whoIs(ctx context.Context) string {
	c := ctx.(Ctx)
	return fmt.Sprintf("%s %s", kindOrNone(c.Principal()), c.Incarnation())
}

// deskSeen is what a desk's handlers recorded.
type deskSeen struct {
	mu        sync.Mutex
	ctx       Ctx
	item      *Reference
	factories int
	late      chan error
	// lateGate holds the open of item 3 until the test lets it go.
	lateGate chan struct{}
}

func (s *deskSeen) factoryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.factories
}

// desk is a desk whose handlers use `ctx.get`; what they got and their contexts are recorded.
func desk(t *testing.T) (Host, *deskSeen, *Reference) {
	t.Helper()
	seen := &deskSeen{late: make(chan error, 1), lateGate: make(chan struct{})}
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(ctxItem, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			seen.mu.Lock()
			seen.factories++
			seen.mu.Unlock()
			if params.Value("id") == float64(3) {
				selfOpen(self, func(context.Context) error { <-seen.lateGate; return nil }, func(NativeCloseReason) {})
			}
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"id": constant(params.Value("id"))},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(ctxDesk, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"use": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					item, err := ctx.(Ctx).Get(ctxItem, jsonObject("id", float64(1)), GetOptions{})
					if err != nil {
						return nil, err
					}
					return item.Member("id").Value()
				},
				"leak": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					seen.mu.Lock()
					seen.ctx = ctx.(Ctx)
					seen.mu.Unlock()
					item, err := ctx.(Ctx).Get(ctxItem, jsonObject("id", float64(2)), GetOptions{})
					seen.mu.Lock()
					seen.item = item
					seen.mu.Unlock()
					return nil, err
				},
				"late": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					c := ctx.(Ctx)
					go func() { _, err := c.Get(ctxItem, jsonObject("id", float64(3)), GetOptions{}); seen.late <- err }()
					// The get's synchronous part ran (its object is created) before the handler settles, as in Pi.
					scopeEventually(t, "the late get created its object", func() bool { return seen.factoryCount() >= 1 })
					return nil, nil
				},
				"who": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return whoIs(ctx), nil },
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	ref, err := host.Get(ctxDesk, jsonObject(), background, GetOptions{})
	must(t, err)
	return host, seen, ref
}

// Mirrors ctx.test.ts "ctx › ctx.get references are released when the handler settles".
func TestCtxGetReferencesAreReleasedWhenTheHandlerSettles(t *testing.T) {
	host, seen, ref := desk(t)
	defer closeHosts(t, host)
	if got, err := ref.Call("use", nil, background); err != nil || got != float64(1) {
		t.Fatalf("use = %v, %v; want 1", got, err)
	}
	if _, err := ref.Call("leak", nil, background); err != nil {
		t.Fatal(err)
	}
	seen.mu.Lock()
	item := seen.item
	seen.mu.Unlock()
	if _, err := item.Member("id").Value(); err == nil || err.Error() != "app.item: released" {
		t.Fatalf("a released reference's read = %v, want app.item: released", err)
	}
	// The object ended with its last reference: a new get creates it anew.
	if _, err := ref.Call("use", nil, background); err != nil {
		t.Fatal(err)
	}
	if got := seen.factoryCount(); got != 3 {
		t.Fatalf("factories = %d, want 3", got)
	}
}

// Mirrors "ctx › ctx.get after the handler settled throws released".
func TestCtxGetAfterTheHandlerSettledThrowsReleased(t *testing.T) {
	host, seen, ref := desk(t)
	defer closeHosts(t, host)
	if _, err := ref.Call("leak", nil, background); err != nil {
		t.Fatal(err)
	}
	seen.mu.Lock()
	settled := seen.ctx
	seen.mu.Unlock()
	_, err := settled.Get(ctxItem, jsonObject("id", float64(1)), GetOptions{})
	wantScopeError(t, err, data.CodeReleased, data.ReasonSettled, "app.item: ctx.get after its call settled")
}

// Mirrors "ctx › ctx.get of a handler whose call was aborted checks the signal first: nothing is created".
func TestCtxGetOfAHandlerWhoseCallWasAbortedChecksTheSignalFirstNothingIsCreated(t *testing.T) {
	var factories int
	var mu sync.Mutex
	entered, gate := make(chan struct{}), make(chan struct{})
	got := make(chan error, 1)
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(ctxItem, func(params *delta.JsonObject, _ Self) (contracts.Implementation, error) {
			mu.Lock()
			factories++
			mu.Unlock()
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"id": constant(params.Value("id"))},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(ctxDesk, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"use": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					close(entered)
					<-gate
					// The caller's abort reaches the call's context through a listener of its own.
					<-ctx.Done()
					_, err := ctx.(Ctx).Get(ctxItem, jsonObject("id", float64(1)), GetOptions{})
					got <- err
					return float64(0), nil
				},
				"leak": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
				"late": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
				"who":  func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return "", nil },
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	defer closeHosts(t, host)
	ref, err := host.Get(ctxDesk, jsonObject(), background, GetOptions{})
	must(t, err)
	callerCtx, abortCaller := chordctx.WithCancel(background)
	done := make(chan struct{})
	go func() { _, _ = ref.Call("use", nil, callerCtx); close(done) }()
	<-entered
	abortCaller(scopeReason("caller gave up"))
	close(gate)
	if err := <-got; err == nil || err.Error() != "caller gave up" {
		t.Fatalf("ctx.get = %v, want the caller's reason", err)
	}
	<-done
	mu.Lock()
	defer mu.Unlock()
	if factories != 0 {
		t.Fatalf("factories = %d, want 0", factories)
	}
}

// Mirrors "ctx › a ctx.get finishing after its handler settled is released at once (race 11)".
func TestCtxAGetFinishingAfterItsHandlerSettledIsReleasedAtOnceRace11(t *testing.T) {
	host, seen, ref := desk(t)
	defer closeHosts(t, host)
	if _, err := ref.Call("late", nil, background); err != nil {
		t.Fatal(err)
	}
	// The get's object opens only now: the handler settled while it waited.
	close(seen.lateGate)
	wantScopeError(t, <-seen.late, data.CodeReleased, data.ReasonSettled, "app.item: ctx.get after its call settled")
	turn(host)
	// The late reference's object ended with it.
	if got := seen.factoryCount(); got != 1 {
		t.Fatalf("factories = %d, want 1", got)
	}
	h := host.(*chordHost)
	var items int
	h.enter(func() {
		for _, object := range h.objectList() {
			if object.served.contract.Name == "app.item" {
				items++
			}
		}
	})
	if items != 0 {
		t.Fatalf("app.item objects alive = %d, want 0", items)
	}
}

// Mirrors "ctx › a ctx.get still waiting when its handler settles gives up: its reference is released at once".
func TestCtxAGetStillWaitingWhenItsHandlerSettlesGivesUpItsReferenceIsReleasedAtOnce(t *testing.T) {
	far := mustContract(declareRemote("app.far", contracts.RemoteDeclaration{
		Version:     1,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants:    []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})},
	}))
	starter := mustContract(declareLocal("app.starter", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1,
		Members: []contracts.LocalEntry{contracts.Mem("start", contracts.NewMethod())},
	}))
	pending := make(chan error, 1)
	var parent *RawPeer
	holds := make(chan *link.HoldMessage, 1)
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(starter, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"start": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					c := ctx.(Ctx)
					go func() { _, err := c.Get(far, jsonObject(), GetOptions{}); pending <- err }()
					// The get's hold is on the parent link before the handler settles.
					holds <- parent.Next("hold").(*link.HoldMessage)
					return nil, nil
				},
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	defer closeHosts(t, host)
	parent = rawParent(t, host, link.PartLimits{})
	ref, err := host.Get(starter, jsonObject(), background, GetOptions{})
	must(t, err)
	if _, err := ref.Call("start", nil, background); err != nil {
		t.Fatal(err)
	}
	hold := <-holds
	if release := parent.Next("release").(*link.ReleaseMessage); release.Hold != hold.Hold {
		t.Fatalf("released hold = %d, want %d", release.Hold, hold.Hold)
	}
	wantScopeError(t, <-pending, data.CodeReleased, data.ReasonSettled, "app.far: ctx.get after its call settled")
}

// Mirrors "ctx › a local handler's ctx.principal is the host's, its incarnation this host's".
func TestCtxALocalHandlersPrincipalIsTheHostsItsIncarnationThisHosts(t *testing.T) {
	host, _, ref := desk(t)
	defer closeHosts(t, host)
	got, err := ref.Call("who", nil, background)
	must(t, err)
	if want := "none " + incarnationOf(host); got != want {
		t.Fatalf("who = %v, want %q", got, want)
	}
}

// Mirrors "local handlers act as the host (§4, audit) › a local object called from a remote handler's ctx.get runs with this host's principal and incarnation".
func TestLocalHandlersActAsTheHostALocalObjectCalledFromARemoteHandlersCtxGetRunsAsTheHost(t *testing.T) {
	store := mustContract(declareLocal("app.local-store", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1,
		Members: []contracts.LocalEntry{contracts.Mem("who", contracts.NewMethod(contracts.TSchema{}, contracts.Str()))},
	}))
	gate := mustContract(declareRemote("app.gate", contracts.RemoteDeclaration{
		Version:     1,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("who", contracts.NewMethod(contracts.TSchema{}, contracts.Str())),
			contracts.Meth("through", contracts.NewMethod(contracts.TSchema{}, contracts.Str())),
		}})},
	}))
	l := linked(t, func(h FacetHost) error {
		if err := h.Serve(store, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"who": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return whoIs(ctx), nil },
			}}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(gate, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"who": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return whoIs(ctx), nil },
				"through": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					item, err := ctx.(Ctx).Get(store, jsonObject(), GetOptions{})
					if err != nil {
						return nil, err
					}
					return item.Call("who", nil, ctx)
				},
			}}}, nil
		}, ServeOptions{})
	}, linkedOptions{})
	defer closeHosts(t, l.Client, l.Server)
	ref, err := l.Client.Get(gate, jsonObject(), background, GetOptions{})
	must(t, err)
	// The remote handler acts as its caller; the local object it reaches acts as this host, both halves.
	who, err := ref.Call("who", nil, background)
	must(t, err)
	if want := "owner " + incarnationOf(l.Client); who != want {
		t.Fatalf("who = %v, want %q", who, want)
	}
	through, err := ref.Call("through", nil, background)
	must(t, err)
	if want := "server " + incarnationOf(l.Server); through != want {
		t.Fatalf("through = %v, want %q", through, want)
	}
}

func TestLinkedAbortSignalsAbortWithTheCallersSignalOrTheCallsTheCallersReasonFirst(t *testing.T) {
	for _, first := range []string{"caller", "call"} {
		callerCtx, abortCaller := chordctx.WithCancel(chordctx.Background())
		callCtx, abortCall := chordctx.WithCancel(chordctx.Background())
		linked := withLinkedAbortSignal(callCtx, callerCtx)
		if first == "caller" {
			abortCaller(scopeReason(first))
		} else {
			abortCall(scopeReason(first))
		}
		<-linked.context.Done()
		if got := context.Cause(linked.context); got == nil || got.Error() != first {
			t.Fatalf("reason after %s aborted = %v, want %q", first, got, first)
		}
	}
	callerCtx, abortCaller := chordctx.WithCancel(chordctx.Background())
	callCtx, abortCall := chordctx.WithCancel(chordctx.Background())
	abortCall(scopeReason("call"))
	abortCaller(scopeReason("caller"))
	linked := withLinkedAbortSignal(callCtx, callerCtx)
	if got := context.Cause(linked.context); got == nil || got.Error() != "caller" {
		t.Fatalf("reason with both aborted before = %v, want the caller's", got)
	}
}

func TestLinkedAbortSignalsDetachFromBothSourcesOnReleaseALaterAbortReachesNothing(t *testing.T) {
	callerCtx, abortCaller := newScopeCounting()
	callCtx, abortCall := newScopeCounting()
	linked := withLinkedAbortSignal(callCtx, callerCtx)
	if callerCtx.count() != 1 || callCtx.count() != 1 {
		t.Fatalf("listeners before release = %d and %d, want one on each source", callerCtx.count(), callCtx.count())
	}
	linked.release()
	if callerCtx.count() != 0 || callCtx.count() != 0 {
		t.Fatalf("listeners after release = %d and %d, want none", callerCtx.count(), callCtx.count())
	}
	abortCaller(scopeReason("late"))
	abortCall(scopeReason("late"))
	if linked.context.Err() != nil {
		t.Fatalf("the linked context aborted after release: %v", linked.context.Err())
	}
}

func TestLinkedAbortSignalsIsTheCallsOwnSignalWithoutACallers(t *testing.T) {
	callCtx, abortCall := chordctx.WithCancel(chordctx.Background())
	linked := withLinkedAbortSignal(callCtx, chordctx.Background())
	if linked.context.Err() != nil {
		t.Fatalf("aborted before the call's signal: %v", linked.context.Err())
	}
	abortCall(scopeReason("call"))
	<-linked.context.Done()
	if got := context.Cause(linked.context); got == nil || got.Error() != "call" {
		t.Fatalf("reason = %v, want the call's", got)
	}
	linked.release()
}
