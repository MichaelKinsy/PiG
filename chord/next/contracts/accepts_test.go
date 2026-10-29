package contracts_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/test/next/contracts/accepts.test.ts.
//
// Designed out: AcceptsTable<C> and the mapping types (Static<...>) are type-level only, so every @ts-expect-error
// line asserts nothing at runtime. A Go table is data (contracts.AcceptsTable of contracts.VersionMappings) whose
// functions take and return JSON values. Inputs a typed Go table cannot express are designed out: a mapping that is
// no function (`args: 1`), an unknown key of a mapping or version, a null table (an empty table is the same).
//
// Go forms: "refuse" is StateMapping{Refuse: true} / MethodMapping{Refuse: true}; a remote table maps state in
// VersionMappings.VariantState (variant, then member), a local one in VersionMappings.State (member).

func notesWithNote(t testing.TB) (v1 *contracts.Contract, noteV2 func() contracts.TSchema) {
	t.Helper()
	note := func() contracts.TSchema { return contracts.Obj(contracts.Prop("text", contracts.Str())) }
	v1 = remote(t, "notes", 1, props(contracts.Prop("session", contracts.Str())),
		variant("editor", state(contracts.Prop("notes", contracts.Arr(note())), contracts.Prop("count", contracts.Int())),
			contracts.Meth("add", contracts.NewMethod(note(), contracts.Str())),
			contracts.Meth("clear", contracts.NewMethod())),
		variant("reader", state(contracts.Prop("notes", contracts.Arr(note()))),
			contracts.Meth("add", contracts.NewMethod(note(), contracts.Str()))))
	return v1, func() contracts.TSchema {
		return contracts.Obj(contracts.Prop("text", contracts.Str()), contracts.Prop("tag", contracts.Str()))
	}
}

func table(entries ...contracts.Entry[contracts.VersionMappings]) contracts.AcceptsTable {
	return contracts.AcceptsTable(entries)
}

func version(name string, mappings contracts.VersionMappings) contracts.Entry[contracts.VersionMappings] {
	return contracts.Entry[contracts.VersionMappings]{Name: name, Value: mappings}
}

// variantState is `{ variant: { member: mapping } }` of a remote table.
func variantState(variant string, members ...contracts.Entry[contracts.StateMapping]) contracts.Entry[[]contracts.Entry[contracts.StateMapping]] {
	return contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{Name: variant, Value: members}
}

func stateMapping(member string, mapping contracts.StateMapping) contracts.Entry[contracts.StateMapping] {
	return contracts.Entry[contracts.StateMapping]{Name: member, Value: mapping}
}

func methodMapping(member string, mapping contracts.MethodMapping) contracts.Entry[contracts.MethodMapping] {
	return contracts.Entry[contracts.MethodMapping]{Name: member, Value: mapping}
}

func evolveWith(t testing.TB, contract *contracts.Contract, edit remoteEdit) *contracts.Contract {
	t.Helper()
	return must(contracts.Evolve(contract, edit))
}

func methods(entries ...contracts.Entry[contracts.MethodMapping]) contracts.VersionMappings {
	return contracts.VersionMappings{Methods: entries}
}

