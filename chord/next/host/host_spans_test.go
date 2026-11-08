package host

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// pi: packages/chord/test/host/host.test.ts, packages/chord/test/host/ctx.test.ts

// Ports packages/chord/test/host/host.test.ts "the host's spans (slice 9)" and packages/chord/test/host/ctx.test.ts
// "ctx.trace (C11, slice 9)", and the root `trace` option of host.ts.

func spanAttributesAre(t testing.TB, span telemetry.RecordedTelemetrySpan, want telemetry.SpanAttributes) {
	t.Helper()
	if len(span.Attributes) != len(want) {
		t.Errorf("%s attributes = %v, want exactly %v", span.Name, span.Attributes, want)
		return
	}
	for _, each := range want {
		if got, ok := span.Attributes.Get(each.Name); !ok || got != each.Value {
			t.Errorf("%s: %s = %v, want %v", span.Name, each.Name, got, each.Value)
		}
	}
}

func TestHostSpansTraceLoadUnderItsCallersSpanPluginsGivenAndLeftOutHostLifetimeActivationsLinkToIt(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	host := newHost(t, HostOptions{Telemetry: recorder, OnError: noopOnError})
	ok := DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveInfoFactory(h, nil) })
	failing := DefineFacet(func(FacetHost, FacetSelf) error { return errors.New("setup") })
	tracedUnder(t, recorder, "start", func(ctx context.Context) {
		if _, err := host.Load([]HostFacet{{Name: "@test/ok", Facet: ok}, {Name: "@test/failing", Facet: failing}}, ctx); err != nil {
			t.Error(err)
		}
	})
	start := spanWhere(t, recorder, "start", nil)
	load := spanWhere(t, recorder, "chord.load", nil)
	if !childOf(load, start) {
		t.Errorf("chord.load is not a child of the caller's span")
	}
	spanAttributesAre(t, load, telemetry.SpanAttributes{telemetry.Attr("chord.host", "client"), telemetry.Attr("plugins", float64(2)), telemetry.Attr("failed", float64(1))})
	if activation := spanWhere(t, recorder, "chord.activate", nil); !linksTo(activation, load) {
		t.Errorf("the host-lifetime activation links %v, want the load span", linksOf(activation))
	}
}

func TestHostSpansTraceAReloadWithOneEventPerObjectsFate(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	host := newHost(t, HostOptions{Telemetry: recorder, OnError: noopOnError})
	facet := func() Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveInfoFactory(h, nil) })
	}
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: facet()}}, background))
	tracedUnder(t, recorder, "apply", func(ctx context.Context) {
		reloadMust(t)(host.Reload([]HostFacet{{Name: "@test/app", Facet: facet()}}, ctx, ReloadOptions{}))
	})
	reload := spanWhere(t, recorder, "chord.reload", nil)
	spanAttributesAre(t, reload, telemetry.SpanAttributes{
		telemetry.Attr("chord.host", "client"), telemetry.Attr("plugins", float64(1)), telemetry.Attr("added", float64(0)),
		telemetry.Attr("changed", float64(1)), telemetry.Attr("removed", float64(0)), telemetry.Attr("failed", float64(0)), telemetry.Attr("fates", float64(1)),
	})
	if len(reload.Events) != 1 || reload.Events[0].Name != "chord.reload.fate" {
		t.Fatalf("events = %v, want one chord.reload.fate", reload.Events)
	}
	if object, _ := reload.Events[0].Attributes.Get("object"); object != "app.info" {
		t.Errorf("fate object = %v, want app.info", object)
	}
	if fate, _ := reload.Events[0].Attributes.Get("fate"); fate != "restarted" {
		t.Errorf("fate = %v, want restarted", fate)
	}
}

