package host

import (
	"context"
	"errors"
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
)

// Ports packages/chord/test/next/host/root.test.ts: `host.root` on a host (createHost, loaded), its scopes sealed when fn
// settles, when its context aborts and at the host's close.

// rootInfo is root.test.ts `Info`: a param `id`, members `id` and `wait`.
var rootInfo = mustContract(declareLocal("app.info", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
	Members: []contracts.LocalEntry{contracts.Mem("id", contracts.Int()), contracts.Mem("wait", contracts.NewMethod())},
}))

// serveRootInfo serves `Info`; its `wait` waits until the caller's context aborts.
func serveRootInfo(h FacetHost, _ FacetSelf) error {
	return h.Serve(rootInfo, func(params *delta.JsonObject, _ Self) (contracts.Implementation, error) {
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"id": constant(params.Value("id"))},
			Methods: map[string]contracts.MethodFunction{
				"wait": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, Sleep(time.Minute, ctx)
				},
			},
		}}, nil
	}, ServeOptions{})
}

// awaitRootOpen waits until host has n open root scopes.
func awaitRootOpen(t testing.TB, host Host, n int) {
	t.Helper()
	h := host.(*chordHost)
	for {
		var open int
		h.enter(func() { open = len(h.roots.open) })
		if open >= n {
			return
		}
		select {
		case <-t.Context().Done():
			t.Fatalf("%d scopes never opened", n)
		default:
		}
		turn(host)
	}
}

func wantRootClosed(t testing.TB, err error) {
	t.Helper()
	wantScopeError(t, err, data.CodeReleased, data.ReasonClosed, "the host is closed")
}

// Mirrors root.test.ts "host.root › gives fn a call context: the host's principal (or the given one), this host's incarnation, `ctx.get`".
func TestHostRootGivesFnACallContext(t *testing.T) {
	host := newHost(t, HostOptions{Principal: serverPrincipal(), OnError: func(HostError) {}})
	defer closeHosts(t, host)
	must(t, host.Load([]HostFacet{{Name: "@app/info", Facet: DefineFacet(serveRootInfo)}}, background))
	type seen struct {
		principal   data.Principal
		incarnation string
		id          any
	}
	value, err := host.Root(func(ctx Ctx) (any, error) {
		info, err := ctx.Get(rootInfo, jsonObject("id", float64(7)), GetOptions{})
		if err != nil {
			return nil, err
		}
		id, err := info.Member("id").Value()
		return seen{ctx.Principal(), ctx.Incarnation(), id}, err
	}, background, RootOptions{})
	must(t, err)
	got := value.(seen)
	if principalKind(got.principal) != "server" || got.id != float64(7) || got.incarnation != incarnationOf(host) {
		t.Fatalf("seen = %+v, want the server principal, this host's incarnation and id 7", got)
	}
	owner := jsonObject("kind", "owner", "access", "local")
	principal, err := host.Root(func(ctx Ctx) (any, error) { return ctx.Principal(), nil }, background, RootOptions{Principal: owner})
	must(t, err)
	if principal != any(owner) {
		t.Fatalf("principal = %v, want the given one", principal)
	}
}

// Mirrors "host.root › seals the context when fn settles: its references released, its signal aborted, `ctx.get` refused `released`".
func TestHostRootSealsTheContextWhenFnSettles(t *testing.T) {
	host, _ := loaded(t, serveRootInfo, loadedOptions{})
	defer closeHosts(t, host)
	var kept Ctx
	value, err := host.Root(func(ctx Ctx) (any, error) {
		kept = ctx
		return ctx.Get(rootInfo, jsonObject("id", float64(1)), GetOptions{})
	}, background, RootOptions{})
	must(t, err)
	info := value.(*Reference)
	if state := refStateOf(info); state.Kind != RefStateEnded || state.Error.Code != data.CodeReleased {
		t.Fatalf("state = %+v, want ended released", state)
	}
	if kept.Err() == nil {
		t.Fatal("the scope's signal is not aborted once fn settled")
	}
	_, err = kept.Get(rootInfo, jsonObject("id", float64(1)), GetOptions{})
	wantScopeError(t, err, data.CodeReleased, data.ReasonSettled, "app.info: ctx.get after its call settled")
	// A throwing fn seals too, and rejects with its error.
	if _, err := host.Root(func(Ctx) (any, error) { return nil, errors.New("fn failed") }, background, RootOptions{}); err == nil || err.Error() != "fn failed" {
		t.Fatalf("err = %v, want fn failed", err)
	}
}

