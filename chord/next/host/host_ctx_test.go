package host

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/host/ctx.test.ts. The cases that build a host (`loaded`, `linked`) run on a real host: a
// handler gets its `ctx` as the *callContext the host made for the call (every method's context.Context is a Ctx). The
// `linked abort signals` cases need no host. The ctx.trace cases are in host_spans_test.go (TestCtxTrace...), its code in
// host_ctx_trace.go.

var (
	ctxItem = mustContract(declareLocal("app.item", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("id", contracts.Int())},
	}))
	ctxDesk = mustContract(declareLocal("app.desk", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1,
		Members: []contracts.LocalEntry{
			contracts.Mem("use", contracts.NewMethod(contracts.Schema{}, contracts.Int())),
			contracts.Mem("leak", contracts.NewMethod()),
			contracts.Mem("late", contracts.NewMethod()),
			contracts.Mem("who", contracts.NewMethod(contracts.Schema{}, contracts.Str())),
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
					seen.mu.Lock()
					seen.ctx = c
					seen.mu.Unlock()
					go func() { _, err := c.Get(ctxItem, jsonObject("id", float64(3)), GetOptions{}); seen.late <- err }()
					// The get's synchronous part ran (its object is created) before the handler settles, as in Pi. In Go the
					// get runs on a goroutine of its own, and the handler suspends (Pi's `await`) while it waits for it.
					chordctx.Suspend(ctx)
					scopeEventually(t, "the late get created its object", func() bool { return seen.factoryCount() >= 1 })
					return nil, nil
				},
				"who": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return whoIs(ctx), nil },
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	ref, err := host.Get(background, ctxDesk, jsonObject(), GetOptions{})
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
	started := make(chan bool, 1)
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
					// Pi: `await gate.promise`, then `await untilAborted(ctx)`.
					chordctx.Suspend(ctx)
					<-gate
					// The caller's abort reaches the call's context through a listener of its own.
					UntilAborted(ctx)
					_, err := ctx.(Ctx).Get(ctxItem, jsonObject("id", float64(1)), GetOptions{})
					// An aborted call's get is refused before it makes anything: no settling signal, no link to it.
					started <- ctx.(*callContext).settling != nil
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
	ref, err := host.Get(background, ctxDesk, jsonObject(), GetOptions{})
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
	if <-started {
		t.Fatal("the refused get made its settling signal")
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
	// Released at once, so not kept for a release that will not come: the settled ctx lists no reference of it.
	var kept int
	h.enter(func() {
		seen.mu.Lock()
		defer seen.mu.Unlock()
		kept = len(seen.ctx.(*callContext).references)
	})
	if kept != 0 {
		t.Fatalf("the settled ctx kept %d references, want 0", kept)
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
					// The get's hold is on the parent link before the handler settles: `await parent.next("hold")`.
					chordctx.Suspend(ctx)
					holds <- parent.Next("hold").(*link.HoldMessage)
					return nil, nil
				},
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	defer closeHosts(t, host)
	parent = rawParent(t, host, link.PartLimits{})
	ref, err := host.Get(background, starter, jsonObject(), GetOptions{})
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
		Members: []contracts.LocalEntry{contracts.Mem("who", contracts.NewMethod(contracts.Schema{}, contracts.Str()))},
	}))
	gate := mustContract(declareRemote("app.gate", contracts.RemoteDeclaration{
		Version:     1,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("who", contracts.NewMethod(contracts.Schema{}, contracts.Str())),
			contracts.Meth("through", contracts.NewMethod(contracts.Schema{}, contracts.Str())),
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
	ref, err := l.Client.Get(background, gate, jsonObject(), GetOptions{})
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

// A call that reads the abort right after it ran (an in-flight call whose attempt was sealed meanwhile) sees it at once: Pi's
// `AbortSignal.any` aborts its result in the step that aborts the input, where the child's Done follows on another goroutine.
func TestLinkedAbortSignalsReportAnAbortOfASourceAtOnce(t *testing.T) {
	for _, first := range []string{"caller", "call"} {
		callerCtx, abortCaller := chordctx.WithCancel(chordctx.Background())
		callCtx, abortCall := chordctx.WithCancel(chordctx.Background())
		linked := withLinkedAbortSignal(callCtx, callerCtx)
		if got := linked.aborted(); got != nil {
			t.Fatalf("aborted before any abort = %v", got)
		}
		if first == "caller" {
			abortCaller(scopeReason(first))
		} else {
			abortCall(scopeReason(first))
		}
		if got := linked.aborted(); got == nil || got.Error() != first {
			t.Fatalf("aborted right after %s aborted = %v, want %q", first, got, first)
		}
		linked.release()
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

// Mirrors ctx.test.ts's linked abort signals: without a caller's signal the call's context is the call's signal itself (Pi's
// `withAbortSignal(signal, context)`, whose signal is `signal` and which has no listener), and its `release` does nothing.
// The call's signal is the attempt's for every call it runs, so a listener per call, released or not, would accumulate until
// the attempt ends.
func TestLinkedAbortSignalsWithoutACallersListenToNothingOnTheCallsSignal(t *testing.T) {
	callCtx, abortCall := newScopeCounting()
	const calls = 1000
	for range calls {
		linked := withLinkedAbortSignal(callCtx, chordctx.Background())
		if got := callCtx.count(); got != 0 {
			t.Fatalf("listeners on the call's signal while a call runs = %d, want none", got)
		}
		linked.release()
	}
	running := withLinkedAbortSignal(callCtx, chordctx.Background())
	abortCall(scopeReason("late"))
	<-running.context.Done()
	if got := context.Cause(running.context); got == nil || got.Error() != "late" {
		t.Fatalf("reason = %v, want the call's", got)
	}
}

// noSignalContext is a caller's context without a Done channel (as context.Background()), a distinct object per call so
// that its collection tells whether anything of the call still holds it.
type noSignalContext struct{ context.Context }

func TestMethodCallsOfACallerWithoutASignalLeaveNothingOnTheAttemptWhenTheyEnd(t *testing.T) {
	host, _, ref := desk(t)
	const calls = 200
	probes := make([]weak.Pointer[noSignalContext], 0, calls)
	for range calls {
		probe := &noSignalContext{Context: background}
		probes = append(probes, weak.Make(probe))
		if _, err := ref.Call("who", nil, probe); err != nil {
			t.Fatal(err)
		}
	}
	// The attempt's opening context and the reference's calls context are the signals of each call: a listener a call left
	// on one keeps its caller's context, for as long as the host lives.
	expectCollected(t, probes...)
	runtime.KeepAlive(host)
	runtime.KeepAlive(ref)
}

// Mirrors "the call chain (decision \"Reload restarts every affected object\": old work) › a method's ctx is its own chain node, above it the chain of the call that made it; a root's has none".
func TestCallChainAMethodsCtxIsItsOwnChainNodeAboveItTheChainOfTheCallThatMadeItARootsHasNone(t *testing.T) {
	run := func() contracts.LocalEntry {
		return contracts.Mem("run", contracts.NewMethod())
	}
	inner := mustContract(declareLocal("app.inner", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{run()}}))
	outer := mustContract(declareLocal("app.outer", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{run()}}))
	var mu sync.Mutex
	var seenInner, seenOuter context.Context
	impl := func(record func(context.Context), next func(Ctx) error) func(*delta.JsonObject, Self) (contracts.Implementation, error) {
		return func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"run": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					mu.Lock()
					record(ctx)
					mu.Unlock()
					if next != nil {
						return nil, next(ctx.(Ctx))
					}
					return nil, nil
				},
			}}}, nil
		}
	}
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(inner, impl(func(c context.Context) { seenInner = c }, nil), ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(outer, impl(func(c context.Context) { seenOuter = c }, func(ctx Ctx) error {
			ref, err := ctx.Get(inner, jsonObject(), GetOptions{})
			if err != nil {
				return err
			}
			_, err = ref.Call("run", nil, ctx)
			return err
		}), ServeOptions{})
	}, loadedOptions{})
	defer closeHosts(t, host)
	var seenRoot Ctx
	_, err := host.Root(func(root Ctx) (any, error) {
		seenRoot = root
		ref, err := root.Get(outer, jsonObject(), GetOptions{})
		if err != nil {
			return nil, err
		}
		return ref.Call("run", nil, root)
	}, background, RootOptions{})
	must(t, err)
	node := chainOf(seenInner)
	if node == nil || node != callNode(seenInner.(*callContext)) {
		t.Fatalf("the inner ctx's chain node = %v, want the inner ctx itself", node)
	}
	up := node.up()
	if up == nil || up != callNode(seenOuter.(*callContext)) {
		t.Fatalf("the node above the inner's = %v, want the outer ctx", up)
	}
	if above := up.up(); above != nil {
		t.Fatalf("the node above the outer's = %v, want none", above)
	}
	if got := chainOf(seenRoot); got != nil {
		t.Fatalf("a root's chain = %v, want none", got)
	}
}

