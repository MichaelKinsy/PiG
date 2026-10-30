package host

import (
	"context"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/test/next/host/ctx.test.ts. Upstream's cases build a host with `loaded` (createHost) and reach
// `ctx` through a handler; a case whose assertions are all about the call context (`ctx.get`'s order of checks, its
// `released` refusals, its cancellation) runs here on a fake callHost whose Acquire is the test's: the fake takes the
// place of the host's `acquire` and the handler's goroutine. The cases whose assertions are the host's (a reference's own
// `released`, a local object's principal over a link) are listed `missing` in the lane's rows.

// scopeItem is `local("app.item", { version: 1, params: { id: int() }, members: { id: int() } })`.
func scopeItem(t testing.TB, name string) *contracts.Contract {
	t.Helper()
	contract, err := contracts.Local(name, contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("id", contracts.Int())},
	})
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

// newScopeCtx is a call context over ctx on a fake host.
func newScopeCtx(t testing.TB, ctx context.Context) (*callContext, *scopeHost, *scopeLoop) {
	t.Helper()
	loop := newScopeLoop()
	host := newScopeHost(loop)
	return newCallContext(ctx, caller{incarnation: host.Incarnation()}, host), host, loop
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

func TestCtxGetAfterTheHandlerSettledThrowsReleased(t *testing.T) {
	Item := scopeItem(t, "app.item")
	cc, host, loop := newScopeCtx(t, chordctx.Background())
	loop.Run(cc.settle)
	_, err := cc.Get(Item, delta.JsonObjectOf("id", 1.0), GetOptions{})
	wantScopeError(t, err, data.CodeReleased, data.ReasonSettled, "app.item: ctx.get after its call settled")
	if host.acquireCount() != 0 {
		t.Fatalf("acquires = %d, want none", host.acquireCount())
	}
}

func TestCtxGetOfAHandlerWhoseCallWasAbortedChecksTheSignalFirstNothingIsCreated(t *testing.T) {
	Item := scopeItem(t, "app.item")
	callerCtx, abortCaller := chordctx.WithCancel(chordctx.Background())
	cc, host, _ := newScopeCtx(t, callerCtx)
	abortCaller(scopeReason("caller gave up"))
	_, err := cc.Get(Item, delta.JsonObjectOf("id", 1.0), GetOptions{})
	if err == nil || err.Error() != "caller gave up" {
		t.Fatalf("err = %v, want the caller's reason", err)
	}
	if host.acquireCount() != 0 {
		t.Fatalf("factories = %d, want 0", host.acquireCount())
	}
}

func TestCtxAGetFinishingAfterItsHandlerSettledIsReleasedAtOnceRace11(t *testing.T) {
	Item := scopeItem(t, "app.item")
	cc, host, loop := newScopeCtx(t, chordctx.Background())
	late := &scopeBinding{reference: "late"}
	entered, gate := make(chan struct{}), make(chan struct{})
	host.acquire = func(scopeAcquire) (acquired, error) {
		close(entered)
		<-gate
		return late, nil
	}
	type got struct {
		reference any
		err       error
	}
	result := make(chan got, 1)
	go func() {
		reference, err := cc.Get(Item, delta.JsonObjectOf("id", 3.0), GetOptions{})
		result <- got{reference, err}
	}()
	<-entered
	// The handler settles while its `get` waits; the get finishes in the next turn.
	loop.Run(cc.settle)
	close(gate)
	outcome := <-result
	if outcome.reference != nil {
		t.Fatalf("reference = %v, want none", outcome.reference)
	}
	wantScopeError(t, outcome.err, data.CodeReleased, data.ReasonSettled, "app.item: ctx.get after its call settled")
	// The late reference's object ended with it.
	if late.released() != 1 {
		t.Fatalf("late reference released %d times, want once", late.released())
	}
}

func TestCtxAGetStillWaitingWhenItsHandlerSettlesGivesUpItsReferenceIsReleasedAtOnce(t *testing.T) {
	Far := scopeItem(t, "app.far")
	cc, host, loop := newScopeCtx(t, chordctx.Background())
	entered := make(chan context.Context, 1)
	host.acquire = func(call scopeAcquire) (acquired, error) {
		entered <- call.ctx
		// A hold waiting for its link: it gives up, releasing its hold, when its context is aborted.
		<-call.ctx.Done()
		return nil, context.Cause(call.ctx)
	}
	result := make(chan error, 1)
	go func() {
		_, err := cc.Get(Far, delta.JsonObjectOf("id", 1.0), GetOptions{})
		result <- err
	}()
	acquireCtx := <-entered
	loop.Run(cc.settle)
	wantScopeError(t, <-result, data.CodeReleased, data.ReasonSettled, "app.far: ctx.get after its call settled")
	if acquireCtx.Err() == nil {
		t.Fatal("the context the acquire waited on was not aborted when the handler settled")
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
	if callerCtx.listeners() != 1 || callCtx.listeners() != 1 {
		t.Fatalf("listeners before release = %d and %d, want one on each source", callerCtx.listeners(), callCtx.listeners())
	}
	linked.release()
	if callerCtx.listeners() != 0 || callCtx.listeners() != 0 {
		t.Fatalf("listeners after release = %d and %d, want none", callerCtx.listeners(), callCtx.listeners())
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

// Own tests: the parts of ctx.test.ts's host cases that are the call context's. Each names the Pi case it mirrors.

// Mirrors "ctx › ctx.get references are released when the handler settles": settle releases every reference taken, once.
func TestCallContextSettleReleasesEveryReferenceTakenOnce(t *testing.T) {
	Item := scopeItem(t, "app.item")
	cc, host, loop := newScopeCtx(t, chordctx.Background())
	var bindings []*scopeBinding
	host.acquire = func(call scopeAcquire) (acquired, error) {
		binding := &scopeBinding{reference: call.params}
		bindings = append(bindings, binding)
		return binding, nil
	}
	for _, id := range []float64{1, 2} {
		reference, err := cc.Get(Item, delta.JsonObjectOf("id", id), GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if reference.(*delta.JsonObject).Value("id") != id {
			t.Fatalf("reference = %v, want the binding's reference", reference)
		}
	}
	if bindings[0].released() != 0 || bindings[1].released() != 0 {
		t.Fatal("a reference was released before its handler settled")
	}
	loop.Run(cc.settle)
	loop.Run(cc.settle)
	for i, binding := range bindings {
		if binding.released() != 1 {
			t.Fatalf("binding %d released %d times, want once", i, binding.released())
		}
	}
}

// Mirrors "ctx › ctx.get references are released when the handler settles": `get` is made for the caller (its principal
// and its origin's incarnation travel with the reference's holds) and returns the binding's reference.
func TestCallContextGetPassesTheCallerAndOptionsAndReturnsTheReference(t *testing.T) {
	Item := scopeItem(t, "app.item")
	owner := delta.JsonObjectOf("kind", "owner")
	loop := newScopeLoop()
	host := newScopeHost(loop)
	host.acquire = func(call scopeAcquire) (acquired, error) {
		return &scopeBinding{reference: "the reference"}, nil
	}
	cc := newCallContext(chordctx.Background(), caller{principal: owner, incarnation: "peer-incarnation"}, host)
	timeout := 250.0
	reference, err := cc.Get(Item, delta.JsonObjectOf("id", 1.0), GetOptions{ReadyTimeout: &timeout})
	if err != nil || reference != "the reference" {
		t.Fatalf("Get = %v, %v", reference, err)
	}
	call := host.acquires[0]
	if call.caller.principal != owner || call.caller.incarnation != "peer-incarnation" || call.Type != Item {
		t.Fatalf("acquire got %+v", call)
	}
	if call.options.ReadyTimeout == nil || *call.options.ReadyTimeout != 250 {
		t.Fatalf("options = %+v, want ReadyTimeout 250", call.options)
	}
	if cc.Principal() != owner || cc.Incarnation() != "peer-incarnation" {
		t.Fatalf("Principal, Incarnation = %v, %q", cc.Principal(), cc.Incarnation())
	}
}

// Mirrors "ctx › a local handler's ctx.principal is the host's, its incarnation this host's": the ctx of a scope made for
// the host's own caller (no principal on a client) acts as it.
func TestCallContextActsAsTheCallerItWasMadeFor(t *testing.T) {
	cc, host, _ := newScopeCtx(t, chordctx.Background())
	if cc.Principal() != nil || cc.Incarnation() != host.Incarnation() {
		t.Fatalf("Principal, Incarnation = %v, %q, want none and the host's", cc.Principal(), cc.Incarnation())
	}
}

// Mirrors the order of `get`'s checks: an acquire that fails before the handler settled is its own error, one that fails
// after it is `released`; the scope's context text is Pi's toString.
func TestCallContextGetErrorsAndString(t *testing.T) {
	Item := scopeItem(t, "app.item")
	cc, host, loop := newScopeCtx(t, chordctx.Background())
	boom := scopeReason("acquire failed")
	host.acquire = func(scopeAcquire) (acquired, error) { return nil, boom }
	if _, err := cc.Get(Item, delta.JsonObjectOf("id", 1.0), GetOptions{}); err != boom {
		t.Fatalf("err = %v, want the acquire's own", err)
	}
	loop.Run(cc.settle)
	if _, err := cc.Get(Item, delta.JsonObjectOf("id", 1.0), GetOptions{}); err == nil || err.Error() != "app.item: ctx.get after its call settled" {
		t.Fatalf("err after settle = %v", err)
	}
	if got, want := cc.String(), "[Context BACKGROUND_CONTEXT].Ctx"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