func TestHostSpansCountTheCostCountersOfItsLinksAndRootsWorkOnItsAdapterUnderContextsWithoutASpan(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	pair := linked(t, func(h FacetHost) error { _, err := serveNotes(h, Notes); return err }, linkedOptions{Telemetry: recorder})
	notes := mustGet(t, pair.Client, Notes, jsonObject("session", "S1"))
	if _, err := notes.Call("add", jsonObject("text", "a"), background); err != nil {
		t.Fatal(err)
	}
	_, err := pair.Server.Root(func(ctx Ctx) (any, error) {
		ref, err := ctx.Get(Notes, jsonObject("session", "S1"), GetOptions{})
		if err == nil {
			ref.Release()
		}
		return nil, err
	}, background, RootOptions{})
	must(t, err)
	counted := map[string]bool{}
	for _, measurement := range recorder.GetMetrics() {
		counted[measurement.Name] = true
	}
	for _, name := range []string{"chord.link.checks", "chord.data.validations", "chord.link.encodes"} {
		if !counted[name] {
			t.Errorf("%s was not counted on the adapter: %v", name, counted)
		}
	}
}

func TestHostSpansRecordARevocationsCountAfterItUnderTheCallersSpan(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	host := newHost(t, HostOptions{
		Address: new(Server.Address(nil)), Principal: serverPrincipal(), Telemetry: recorder, OnError: noopOnError,
		RevokedBy: func(principal data.Principal) ([]delta.JsonValue, error) {
			if principal.Value("kind") == "guest" {
				return []delta.JsonValue{principal.Value("session")}, nil
			}
			return nil, nil
		},
	})
	mustLoad(t)(host.Load(nil, background))
	a, _ := link.MemoryLinkPair()
	must(t, host.Accept(a, LinkOptions{Principal: guestPrincipal("S1")}, background))
	tracedUnder(t, recorder, "admin", func(ctx context.Context) { must(t, host.Revoke("S1", ctx)) })
	revoke := spanWhere(t, recorder, "chord.revoke", nil)
	spanAttributesAre(t, revoke, telemetry.SpanAttributes{telemetry.Attr("chord.host", "server"), telemetry.Attr("count", float64(1))})
	must(t, host.Revoke("S1", background))
	if got := len(spansNamed(recorder, "chord.revoke")); got != 1 {
		t.Errorf("chord.revoke spans = %d, want 1 (a revoke without a span records none)", got)
	}
}

func TestHostRootRunsItsScopeInANewRootSpanOfItsAdapterWithItsHostAndAttributesAFailureMarksItFailed(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	host := newHost(t, HostOptions{Telemetry: recorder, OnError: noopOnError})
	mustLoad(t)(host.Load(nil, background))
	trace := &RootTrace{Name: "app.click", Attributes: telemetry.SpanAttributes{telemetry.Attr("button", "ok")}}
	var seen telemetry.TelemetrySpan
	_, err := host.Root(func(ctx Ctx) (any, error) { seen = chordctx.Telemetry(ctx); return nil, nil }, background, RootOptions{Trace: trace})
	must(t, err)
	click := spanWhere(t, recorder, "app.click", nil)
	if click.ParentID != nil {
		t.Errorf("the root span has a parent")
	}
	spanAttributesAre(t, click, telemetry.SpanAttributes{telemetry.Attr("chord.host", "client"), telemetry.Attr("button", "ok")})
	if seen == telemetry.TelemetrySpan(telemetry.NoopTelemetryContext) {
		t.Errorf("ctx.telemetry is the no-op context")
	}
	_, err = host.Root(func(Ctx) (any, error) { return nil, errors.New("broke") }, background, RootOptions{Trace: &RootTrace{Name: "app.fails"}})
	if err == nil {
		t.Fatal("a failing root returned no error")
	}
	failed := spanWhere(t, recorder, "app.fails", nil)
	if failed.Status.Error == nil || failed.Status.Error.Name != "trace_failed" || failed.Status.Error.Message != "broke" {
		t.Errorf("status = %+v, want error trace_failed: broke", failed.Status)
	}
}