// ctxProbe is a local type whose handlers use their ctx by the test's own steps: `idle` only keeps it, `counted` takes
// `ctx.get` while counting the listeners on its settling signal, `far` runs the test's own function on it.
var ctxProbe = mustContract(declareLocal("app.ctx-probe", contracts.LocalDeclaration{Params: contracts.Params{},
	Version: 1,
	Members: []contracts.LocalEntry{
		contracts.Mem("idle", contracts.NewMethod()),
		contracts.Mem("counted", contracts.NewMethod()),
		contracts.Mem("far", contracts.NewMethod()),
	},
}))

// ctxProbeHost serves ctxItem (counting its factories into seen) and ctxProbe, whose `idle` records its ctx into seen and
// whose `far` runs far.
func ctxProbeHost(t *testing.T, seen *deskSeen, far func(ctx Ctx) error) Host {
	t.Helper()
	return ctxProbeHostOn(t, seen, far, nil)
}

// ctxProbeHostOn is ctxProbeHost on the loop (nil is the real one).
func ctxProbeHostOn(t *testing.T, seen *deskSeen, far func(ctx Ctx) error, loop sectionLoop) Host {
	t.Helper()
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(ctxItem, func(params *delta.JsonObject, _ Self) (contracts.Implementation, error) {
			seen.mu.Lock()
			seen.factories++
			seen.mu.Unlock()
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"id": constant(params.Value("id"))},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(ctxProbe, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"idle": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					seen.mu.Lock()
					seen.ctx = ctx.(Ctx)
					seen.mu.Unlock()
					return nil, nil
				},
				// counted takes a reference while a counting signal stands for the ctx's settling one.
				"counted": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					call := ctx.(*callContext)
					counting, abort := newScopeCounting()
					call.settling, call.settlingCancel = counting, abort
					if _, err := call.Get(ctxItem, jsonObject("id", float64(1)), GetOptions{}); err != nil {
						return nil, err
					}
					if got := counting.count(); got != 0 {
						return nil, fmt.Errorf("listeners left on the settling signal after the get = %d, want none", got)
					}
					return nil, nil
				},
				"far": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return nil, far(ctx.(Ctx)) },
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop})
	return host
}