// Mirrors "host.root › an abort rejects at once and seals: a running call through its reference rejects, a later get is `released`".
func TestHostRootAnAbortRejectsAtOnceAndSeals(t *testing.T) {
	host, _ := loaded(t, serveRootInfo, loadedOptions{})
	defer closeHosts(t, host)
	cancellable, cancel := chordctx.WithCancel(background)
	never := make(chan struct{})
	defer close(never)
	var kept Ctx
	started := make(chan []*parkedCallResult, 1)
	running := make(chan error, 1)
	go func() {
		_, err := host.Root(func(ctx Ctx) (any, error) {
			kept = ctx
			info, err := ctx.Get(rootInfo, jsonObject("id", float64(1)), GetOptions{})
			if err != nil {
				return nil, err
			}
			started <- startCalls(t, info, "wait", 1)
			// Uncooperative: it waits on nothing of Chord's.
			<-never
			return nil, nil
		}, cancellable, RootOptions{})
		running <- err
	}()
	call := <-started
	cancel(scopeReason("stop"))
	if err := <-running; err == nil || err.Error() != "stop" {
		t.Fatalf("root = %v, want stop", err)
	}
	if _, err := call[0].wait(background); err == nil {
		t.Fatal("the running call did not reject")
	} else {
		wantRejection(t, err, data.CodeReleased)
	}
	_, err := kept.Get(rootInfo, jsonObject("id", float64(2)), GetOptions{})
	wantScopeError(t, err, data.CodeReleased, data.ReasonSettled, "app.info: ctx.get after its call settled")
}

// wantRejection fails unless err is a ChordError of code.
func wantRejection(t testing.TB, err error, code data.ChordErrorCode) {
	t.Helper()
	if chordErr, ok := data.IsChordError(err); !ok || chordErr.Code != code {
		t.Fatalf("err = %v, want a ChordError of code %s", err, code)
	}
}

// Mirrors "host.root › an aborted context rejects before fn runs; a synchronous throw is a rejection".
func TestHostRootAnAbortedContextRejectsBeforeFnRunsASynchronousThrowIsARejection(t *testing.T) {
	host, _ := loaded(t, serveRootInfo, loadedOptions{})
	defer closeHosts(t, host)
	aborted, cancel := chordctx.WithCancel(background)
	cancel(scopeReason("gone"))
	ran := false
	if _, err := host.Root(func(Ctx) (any, error) { ran = true; return nil, nil }, aborted, RootOptions{}); err == nil || err.Error() != "gone" {
		t.Fatalf("err = %v, want gone", err)
	}
	if ran {
		t.Fatal("fn ran for an aborted context")
	}
	if _, err := host.Root(func(Ctx) (any, error) { return nil, scopeReason("sync") }, background, RootOptions{}); err == nil || err.Error() != "sync" {
		t.Fatalf("err = %v, want sync", err)
	}
	// A panic is a Go fn's synchronous throw.
	if _, err := host.Root(func(Ctx) (any, error) { panic(scopeReason("sync")) }, background, RootOptions{}); err == nil || err.Error() != "sync" {
		t.Fatalf("err = %v, want sync", err)
	}
}

// Mirrors "host.root › a `ctx.get` before `load` resolved parks until it did".
func TestHostRootACtxGetBeforeLoadResolvedParksUntilItDid(t *testing.T) {
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	type result struct {
		id  any
		err error
	}
	got := make(chan result, 1)
	go func() {
		value, err := host.Root(func(ctx Ctx) (any, error) {
			info, err := ctx.Get(rootInfo, jsonObject("id", float64(3)), GetOptions{})
			if err != nil {
				return nil, err
			}
			return info.Member("id").Value()
		}, background, RootOptions{})
		got <- result{value, err}
	}()
	awaitUnbound(t, host)
	must(t, host.Load([]HostFacet{{Name: "@app/info", Facet: DefineFacet(serveRootInfo)}}, background))
	if outcome := <-got; outcome.err != nil || outcome.id != float64(3) {
		t.Fatalf("root = %v, %v; want 3", outcome.id, outcome.err)
	}
}

// Mirrors "host.root › `host.close` seals every open scope (an fn sleeping on its ctx ends); after it, `root` rejects `released`".
func TestHostRootHostCloseSealsEveryOpenScopeAndAfterItRootRejectsReleased(t *testing.T) {
	host, _ := loaded(t, serveRootInfo, loadedOptions{})
	sleeping := make(chan error, 1)
	go func() {
		_, err := host.Root(func(ctx Ctx) (any, error) { return nil, Sleep(time.Minute, ctx) }, background, RootOptions{})
		sleeping <- err
	}()
	awaitRootOpen(t, host, 1)
	must(t, host.Close(background))
	wantRootClosed(t, <-sleeping)
	_, err := host.Root(func(Ctx) (any, error) { return 1, nil }, background, RootOptions{})
	wantRootClosed(t, err)
}

