package registries_test

import (
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports the registry-layer behavior of packages/chord/test/next/registries/record.test.ts that does not need a host:
// the same inputs and expected results, driven on a RegistryRecord with the contributions a facet or a host-lifetime
// object would make (`self.contribute`, `self.writable`, `self.derive`).
//
// Upstream's cases are written against the host (`loadedPlugins`, `host.registry`, `self.contribute`, `host.get`,
// `self.tx`, `self.state`, `storeRestartable`, `host.hold`): each is pending the host, documents, store and contracts
// lanes. Pending: "the table"/"is sorted ..." (host load order), "composes every served registry once at the end of
// load's setups ... before any object activates" (host.load), "composes a watched registry in the batch of the commit
// that changed a contribution" (documents `defineState`, `self.tx`), "refuses a contribution closing a cycle ... one
// through a derive is cut and reported" and "refuses a cycle declared within one factory" and "leaves no cycle edge
// from a contribution that never entered a table" (attempts, `host.hold`), "follows only Chord watchables" (host
// `contentOf`), "a followed contribution whose runtime ended its observer stays with its last value" (`self.follow`),
// "an object's contribution is in the table from live until it fails..." and "the last good composition (decision 5)"
// (`storeRestartable`), and "the table's memory" (`host.close`, `expectCollected` over a host). The tests below cover
// the record-level behavior of each of them that a record exposes.

var Names = registries.Registry[string, []string]{Kind: "registry", Name: "app.names", Version: 1}

func namesProvider() registries.RegistryProvider[string, []string] {
	return registries.RegistryProvider[string, []string]{Compose: listed}
}

func TestTheTable(t *testing.T) {
	t.Run("is sorted by plugin name, then type, a facet first: never load order", func(t *testing.T) {
		e := newEnv(t)
		contribute(e, Names, "@z/late", "", plain("z"))
		contribute(e, Names, "@a/early", "app.setter", plain("store"))
		contribute(e, Names, "@a/early", "", plain("a"))
		record := serve(e, Names, namesProvider())
		want := []string{"@a/early:=a", "@a/early:app.setter=store", "@z/late:=z"}
		if got := composed(t, record); !reflect.DeepEqual(got, want) {
			t.Fatalf("composition = %v, want %v", got, want)
		}
	})

	t.Run("adapts an older contribution once per value; a newer one, or an older one without an adapter, is left out and reported once", func(t *testing.T) {
		type oldValue struct{ old string }
		V3 := mustDefine[string, []string](t, "app.versions", 3)
		V2 := mustDefine[oldValue, any](t, "app.versions", 2)
		V1 := mustDefine[string, any](t, "app.versions", 1)
		V4 := mustDefine[string, any](t, "app.versions", 4)
		e := newEnv(t)
		var adapted []string
		record := serve(e, V3, registries.RegistryProvider[string, []string]{
			Compose: listed,
			Accepts: registries.RegistryAccepts[string, []string]{
				2: {Contribution: func(old any) (string, error) {
					adapted = append(adapted, old.(oldValue).old)
					return "adapted " + old.(oldValue).old, nil
				}},
			},
		})
		contribute(e, V1, "@app/one", "", plain("one"))
		two := newCell(oldValue{old: "first"})
		contribute(e, V2, "@app/two", "app.setter", two.native())
		contribute(e, V3, "@app/three", "", plain("three"))
		contribute(e, V4, "@app/four", "", plain("four"))
		want := []string{"@app/three:=three", "@app/two:app.setter=adapted first"}
		if got := composed(t, record); !reflect.DeepEqual(got, want) {
			t.Fatalf("composition = %v, want %v", got, want)
		}
		wantErrors := []registries.RegistryError{
			{Kind: "contribution_refused", Registry: "app.versions", Plugin: "@app/four", Version: 4},
			{Kind: "contribution_refused", Registry: "app.versions", Plugin: "@app/one", Version: 1},
		}
		if !reflect.DeepEqual(e.errors, wantErrors) {
			t.Fatalf("errors = %#v, want %#v", e.errors, wantErrors)
		}
		two.set(e, oldValue{old: strconv.Itoa(5)}, nil)
		want = []string{"@app/three:=three", "@app/two:app.setter=adapted 5"}
		if got := composed(t, record); !reflect.DeepEqual(got, want) {
			t.Fatalf("composition = %v, want %v", got, want)
		}
		// Each value adapted once, though the table was built again; no refusal reported twice.
		if !reflect.DeepEqual(adapted, []string{"first", "5"}) {
			t.Fatalf("adapted = %v", adapted)
		}
		if got := kinds(e.errors); !reflect.DeepEqual(got, []string{"contribution_refused", "contribution_refused"}) {
			t.Fatalf("kinds = %v", got)
		}
	})

	t.Run("takes a contribution declared before its provider's setup into the first composition", func(t *testing.T) {
		e := newEnv(t)
		contribute(e, Names, "@app/a", "", plain("first"))
		var seen []string
		record := serve(e, Names, registries.RegistryProvider[string, []string]{
			Compose: func(table registries.RegistryTable[string], context registries.ComposeContext) ([]string, error) {
				seen, _ = listed(table, context)
				return seen, nil
			},
		})
		e.change(func() {
			if err := record.ComposeServed(); err != nil {
				t.Fatal(err)
			}
		})
		if want := []string{"@app/a:=first"}; !reflect.DeepEqual(seen, want) {
			t.Fatalf("composed = %v, want %v", seen, want)
		}
	})
}

func TestProviderServing(t *testing.T) {
	t.Run("one per record: a second serve is refused", func(t *testing.T) {
		e := newEnv(t)
		record := serve(e, Names, namesProvider())
		err := record.Serve(1, namesProvider().Erase())
		if err == nil || err.Error() != "registry app.names is served twice" {
			t.Fatalf("second serve error = %v", err)
		}
	})

	t.Run("a provider without compose is refused (plugin code is not type-checked)", func(t *testing.T) {
		e := newEnv(t)
		err := e.record("app.names").Serve(1, registries.RegistryProvider[any, any]{})
		if err == nil || err.Error() != "registry app.names: compose is a function" {
			t.Fatalf("serve error = %v", err)
		}
	})

	t.Run("a registry nobody serves has no value: unavailable/undeclared; its contributions wait in the table", func(t *testing.T) {
		e := newEnv(t)
		contribute(e, Names, "@app/a", "", plain("waiting"))
		record := e.record("app.names")
		start := startOf(t, record)
		chordErr, ok := data.IsChordError(start.Error)
		if start.Kind != state.StartFailed || start.HasLast || !ok || chordErr.Code != data.CodeUnavailable || chordErr.Reason != data.ReasonUndeclared {
			t.Fatalf("start = %#v", start)
		}
		if chordErr.Message != "registry app.names: no plugin serves it here" {
			t.Fatalf("message = %q", chordErr.Message)
		}
		if _, ok := record.ProvidedVersion(); ok {
			t.Fatal("a provider is served")
		}
		serve(e, Names, namesProvider())
		if got, want := composed(t, record), []string{"@app/a:=waiting"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("composition = %v, want %v", got, want)
		}
	})
}

func TestCompose(t *testing.T) {
	composes := func(e *env) float64 { return e.meter.Counts()["chord.registry.composes"] }

	t.Run("composes every served registry once at the end of load's setups, an empty table too", func(t *testing.T) {
		e := newEnv(t)
		Other := mustDefine[string, int](t, "app.other", 1)
		a := serve(e, Names, namesProvider())
		b := serve(e, Other, registries.RegistryProvider[string, int]{
			Compose: func(table registries.RegistryTable[string], _ registries.ComposeContext) (int, error) {
				return len(table), nil
			},
		})
		contribute(e, Names, "@app/provider", "", plain("a"))
		e.change(func() {
			for _, record := range []*registries.RegistryRecord{a, b} {
				if err := record.ComposeServed(); err != nil {
					t.Fatal(err)
				}
			}
		})
		if got := composed(t, b); got != 0 {
			t.Fatalf("other = %v, want 0", got)
		}
		if got, want := composed(t, a), []string{"@app/provider:=a"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("names = %v, want %v", got, want)
		}
		if got := composes(e); got != 2 {
			t.Fatalf("composes = %v, want 2", got)
		}
	})

	t.Run("is lazy: a change marks the table, a read composes it once; no compose for an unchanged table", func(t *testing.T) {
		Flags := mustDefine[bool, int](t, "app.flags", 1)
		e := newEnv(t)
		record := serve(e, Flags, registries.RegistryProvider[bool, int]{
			Compose: func(table registries.RegistryTable[bool], _ registries.ComposeContext) (int, error) {
				n := 0
				for _, entry := range table {
					if entry.Value {
						n++
					}
				}
				return n, nil
			},
		})
		flag := newCell(true)
		contribute(e, Flags, "@app/flags", "app.flagger", flag.json())
		if got := composed(t, record); got != 1 {
			t.Fatalf("flags = %v, want 1", got)
		}
		base := composes(e)
		if got := composed(t, record); got != 1 || composes(e) != base {
			t.Fatalf("a read composed again: %v, %v/%v", got, composes(e), base)
		}
		// The derive changes, its value does not: the table stays, nothing composes.
		flag.set(e, true, nil)
		flag.set(e, true, nil)
		if got := composed(t, record); got != 1 || composes(e) != base {
			t.Fatalf("an unchanged table composed: %v, %v/%v", got, composes(e), base)
		}
		// Nobody reads it: nothing composes until a read.
		flag.set(e, false, nil)
		flag.set(e, false, nil)
		if composes(e) != base {
			t.Fatalf("composed without a read: %v/%v", composes(e), base)
		}
		if got := composed(t, record); got != 0 {
			t.Fatalf("flags = %v, want 0", got)
		}
		if composes(e) != base+1 {
			t.Fatalf("composes = %v, want %v", composes(e), base+1)
		}
	})

	t.Run("reports a conflict once per episode", func(t *testing.T) {
		Routes := mustDefine[[]registries.RecordEntry[string], *registries.KeyedItems[string]](t, "app.routes", 1)
		e := newEnv(t)
		record := serve(e, Routes, registries.RegistryProvider[[]registries.RecordEntry[string], *registries.KeyedItems[string]]{
			Compose: func(table registries.RegistryTable[[]registries.RecordEntry[string]], context registries.ComposeContext) (*registries.KeyedItems[string], error) {
				return registries.KeyedEntries(table, func(c []registries.RecordEntry[string]) []registries.RecordEntry[string] { return c }, context.Report), nil
			},
		})
		contribute(e, Routes, "@app/a", "", plain([]registries.RecordEntry[string]{{Key: "/x", Value: "a"}}))
		b := newCell([]registries.RecordEntry[string]{{Key: "/x", Value: "b"}})
		contribute(e, Routes, "@app/b", "app.setter", b.json())
		routes := composed(t, record).(*registries.KeyedItems[string])
		if item, _ := routes.Get("/x"); item != (registries.TableItem[string]{Value: "a", Plugin: "@app/a"}) {
			t.Fatalf("/x = %#v", item)
		}
		for n := 1; n <= 2; n++ {
			b.set(e, []registries.RecordEntry[string]{{Key: "/x", Value: "b"}, {Key: "/" + strconv.Itoa(n), Value: "b"}}, nil)
			composed(t, record)
		}
		want := []registries.RegistryError{{
			Kind: "registry_conflict", Registry: "app.routes", Message: `"/x" is contributed twice`, Plugins: []string{"@app/a", "@app/b"},
		}}
		if !reflect.DeepEqual(e.errors, want) {
			t.Fatalf("errors = %#v, want %#v", e.errors, want)
		}
	})

	t.Run("a throwing compose: reported once, a failed start, the last good composition kept; the next good one heals", func(t *testing.T) {
		Count := mustDefine[int, int](t, "app.count", 1)
		e := newEnv(t)
		record := serve(e, Count, registries.RegistryProvider[int, int]{
			Compose: func(table registries.RegistryTable[int], _ registries.ComposeContext) (int, error) {
				sum := 0
				for _, entry := range table {
					sum += entry.Value
				}
				if sum < 0 {
					return 0, errors.New("negative: " + strconv.Itoa(sum))
				}
				return sum, nil
			},
		})
		n := newCell(1)
		contribute(e, Count, "@app/count", "app.setter", n.native())
		if got := composed(t, record); got != 1 {
			t.Fatalf("count = %v", got)
		}
		n.set(e, -1, nil)
		start := startOf(t, record)
		if start.Kind != state.StartFailed || !start.HasLast || start.Last != 1 || start.Error.Error() != "negative: -1" {
			t.Fatalf("start = %#v", start)
		}
		n.set(e, -2, nil)
		if start := startOf(t, record); start.Kind != state.StartFailed || start.Last != 1 {
			t.Fatalf("start = %#v", start)
		}
		if len(e.errors) != 1 || e.errors[0].Kind != "registry_failed" || e.errors[0].Registry != "app.count" || e.errors[0].Error.Error() != "negative: -1" {
			t.Fatalf("errors = %#v", e.errors)
		}
		n.set(e, 1, nil)
		if got := composed(t, record); got != 1 {
			t.Fatalf("healed = %v", got)
		}
		n.set(e, 3, nil)
		if got := composed(t, record); got != 3 {
			t.Fatalf("count = %v", got)
		}
	})

	t.Run("a compose that panics is a failure episode too", func(t *testing.T) {
		Count := mustDefine[int, int](t, "app.count", 1)
		e := newEnv(t)
		record := serve(e, Count, registries.RegistryProvider[int, int]{
			Compose: func(registries.RegistryTable[int], registries.ComposeContext) (int, error) { panic("bug") },
		})
		start := startOf(t, record)
		if start.Kind != state.StartFailed || start.HasLast || start.Error.Error() != "bug" {
			t.Fatalf("start = %#v", start)
		}
		if got := kinds(e.errors); !reflect.DeepEqual(got, []string{"registry_failed"}) {
			t.Fatalf("kinds = %v", got)
		}
	})

	t.Run("before any good composition, the start fails with the error and no last value", func(t *testing.T) {
		Broken := mustDefine[int, int](t, "app.broken", 1)
		e := newEnv(t)
		record := serve(e, Broken, registries.RegistryProvider[int, int]{
			Compose: func(registries.RegistryTable[int], registries.ComposeContext) (int, error) {
				return 0, errors.New("bug")
			},
		})
		start := startOf(t, record)
		if start.Kind != state.StartFailed || start.HasLast || start.Error.Error() != "bug" {
			t.Fatalf("start = %#v", start)
		}
		if got := kinds(e.errors); !reflect.DeepEqual(got, []string{"registry_failed"}) {
			t.Fatalf("kinds = %v", got)
		}
		// The value reads throw the error too (a Go getter panics with it).
		defer func() {
			if recovered := recover(); recovered == nil || recovered.(error).Error() != "bug" {
				t.Fatalf("Value panicked with %v", recovered)
			}
		}()
		record.Value(1).Value()
	})
}

func TestContributions(t *testing.T) {
	t.Run("a watchable contribution that fails is left out and reported once, and is back once it heals", func(t *testing.T) {
		e := newEnv(t)
		record := serve(e, Names, namesProvider())
		contribute(e, Names, "@app/names", "", plain("facet"))
		derive := newCell("n1")
		contribute(e, Names, "@app/names", "app.failing", derive.json())
		if got, want := composed(t, record), []string{"@app/names:=facet", "@app/names:app.failing=n1"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("composition = %v", got)
		}
		derive.set(e, nil, errors.New("negative"))
		if got, want := composed(t, record), []string{"@app/names:=facet"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("composition = %v", got)
		}
		derive.set(e, nil, errors.New("negative"))
		if got, want := composed(t, record), []string{"@app/names:=facet"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("composition = %v", got)
		}
		derive.set(e, "n2", nil)
		if got, want := composed(t, record), []string{"@app/names:=facet", "@app/names:app.failing=n2"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("composition = %v", got)
		}
		if len(e.errors) != 1 {
			t.Fatalf("errors = %#v", e.errors)
		}
		got := e.errors[0]
		if got.Kind != "contribution_failed" || got.Registry != "app.names" || got.Plugin != "@app/names" || got.Type != "app.failing" || got.Error.Error() != "negative" {
			t.Fatalf("error = %#v", got)
		}
	})

	t.Run("a contribution adapter that throws leaves that contribution out (`contribution_failed`)", func(t *testing.T) {
		V2 := mustDefine[string, []string](t, "app.adapted", 2)
		V1 := mustDefine[string, any](t, "app.adapted", 1)
		e := newEnv(t)
		record := serve(e, V2, registries.RegistryProvider[string, []string]{
			Compose: listed,
			Accepts: registries.RegistryAccepts[string, []string]{
				1: {Contribution: func(any) (string, error) { return "", errors.New("cannot adapt") }},
			},
		})
		contribute(e, V2, "@app/provider", "", plain("current"))
		contribute(e, V1, "@app/old", "", plain("old"))
		if got, want := composed(t, record), []string{"@app/provider:=current"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("composition = %v", got)
		}
		if len(e.errors) != 1 || e.errors[0].Kind != "contribution_failed" || e.errors[0].Registry != "app.adapted" ||
			e.errors[0].Plugin != "@app/old" || e.errors[0].Type != "" || e.errors[0].Error.Error() != "cannot adapt" {
			t.Fatalf("errors = %#v", e.errors)
		}
	})

	t.Run("a contribution adapter that panics leaves that contribution out too", func(t *testing.T) {
		V2 := mustDefine[string, []string](t, "app.adapted", 2)
		V1 := mustDefine[string, any](t, "app.adapted", 1)
		e := newEnv(t)
		record := serve(e, V2, registries.RegistryProvider[string, []string]{
			Compose: listed,
			Accepts: registries.RegistryAccepts[string, []string]{1: {Contribution: func(any) (string, error) { panic("cannot adapt") }}},
		})
		contribute(e, V1, "@app/old", "", plain("old"))
		if got := composed(t, record); !reflect.DeepEqual(got, []string{}) {
			t.Fatalf("composition = %#v", got)
		}
		if got := kinds(e.errors); !reflect.DeepEqual(got, []string{"contribution_failed"}) {
			t.Fatalf("kinds = %v", got)
		}
	})

	t.Run("refuses a contribution closing a cycle with the registries' tables, and one through earlier declarations", func(t *testing.T) {
		A := mustDefine[any, any](t, "app.a", 1)
		B := mustDefine[any, any](t, "app.b", 1)
		e := newEnv(t)
		a := serve(e, A, registries.RegistryProvider[any, any]{Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
			return len(table), nil
		}})
		b := serve(e, B, registries.RegistryProvider[any, any]{Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
			return len(table), nil
		}})
		if err := a.AssertNoCycle(b.Value(1), nil); err != nil {
			t.Fatalf("A reading B: %v", err)
		}
		// Declared in one factory: B's value into A, then A's into B.
		pending := []registries.RegistryEdge{{From: a, To: b}}
		err := b.AssertNoCycle(a.Value(1), pending)
		if chordErr, ok := data.IsChordError(err); !ok || chordErr.Code != data.CodeInvalid || chordErr.Reason != data.ReasonCycle {
			t.Fatalf("pending edge error = %v", err)
		}
		// Live: A's table holds B's value.
		reads := contribute(e, A, "@app/direct", "app.direct", registries.ContributionContent{Kind: registries.ContributionNative, Native: b.Value(1)})
		err = b.AssertNoCycle(a.Value(1), nil)
		if chordErr, ok := data.IsChordError(err); !ok || chordErr.Reason != data.ReasonCycle || chordErr.Message != "registries app.b and app.a read each other" {
			t.Fatalf("table edge error = %v", err)
		}
		// An edge lives as long as its contribution is in the table.
		e.change(func() { a.Remove(reads) })
		if err := b.AssertNoCycle(a.Value(1), nil); err != nil {
			t.Fatalf("after remove: %v", err)
		}
	})

	t.Run("a registry read while it composes is a cycle: the contribution that closes it is cut and reported (invalid/cycle)", func(t *testing.T) {
		A := mustDefine[any, any](t, "app.a", 1)
		B := mustDefine[any, any](t, "app.b", 1)
		e := newEnv(t)
		count := registries.RegistryProvider[any, any]{
			Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
				return len(table), nil
			},
		}
		a := serve(e, A, count)
		b := serve(e, B, count)
		// A's table reads B's value, and B's table reads A's: A composing reads B, which reads A while A composes.
		contribute(e, A, "@app/cycles", "app.derived", registries.ContributionContent{Kind: registries.ContributionNative, Native: b.Value(1)})
		contribute(e, B, "@app/back", "app.back", registries.ContributionContent{Kind: registries.ContributionNative, Native: a.Value(1)})
		if got := composed(t, a); got != 1 {
			t.Fatalf("A = %v, want 1", got)
		}
		if len(e.errors) != 1 {
			t.Fatalf("errors = %#v", e.errors)
		}
		cut := e.errors[0]
		chordErr, ok := data.IsChordError(cut.Error)
		if cut.Kind != "contribution_failed" || cut.Registry != "app.b" || cut.Plugin != "@app/back" || !ok || chordErr.Code != data.CodeInvalid || chordErr.Reason != data.ReasonCycle {
			t.Fatalf("error = %#v", cut)
		}
	})

	t.Run("follows only Chord watchables: anything else is a plain value, whatever methods it has (Sol)", func(t *testing.T) {
		type native struct {
			value string
			watch func() string
		}
		Natives := mustDefine[native, []string](t, "app.natives", 1)
		e := newEnv(t)
		record := serve(e, Natives, registries.RegistryProvider[native, []string]{
			Compose: func(table registries.RegistryTable[native], _ registries.ComposeContext) ([]string, error) {
				out := []string{}
				for _, entry := range table {
					out = append(out, entry.Value.watch())
				}
				return out, nil
			},
		})
		contribute(e, Natives, "@app/x", "", plain(native{value: "a handle", watch: func() string { return "a native operation" }}))
		if got, want := composed(t, record), []string{"a native operation"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("composition = %v", got)
		}
	})

	t.Run("keeps an `undefined` contribution in the table (Sol, Astra)", func(t *testing.T) {
		Nothing := mustDefine[any, int](t, "app.undefined", 1)
		e := newEnv(t)
		record := serve(e, Nothing, registries.RegistryProvider[any, int]{
			Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (int, error) {
				return len(table), nil
			},
		})
		contribute(e, Nothing, "@app/a", "", plain(nil))
		if got := composed(t, record); got != 1 {
			t.Fatalf("composition = %v, want 1", got)
		}
		// Contributing it again as the same `undefined` leaves the entry itself unchanged: nothing composes.
		before := e.meter.Counts()["chord.registry.composes"]
		e.change(func() { record.ContributionChanged() })
		composed(t, record)
		if after := e.meter.Counts()["chord.registry.composes"]; after != before {
			t.Fatalf("an unchanged undefined contribution composed: %v/%v", after, before)
		}
	})

	t.Run("leaves out a registry's value contributed to another while that registry's compose throws (Sol, Astra)", func(t *testing.T) {
		Source := mustDefine[int, int](t, "app.source", 1)
		Target := mustDefine[int, []int](t, "app.target", 1)
		e := newEnv(t)
		source := serve(e, Source, registries.RegistryProvider[int, int]{
			Compose: func(table registries.RegistryTable[int], _ registries.ComposeContext) (int, error) {
				n := 0
				for _, entry := range table {
					n += entry.Value
				}
				if n < 0 {
					return 0, errors.New("negative")
				}
				return n, nil
			},
		})
		target := serve(e, Target, registries.RegistryProvider[int, []int]{
			Compose: func(table registries.RegistryTable[int], _ registries.ComposeContext) ([]int, error) {
				out := []int{}
				for _, entry := range table {
					out = append(out, entry.Value)
				}
				return out, nil
			},
		})
		n := newCell(1)
		contribute(e, Source, "@app/a", "app.setter", n.native())
		contribute(e, Target, "@app/a", "app.bridge", registries.ContributionContent{Kind: registries.ContributionNative, Native: source.Value(1)})
		if got := composed(t, target); !reflect.DeepEqual(got, []int{1}) {
			t.Fatalf("target = %v", got)
		}
		n.set(e, -1, nil)
		// The source's readers keep its last good value; as a contribution it is failed: left out, reported.
		if got := source.Value(1).Value(); got != 1 {
			t.Fatalf("source value = %v, want 1", got)
		}
		if got := composed(t, target); !reflect.DeepEqual(got, []int{}) {
			t.Fatalf("target = %v", got)
		}
		var failed *registries.RegistryError
		for i := range e.errors {
			if e.errors[i].Kind == "contribution_failed" {
				failed = &e.errors[i]
				break
			}
		}
		if failed == nil || failed.Registry != "app.target" || failed.Plugin != "@app/a" || failed.Type != "app.bridge" || failed.Error.Error() != "negative" {
			t.Fatalf("errors = %#v", e.errors)
		}
		n.set(e, 3, nil)
		if got := composed(t, target); !reflect.DeepEqual(got, []int{3}) {
			t.Fatalf("target = %v", got)
		}
	})
}