// root.test.ts › host.root's trace › starts nothing without an adapter that records: the scope's ctx keeps its caller's
// telemetry.
func TestHostRootStartsNothingWithoutAnAdapterThatRecords(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	mustLoad(t)(host.Load(nil, background))
	same, err := host.Root(func(ctx Ctx) (any, error) {
		return chordctx.Telemetry(ctx) == chordctx.Telemetry(background), nil
	}, background, RootOptions{Trace: &RootTrace{Name: "x"}})
	must(t, err)
	if same != true {
		t.Errorf("the root's ctx telemetry differs from its caller's, want the same")
	}
}

// --- ctx.trace ------------------------------------------------------------------------------------------------------------------

var tracedType = mustContract(declareLocal("app.traced", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("work", contracts.NewMethod(contracts.Schema{}, contracts.Int())),
		contracts.Mem("failing", contracts.NewMethod(contracts.Schema{}, contracts.Str())),
	},
}))

// tracedFacet serves tracedType: work runs workFn, failing runs failingFn; and the Page type for references.
func tracedFacet(workFn, failingFn func(ctx Ctx) (any, error)) Facet {
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := servePage(h, nil); err != nil {
			return err
		}
		return h.Serve(tracedType, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"work": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					if workFn == nil {
						return nil, nil
					}
					return workFn(ctx.(Ctx))
				},
				"failing": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					if failingFn == nil {
						return nil, nil
					}
					return failingFn(ctx.(Ctx))
				},
			}}}, nil
		}, ServeOptions{})
	})
}

// tracedHost is a host recording to recorder, serving tracedType.
func tracedHost(t testing.TB, recorder *telemetry.InMemoryTelemetryContext, workFn, failingFn func(ctx Ctx) (any, error)) Host {
	t.Helper()
	host := newHost(t, HostOptions{Telemetry: recorder, OnError: noopOnError})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/traced", Facet: tracedFacet(workFn, failingFn)}}, background))
	return host
}

func hasCode(err error, code data.ChordErrorCode) bool {
	_, ok := data.IsChordErrorCode(err, code)
	return ok
}

// Ports ctx.test.ts › ctx.trace (packages/chord/test/host/ctx.test.ts:267, `ctx.trace("app.step", ...)`).
func TestCtxTraceRunsFnInAChildSpanOfTheHandlersWithAChildCtxTheSamePrincipalSignalAndReferences(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	var outer, inner Ctx
	var item *Reference
	host := tracedHost(t, recorder, func(ctx Ctx) (any, error) {
		outer = ctx
		return ctx.Trace("app.step", telemetry.SpanAttributes{telemetry.Attr("n", float64(1))}, func(child Ctx) (any, error) {
			inner = child
			var err error
			if item, err = child.Get(hostPage, jsonObject("id", 3), GetOptions{}); err != nil {
				return nil, err
			}
			return child.Trace("app.deeper", nil, func(Ctx) (any, error) { return float64(3), nil })
		})
	}, nil)
	ref := mustGet(t, host, tracedType, jsonObject())
	tracedUnder(t, recorder, "caller", func(ctx context.Context) {
		got, err := ref.Call("work", nil, ctx)
		must(t, err)
		if got != float64(3) {
			t.Errorf("work = %v, want 3", got)
		}
	})
	step := spanWhere(t, recorder, "app.step", nil)
	method := spanWhere(t, recorder, "chord.method", map[string]any{"type": "app.traced"})
	if !childOf(step, method) {
		t.Errorf("app.step is not a child of the handler's chord.method")
	}
	spanAttributesAre(t, step, telemetry.SpanAttributes{telemetry.Attr("chord.host", "client"), telemetry.Attr("n", float64(1))})
	if deeper := spanWhere(t, recorder, "app.deeper", nil); !childOf(deeper, step) {
		t.Errorf("app.deeper is not a child of app.step")
	}
	if inner == outer {
		t.Errorf("the child ctx is the handler's")
	}
	if !reflect.DeepEqual(inner.Principal(), outer.Principal()) || inner.Incarnation() != outer.Incarnation() {
		t.Errorf("the child ctx has another principal or incarnation")
	}
	// The child's references are the handler's: released when the handler settled.
	if state := item.State().Value(); state.Kind != RefStateEnded {
		t.Errorf("the child's reference is %v after the handler settled, want ended", state.Kind)
	}
	if _, err := inner.Get(hostPage, jsonObject("id", 3), GetOptions{}); !hasCode(err, data.CodeReleased) {
		t.Errorf("get on the child after the handler settled = %v, want released", err)
	}
}