// Mirrors "host.root › aborts the scope's own signal when fn settles and at `host.close`, the caller's context having one (Fable, Opus, Sol)".
func TestHostRootAbortsTheScopesOwnSignalWhenFnSettlesAndAtHostCloseTheCallersContextHavingOne(t *testing.T) {
	host, _ := loaded(t, serveRootInfo, loadedOptions{})
	settled, _ := chordctx.WithCancel(background)
	var kept Ctx
	if _, err := host.Root(func(ctx Ctx) (any, error) { kept = ctx; return nil, nil }, settled, RootOptions{}); err != nil {
		t.Fatal(err)
	}
	if kept.Err() == nil {
		t.Fatal("the scope's signal is not aborted once fn settled")
	}
	if settled.Err() != nil {
		t.Fatal("the caller's own signal was aborted")
	}
	closing, _ := chordctx.WithCancel(background)
	ended := make(chan struct{})
	outcome := make(chan error, 1)
	go func() {
		_, err := host.Root(func(ctx Ctx) (any, error) {
			defer close(ended)
			return nil, Sleep(time.Minute, ctx)
		}, closing, RootOptions{})
		outcome <- err
	}()
	awaitRootOpen(t, host, 1)
	must(t, host.Close(background))
	<-ended
	if closing.Err() != nil {
		t.Fatal("the caller's own signal was aborted by the close")
	}
	wantRootClosed(t, <-outcome)
}

// Mirrors "host.root › `host.close` rejects the caller at once, whatever an uncooperative fn still does (Sol)".
func TestHostRootHostCloseRejectsTheCallerAtOnceWhateverAnUncooperativeFnStillDoes(t *testing.T) {
	host, _ := loaded(t, serveRootInfo, loadedOptions{})
	forever := make(chan struct{})
	defer close(forever)
	outcome := make(chan error, 1)
	go func() {
		_, err := host.Root(func(Ctx) (any, error) { <-forever; return nil, nil }, background, RootOptions{})
		outcome <- err
	}()
	awaitRootOpen(t, host, 1)
	must(t, host.Close(background))
	wantRootClosed(t, <-outcome)
}

// Mirrors "host.root › leaves no listener on the caller's signal once `host.close` rejected an fn that never settles (Astra)".
func TestHostRootLeavesNoListenerOnTheCallersSignalOnceHostCloseRejectedAnFnThatNeverSettles(t *testing.T) {
	callers, _ := newScopeCounting()
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	forever := make(chan struct{})
	defer close(forever)
	outcome := make(chan error, 1)
	go func() {
		_, err := host.Root(func(Ctx) (any, error) { <-forever; return nil, nil }, callers, RootOptions{})
		outcome <- err
	}()
	awaitRootOpen(t, host, 1)
	if callers.count() == 0 {
		t.Fatal("the running scope has no listener on the caller's signal")
	}
	must(t, host.Close(background))
	wantRootClosed(t, <-outcome)
	if callers.count() != 0 {
		t.Fatalf("listeners on the caller's signal = %d, want none", callers.count())
	}
}

// Mirrors "host.root › keeps nothing once sealed: its scope and references are collected while the caller's signal lives on".
func TestHostRootKeepsNothingOnceSealedItsScopeAndReferencesAreCollected(t *testing.T) {
	host, _ := loaded(t, serveRootInfo, loadedOptions{})
	defer closeHosts(t, host)
	long, _ := chordctx.WithCancel(background)
	var mu sync.Mutex
	made := func() (weak.Pointer[callContext], weak.Pointer[Reference]) {
		var scope weak.Pointer[callContext]
		var reference weak.Pointer[Reference]
		_, err := host.Root(func(ctx Ctx) (any, error) {
			info, err := ctx.Get(rootInfo, jsonObject("id", float64(1)), GetOptions{})
			mu.Lock()
			scope, reference = weak.Make(ctx.(*callContext)), weak.Make(info)
			mu.Unlock()
			return nil, err
		}, long, RootOptions{})
		must(t, err)
		return scope, reference
	}
	scope, reference := made()
	nexttest.ExpectCollected(t, scope)
	nexttest.ExpectCollected(t, reference)
	if long.Err() != nil {
		t.Fatal("the caller's signal was aborted")
	}
}