func TestAcceptsStateMappings(t *testing.T) {
	notes, noteV2 := notesWithNote(t)
	v2 := evolveWith(t, notes, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
		return e.State("editor", "notes", contracts.Arr(noteV2()))
	})

	t.Run("a changed member maps the current member to the old one", func(t *testing.T) {
		accepts := table(version("1", contracts.VersionMappings{VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{
			variantState("editor", stateMapping("notes", stateFn()))}}))
		checked := must(contracts.CheckAccepts(v2, accepts))
		if !reflect.DeepEqual(checked.Versions, []contracts.AcceptedVersion{{Version: 1, Fingerprint: notes.Fingerprint}}) {
			t.Fatalf("versions = %+v", checked.Versions)
		}
		_, err := contracts.CheckAccepts(v2, table(version("1", contracts.VersionMappings{VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{
			variantState("editor")}})))
		wantError(t, err, "notes accepts[1].state.editor.notes: missing mapping")
		// "refuse" is only for removed members.
		_, err = contracts.CheckAccepts(v2, table(version("1", contracts.VersionMappings{VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{
			variantState("editor", stateMapping("notes", contracts.StateMapping{Refuse: true}))}})))
		wantError(t, err, "notes accepts[1].state.editor.notes: expected a function")
	})

	t.Run("a removed member maps the variant's whole current state, or refuses", func(t *testing.T) {
		v3 := evolveWith(t, v2, func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.RemoveState("editor", "count") })
		checked := must(contracts.CheckAccepts(v3, table(
			version("1", contracts.VersionMappings{VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{
				variantState("editor", stateMapping("notes", stateFn()), stateMapping("count", stateFn()))}}),
			version("2", contracts.VersionMappings{VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{
				variantState("editor", stateMapping("count", contracts.StateMapping{Refuse: true}))}}))))
		if len(checked.Versions) != 2 || checked.Versions[0].Version != 1 || checked.Versions[1].Version != 2 {
			t.Fatalf("versions = %+v", checked.Versions)
		}
		// Neither a function nor "refuse".
		_, err := contracts.CheckAccepts(v3, table(version("2", contracts.VersionMappings{VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{
			variantState("editor", stateMapping("count", contracts.StateMapping{}))}})))
		wantError(t, err, `notes accepts[2].state.editor.count: expected a function or "refuse"`)
	})

	t.Run("an absent section is allowed only when nothing is needed there", func(t *testing.T) {
		_, err := contracts.CheckAccepts(v2, table(version("1", contracts.VersionMappings{})))
		wantError(t, err, "notes accepts[1].state.editor: missing mapping")
		v3 := evolveWith(t, v2, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.AddState("reader", "count", contracts.Int())
		})
		_, err = contracts.CheckAccepts(v3, table(version("2", contracts.VersionMappings{})))
		wantNoError(t, err)
		_, err = contracts.CheckAccepts(v3, table(version("2", contracts.VersionMappings{
			VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{},
			Methods:      []contracts.Entry[contracts.MethodMapping]{}})))
		wantNoError(t, err)
	})

	t.Run("unnecessary mappings are refused", func(t *testing.T) {
		_, err := contracts.CheckAccepts(v2, table(version("1", contracts.VersionMappings{VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{
			variantState("editor", stateMapping("notes", stateFn())),
			variantState("reader", stateMapping("notes", stateFn()))}})))
		wantError(t, err, "notes accepts[1].state.reader: unnecessary mapping")
		v3 := evolveWith(t, v2, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.AddState("reader", "count", contracts.Int())
		})
		// Added members need nothing.
		_, err = contracts.CheckAccepts(v3, table(version("2", contracts.VersionMappings{VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{
			variantState("reader", stateMapping("count", stateFn()))}})))
		wantError(t, err, "notes accepts[2].state.reader: unnecessary mapping")
	})

	t.Run("is checked by edit, not by schema: a no-op edit and a reverted change still need mappings", func(t *testing.T) {
		note := func() contracts.TSchema { return contracts.Obj(contracts.Prop("text", contracts.Str())) }
		noop := evolveWith(t, notes, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.State("reader", "notes", contracts.Arr(note()))
		})
		_, err := contracts.CheckAccepts(noop, table(version("1", contracts.VersionMappings{})))
		wantError(t, err, "notes accepts[1].state.reader: missing mapping")
		identity := func(variant, member string) contracts.AcceptsTable {
			return table(version("1", contracts.VersionMappings{VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{
				variantState(variant, stateMapping(member, stateFn()))}}))
		}
		_, err = contracts.CheckAccepts(noop, identity("reader", "notes"))
		wantNoError(t, err)
		reverted := evolveWith(t, v2, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.State("editor", "notes", contracts.Arr(note()))
		})
		_, err = contracts.CheckAccepts(reverted, identity("editor", "notes"))
		wantNoError(t, err)
		readded := evolveWith(t, notes, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.RemoveState("editor", "count").AddState("editor", "count", contracts.Int())
		})
		_, err = contracts.CheckAccepts(readded, identity("editor", "count"))
		wantNoError(t, err)
	})
}