func TestCtxTraceMarksItsSpanFailedTraceFailedAndReturnsTheError(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	host := tracedHost(t, recorder, nil, func(ctx Ctx) (any, error) {
		_, err := ctx.Trace("app.fails", nil, func(Ctx) (any, error) { return nil, errors.New("broke") })
		return nil, err
	})
	ref := mustGet(t, host, tracedType, jsonObject())
	tracedUnder(t, recorder, "caller", func(ctx context.Context) {
		if _, err := ref.Call("failing", nil, ctx); err == nil {
			t.Errorf("the call succeeded")
		}
	})
	failed := spanWhere(t, recorder, "app.fails", nil)
	if failed.Status.Error == nil || failed.Status.Error.Name != "trace_failed" || failed.Status.Error.Message != "broke" {
		t.Errorf("status = %+v, want error trace_failed: broke", failed.Status)
	}
}

func TestCtxTraceReturnsAnErrorNeverPanicsWhenFnPanicsRecordedOrNot(t *testing.T) {
	for _, recorder := range []*telemetry.InMemoryTelemetryContext{telemetry.NewInMemoryTelemetryContext(), nil} {
		var options HostOptions
		options.OnError = noopOnError
		if recorder != nil {
			options.Telemetry = recorder
		}
		host := newHost(t, options)
		mustLoad(t)(host.Load([]HostFacet{{Name: "@test/traced", Facet: tracedFacet(func(ctx Ctx) (any, error) {
			_, err := ctx.Trace("app.sync", nil, func(Ctx) (any, error) { panic(errors.New("sync")) })
			if err == nil {
				return nil, errors.New("no error")
			}
			return float64(7), nil
		}, nil)}}, background))
		ref := mustGet(t, host, tracedType, jsonObject())
		var got any
		var err error
		if recorder == nil {
			got, err = ref.Call("work", nil, background)
		} else {
			tracedUnder(t, recorder, "caller", func(ctx context.Context) { got, err = ref.Call("work", nil, ctx) })
		}
		must(t, err)
		if got != float64(7) {
			t.Errorf("work = %v, want 7", got)
		}
		if recorder != nil && !slices.Contains(spanNames(recorder), "app.sync") {
			t.Errorf("spans = %v, want app.sync", spanNames(recorder))
		}
	}
}

func TestCtxTraceGivesFnTheHandlersOwnCtxWhenNothingRecords(t *testing.T) {
	var outer, inner Ctx
	host := newHost(t, HostOptions{OnError: noopOnError})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/traced", Facet: tracedFacet(func(ctx Ctx) (any, error) {
		outer = ctx
		return ctx.Trace("app.step", nil, func(child Ctx) (any, error) { inner = child; return float64(3), nil })
	}, nil)}}, background))
	ref := mustGet(t, host, tracedType, jsonObject())
	if _, err := ref.Call("work", nil, background); err != nil {
		t.Fatal(err)
	}
	if inner != outer {
		t.Errorf("fn got another ctx than the handler's")
	}
}

// --- natives (native.test.ts "spans (slice 9)") ---------------------------------------------------------------------------------

func allSettled(recorder *telemetry.InMemoryTelemetryContext, names ...string) bool {
	for _, name := range names {
		found := spansNamed(recorder, name)
		if len(found) == 0 {
			return false
		}
		for _, span := range found {
			if !span.Settled {
				return false
			}
		}
	}
	return true
}

