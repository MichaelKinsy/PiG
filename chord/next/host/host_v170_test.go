package host

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Tests for v1.7.0 host additions:
//   - ERNDL-1191: self.FollowOver(over, attach) over changing registry value / writable, follow_failed, backoff retries
//   - ERNDL-1190: transaction cancel attribution (own object end does NOT report transaction_cancelled; caller cancel DOES)
//   - JSON-only named query args (reject NaN/Inf with invalid/args, canonical JSON sharing)
//   - Lazy ReferenceQuery creation on references

func TestFollowOverChangingSource(t *testing.T) {
	reg, err := registries.DefineRegistry[any, any]("app.follow.reg", registries.DefineRegistryOptions{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	viewer := exLocal("app.follow.viewer", contracts.Params{}, contracts.Mem("shown", exStr()))

	t.Run("re-attaches when registry value changes and detaches previous", func(t *testing.T) {
		var attachedVals []string
		var detachedVals []string
		var reported []HostError
		h, err := CreateHost(HostOptions{OnError: func(e HostError) { reported = append(reported, e) }})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Close(background) })

		prov := func(val string) HostFacet {
			return HostFacet{
				Name: "@test/prov",
				Facet: DefineFacet(func(fh FacetHost, _ FacetSelf) error {
					return fh.Serve(reg, registries.RegistryProvider[any, any]{
						Compose: func(registries.RegistryTable[any], registries.ComposeContext) (any, error) { return val, nil },
					}, ServeOptions{})
				}),
			}
		}
		vPlugin := HostFacet{
			Name: "@test/viewer",
			Facet: DefineFacet(func(fh FacetHost, _ FacetSelf) error {
				return fh.Serve(viewer, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
					r := self.Registry(reg)
					shown := self.FollowOver(r, func(val any, _ FollowObserver[any], _ context.Context) (*FollowAttachment[delta.JsonValue], error) {
						s := val.(string)
						attachedVals = append(attachedVals, s)
						return &FollowAttachment[delta.JsonValue]{
							Value:  "v:" + s,
							Detach: func() { detachedVals = append(detachedVals, s) },
						}, nil
					})
					return contracts.MembersImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"shown": shown}}, nil
				}, ServeOptions{})
			}),
		}

		if _, err := h.Load([]HostFacet{prov("one"), vPlugin}, background); err != nil {
			t.Fatal(err)
		}
		ref := mustGet(t, h, viewer, jsonObject())
		defer ref.Release()
		if got, err := ref.Member("shown").Value(); err != nil || got != "v:one" {
			t.Fatalf("initial shown = %v, %v; want v:one", got, err)
		}

		if _, err := h.Reload([]HostFacet{prov("two"), vPlugin}, background, ReloadOptions{}); err != nil {
			t.Fatal(err)
		}
		deliveredOn(h)
		if got, err := ref.Member("shown").Value(); err != nil || got != "v:two" {
			t.Fatalf("shown after reload = %v, %v; want v:two", got, err)
		}
		if len(attachedVals) != 2 || attachedVals[0] != "one" || attachedVals[1] != "two" {
			t.Fatalf("attachedVals = %v, want [one two]", attachedVals)
		}
		if len(detachedVals) != 1 || detachedVals[0] != "one" {
			t.Fatalf("detachedVals = %v, want [one]", detachedVals)
		}
		if len(reported) != 0 {
			t.Fatalf("reported = %+v, want none", reported)
		}
	})

	t.Run("failure while live reports follow_failed once and recovers on next value", func(t *testing.T) {
		var reported []HostError
		h, err := CreateHost(HostOptions{OnError: func(e HostError) { reported = append(reported, e) }})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Close(background) })

		prov := func(val string) HostFacet {
			return HostFacet{
				Name: "@test/prov",
				Facet: DefineFacet(func(fh FacetHost, _ FacetSelf) error {
					return fh.Serve(reg, registries.RegistryProvider[any, any]{
						Compose: func(registries.RegistryTable[any], registries.ComposeContext) (any, error) { return val, nil },
					}, ServeOptions{})
				}),
			}
		}
		vPlugin := HostFacet{
			Name: "@test/viewer",
			Facet: DefineFacet(func(fh FacetHost, _ FacetSelf) error {
				return fh.Serve(viewer, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
					r := self.Registry(reg)
					shown := self.FollowOver(r, func(val any, _ FollowObserver[any], _ context.Context) (*FollowAttachment[delta.JsonValue], error) {
						s := val.(string)
						if s == "bad" {
							return nil, errors.New("attach boom")
						}
						return &FollowAttachment[delta.JsonValue]{
							Value:  "ok:" + s,
							Detach: func() {},
						}, nil
					})
					return contracts.MembersImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"shown": shown}}, nil
				}, ServeOptions{})
			}),
		}

		if _, err := h.Load([]HostFacet{prov("good"), vPlugin}, background); err != nil {
			t.Fatal(err)
		}
		ref := mustGet(t, h, viewer, jsonObject())
		defer ref.Release()
		if got, err := ref.Member("shown").Value(); err != nil || got != "ok:good" {
			t.Fatalf("initial = %v, %v; want ok:good", got, err)
		}

		// Switch to "bad": attach fails while live -> follow_failed reported, object stays live, member fails.
		if _, err := h.Reload([]HostFacet{prov("bad"), vPlugin}, background, ReloadOptions{}); err != nil {
			t.Fatal(err)
		}
		deliveredOn(h)
		if _, err := ref.Member("shown").Value(); err == nil {
			t.Fatal("expected member error after attach failure")
		}
		foundFollowFailed := false
		for _, e := range reported {
			if e.Kind == ErrFollowFailed {
				foundFollowFailed = true
			}
		}
		if !foundFollowFailed {
			t.Fatalf("reported = %+v, want follow_failed", reported)
		}
		if st := ref.State().Value(); st.Kind != RefStateLive {
			t.Fatalf("ref state = %v, want live", st.Kind)
		}

		// Switch to "healed": recovers.
		if _, err := h.Reload([]HostFacet{prov("healed"), vPlugin}, background, ReloadOptions{}); err != nil {
			t.Fatal(err)
		}
		deliveredOn(h)
		if got, err := ref.Member("shown").Value(); err != nil || got != "ok:healed" {
			t.Fatalf("recovered = %v, %v; want ok:healed", got, err)
		}
	})
}