func TestAcceptsMethodMappings(t *testing.T) {
	notes, noteV2 := notesWithNote(t)
	mapping := func(args, result bool) contracts.MethodMapping {
		var m contracts.MethodMapping
		if args {
			m.Args = fn
		}
		if result {
			m.Result = fn
		}
		return m
	}
	check := func(contract *contracts.Contract, v string, entries ...contracts.Entry[contracts.MethodMapping]) error {
		_, err := contracts.CheckAccepts(contract, table(version(v, methods(entries...))))
		return err
	}

	t.Run("changed args map old to current; a changed result maps current to old; both need both", func(t *testing.T) {
		args := evolveWith(t, notes, func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.Args("add", noteV2()) })
		wantNoError(t, check(args, "1", methodMapping("add", mapping(true, false))))
		wantError(t, check(args, "1", methodMapping("add", mapping(true, true))), "notes accepts[1].methods.add.result: unnecessary mapping")

		result := evolveWith(t, notes, func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.Result("add", contracts.Int()) })
		wantNoError(t, check(result, "1", methodMapping("add", mapping(false, true))))
		wantError(t, check(result, "1", methodMapping("add", mapping(false, false))), "notes accepts[1].methods.add.result: missing mapping")

		both := evolveWith(t, notes, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.Method("add", noteV2(), contracts.Int())
		})
		wantNoError(t, check(both, "1", methodMapping("add", mapping(true, true))))
		wantError(t, check(both, "1", methodMapping("add", mapping(true, false))), "notes accepts[1].methods.add.result: missing mapping")
	})

	t.Run("a removed method takes a call replacement or refuses", func(t *testing.T) {
		v2 := evolveWith(t, notes, func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.RemoveMethod("clear") })
		wantNoError(t, check(v2, "1", methodMapping("clear", contracts.MethodMapping{Call: call})))
		wantNoError(t, check(v2, "1", methodMapping("clear", contracts.MethodMapping{Refuse: true})))
		// A removed method has no args mapping.
		wantError(t, check(v2, "1", methodMapping("clear", mapping(true, false))), `notes accepts[1].methods.clear: expected { call } or "refuse"`)
		// The removed method needs a replacement or a refusal.
		_, err := contracts.CheckAccepts(v2, table(version("1", contracts.VersionMappings{})))
		wantError(t, err, "notes accepts[1].methods.clear: missing mapping")
	})

	t.Run("a method removed from one variant that had it counts as removed", func(t *testing.T) {
		v2 := evolveWith(t, notes, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.RemoveMethod("add").AddMethod("editor", "add", contracts.Obj(contracts.Prop("text", contracts.Str())), contracts.Str())
		})
		wantError(t, check(v2, "1", methodMapping("add", mapping(true, true))), `notes accepts[1].methods.add: expected { call } or "refuse"`)
		wantNoError(t, check(v2, "1", methodMapping("add", contracts.MethodMapping{Refuse: true})))
	})

	t.Run("call and refuse are also allowed for changed methods", func(t *testing.T) {
		v2 := evolveWith(t, notes, func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.Args("add", noteV2()) })
		wantNoError(t, check(v2, "1", methodMapping("add", contracts.MethodMapping{Call: call})))
		wantNoError(t, check(v2, "1", methodMapping("add", contracts.MethodMapping{Refuse: true})))
		// call cannot be combined with args.
		wantError(t, check(v2, "1", methodMapping("add", contracts.MethodMapping{Call: call, Args: fn})), `notes accepts[1].methods.add: expected { call } or "refuse"`)
	})

	t.Run("added methods and methods added to a new variant need nothing", func(t *testing.T) {
		v2 := evolveWith(t, notes, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.AddMethod("reader", "clear", contracts.Nul(), contracts.Nul())
		})
		checked := must(contracts.CheckAccepts(v2, table(version("1", contracts.VersionMappings{}))))
		if len(checked.Versions) != 1 {
			t.Fatalf("versions = %+v", checked.Versions)
		}
		wantError(t, check(v2, "1", methodMapping("clear", contracts.MethodMapping{Refuse: true})), "notes accepts[1].methods.clear: unnecessary mapping")
	})

	t.Run("refuses wrong shapes", func(t *testing.T) {
		v2 := evolveWith(t, notes, func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.Args("add", noteV2()) })
		// A mapping that is no function is designed out; an absent one is missing.
		wantError(t, check(v2, "1", methodMapping("add", contracts.MethodMapping{})), "notes accepts[1].methods.add.args: missing mapping")
		_, err := contracts.CheckAccepts(v2, table(
			version("1", methods(methodMapping("add", mapping(true, false)))),
			version("other", contracts.VersionMappings{})))
		wantError(t, err, "notes accepts[other]: not an older version")
	})
}