func TestNativeSpansOpenEachNativeInAChordOpenSpanOfItsAttemptsAndCloseItInAChordCloseRoot(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	host, _ := loaded(t, serveRes(func(_ float64, self Self) {
		self.Open(func(opening context.Context) (any, error) {
			_ = chordctx.Telemetry(opening).StartSpan(telemetry.SpanOptions{Name: "app.connect"}, func(telemetry.TelemetrySpan) error { return nil })
			return 1, nil
		}, func(any, NativeCloseReason, context.Context) error { return nil })
		self.Open(func(context.Context) (any, error) { return nil, errors.New("no socket") },
			func(any, NativeCloseReason, context.Context) error { return nil })
	}), loadedOptions{Telemetry: recorder})
	ready := 50 * time.Millisecond
	if _, err := host.Get(background, nativeRes, jsonObject("id", float64(1)), GetOptions{ReadyTimeout: &ready}); err == nil {
		t.Fatal("the get succeeded")
	}
	scopeEventually(t, "the spans to settle", func() bool { return allSettled(recorder, "chord.open", "chord.close") })
	activation := spanWhere(t, recorder, "chord.activate", nil)
	opens := spansNamed(recorder, "chord.open")
	if len(opens) < 2 {
		t.Fatalf("opens = %d, want the two natives'", len(opens))
	}
	for index, want := range []struct {
		native float64
		status telemetry.SpanStatusCode
	}{{1, "ok"}, {2, "error"}} {
		native, _ := opens[index].Attributes.Get("native")
		if native != want.native || !childOf(opens[index], activation) || opens[index].Status.Status != want.status {
			t.Errorf("open %d: native %v, child of the activation %v, status %v; want native %v, child, %v", index, native, childOf(opens[index], activation), opens[index].Status.Status, want.native, want.status)
		}
	}
	if status := opens[1].Status; status.Error == nil || status.Error.Name != "open_failed" || status.Error.Message != "no socket" {
		t.Errorf("the failed open's status = %+v", status)
	}
	if connect := spanWhere(t, recorder, "app.connect", nil); !childOf(connect, opens[0]) {
		t.Errorf("the opener's span is not a child of chord.open")
	}
	closed := spanWhere(t, recorder, "chord.close", nil)
	if closed.ParentID != nil {
		t.Errorf("chord.close has a parent")
	}
	spanAttributesAre(t, closed, telemetry.SpanAttributes{
		telemetry.Attr("chord.host", "client"), telemetry.Attr("type", "app.res"), telemetry.Attr("native", float64(1)), telemetry.Attr("reason", "end"),
	})
	closeHosts(t, host)
}

func TestNativeSpansKeepAnOpenItsAttemptAbandonedCancelledOk(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	opening := make(chan struct{})
	host, _ := loaded(t, serveRes(func(_ float64, self Self) {
		self.Open(func(ctx context.Context) (any, error) {
			close(opening)
			<-ctx.Done()
			return nil, context.Cause(ctx)
		}, func(any, NativeCloseReason, context.Context) error { return nil })
	}), loadedOptions{Telemetry: recorder, ReadyTimeout: new(30 * time.Millisecond)})
	if _, err := host.Get(background, nativeRes, jsonObject("id", float64(1)), GetOptions{ReadyTimeout: new(100 * time.Millisecond)}); err == nil {
		t.Fatal("the get succeeded")
	}
	scopeEventually(t, "the open to settle", func() bool { return allSettled(recorder, "chord.open") })
	open := spanWhere(t, recorder, "chord.open", nil)
	if !open.Settled || open.Status.Status != telemetry.SpanStatusCodeOK || open.Status.Error != nil {
		t.Errorf("open: settled %v, status %+v, want settled and ok", open.Settled, open.Status)
	}
	closeHosts(t, host)
}

