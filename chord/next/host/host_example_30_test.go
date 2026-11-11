package host

// pi: packages/chord/test/next/examples/30-host-root.ts

import (
	"context"
	"errors"
	"fmt"
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
						exPush(draft, fmt.Sprintf("%s (by %s)", exParam(args, "text"), principalKind(exCtx(ctx).Principal())))
					}, ctx)
				}),
			}), nil
		}, ServeOptions{})
	})
	errs := &hostErrors{}
	host := newHost(t, HostOptions{Principal: serverPrincipal(), OnError: errs.push})
	// 1. A scope made before `load` resolved: its `ctx.get` parks until the host is loaded.
	type outcome struct {
		value any
		err   error
	}
	earlyDone := make(chan outcome, 1)
	go func() {
		value, err := host.Root(func(ctx Ctx) (any, error) {
			reference, err := ctx.Get(notes, jsonObject("session", "s1"), GetOptions{})
			if err != nil {
				return nil, err
			}
			return reference.Member("list").Value()
		}, background, RootOptions{})
		earlyDone <- outcome{value, err}
	}()
	mustLoad(t)(host.Load([]HostFacet{{Name: "@example/notes", Facet: facet}}, background))
	early := <-earlyDone
	must(t, early.err)
	exEqual(t, "before load, read after it", early.value, exJSON(t, `[]`))
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
	// 6. Closing the host seals the scopes still open and rejects their callers at once: an fn sleeping on its ctx ends.
	sleeping := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		_, err := host.Root(func(ctx Ctx) (any, error) { close(started); return nil, Sleep(60*time.Second, ctx) }, background, RootOptions{})
		sleeping <- err
	}()
	<-started
	exCloseHost(t, host)
	_ = exCode(t, <-sleeping, data.CodeReleased, data.ReasonClosed)
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}