func TestTransactionCancelledAttributionERNDL1190(t *testing.T) {
	listDef, err := documents.DefineState("list", documents.StateOptions{
		Schema:  exObj(exProp("items", contracts.Arr(exStr()))),
		Version: 1,
		Initial: func() *documents.JsonObject { return delta.JsonObjectOf("items", []delta.JsonValue{}) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerContract := exLocal("app.txattr.owner", contracts.Params{exProp("id", exInt())},
		contracts.Mem("touch", contracts.NewMethod(exObj())))

	t.Run("own object end via release does not report transaction_cancelled", func(t *testing.T) {
		var reported []HostError
		h, err := CreateHost(HostOptions{OnError: func(e HostError) { reported = append(reported, e) }})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Close(background) })
		entered := make(chan struct{})
		done := make(chan struct{})

		if _, err := h.Load([]HostFacet{{
			Name: "@test/owner",
			Facet: DefineFacet(func(fh FacetHost, _ FacetSelf) error {
				return fh.Serve(ownerContract, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
					list := self.State(listDef)
					self.OnLive(func(c Ctx) *Handled {
						return Awaiting(h.(*chordHost).loop(), func() error {
							defer close(done)
							_, _ = self.Tx(func(tx documents.Tx) (any, error) {
								close(entered)
								<-c.Done()
								_, _ = tx.Draft(list)
								return nil, context.Cause(c)
							}, c)
							return nil
						})
					})
					return contracts.MembersImplementation{
							Methods: map[string]contracts.MethodFunction{
								"touch": func(_ delta.JsonValue, _ context.Context) (delta.JsonValue, error) { return nil, nil },
							},
						}, nil
				}, ServeOptions{})
			}),
		}}, background); err != nil {
			t.Fatal(err)
		}

		ref := mustGet(t, h, ownerContract, jsonObject("id", int64(1)))
		<-entered
		ref.Release()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for onLive tx to end")
		}
		deliveredOn(h)
		for _, e := range reported {
			if e.Kind == ErrTransactionCancelled {
				t.Fatalf("unexpected transaction_cancelled reported for own object end: %+v", e)
			}
		}
	})

	t.Run("caller cancel still reports transaction_cancelled", func(t *testing.T) {
		var reported []HostError
		h, err := CreateHost(HostOptions{OnError: func(e HostError) { reported = append(reported, e) }})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Close(background) })
		entered := make(chan struct{})
		done := make(chan struct{})

		if _, err := h.Load([]HostFacet{{
			Name: "@test/owner",
			Facet: DefineFacet(func(fh FacetHost, _ FacetSelf) error {
				return fh.Serve(ownerContract, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
					_ = self.State(listDef)
					return contracts.MembersImplementation{
							Methods: map[string]contracts.MethodFunction{
								"touch": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
									defer close(done)
									return self.Tx(func(tx documents.Tx) (any, error) {
										close(entered)
										chordctx.Suspend(ctx)
										<-ctx.Done()
										return nil, context.Cause(ctx)
									}, ctx)
								},
							},
						}, nil
				}, ServeOptions{})
			}),
		}}, background); err != nil {
			t.Fatal(err)
		}

		ref := mustGet(t, h, ownerContract, jsonObject("id", int64(2)))
		defer ref.Release()
		callCtx, cancel := chordctx.WithCancel(background)
		go func() {
			_, _ = ref.Call("touch", jsonObject(), callCtx)
		}()
		<-entered
		cancel(errors.New("caller aborted"))
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for caller-cancelled tx")
		}
		deliveredOn(h)
		found := false
		for _, e := range reported {
			if e.Kind == ErrTransactionCancelled {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected transaction_cancelled for caller cancel, got %+v", reported)
		}
	})
}

