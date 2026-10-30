package host

import (
	"context"
	"errors"
	"testing"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/host/root.test.ts. Upstream's cases go through `host.root` of a createHost host; a case
// whose assertions are all the scope's (sealing, rejection, listeners, collection) runs here on rootScopes over a fake
// callHost, with `host.close(reason)` as rootScopes.close(closed): the host's own `released/closed` error is built here as
// the host builds it. The cases that assert the host's (its default principal, `load`, a reference's own `released`, root
// after close) are listed `missing` in the lane's rows, with own tests for the scope's part of them below.

// scopeClosed is the abort reason of a closed host: `released/closed`.
func scopeClosed() error {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonClosed, Message: "host closed"}, nil)
}

func newScopeRoots(t testing.TB) (*rootScopes, *scopeHost, *scopeLoop) {
	t.Helper()
	loop := newScopeLoop()
	host := newScopeHost(loop)
	return newRootScopes(host), host, loop
}

func TestHostRootAnAbortedContextRejectsBeforeFnRunsASynchronousThrowIsARejection(t *testing.T) {
	scopes, _, _ := newScopeRoots(t)
	aborted, cancel := chordctx.WithCancel(chordctx.Background())
	cancel(scopeReason("gone"))
	ran := false
	_, err := scopes.run(func(Ctx) (any, error) {
		ran = true
		return nil, nil
	}, nil, aborted)
	if err == nil || err.Error() != "gone" {
		t.Fatalf("err = %v, want gone", err)
	}
	if ran {
		t.Fatal("fn ran for an aborted context")
	}
	// A throw before fn's first await is a rejection, as a panic is its Go form.
	_, err = scopes.run(func(Ctx) (any, error) { return nil, scopeReason("sync") }, nil, chordctx.Background())
	if err == nil || err.Error() != "sync" {
		t.Fatalf("err = %v, want sync", err)
	}
	_, err = scopes.run(func(Ctx) (any, error) { panic(scopeReason("panicked")) }, nil, chordctx.Background())
	if err == nil || err.Error() != "panicked" {
		t.Fatalf("err = %v, want panicked", err)
	}
}

func TestHostRootAbortsTheScopesOwnSignalWhenFnSettlesAndAtHostCloseTheCallersContextHavingOne(t *testing.T) {
	scopes, _, loop := newScopeRoots(t)
	settled, _ := chordctx.WithCancel(chordctx.Background())
	var kept Ctx
	if _, err := scopes.run(func(ctx Ctx) (any, error) {
		kept = ctx
		return nil, nil
	}, nil, settled); err != nil {
		t.Fatal(err)
	}
	if kept.Err() == nil {
		t.Fatal("the scope's signal is not aborted once fn settled")
	}
	if settled.Err() != nil {
		t.Fatal("the caller's own signal was aborted")
	}
	closing, _ := chordctx.WithCancel(chordctx.Background())
	entered, ended := make(chan struct{}), make(chan struct{})
	outcome := make(chan error, 1)
	go func() {
		_, err := scopes.run(func(ctx Ctx) (any, error) {
			close(entered)
			// Sleeps on its ctx: ends with it.
			<-ctx.Done()
			close(ended)
			return nil, context.Cause(ctx)
		}, nil, closing)
		outcome <- err
	}()
	<-entered
	loop.Run(func() { scopes.close(scopeClosed()) })
	<-ended
	if closing.Err() != nil {
		t.Fatal("the caller's own signal was aborted by the close")
	}
	wantScopeError(t, <-outcome, data.CodeReleased, data.ReasonClosed, "host closed")
}

func TestHostRootHostCloseRejectsTheCallerAtOnceWhateverAnUncooperativeFnStillDoes(t *testing.T) {
	scopes, _, loop := newScopeRoots(t)
	forever := make(chan struct{})
	t.Cleanup(func() { close(forever) })
	entered := make(chan struct{})
	outcome := make(chan error, 1)
	go func() {
		_, err := scopes.run(func(Ctx) (any, error) {
			close(entered)
			<-forever
			return nil, nil
		}, nil, chordctx.Background())
		outcome <- err
	}()
	<-entered
	loop.Run(func() { scopes.close(scopeClosed()) })
	wantScopeError(t, <-outcome, data.CodeReleased, data.ReasonClosed, "host closed")
}