func TestTheLastGoodComposition(t *testing.T) {
	t.Run("is dropped when a contribution it was built from leaves: the start fails until `compose` succeeds (decision 5)", func(t *testing.T) {
		Count := mustDefine[int, int](t, "app.count", 1)
		e := newEnv(t)
		record := serve(e, Count, registries.RegistryProvider[int, int]{
			Compose: func(table registries.RegistryTable[int], _ registries.ComposeContext) (int, error) {
				if len(table) == 0 {
					return 0, errors.New("no contributor")
				}
				return table[0].Value, nil
			},
		})
		first := contribute(e, Count, "@test/app", "app.store", plain(1))
		if got := composed(t, record); got != 1 {
			t.Fatalf("count = %v", got)
		}
		// Its contributor failed: the last good value was built from its contribution (a native may have closed with it).
		e.change(func() { record.Remove(first) })
		start := startOf(t, record)
		if start.Kind != state.StartFailed || start.HasLast || start.Error.Error() != "no contributor" {
			t.Fatalf("start = %#v", start)
		}
		contribute(e, Count, "@test/app", "app.store", plain(2))
		if got := composed(t, record); got != 2 {
			t.Fatalf("count = %v", got)
		}
	})

	t.Run("is kept while every contribution it was built from is still in the table", func(t *testing.T) {
		Count := mustDefine[int, int](t, "app.count", 1)
		e := newEnv(t)
		record := serve(e, Count, registries.RegistryProvider[int, int]{
			Compose: func(table registries.RegistryTable[int], _ registries.ComposeContext) (int, error) {
				if table[0].Value < 0 {
					return 0, errors.New("negative")
				}
				return table[0].Value, nil
			},
		})
		n := newCell(4)
		contribute(e, Count, "@test/app", "", n.native())
		composed(t, record)
		n.set(e, -1, nil)
		start := startOf(t, record)
		if start.Kind != state.StartFailed || !start.HasLast || start.Last != 4 {
			t.Fatalf("start = %#v", start)
		}
	})
}

func TestTheTablesMemory(t *testing.T) {
	t.Run("close: the contributions stop following their sources, the values end, and a kept value reads its last composition", func(t *testing.T) {
		e := newEnv(t)
		record := serve(e, Names, namesProvider())
		label := newCell("followed")
		contribute(e, Names, "@app/kept", "app.kept", label.json())
		value := record.Value(1)
		if got, want := composed(t, record), []string{"@app/kept:app.kept=followed"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("composition = %v", got)
		}
		if label.dependents.Len() != 1 {
			t.Fatalf("followers = %d, want 1", label.dependents.Len())
		}
		record.Close()
		if label.dependents.Len() != 0 {
			t.Fatalf("followers after close = %d", label.dependents.Len())
		}
		if got, want := value.Value(), []string{"@app/kept:app.kept=followed"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("kept value = %v", got)
		}
		if record.Value(1) == value {
			t.Fatal("a closed record handed out its old value")
		}
	})
}
