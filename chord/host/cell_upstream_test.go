package host

import (
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Tests derived from Pi packages/chord/test/state.test.ts and packages/chord/src/cell.ts, driving Cell and Derived
// directly (the host and object layers that wire them to references are ported elsewhere).

type cellTestCtx struct{ Ctx }

func allowWrites(Ctx) error { return nil }

func collectReports(t *testing.T) (SourceOptions, *[]error) {
	t.Helper()
	reports := &[]error{}
	return SourceOptions{Report: func(err error) { *reports = append(*reports, err) }}, reports
}

func jsonText(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

var (
	itemSchema  = Schema{"type": "object", "properties": Schema{"id": Schema{"type": "number"}, "label": Schema{"type": "string"}}, "required": []any{"id", "label"}}
	itemsSchema = Schema{"type": "object", "properties": Schema{"items": Schema{"type": "array", "items": itemSchema}, "count": Schema{"type": "number"}}, "required": []any{"items", "count"}}
)

// state.test.ts:75-97 "publishes snapshots then exact ops; derivations follow": a draft change publishes only the
// mutated paths without the { v } box (cell.ts:107-126 unbox, :150-175 change), and a derivation recomputes with it.
func TestCellChangePublishesUnboxedOpsAndDerivationsFollow(t *testing.T) {
	options, reports := collectReports(t)
	list, err := NewCell[any]("list", itemsSchema, delta.JsonObjectOf("items", []any{}, "count", 0.0), allowWrites, options)
	if err != nil {
		t.Fatal(err)
	}
	labels, err := NewDerived("labels", Sources{list}, func(values ...any) any {
		items := values[0].(*delta.JsonObject).Value("items").([]any)
		out := make([]any, len(items))
		for index, item := range items {
			out[index] = item.(*delta.JsonObject).Value("label")
		}
		return out
	}, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := labels.Attach(nil); err != nil {
		t.Fatal(err)
	}
	var events []StateEvent[any]
	var labelEvents []string
	list.Events(func(event StateEvent[any]) { events = append(events, event) })
	labels.Events(func(event StateEvent[any]) { labelEvents = append(labelEvents, string(event.Type)) })
	err = list.Change(cellTestCtx{}, func(draft any) {
		object := draft.(*delta.Object)
		if _, err := object.Array("items").Push(delta.JsonObjectOf("id", 1.0, "label", "a")); err != nil {
			t.Fatal(err)
		}
		if err := object.Set("count", object.Get("count").(float64)+1); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != StateEventSnap || events[1].Type != StateEventOps {
		t.Fatalf("events = %+v", events)
	}
	ops := make([]string, len(events[1].Ops))
	for index, op := range events[1].Ops {
		ops[index] = jsonText(t, op)
	}
	sort.Strings(ops)
	if want := []string{`["p",["items"],0,0,[{"id":1,"label":"a"}]]`, `["s",["count"],1]`}; !slices.Equal(ops, want) {
		t.Fatalf("ops = %v, want %v", ops, want)
	}
	if got := jsonText(t, events[1].Value); got != `{"items":[{"id":1,"label":"a"}],"count":1}` {
		t.Fatalf("event value = %s", got)
	}
	if got := jsonText(t, labels.Value()); got != `["a"]` {
		t.Fatalf("labels = %s", got)
	}
	if !slices.Equal(labelEvents, []string{"snap", "ops"}) {
		t.Fatalf("label events = %v", labelEvents)
	}
	if err := list.Replace(cellTestCtx{}, delta.JsonObjectOf("items", []any{}, "count", 0.0), CellReplaceOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := jsonText(t, labels.Value()); got != `[]` {
		t.Fatalf("labels after replace = %s", got)
	}
	if len(*reports) != 0 {
		t.Fatalf("reports = %v", *reports)
	}
}

// state.test.ts:99-130 "supports primitive cells and value-returning changes": a primitive change publishes one r op.
func TestCellChangeValuePrimitivePublishesReplace(t *testing.T) {
	options, _ := collectReports(t)
	n, err := NewCell[any]("n", Schema{"type": "number"}, 0.0, allowWrites, options)
	if err != nil {
		t.Fatal(err)
	}
	var values []any
	var events []string
	n.Events(func(event StateEvent[any]) {
		events = append(events, string(event.Type)+jsonText(t, event.Ops)+jsonText(t, event.Value))
	})
	n.Subscribe(func(value any) { values = append(values, value) })
	for range 2 {
		if err := n.ChangeValue(cellTestCtx{}, func(draft any) any { return draft.(float64) + 1 }); err != nil {
			t.Fatal(err)
		}
	}
	if got := jsonText(t, values); got != `[0,1,2]` {
		t.Fatalf("values = %s", got)
	}
	if want := []string{"snapnull0", `ops[["r",1]]1`, `ops[["r",2]]2`}; !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

// state.test.ts:132-150 "rejects a returned value that does not fit the schema, even outside dev mode" (cell.ts:161).
func TestCellChangeValueValidatesReturnedValueOutsideDev(t *testing.T) {
	options, _ := collectReports(t)
	list, err := NewCell[any]("list", Schema{"type": "array", "items": Schema{"type": "number"}}, []any{}, allowWrites, options)
	if err != nil {
		t.Fatal(err)
	}
	err = list.ChangeValue(cellTestCtx{}, func(draft any) any {
		length, _ := draft.(*delta.Array).Push(1.0)
		return float64(length)
	})
	if !IsChordError(err, CodeInvalid) {
		t.Fatalf("err = %v, want invalid", err)
	}
	if got := jsonText(t, list.Value()); got != `[]` {
		t.Fatalf("value = %s, want the aborted change discarded", got)
	}
}

// state.test.ts:152-173 "validates initial values always and writes only in dev mode" (cell.ts:91-97, :138).
func TestCellValidatesInitialAlwaysAndWritesInDevMode(t *testing.T) {
	_, err := NewCell[any]("x", Schema{"type": "object", "properties": Schema{"n": Schema{"type": "number"}}}, delta.JsonObjectOf("n", "no"), allowWrites, SourceOptions{})
	var chordError *ChordError
	if !errors.As(err, &chordError) || chordError.Code != CodeInvalid || chordError.Reason != "state" {
		t.Fatalf("err = %v", err)
	}
	if want := "invalid/state: initial value of state x: "; len(err.Error()) < len(want) || err.Error()[:len(want)] != want {
		t.Fatalf("message = %q", err.Error())
	}
	bad := delta.JsonObjectOf("items", []any{}, "count", "x")
	lax, _ := NewCell[any]("list", itemsSchema, delta.JsonObjectOf("items", []any{}, "count", 0.0), allowWrites, SourceOptions{})
	if err := lax.Replace(cellTestCtx{}, bad, CellReplaceOptions{}); err != nil {
		t.Fatalf("lax replace = %v", err)
	}
	dev, _ := NewCell[any]("list", itemsSchema, delta.JsonObjectOf("items", []any{}, "count", 0.0), allowWrites, SourceOptions{Dev: true})
	err = dev.Replace(cellTestCtx{}, bad, CellReplaceOptions{})
	if !errors.As(err, &chordError) || chordError.Code != CodeInvalid || chordError.Reason != "state" {
		t.Fatalf("dev replace err = %v", err)
	}
	if got := jsonText(t, dev.Value()); got != `{"items":[],"count":0}` {
		t.Fatalf("dev value = %s", got)
	}
}

// cell.ts:153, :184 and state.test.ts:175-193: the write guard runs first and its refusal leaves the cell unchanged.
func TestCellWriteGuardRefusesWrites(t *testing.T) {
	refusal := Invalid("read_only", "state x is read-only here", nil)
	cell, _ := NewCell[any]("x", Schema{"type": "number"}, 1.0, func(Ctx) error { return refusal }, SourceOptions{})
	if err := cell.Replace(cellTestCtx{}, 2.0, CellReplaceOptions{}); err != refusal {
		t.Fatalf("replace err = %v", err)
	}
	if err := cell.ChangeValue(cellTestCtx{}, func(any) any { return 3.0 }); err != refusal {
		t.Fatalf("change err = %v", err)
	}
	if cell.Value() != 1.0 {
		t.Fatalf("value = %v", cell.Value())
	}
}

// state.test.ts:242-282 "re-entrant writes publish in adoption order; derivations never go stale" (publish.ts:1-8).
func TestCellReentrantWritesPublishInAdoptionOrder(t *testing.T) {
	options, _ := collectReports(t)
	n, _ := NewCell[any]("n", Schema{"type": "number"}, 0.0, allowWrites, options)
	d, _ := NewDerived("d", Sources{n}, func(values ...any) any { return values[0].(float64) * 2 }, options)
	if err := d.Attach(nil); err != nil {
		t.Fatal(err)
	}
	// Subscribed first: re-enters the write while revision 1 is being published.
	n.Subscribe(func(value any) {
		if value == 1.0 {
			if err := n.Replace(cellTestCtx{}, 2.0, CellReplaceOptions{}); err != nil {
				t.Error(err)
			}
		}
	})
	var replica any
	var values []string
	n.Events(func(event StateEvent[any]) {
		if event.Type == StateEventOps {
			applied, err := delta.ApplyImmutable(replica, event.Ops)
			if err != nil {
				t.Fatal(err)
			}
			replica = applied
		} else {
			replica = event.Value
		}
		values = append(values, jsonText(t, replica))
	})
	var doubled []string
	d.Subscribe(func(value any) { doubled = append(doubled, jsonText(t, value)) })
	if err := n.Replace(cellTestCtx{}, 1.0, CellReplaceOptions{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(values, []string{"0", "1", "2"}) {
		t.Fatalf("values = %v", values)
	}
	if d.Value() != 4.0 || !slices.Equal(doubled, []string{"0", "2", "4"}) {
		t.Fatalf("d = %v, doubled = %v", d.Value(), doubled)
	}
}

// cell.ts:177-190: replace publishes a structural diff when the new revision shares containers with the old one (or
// diff is true), otherwise one r op; an equal value publishes nothing.
func TestCellReplaceDiffSelection(t *testing.T) {
	shared := delta.JsonObjectOf("a", 1.0)
	cell, _ := NewCell[any]("s", Schema{}, delta.JsonObjectOf("keep", shared, "n", 1.0), allowWrites, SourceOptions{})
	var ops []string
	cell.Events(func(event StateEvent[any]) {
		if event.Type == StateEventOps {
			ops = append(ops, jsonText(t, event.Ops))
		}
	})
	if err := cell.Replace(cellTestCtx{}, delta.JsonObjectOf("keep", shared, "n", 2.0), CellReplaceOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := cell.Replace(cellTestCtx{}, delta.JsonObjectOf("keep", delta.JsonObjectOf("a", 1.0), "n", 3.0), CellReplaceOptions{}); err != nil {
		t.Fatal(err)
	}
	diff := true
	if err := cell.Replace(cellTestCtx{}, delta.JsonObjectOf("keep", delta.JsonObjectOf("a", 1.0), "n", 4.0), CellReplaceOptions{Diff: &diff}); err != nil {
		t.Fatal(err)
	}
	if err := cell.Replace(cellTestCtx{}, delta.JsonObjectOf("keep", delta.JsonObjectOf("a", 1.0), "n", 4.0), CellReplaceOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{`[["s",["n"],2]]`, `[["r",{"keep":{"a":1},"n":3}]]`, `[["s",["n"],4]]`}
	if !slices.Equal(ops, want) {
		t.Fatalf("ops = %v, want %v", ops, want)
	}
}

// cell.ts:192-199: reset installs value and schema without validation and publishes a reset event.
func TestCellResetPublishesReset(t *testing.T) {
	cell, _ := NewCell[any]("r", Schema{"type": "number"}, 1.0, allowWrites, SourceOptions{Dev: true})
	var events []string
	cell.Events(func(event StateEvent[any]) {
		events = append(events, string(event.Type)+jsonText(t, event.Ops)+jsonText(t, event.Value))
	})
	next := Schema{"type": "string"}
	if err := cell.Reset("one", next); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(events, []string{"snapnull1", `resetnull"one"`}) {
		t.Fatalf("events = %v", events)
	}
	if cell.Schema["type"] != "string" || cell.Value() != "one" {
		t.Fatalf("schema = %v, value = %v", cell.Schema, cell.Value())
	}
}

// cell.ts:46-58 Fanout.emit: a listener that unsubscribes before delivery is skipped; a panicking listener is reported
// and the writer still succeeds.
func TestCellFanoutSkipsUnsubscribedAndReportsPanics(t *testing.T) {
	options, reports := collectReports(t)
	cell, _ := NewCell[any]("f", Schema{}, 0.0, allowWrites, options)
	var late []any
	var unsubscribeLate func()
	cell.Subscribe(func(value any) {
		if value == 1.0 {
			unsubscribeLate()
		}
	})
	unsubscribeLate = cell.Subscribe(func(value any) { late = append(late, value) })
	cell.Subscribe(func(value any) {
		if value == 1.0 {
			panic(errors.New("listener failed"))
		}
	})
	if err := cell.Replace(cellTestCtx{}, 1.0, CellReplaceOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(late) != 1 || late[0] != 0.0 {
		t.Fatalf("late = %v", late)
	}
	if len(*reports) != 1 || (*reports)[0].Error() != "listener failed" {
		t.Fatalf("reports = %v", *reports)
	}
}

// cell.ts:299-336: a derivation over a non-object readable refreshes when it publishes, and detach stops it; a
// throwing derivation is reported by refresh.
func TestDerivedFollowsExternalReadable(t *testing.T) {
	options, reports := collectReports(t)
	external, _ := NewCell[any]("ext", Schema{}, 1.0, allowWrites, options)
	source := readableOnly{external}
	d, _ := NewDerived("d", Sources{source}, func(values ...any) any {
		if values[0] == 3.0 {
			panic(errors.New("three"))
		}
		return values[0].(float64) + 10
	}, options)
	if err := d.Attach(nil); err != nil {
		t.Fatal(err)
	}
	if IsSource(source) || !IsSource(d) || external.Dependents.Len() != 0 {
		t.Fatalf("source classification wrong")
	}
	_ = external.Replace(cellTestCtx{}, 2.0, CellReplaceOptions{})
	if d.Value() != 12.0 {
		t.Fatalf("d = %v", d.Value())
	}
	_ = external.Replace(cellTestCtx{}, 3.0, CellReplaceOptions{})
	if d.Value() != 12.0 || len(*reports) != 1 || (*reports)[0].Error() != "three" {
		t.Fatalf("d = %v, reports = %v", d.Value(), *reports)
	}
	d.Detach()
	_ = external.Replace(cellTestCtx{}, 4.0, CellReplaceOptions{})
	if d.Value() != 12.0 {
		t.Fatalf("detached d = %v", d.Value())
	}
}

// readableOnly hides the object source behind the Readable interface (Pi: a readable that is not a Source).
type readableOnly struct{ cell *Cell[any] }

func (r readableOnly) Value() any                                   { return r.cell.Value() }
func (r readableOnly) Subscribe(listener func(any)) func()          { return r.cell.Subscribe(listener) }
func (r readableOnly) Events(listener func(StateEvent[any])) func() { return r.cell.Events(listener) }

// cell.ts:241-260 computeDependents: transitive dependents recompute in creation order (seq) against candidate values,
// so a derivation over a derivation never reads a stale value; cell.ts:73-81 dev-mode writes also validate against the
// schemas of remote members exposing the source (schemas).
func TestCellDependentChainAndExposedSchemas(t *testing.T) {
	options, _ := collectReports(t)
	options.Dev = true
	c, _ := NewCell[any]("c", Schema{"type": "number"}, 1.0, allowWrites, options)
	first, _ := NewDerived("first", Sources{c}, func(values ...any) any { return values[0].(float64) + 1 }, options)
	second, _ := NewDerived("second", Sources{first, c}, func(values ...any) any {
		return values[0].(float64) * values[1].(float64)
	}, options)
	if first.Seq >= second.Seq || c.Seq >= first.Seq {
		t.Fatalf("seq not increasing: %d %d %d", c.Seq, first.Seq, second.Seq)
	}
	if err := first.Attach(nil); err != nil {
		t.Fatal(err)
	}
	if err := second.Attach(nil); err != nil {
		t.Fatal(err)
	}
	if !c.Dependents.Has(first) || !c.Dependents.Has(second) || !first.Dependents.Has(second) {
		t.Fatal("dependents not recorded")
	}
	var seen []string
	second.Subscribe(func(value any) { seen = append(seen, jsonText(t, value)) })
	if err := c.Replace(cellTestCtx{}, 3.0, CellReplaceOptions{}); err != nil {
		t.Fatal(err)
	}
	if first.Value() != 4.0 || second.Value() != 12.0 || !slices.Equal(seen, []string{"2", "12"}) {
		t.Fatalf("first = %v, second = %v, seen = %v", first.Value(), second.Value(), seen)
	}
	second.Schemas = []Schema{{"type": "number", "maximum": 20}}
	err := c.Replace(cellTestCtx{}, 5.0, CellReplaceOptions{})
	var chordError *ChordError
	if !errors.As(err, &chordError) || chordError.Reason != "state" || c.Value() != 3.0 || second.Value() != 12.0 {
		t.Fatalf("err = %v, c = %v, second = %v", err, c.Value(), second.Value())
	}
	c.Schemas = []Schema{{"type": "number", "maximum": 2}}
	if err := c.Replace(cellTestCtx{}, 3.5, CellReplaceOptions{}); !IsChordError(err, CodeInvalid) || c.Key != "c" {
		t.Fatalf("own exposed schema not checked: %v", err)
	}
}
