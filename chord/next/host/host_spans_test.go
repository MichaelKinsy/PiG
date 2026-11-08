package host

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

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