// ctx.ts `get`: a settled scope refuses `released` before it makes anything: no object is created for it, no settling
// signal is made, and its references are gone with the settle.
func TestCtxGetOnASettledCtxMakesNothing(t *testing.T) {
	seen := &deskSeen{}
	host := ctxProbeHost(t, seen, nil)
	defer closeHosts(t, host)
	ref, err := host.Get(background, ctxProbe, jsonObject(), GetOptions{})
	must(t, err)
	if _, err := ref.Call("idle", nil, background); err != nil {
		t.Fatal(err)
	}
	seen.mu.Lock()
	settled := seen.ctx.(*callContext)
	seen.mu.Unlock()
	if settled.references != nil {
		t.Fatalf("a settled ctx kept %d references", len(settled.references))
	}
	reference, err := settled.Get(ctxItem, jsonObject("id", float64(1)), GetOptions{})
	wantScopeError(t, err, data.CodeReleased, data.ReasonSettled, "app.item: ctx.get after its call settled")
	if reference != nil || seen.factoryCount() != 0 || settled.settling != nil {
		t.Fatalf("reference %v, factories %d, settling signal made %v; want nothing made", reference, seen.factoryCount(), settled.settling != nil)
	}
}

// ctx.ts `get`: `finally { scoped.release() }`: once the reference is acquired nothing of the get listens to the ctx's
// settling signal or the caller's any more.
func TestCtxGetLeavesNoListenerOnTheSettlingSignalOnceItAcquired(t *testing.T) {
	seen := &deskSeen{}
	host := ctxProbeHost(t, seen, nil)
	defer closeHosts(t, host)
	ref, err := host.Get(background, ctxProbe, jsonObject(), GetOptions{})
	must(t, err)
	// A caller with a signal: the get links to it and to the settling one.
	caller, cancel := chordctx.WithCancel(background)
	defer cancel(nil)
	if _, err := ref.Call("counted", nil, caller); err != nil {
		t.Fatal(err)
	}
}