func TestHostRootLeavesNoListenerOnTheCallersSignalOnceHostCloseRejectedAnFnThatNeverSettles(t *testing.T) {
	scopes, _, loop := newScopeRoots(t)
	callers, _ := newScopeCounting()
	forever := make(chan struct{})
	t.Cleanup(func() { close(forever) })
	entered := make(chan struct{})
	outcome := make(chan error, 1)
	go func() {
		_, err := scopes.run(func(Ctx) (any, error) {
			close(entered)
			<-forever
			return nil, nil
		}, nil, callers)
		outcome <- err
	}()
	<-entered
	if callers.listeners() == 0 {
		t.Fatal("the running scope has no listener on the caller's signal")
	}
	loop.Run(func() { scopes.close(scopeClosed()) })
	wantScopeError(t, <-outcome, data.CodeReleased, data.ReasonClosed, "host closed")
	if callers.listeners() != 0 {
		t.Fatalf("listeners on the caller's signal = %d, want none", callers.listeners())
	}
}

func TestHostRootKeepsNothingOnceSealedItsScopeAndReferencesAreCollectedWhileTheCallersSignalLivesOn(t *testing.T) {
	scopes, host, _ := newScopeRoots(t)
	Info := scopeItem(t, "app.info")
	host.acquire = func(call scopeAcquire) (acquired, error) {
		return &scopeBinding{reference: &struct{ id int }{id: 1}}, nil
	}
	long, _ := chordctx.WithCancel(chordctx.Background())
	scope, reference := func() (weak.Pointer[callContext], weak.Pointer[struct{ id int }]) {
		var madeScope weak.Pointer[callContext]
		var madeReference weak.Pointer[struct{ id int }]
		if _, err := scopes.run(func(ctx Ctx) (any, error) {
			madeScope = weak.Make(ctx.(*callContext))
			got, err := ctx.Get(Info, delta.JsonObjectOf("id", 1.0), GetOptions{})
			if err != nil {
				return nil, err
			}
			madeReference = weak.Make(got.(*struct{ id int }))
			return nil, nil
		}, nil, long); err != nil {
			t.Fatal(err)
		}
		return madeScope, madeReference
	}()
	host.mu.Lock()
	host.acquires = nil
	host.mu.Unlock()
	nexttest.ExpectCollected(t, scope)
	nexttest.ExpectCollected(t, reference)
	if long.Err() != nil {
		t.Fatal("the caller's signal was aborted")
	}
}

// Own tests: the scope's part of the cases that need a host. Each names the Pi case it mirrors.