func TestNativeSpansMarkACloseThatFailedFailed(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	host, _ := loaded(t, serveRes(func(_ float64, self Self) {
		self.Open(func(context.Context) (any, error) { return 1, nil },
			func(any, NativeCloseReason, context.Context) error { return errors.New("stuck") })
	}), loadedOptions{Telemetry: recorder})
	if _, err := host.Get(background, nativeRes, jsonObject("id", float64(1)), GetOptions{}); err != nil {
		t.Fatal(err)
	}
	_ = host.Close(background)
	scopeEventually(t, "the close to settle", func() bool { return allSettled(recorder, "chord.close") })
	closed := spanWhere(t, recorder, "chord.close", nil)
	if reason, _ := closed.Attributes.Get("reason"); reason != "close" {
		t.Errorf("reason = %v, want close", reason)
	}
	if status := closed.Status; status.Error == nil || status.Error.Name != "close_failed" || status.Error.Message != "stuck" {
		t.Errorf("status = %+v, want error close_failed: stuck", status)
	}
}

// --- who a span was for (principalAttributes) ---------------------------------------------------------------------------

// whoMethodSpan is the attributes of the chord.method span of a call to a host acting as u1, recording on recorder.
func whoMethodSpan(t *testing.T, recorder *telemetry.InMemoryTelemetryContext, hook PrincipalAttributes) telemetry.RecordedTelemetrySpan {
	t.Helper()
	host := newHost(t, HostOptions{
		Principal: delta.JsonObjectOf("kind", "user", "id", "u1", "name", "Ada"), Telemetry: recorder,
		PrincipalAttributes: hook, OnError: noopOnError,
	})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/traced", Facet: tracedFacet(nil, nil)}}, background))
	ref := mustGet(t, host, tracedType, jsonObject())
	tracedUnder(t, recorder, "work", func(ctx context.Context) {
		_, err := ref.Call("work", nil, ctx)
		must(t, err)
	})
	return spanWhere(t, recorder, "chord.method", map[string]any{"type": "app.traced"})
}

// host.test.ts › who a span was for (principalAttributes) › records a method's principal key as `chord.principal` by default
func TestPrincipalAttributesRecordAMethodsPrincipalKeyAsChordPrincipalByDefault(t *testing.T) {
	span := whoMethodSpan(t, telemetry.NewInMemoryTelemetryContext(), nil)
	if got, _ := span.Attributes.Get("chord.principal"); got != `{"id":"u1","kind":"user","name":"Ada"}` {
		t.Errorf("chord.principal = %v", got)
	}
}

// › records the hook's attributes in its place, called once per recorded span
func TestPrincipalAttributesRecordTheHooksAttributesInItsPlaceCalledOncePerRecordedSpan(t *testing.T) {
	var calls atomic.Int32
	span := whoMethodSpan(t, telemetry.NewInMemoryTelemetryContext(), func(principal data.Principal) telemetry.SpanAttributes {
		calls.Add(1)
		return telemetry.SpanAttributes{telemetry.Attr("chord.principal", principalKind(principal)), telemetry.Attr("user.name", "Ada")}
	})
	attributesWant(t, span, map[string]any{"chord.principal": "user", "user.name": "Ada"})
	if got := calls.Load(); got != 1 {
		t.Errorf("hook calls = %d, want 1", got)
	}
}

// › records nothing of the principal when the hook returns {}
func TestPrincipalAttributesRecordNothingOfThePrincipalWhenTheHookReturnsNothing(t *testing.T) {
	span := whoMethodSpan(t, telemetry.NewInMemoryTelemetryContext(), func(data.Principal) telemetry.SpanAttributes { return nil })
	for _, name := range []string{"chord.principal", "user.name"} {
		if got, ok := span.Attributes.Get(name); ok {
			t.Errorf("%s = %v, want none", name, got)
		}
	}
	attributesWant(t, span, map[string]any{"principal.kind": "user"})
}