// ctx.ts `get`: what the acquire rejects with is what `get` rejects with while the handler is still running (a reference
// that never goes live in its time, here).
func TestCtxGetRejectsWithTheAcquireErrorWhileItsHandlerRuns(t *testing.T) {
	far := mustContract(declareRemote("app.far-away", contracts.RemoteDeclaration{
		Version:     1,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants:    []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})},
	}))
	var got error
	var reference *Reference
	host := ctxProbeHost(t, &deskSeen{}, func(ctx Ctx) error {
		var err error
		reference, err = ctx.Get(far, jsonObject(), GetOptions{ReadyTimeout: new(20 * time.Millisecond)})
		got = err
		return nil
	})
	defer closeHosts(t, host)
	ref, err := host.Get(background, ctxProbe, jsonObject(), GetOptions{})
	must(t, err)
	if _, err := ref.Call("far", nil, background); err != nil {
		t.Fatal(err)
	}
	if reference != nil {
		t.Fatalf("get returned a reference %v with its error %v", reference, got)
	}
	if _, ok := data.IsChordErrorCode(got, data.CodeUnavailable); !ok {
		t.Fatalf("get error = %v, want unavailable", got)
	}
}

// ctx.ts `withLinkedAbortSignal` without a caller's signal: the child is the call's signal; an abort of it is reported by
// `aborted` at once, from the child itself.
func TestLinkedAbortSignalsReportTheCallsAbortAtOnceWithoutACallersSignal(t *testing.T) {
	callCtx, abortCall := chordctx.WithCancel(chordctx.Background())
	linked := withLinkedAbortSignal(callCtx, chordctx.Background())
	if got := linked.aborted(); got != nil {
		t.Fatalf("aborted before any abort = %v", got)
	}
	abortCall(scopeReason("call"))
	if got := linked.aborted(); got == nil || got.Error() != "call" {
		t.Fatalf("aborted right after the call's abort = %v, want %q", got, "call")
	}
}

// ctx.ts `get`: "Made for the caller: its principal and its origin's incarnation travel with the reference's holds". A
// reference a remote handler takes with ctx.get is listed as the handler's caller's: its principal and its host's
// incarnation (`host.references`), not the server's.
func TestCtxGetMakesTheReferenceForTheCallerPrincipalAndIncarnation(t *testing.T) {
	peer := mustContract(declareRemote("app.ctx-peer", contracts.RemoteDeclaration{
		Version:     1,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants:    []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})},
	}))
	door := mustContract(declareRemote("app.ctx-door", contracts.RemoteDeclaration{
		Version:     1,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("enter", contracts.NewMethod()),
		}})},
	}))
	var server Host
	var listed []any
	l := linked(t, func(h FacetHost) error {
		if err := h.Serve(peer, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(1.0)},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(door, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"enter": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					if _, err := ctx.(Ctx).Get(peer, jsonObject(), GetOptions{}); err != nil {
						return nil, err
					}
					listed = presenceList(t, server, peer)
					return nil, nil
				},
			}}}, nil
		}, ServeOptions{})
	}, linkedOptions{})
	defer closeHosts(t, l.Client, l.Server)
	server = l.Server
	server.References(peer)
	ref, err := l.Client.Get(background, door, jsonObject(), GetOptions{})
	must(t, err)
	if _, err := ref.Call("enter", nil, background); err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, listed, []any{
		jsonObject("params", jsonObject(), "principal", ownerPrincipal(), "incarnation", incarnationOf(l.Client)),
	})
}

