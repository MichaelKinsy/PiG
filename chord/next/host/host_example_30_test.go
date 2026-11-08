package host

// pi: packages/chord/test/examples/30-host-root.ts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// --- 30-host-root.ts -----------------------------------------------------------------------------------------------

func TestExample30HostRoot(t *testing.T) {
	notes := exLocal("example.notes", contracts.Params{exProp("session", exStr())},
		contracts.Mem("list", exArr(exStr())), contracts.Mem("add", contracts.NewMethod(exObj(exProp("text", exStr())))))
	list := exStringListDoc("example.list")
	// heldGate parks the next `add`'s change (set only by step 6) until its scope ended: Pi's change runs after the root's fn.
	var heldGate atomic.Pointer[chan struct{}]
	entered := make(chan struct{}, 1)
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(notes, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, list)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"list": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("list"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, doc.Change(func(draft *delta.Object) {
						if gate := heldGate.Load(); gate != nil {
							entered <- struct{}{}
							<-*gate
							// The caller scope's abort reaches the handler's context on a goroutine of its own (Pi's listener is synchronous).
							<-ctx.Done()
						}
						exPush(draft, fmt.Sprintf("%s (by %s)", exParam(args, "text"), principalKind(exCtx(ctx).Principal())))
					}, ctx)
				}),
			}), nil
		}, ServeOptions{})
	})
	errs := &hostErrors{}
	host := newHost(t, HostOptions{Principal: serverPrincipal(), OnError: errs.push})
	// 1. A scope made before `load`: its `ctx.get` is refused at once (`not_ready`, "call host.load first").
	_, err := host.Root(func(ctx Ctx) (any, error) {
		reference, err := ctx.Get(notes, jsonObject("session", "s1"), GetOptions{})
		if err != nil {
			return nil, err
		}
		return reference.Member("list").Value()
	}, background, RootOptions{})
	if chordErr := exCode(t, err, data.CodeNotReady, ""); !strings.Contains(chordErr.Message, "call host.load first") {
		t.Fatalf("before load: %v", err)
	}
	mustLoad(t)(host.Load([]HostFacet{{Name: "@example/notes", Facet: facet}}, background))
	// 2. A tool's call: get, call, return plain data. The reference is released when fn settles.
	addNote := func(text string) any {
		value, err := host.Root(func(ctx Ctx) (any, error) {
			reference, err := ctx.Get(notes, jsonObject("session", "s1"), GetOptions{})
			if err != nil {
				return nil, err
			}
			if _, err := reference.Call("add", jsonObject("text", text), ctx); err != nil {
				return nil, err
			}
			return reference.Member("list").Value()
		}, background, RootOptions{})
		must(t, err)
		return value
	}
	exEqual(t, "after a tool call", addNote("first"), exJSON(t, `["first (by server)"]`))
	// 3. Base code acting for an authenticated user names the principal.
	owner := jsonObject("kind", "owner", "name", "ada")
	principal, err := host.Root(func(ctx Ctx) (any, error) { return ctx.Principal(), nil }, background, RootOptions{Principal: owner})
	must(t, err)
	exEqual(t, "as the owner", principal, owner)
	// 4. A kept ctx is sealed once fn settled.
	var kept Ctx
	_, err = host.Root(func(ctx Ctx) (any, error) { kept = ctx; return nil, nil }, background, RootOptions{})
	must(t, err)
	if kept.Err() == nil {
		t.Fatal("kept ctx aborted: false")
	}
	_, err = kept.Get(notes, jsonObject("session", "s1"), GetOptions{})
	_ = exCode(t, err, data.CodeReleased, "")
	// 5. Cancelling the context rejects at once and seals the scope; an fn that ignores its signal fails at its next use
	// of Chord.
	cancellable, cancel := chordctx.WithCancel(background)
	stubborn := make(chan error, 1)
	released := make(chan struct{})
	go func() {
		_, err := host.Root(func(ctx Ctx) (any, error) {
			<-released
			_, err := ctx.Get(notes, jsonObject("session", "s1"), GetOptions{})
			return nil, err
		}, cancellable, RootOptions{})
		stubborn <- err
	}()
	scopeEventually(t, "the scope is open", func() bool { return exActivity(t, host).References >= 0 })
	cancel(errors.New("user pressed stop"))
	if err := <-stubborn; err == nil || err.Error() != "user pressed stop" {
		t.Fatalf("cancelled: %v", err)
	}
	close(released)
	// 6. The rule: await what you start in a root or a handler. A call left running when fn returns is cancelled with the
	// scope, and the change it was about to make never commits: it rejects, and onError gets `transaction_cancelled`
	// naming the object and the scope that ended.
	// A held reference keeps the object alive: the scope ending releases only its own.
	_ = exGet(t, host, notes, jsonObject("session", "s1"))
	gate := make(chan struct{})
	heldGate.Store(&gate)
	unawaited := make(chan error, 1)
	_, err = host.Root(func(ctx Ctx) (any, error) {
		reference, err := ctx.Get(notes, jsonObject("session", "s1"), GetOptions{})
		if err != nil {
			return nil, err
		}
		go func() {
			_, err := reference.Call("add", jsonObject("text", "lost"), ctx)
			unawaited <- err
		}()
		<-entered
		return nil, nil
	}, background, RootOptions{})
	must(t, err)
	close(gate)
	_ = exCode(t, <-unawaited, data.CodeReleased, "")
	scopeEventually(t, "the transaction_cancelled report", func() bool { return errs.Len() == 1 })
	if report := errs.All()[0]; report.Kind != ErrTransactionCancelled || report.Object == "" || report.Error == nil {
		t.Fatalf("report = %+v, want transaction_cancelled naming the object and the scope that ended", report)
	}
	heldGate.Store(nil)
	// 7. Closing the host seals the scopes still open and rejects their callers at once: an fn sleeping on its ctx ends.
	sleeping := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		_, err := host.Root(func(ctx Ctx) (any, error) { close(started); return nil, Sleep(60*time.Second, ctx) }, background, RootOptions{})
		sleeping <- err
	}()
	<-started
	exCloseHost(t, host)
	_ = exCode(t, <-sleeping, data.CodeReleased, data.ReasonClosed)
	if errs.Len() > 1 {
		t.Fatalf("background errors: %v", errs.All())
	}
}