// › never calls the hook when nothing records (the NOOP adapter)
func TestPrincipalAttributesNeverCallTheHookWhenNothingRecords(t *testing.T) {
	var calls atomic.Int32
	host := newHost(t, HostOptions{
		Principal: delta.JsonObjectOf("kind", "user"), OnError: noopOnError,
		PrincipalAttributes: func(data.Principal) telemetry.SpanAttributes {
			calls.Add(1)
			return telemetry.SpanAttributes{telemetry.Attr("chord.principal", "x")}
		},
	})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/traced", Facet: tracedFacet(nil, nil)}}, background))
	_, err := mustGet(t, host, tracedType, jsonObject()).Call("work", nil, background)
	must(t, err)
	if got := calls.Load(); got != 0 {
		t.Errorf("hook calls = %d, want 0", got)
	}
}

// › records a call span's principal: whom its reference acts for
func TestPrincipalAttributesRecordACallSpansPrincipalWhomItsReferenceActsFor(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	client := newHost(t, HostOptions{
		Principal: delta.JsonObjectOf("kind", "user", "id", "u1"), Telemetry: recorder, OnError: noopOnError,
		PrincipalAttributes: func(principal data.Principal) telemetry.SpanAttributes {
			return telemetry.SpanAttributes{telemetry.Attr("chord.principal", principalKind(principal)+":"+principal.Value("id").(string))}
		},
	})
	mustLoad(t)(client.Load(nil, background))
	peer := rawParent(t, client, link.PartLimits{})
	tracedUnder(t, recorder, "click", func(ctx context.Context) {
		got := make(chan *Reference, 1)
		go func() {
			notes, err := client.Get(ctx, Notes, jsonObject("session", "S1"), GetOptions{})
			must(t, err)
			got <- notes
		}()
		bindHold(t, peer, peer.Next("hold").(*link.HoldMessage).Hold)
		notes := <-got
		pending := startCall(t, notes, "add", jsonObject("text", "a"), ctx)
		call := peer.Next("call").(*link.CallMessage)
		resultPart, err := link.JsonPart(float64(1))
		must(t, err)
		peer.Send(&link.ResultMessage{Call: call.Call, Value: resultPart})
		if _, err := pending.wait(background); err != nil {
			t.Error(err)
		}
	})
	attributesWant(t, spanWhere(t, recorder, "chord.call", nil), map[string]any{"chord.principal": "user:u1"})
}

// facet.test.ts › serve options › starts no method span for a type served `untraced`
func TestServeOptionsStartNoMethodSpanForATypeServedUntraced(t *testing.T) {
	quiet := mustContract(contracts.Local("app.quiet", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("ping", contracts.NewMethod())}}))
	loud := mustContract(contracts.Local("app.loud", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("ping", contracts.NewMethod())}}))
	ping := func() contracts.Implementation {
		return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"ping": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
		}}}
	}
	recorder := telemetry.NewInMemoryTelemetryContext()
	host := newHost(t, HostOptions{Telemetry: recorder, OnError: noopOnError})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(quiet, func(*delta.JsonObject, Self) (contracts.Implementation, error) { return ping(), nil }, ServeOptions{Untraced: true}); err != nil {
			return err
		}
		return h.Serve(loud, func(*delta.JsonObject, Self) (contracts.Implementation, error) { return ping(), nil }, ServeOptions{})
	})}}, background))
	quietRef, loudRef := mustGet(t, host, quiet, jsonObject()), mustGet(t, host, loud, jsonObject())
	tracedUnder(t, recorder, "work", func(ctx context.Context) {
		for _, ref := range []*Reference{quietRef, loudRef} {
			_, err := ref.Call("ping", nil, ctx)
			must(t, err)
		}
	})
	var types []any
	for _, span := range spansNamed(recorder, "chord.method") {
		typ, _ := span.Attributes.Get("type")
		types = append(types, typ)
	}
	if !reflect.DeepEqual(types, []any{"app.loud"}) {
		t.Errorf("chord.method types = %v, want [app.loud]", types)
	}
}