func TestAcceptsVersions(t *testing.T) {
	notes, noteV2 := notesWithNote(t)

	t.Run("refuses unknown versions and the current version", func(t *testing.T) {
		v2 := evolveWith(t, notes, func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e })
		_, err := contracts.CheckAccepts(v2, table(version("3", contracts.VersionMappings{})))
		wantError(t, err, "notes accepts[3]: not an older version")
		_, err = contracts.CheckAccepts(v2, table(version("2", contracts.VersionMappings{})))
		wantError(t, err, "notes accepts[2]: not an older version")
		checked := must(contracts.CheckAccepts(v2, table()))
		if len(checked.Versions) != 0 {
			t.Fatalf("versions = %+v", checked.Versions)
		}
	})

	t.Run("maps directly over several versions: every edit after the accepted version counts", func(t *testing.T) {
		v2 := evolveWith(t, notes, func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.Args("add", noteV2()) })
		v3 := evolveWith(t, v2, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.Result("add", contracts.Int()).RemoveState("editor", "count")
		})
		from := func(args bool) contracts.VersionMappings {
			add := contracts.MethodMapping{Result: fn}
			if args {
				add.Args = fn
			}
			return contracts.VersionMappings{
				VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{
					variantState("editor", stateMapping("count", contracts.StateMapping{Refuse: true}))},
				Methods: []contracts.Entry[contracts.MethodMapping]{methodMapping("add", add)},
			}
		}
		checked := must(contracts.CheckAccepts(v3, table(
			version("1", from(true)),
			version("2", contracts.VersionMappings{
				VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{variantState("editor", stateMapping("count", stateFn()))},
				Methods:      []contracts.Entry[contracts.MethodMapping]{methodMapping("add", contracts.MethodMapping{Result: fn})}}))))
		want := []contracts.AcceptedVersion{{Version: 1, Fingerprint: notes.Fingerprint}, {Version: 2, Fingerprint: v2.Fingerprint}}
		if !reflect.DeepEqual(checked.Versions, want) {
			t.Fatalf("versions = %+v", checked.Versions)
		}
		// From v1, args changed too.
		_, err := contracts.CheckAccepts(v3, table(version("1", from(false))))
		wantError(t, err, "notes accepts[1].methods.add.args: missing mapping")
	})

	t.Run("never calls a mapping", func(t *testing.T) {
		v2 := evolveWith(t, notes, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.Args("add", noteV2()).RemoveMethod("clear").State("editor", "count", contracts.Str())
		})
		var calls int
		counting := func(value any) (any, error) { calls++; return value, nil }
		counted := contracts.Mapping(counting)
		_, err := contracts.CheckAccepts(v2, table(version("1", contracts.VersionMappings{
			VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{
				variantState("editor", stateMapping("count", contracts.StateMapping{Func: counted}))},
			Methods: []contracts.Entry[contracts.MethodMapping]{
				methodMapping("add", contracts.MethodMapping{Args: counted}),
				methodMapping("clear", contracts.MethodMapping{Call: func(args any, _ context.Context) (any, error) { calls++; return args, nil }})},
		})))
		wantNoError(t, err)
		if calls != 0 {
			t.Fatalf("%d mappings were called", calls)
		}
	})

	t.Run("local tables are flat: state without a variant level", func(t *testing.T) {
		v1 := local(t, "info", 1, noParams(), contracts.Mem("title", contracts.Str()), contracts.Mem("save", contracts.NewMethod(contracts.Str())))
		v2 := must(evolveLocal(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor {
			return e.State("title", contracts.Int()).Args("save", contracts.Int())
		}))
		_, err := contracts.CheckAccepts(v2, table(version("1", contracts.VersionMappings{
			State:   []contracts.Entry[contracts.StateMapping]{stateMapping("title", stateFn())},
			Methods: []contracts.Entry[contracts.MethodMapping]{methodMapping("save", contracts.MethodMapping{Args: fn})}})))
		wantNoError(t, err)
		// A remote-shaped section (`members`) is a mapping for a member that does not exist.
		_, err = contracts.CheckAccepts(v2, table(version("1", contracts.VersionMappings{
			State:   []contracts.Entry[contracts.StateMapping]{stateMapping("members", stateFn())},
			Methods: []contracts.Entry[contracts.MethodMapping]{methodMapping("save", contracts.MethodMapping{Refuse: true})}})))
		wantError(t, err, "info accepts[1].state.members: unnecessary mapping")
	})

	t.Run("a table of the wrong kind is refused", func(t *testing.T) {
		v2 := evolveWith(t, notes, func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.Args("add", noteV2()) })
		_, err := contracts.CheckAccepts(v2, table(version("1", contracts.VersionMappings{State: []contracts.Entry[contracts.StateMapping]{stateMapping("x", stateFn())}})))
		wantError(t, err, "notes accepts[1].state: a remote table maps state per variant (VariantState)")
		v1 := local(t, "info", 1, noParams(), contracts.Mem("title", contracts.Str()))
		l2 := must(evolveLocal(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("title", contracts.Int()) }))
		_, err = contracts.CheckAccepts(l2, table(version("1", contracts.VersionMappings{VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{variantState("members")}})))
		wantError(t, err, "info accepts[1].variantState: a local table has no variants (State)")
	})
}