// ctx.ts `withLinkedAbortSignal`: `AbortSignal.any` takes the reason of the source that aborted first, which listeners on
// goroutines of their own cannot tell by when they run: linkedCause ranks the sources by when they aborted, a source it
// cannot rank (a context of another package) after the ones it can, in the order given (the caller's first), and a source
// that did not abort never.
func TestLinkedAbortSignalsTheReasonIsThatOfTheSourceThatAbortedFirst(t *testing.T) {
	stdlib := func(reason string) context.Context {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(scopeReason(reason))
		return ctx
	}
	chordAborted := func(reason string) context.Context {
		ctx, abort := chordctx.WithCancel(chordctx.Background())
		abort(scopeReason(reason))
		return ctx
	}
	notAborted, _ := chordctx.WithCancel(chordctx.Background())
	cause := func(sources [2]context.Context) string { return linkedCause(sources).Error() }
	caller, call := chordAborted("caller"), chordAborted("call")
	if got := cause([2]context.Context{caller, call}); got != "caller" {
		t.Errorf("caller aborted first: %q, want caller", got)
	}
	call, caller = chordAborted("call"), chordAborted("caller")
	if got := cause([2]context.Context{caller, call}); got != "call" {
		t.Errorf("call aborted first: %q, want call", got)
	}
	if got := cause([2]context.Context{notAborted, chordAborted("call")}); got != "call" {
		t.Errorf("only the call aborted: %q, want call", got)
	}
	if got := cause([2]context.Context{chordAborted("caller"), notAborted}); got != "caller" {
		t.Errorf("only the caller aborted: %q, want caller", got)
	}
	if got := cause([2]context.Context{stdlib("caller"), stdlib("call")}); got != "caller" {
		t.Errorf("two sources it cannot rank: %q, want the caller's (the first given)", got)
	}
	if got := cause([2]context.Context{stdlib("caller"), chordAborted("call")}); got != "call" {
		t.Errorf("an unranked caller and a ranked call: %q, want the ranked one", got)
	}
	if got := cause([2]context.Context{chordAborted("caller"), stdlib("call")}); got != "caller" {
		t.Errorf("a ranked caller and an unranked call: %q, want the ranked one", got)
	}
	if got := cause([2]context.Context{notAborted, stdlib("call")}); got != "call" {
		t.Errorf("an unaborted caller and an unranked call: %q, want call", got)
	}
}

// ctx.ts `withLinkedAbortSignal`: a source that aborts removes the listener on the other one (`forward` runs `release()`).
func TestLinkedAbortSignalsAnAbortOfOneSourceRemovesTheListenerOnTheOther(t *testing.T) {
	for _, first := range []string{"caller", "call"} {
		callerCtx, abortCaller := newScopeCounting()
		callCtx, abortCall := newScopeCounting()
		linked := withLinkedAbortSignal(callCtx, callerCtx)
		if first == "caller" {
			abortCaller(scopeReason(first))
		} else {
			abortCall(scopeReason(first))
		}
		<-linked.context.Done()
		scopeEventually(t, "the listeners to go", func() bool { return callerCtx.count() == 0 && callCtx.count() == 0 })
	}
}

// Deterministic pins of ctx.ts `get`'s two settle-turn statements (Pi ctx.ts:112-115 and 131-136), which the timing-driven
// TestCtxAGetFinishingAfterItsHandlerSettledIsReleasedAtOnceRace11 reaches only through whichever of the settle's two effects
// wins: a get still waiting when its handler settles gives up (the settling signal), and only a get whose acquire already
// finished is released by `binding.release()`.

// getName is the function name of callContext.Get as runtime reports it, the frame of the sections `Get` runs.
var getName = runtime.FuncForPC(reflect.ValueOf((*callContext).Get).Pointer()).Name()

// settlingLoop is a scopeLoop that settles a ctx in the turn of a `Get`'s section after its acquire: the Go form of "the get
// finished in the turn its handler settled", at the exact point between `acquire` and `get`'s second step, without a timing.
// The first section Get runs is its first synchronous part, the second the one after the acquire.
type settlingLoop struct {
	*scopeLoop
	mu sync.Mutex
	// call is the ctx whose Get is watched; nil settles nothing.
	call *callContext
	// sections counts the sections `call.Get` ran, settled the settles done.
	sections, settled int
}

var _ sectionLoop = (*settlingLoop)(nil)