// Mirrors "host.root › gives fn a call context: the host's principal (or the given one), this host's incarnation,
// `ctx.get`": the scope acts as the principal it is given, with the host's incarnation, and gets references.
func TestRootScopesRunGivesFnACallContext(t *testing.T) {
	scopes, host, _ := newScopeRoots(t)
	Info := scopeItem(t, "app.info")
	host.acquire = func(call scopeAcquire) (acquired, error) {
		return &scopeBinding{reference: call.params}, nil
	}
	server := delta.JsonObjectOf("kind", "server")
	type seen struct {
		principal   data.Principal
		incarnation string
		id          any
	}
	value, err := scopes.run(func(ctx Ctx) (any, error) {
		got, err := ctx.Get(Info, delta.JsonObjectOf("id", 7.0), GetOptions{})
		if err != nil {
			return nil, err
		}
		return seen{ctx.Principal(), ctx.Incarnation(), got.(*delta.JsonObject).Value("id")}, nil
	}, server, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := value.(seen)
	if got.principal != server || got.incarnation != host.Incarnation() || got.id != 7.0 {
		t.Fatalf("seen = %+v", got)
	}
	owner := delta.JsonObjectOf("kind", "owner", "access", "local")
	principal, err := scopes.run(func(ctx Ctx) (any, error) { return ctx.Principal(), nil }, owner, chordctx.Background())
	if err != nil || principal != owner {
		t.Fatalf("principal = %v, %v", principal, err)
	}
}

// Mirrors "host.root › seals the context when fn settles: its references released, its signal aborted, `ctx.get` refused
// `released`": including a throwing fn, which seals too and rejects with its error.
func TestRootScopesRunSealsTheContextWhenFnSettles(t *testing.T) {
	scopes, host, _ := newScopeRoots(t)
	Info := scopeItem(t, "app.info")
	binding := &scopeBinding{reference: "info"}
	host.acquire = func(scopeAcquire) (acquired, error) { return binding, nil }
	var kept Ctx
	got, err := scopes.run(func(ctx Ctx) (any, error) {
		kept = ctx
		return ctx.Get(Info, delta.JsonObjectOf("id", 1.0), GetOptions{})
	}, nil, chordctx.Background())
	if err != nil || got != "info" {
		t.Fatalf("run = %v, %v", got, err)
	}
	if binding.released() != 1 {
		t.Fatalf("the reference was released %d times, want once", binding.released())
	}
	if kept.Err() == nil {
		t.Fatal("the scope's signal is not aborted")
	}
	_, err = kept.Get(Info, delta.JsonObjectOf("id", 1.0), GetOptions{})
	wantScopeError(t, err, data.CodeReleased, data.ReasonSettled, "app.info: ctx.get after its call settled")
	failure := errors.New("fn failed")
	kept = nil
	if _, err := scopes.run(func(ctx Ctx) (any, error) {
		kept = ctx
		return nil, failure
	}, nil, chordctx.Background()); err != failure {
		t.Fatalf("err = %v, want fn's own", err)
	}
	if kept.Err() == nil {
		t.Fatal("a throwing fn did not seal its scope")
	}
}

// Mirrors "host.root › an abort rejects at once and seals": a running reference is released, a later get is `released`,
// and the uncooperative fn runs on.
func TestRootScopesRunAnAbortRejectsAtOnceAndSeals(t *testing.T) {
	scopes, host, _ := newScopeRoots(t)
	Info := scopeItem(t, "app.info")
	binding := &scopeBinding{reference: "info"}
	host.acquire = func(scopeAcquire) (acquired, error) { return binding, nil }
	cancellable, cancel := chordctx.WithCancel(chordctx.Background())
	forever := make(chan struct{})
	t.Cleanup(func() { close(forever) })
	kept := make(chan Ctx, 1)
	outcome := make(chan error, 1)
	go func() {
		_, err := scopes.run(func(ctx Ctx) (any, error) {
			if _, err := ctx.Get(Info, delta.JsonObjectOf("id", 1.0), GetOptions{}); err != nil {
				return nil, err
			}
			kept <- ctx
			// Uncooperative: it waits on nothing of Chord's.
			<-forever
			return nil, nil
		}, nil, cancellable)
		outcome <- err
	}()
	ctx := <-kept
	cancel(scopeReason("stop"))
	if err := <-outcome; err == nil || err.Error() != "stop" {
		t.Fatalf("err = %v, want stop", err)
	}
	scopeEventually(t, "the scope sealed: its reference released", func() bool { return binding.released() == 1 })
	_, err := ctx.Get(Info, delta.JsonObjectOf("id", 2.0), GetOptions{})
	wantScopeError(t, err, data.CodeReleased, data.ReasonSettled, "app.info: ctx.get after its call settled")
}

// Mirrors "host.root › `host.close` seals every open scope (an fn sleeping on its ctx ends)": close seals each open scope
// once with the host's reason and rejects its caller with it; a scope whose fn settled before is not touched.
func TestRootScopesCloseSealsEveryOpenScopeAndRejectsItsCaller(t *testing.T) {
	scopes, _, loop := newScopeRoots(t)
	entered := make(chan struct{}, 2)
	ended := make(chan error, 2)
	outcomes := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := scopes.run(func(ctx Ctx) (any, error) {
				entered <- struct{}{}
				<-ctx.Done()
				ended <- context.Cause(ctx)
				return nil, nil
			}, nil, chordctx.Background())
			outcomes <- err
		}()
	}
	<-entered
	<-entered
	reason := scopeClosed()
	loop.Run(func() { scopes.close(reason) })
	for range 2 {
		if cause := <-ended; cause != reason {
			t.Fatalf("a scope ended with %v, want the host's reason", cause)
		}
		if err := <-outcomes; err != reason {
			t.Fatalf("a caller got %v, want the host's reason", err)
		}
	}
	loop.Run(func() { scopes.close(reason) })
}