func TestNamedQueryJSONOnlyArgsAndLazyReferenceQueries(t *testing.T) {
	items := mustTableDef("items", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Int())}, Key: []string{"id"}})
	providerContract := exLocal("app.nq170.prov", contracts.Params{},
		contracts.Mem("q", contracts.Queries(contracts.Unknown(), []contracts.Property{exProp("all", contracts.Unknown())})))

	h, err := CreateHost(HostOptions{Store: memoryStore(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(background) })
	if _, err := h.Load([]HostFacet{{
		Name: "@test/prov",
		Facet: DefineFacet(func(fh FacetHost, _ FacetSelf) error {
			return fh.Serve(providerContract, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
				ss := self.(StoreSelf)
				tbl := ss.Table(items)
				qs := ss.Queries(tbl, QueriesOptions{}, map[string]QueryBuilder{
					"all": {Build: func(_ delta.JsonValue) *store.Query { return store.NewQuery(items) }},
				})
				return contracts.MembersImplementation{Queries: map[string]nqNamed{"q": qs}}, nil
			}, ServeOptions{})
		}),
	}}, background); err != nil {
		t.Fatal(err)
	}

	ref := mustGet(t, h, providerContract, jsonObject())
	defer ref.Release()

	// Lazy: before Queries("q") is called, b.queries is nil.
	if ref.binding.queries != nil {
		t.Fatal("expected binding.queries to be nil before first Queries() call")
	}
	qs := ref.Queries("q")
	if qs == nil || qs["all"] == nil {
		t.Fatal("expected lazy Queries(\"q\")[\"all\"] to materialize")
	}
	own, err := qs["all"].Provided()
	if err != nil {
		t.Fatal(err)
	}

	// Non-strict JSON (NaN) must be rejected with invalid/args.
	if _, err := own.Target(math.NaN(), background); err == nil {
		t.Fatal("expected NaN query args to be rejected")
	} else if ce, ok := data.IsChordError(err); !ok || ce.Code != data.CodeInvalid || ce.Reason != data.ReasonArgs {
		t.Fatalf("err = %v, want invalid/args", err)
	}

	// Valid JSON args produce a sharing target with canonical JSON (-0 -> 0, sorted keys).
	t1, err := own.Target(delta.JsonObjectOf("b", float64(1), "a", math.Copysign(0, -1)), background)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := own.Target(delta.JsonObjectOf("a", float64(0), "b", float64(1)), background)
	if err != nil {
		t.Fatal(err)
	}
	if t1.Sharing == nil || t2.Sharing == nil || t1.Sharing.Args != t2.Sharing.Args {
		t.Fatalf("canonical sharing args mismatch: %+v vs %+v", t1.Sharing, t2.Sharing)
	}
}