// Run runs work as a section; the one `Get` runs after its acquire settles the ctx first, in that same section.
func (l *settlingLoop) Run(work func()) {
	pc, _, _, ok := runtime.Caller(1)
	var frame string
	if fn := runtime.FuncForPC(pc); ok && fn != nil {
		frame = fn.Name()
	}
	l.mu.Lock()
	settling := false
	if l.call != nil && frame == getName {
		l.sections++
		settling = l.sections == 2
	}
	call := l.call
	l.mu.Unlock()
	if !settling {
		l.scopeLoop.Run(work)
		return
	}
	l.scopeLoop.Run(func() {
		call.settle()
		l.mu.Lock()
		l.settled++
		l.mu.Unlock()
		work()
	})
}

// Mirrors "ctx › a ctx.get finishing after its handler settled is released at once (race 11)", pinned deterministically: the
// handler settles in the turn the get finished its acquire (ctx.ts:112-115 `binding.release()`), no sleep, no gate.
func TestCtxAGetThatFinishedItsAcquireInTheTurnItsHandlerSettledIsReleasedAtOnceRace11Deterministic(t *testing.T) {
	loop := &settlingLoop{scopeLoop: newScopeLoop()}
	seen := &deskSeen{}
	host := ctxProbeHostOn(t, seen, nil, loop)
	defer closeHosts(t, host)
	_, err := host.Root(func(root Ctx) (any, error) {
		loop.mu.Lock()
		loop.call = root.(*callContext)
		loop.mu.Unlock()
		reference, err := root.Get(ctxItem, jsonObject("id", float64(1)), GetOptions{})
		if reference != nil {
			t.Errorf("reference of a get that finished after the settle = %v, want none", reference)
		}
		return nil, err
	}, background, RootOptions{})
	wantScopeError(t, err, data.CodeReleased, data.ReasonSettled, "app.item: ctx.get after its call settled")
	loop.mu.Lock()
	sections, settled := loop.sections, loop.settled
	loop.mu.Unlock()
	if sections != 2 || settled != 1 {
		t.Fatalf("Get ran %d sections and the ctx settled %d times, want 2 and 1", sections, settled)
	}
	turn(host)
	// The object was made for the get and ended with its reference: the release was at once, not left for a settle that will
	// not come.
	if got := seen.factoryCount(); got != 1 {
		t.Fatalf("factories = %d, want 1", got)
	}
	h := host.(*chordHost)
	var items, kept int
	h.enter(func() {
		for _, object := range h.objectList() {
			if object.served.contract.Name == "app.item" {
				items++
			}
		}
		kept = len(loop.call.references)
	})
	if items != 0 {
		t.Fatalf("app.item objects alive = %d, want 0", items)
	}
	if kept != 0 {
		t.Fatalf("the settled ctx kept %d references, want 0", kept)
	}
}

// ctx.ts:131-136 `settle`: `this.#references = undefined` after the references were taken: a settle releases each reference
// and leaves none behind, so a later settle (or a late get) finds nothing to release twice.
func TestCtxSettleReleasesTheReferencesAndForgetsThem(t *testing.T) {
	seen := &deskSeen{}
	host := ctxProbeHost(t, seen, nil)
	defer closeHosts(t, host)
	h := host.(*chordHost)
	var call *callContext
	_, err := host.Root(func(root Ctx) (any, error) {
		call = root.(*callContext)
		for id := 1; id <= 2; id++ {
			if _, err := call.Get(ctxItem, jsonObject("id", float64(id)), GetOptions{}); err != nil {
				return nil, err
			}
		}
		var held, items int
		h.enter(func() {
			held = len(call.references)
			for _, object := range h.objectList() {
				if object.served.contract.Name == "app.item" {
					items++
				}
			}
		})
		if held != 2 || items != 2 {
			t.Errorf("before the settle: %d references, %d items, want 2 and 2", held, items)
		}
		h.enter(call.settle)
		return nil, nil
	}, background, RootOptions{})
	must(t, err)
	turn(host)
	var kept, items int
	h.enter(func() {
		kept = len(call.references)
		for _, object := range h.objectList() {
			if object.served.contract.Name == "app.item" {
				items++
			}
		}
	})
	if kept != 0 || call.references != nil {
		t.Fatalf("the settled ctx kept %d references (nil: %v), want none", kept, call.references == nil)
	}
	if items != 0 {
		t.Fatalf("app.item objects alive after the settle = %d, want 0", items)
	}
}
