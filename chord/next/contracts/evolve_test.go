package contracts_test

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/test/next/contracts/evolve.test.ts.
//
// Designed out: every expectTypeOf and @ts-expect-error (the editor interfaces and Implementation<C> track edits in
// the types; the Go editors are RemoteEditor and LocalEditor, both holding one draft), and the "types track every
// step" case of "long histories".
//
// Go form of a chained edit: an editor method returns its editor; the first failing edit is kept and every later edit
// of the chain is skipped, and contracts.Evolve returns that error (upstream: the first failing edit throws out of
// `edit`).

func notesV1(t testing.TB) *contracts.Contract {
	t.Helper()
	note := contracts.Obj(contracts.Prop("text", contracts.Str()))
	return remote(t, "notes", 1, props(contracts.Prop("session", contracts.Str())),
		variant("editor", state(contracts.Prop("notes", contracts.Arr(note)), contracts.Prop("count", contracts.Int())),
			contracts.Meth("add", contracts.NewMethod(note, contracts.Str())),
			contracts.Meth("clear", contracts.NewMethod())),
		variant("reader", state(contracts.Prop("notes", contracts.Arr(note))),
			contracts.Meth("add", contracts.NewMethod(note, contracts.Str()))))
}

func infoV1(t testing.TB) *contracts.Contract {
	t.Helper()
	return local(t, "info", 1, noParams(), contracts.Mem("title", contracts.Str()), contracts.Mem("clear", contracts.NewMethod()))
}

type remoteEdit = func(e *contracts.RemoteEditor) *contracts.RemoteEditor

type localEdit = func(e *contracts.LocalEditor) *contracts.LocalEditor

func evolveRemote(contract *contracts.Contract, edit remoteEdit) (*contracts.Contract, error) {
	return contracts.Evolve(contract, edit)
}

func evolveLocal(contract *contracts.Contract, edit localEdit) (*contracts.Contract, error) {
	return contracts.Evolve(contract, edit)
}

// current lists the current state members and methods of a variant, in order.
func current(t testing.TB, contract *contracts.Contract, variant string) (state, methods []string) {
	t.Helper()
	members := currentVariants(t, contract).Value(variant)
	return keys(members.State), keys(members.Methods)
}

func TestEvolveEdits(t *testing.T) {
	t.Run("state changes one state member of one variant", func(t *testing.T) {
		v2 := must(evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.State("editor", "count", contracts.Str())
		}))
		if v2.Version != 2 {
			t.Fatalf("version = %d", v2.Version)
		}
		editor := currentVariants(t, v2).Value("editor").State.Value("count")
		sameAs(t, editor.Normal, js(t, `{"type":"string"}`))
		reader := currentVariants(t, v2).Value("reader").State.Value("notes")
		sameAs(t, reader.Normal, js(t, `{"type":"array","items":{"additionalProperties":false,"properties":{"text":{"type":"string"}},"required":["text"],"type":"object"}}`))
		_, err := evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.State("editor", "missing", contracts.Str())
		})
		wantError(t, err, "notes v2.editor.state.missing: no such state member")
	})

	t.Run("addState adds a state member", func(t *testing.T) {
		v2 := must(evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.AddState("reader", "count", contracts.Int())
		}))
		if state, _ := current(t, v2, "reader"); !reflect.DeepEqual(state, []string{"notes", "count"}) {
			t.Fatalf("state = %v", state)
		}
		_, err := evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.AddState("reader", "notes", contracts.Int())
		})
		wantError(t, err, "notes v2.reader.state.notes: already declared")
	})

	t.Run("removeState removes a state member", func(t *testing.T) {
		v2 := must(evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.RemoveState("editor", "count")
		}))
		if state, _ := current(t, v2, "editor"); !reflect.DeepEqual(state, []string{"notes"}) {
			t.Fatalf("state = %v", state)
		}
	})

	t.Run("args and result change one side of a method in every variant that has it", func(t *testing.T) {
		v2 := must(evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.Args("add", contracts.Obj(contracts.Prop("text", contracts.Str()), contracts.Prop("tag", contracts.Str()))).Result("add", contracts.Int())
		}))
		version := dataOf(t, v2).Versions[1]
		editor, reader := version.Variants.Value("editor").Methods.Value("add"), version.Variants.Value("reader").Methods.Value("add")
		if !reflect.DeepEqual(editor, reader) {
			t.Fatalf("editor %+v, reader %+v", editor, reader)
		}
		want := []contracts.Change{
			{Variant: "editor", Member: "add", Side: contracts.SideArgs},
			{Variant: "reader", Member: "add", Side: contracts.SideArgs},
			{Variant: "editor", Member: "add", Side: contracts.SideResult},
			{Variant: "reader", Member: "add", Side: contracts.SideResult},
		}
		if !reflect.DeepEqual(version.Changes, want) {
			t.Fatalf("changes = %v", version.Changes)
		}
		sameAs(t, editor.Result.Normal, js(t, `{"type":"integer"}`))
	})

	t.Run("method replaces both sides", func(t *testing.T) {
		v2 := must(evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.Method("clear", contracts.Obj(contracts.Prop("all", contracts.Bool())), contracts.Bool())
		}))
		clear := currentVariants(t, v2).Value("editor").Methods.Value("clear")
		sameAs(t, clear.Result.Normal, js(t, `{"type":"boolean"}`))
		_, err := evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.Method("missing", contracts.Nul(), contracts.Nul())
		})
		wantError(t, err, `notes v2: no method "missing"`)
	})

	t.Run("addMethod adds a method; to another variant only with the same signature", func(t *testing.T) {
		v2 := must(evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.AddMethod("reader", "clear", contracts.Nul(), contracts.Nul())
		}))
		if _, methods := current(t, v2, "reader"); !reflect.DeepEqual(methods, []string{"add", "clear"}) {
			t.Fatalf("methods = %v", methods)
		}
		_, err := evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.AddMethod("reader", "clear", contracts.Str(), contracts.Nul())
		})
		wantError(t, err, "notes v2.reader.methods.clear: a method has the same signature in every variant")
		_, err = evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.AddMethod("reader", "add", contracts.Nul(), contracts.Nul())
		})
		wantError(t, err, "notes v2.reader.methods.add: already declared")
	})

	t.Run("removeMethod removes a method from every variant", func(t *testing.T) {
		v2 := must(evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.RemoveMethod("add") }))
		if _, methods := current(t, v2, "editor"); !reflect.DeepEqual(methods, []string{"clear"}) {
			t.Fatalf("editor methods = %v", methods)
		}
		if _, methods := current(t, v2, "reader"); len(methods) != 0 {
			t.Fatalf("reader methods = %v", methods)
		}
		_, err := evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.RemoveMethod("missing") })
		wantError(t, err, `notes v2: no method "missing"`)
	})

	t.Run("local edits take no variant", func(t *testing.T) {
		v2 := must(evolveLocal(infoV1(t), func(e *contracts.LocalEditor) *contracts.LocalEditor {
			return e.State("title", contracts.Int()).
				AddState("body", contracts.Str()).
				AddMethod("save", contracts.Obj(contracts.Prop("force", contracts.Bool())), contracts.Nul()).
				RemoveMethod("clear")
		}))
		state, methods := current(t, v2, contracts.LocalVariant)
		if !reflect.DeepEqual(state, []string{"title", "body"}) || !reflect.DeepEqual(methods, []string{"save"}) {
			t.Fatalf("state = %v, methods = %v", state, methods)
		}
		v3 := must(evolveLocal(v2, func(e *contracts.LocalEditor) *contracts.LocalEditor {
			return e.RemoveState("body").Args("save", contracts.Nul())
		}))
		// Arguments changed to `null`: the method omits them now.
		if !contracts.TakesNoArgs(currentVariants(t, v3).Value(contracts.LocalVariant).Methods.Value("save")) {
			t.Fatal("save still takes arguments")
		}
	})

	t.Run("checks every edit: closed arguments and the vocabulary", func(t *testing.T) {
		_, err := evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.Args("add", tbObject(contracts.Prop("text", contracts.Str())))
		})
		wantError(t, err, "notes v2.editor.methods.add.args: argument objects must be closed")
		_, err = evolveLocal(infoV1(t), func(e *contracts.LocalEditor) *contracts.LocalEditor {
			return e.AddMethod("m", tbObject(), contracts.Nul())
		})
		wantError(t, err, "info v2.members.m.args: argument objects must be closed")
		_, err = evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.State("editor", "count", raw(t, `{"not":{}}`))
		})
		wantError(t, err, `notes v2.editor.state.count: unsupported keyword "not"`)
		_, err = evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.State("editor", "count", tbObject())
		})
		wantNoError(t, err)
	})

	t.Run("the first failing edit of a chain is the one reported, and later edits are skipped", func(t *testing.T) {
		_, err := evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.State("editor", "missing", contracts.Str()).AddState("reader", "notes", contracts.Int())
		})
		wantError(t, err, "notes v2.editor.state.missing: no such state member")
	})
}

func TestEvolveKindsAndFixedParts(t *testing.T) {
	t.Run("a member's kind never changes, also not after a removal and re-add", func(t *testing.T) {
		_, err := evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.RemoveState("editor", "count").AddMethod("editor", "count", contracts.Nul(), contracts.Nul())
		})
		wantError(t, err, `notes v2.editor.methods.count: "count" is a state member; its kind never changes`)
		v2 := must(evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.RemoveMethod("clear") }))
		v3 := must(evolveRemote(v2, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.State("editor", "count", contracts.Str())
		}))
		_, err = evolveRemote(v3, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.AddState("reader", "clear", contracts.Str())
		})
		wantError(t, err, `notes v4.reader.state.clear: "clear" is a method member; its kind never changes`)
	})

	t.Run("the variant set never changes", func(t *testing.T) {
		_, err := evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.AddState("admin", "x", contracts.Str())
		})
		wantError(t, err, "notes v2.admin: unknown variant (the variant set never changes)")
	})

	t.Run("a new base is a full declaration with an explicit version and no older versions", func(t *testing.T) {
		base := remote(t, "notes", 5, props(contracts.Prop("session", contracts.Str()), contracts.Prop("tenant", contracts.Str())), variant("editor", nil))
		var versions []int
		for _, version := range dataOf(t, base).Versions {
			versions = append(versions, version.Version)
		}
		if !reflect.DeepEqual(versions, []int{5}) {
			t.Fatalf("versions = %v", versions)
		}
		if got := must(evolveRemote(base, func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e })).Version; got != 6 {
			t.Fatalf("evolved version = %d", got)
		}
	})

	t.Run("evolve must return the editor it was given", func(t *testing.T) {
		other := must(evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e }))
		if other.Version != 2 {
			t.Fatalf("version = %d", other.Version)
		}
		var foreign *contracts.RemoteEditor
		must(evolveRemote(notesV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			foreign = e
			return e
		}))
		_, err := evolveRemote(notesV1(t), func(*contracts.RemoteEditor) *contracts.RemoteEditor { return foreign })
		wantError(t, err, "notes v2: evolve must return the editor it was given")
		_, err = evolveRemote(notesV1(t), func(*contracts.RemoteEditor) *contracts.RemoteEditor { return nil })
		wantError(t, err, "notes v2: evolve must return the editor it was given")
	})

	t.Run("an editor of the other kind is refused", func(t *testing.T) {
		_, err := evolveLocal(notesV1(t), func(e *contracts.LocalEditor) *contracts.LocalEditor { return e })
		wantError(t, err, "notes: evolve with a local editor needs a local contract")
		_, err = evolveRemote(infoV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e })
		wantError(t, err, "info: evolve with a remote editor needs a remote contract")
	})

	t.Run("leaves the previous contract untouched", func(t *testing.T) {
		previous := notesV1(t)
		must(evolveRemote(previous, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.RemoveState("editor", "count").RemoveMethod("add")
		}))
		state, methods := current(t, previous, "editor")
		if !reflect.DeepEqual(state, []string{"notes", "count"}) || !reflect.DeepEqual(methods, []string{"add", "clear"}) {
			t.Fatalf("state = %v, methods = %v", state, methods)
		}
	})
}

func TestEvolveLongHistories(t *testing.T) {
	t.Run("count versions beyond 9 and 99", func(t *testing.T) {
		contract := infoV1(t)
		for range 120 {
			contract = must(evolveLocal(contract, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e }))
		}
		if contract.Version != 121 || len(dataOf(t, contract).Versions) != 121 {
			t.Fatalf("version = %d, versions = %d", contract.Version, len(dataOf(t, contract).Versions))
		}
		for _, tc := range [][2]float64{{9, 10}, {99, 100}} {
			base := local(t, "i", tc[0], noParams(), contracts.Mem("s", contracts.Str()))
			if got := must(evolveLocal(base, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e })).Version; float64(got) != tc[1] {
				t.Errorf("%v evolves to %d", tc[0], got)
			}
		}
		v100 := local(t, "i", 100, noParams(), contracts.Mem("s", contracts.Str()))
		v101 := must(evolveLocal(v100, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("s", contracts.Int()) }))
		if v101.Version != 101 {
			t.Fatalf("version = %d", v101.Version)
		}
	})
}

func TestEvolveKeyedEdits(t *testing.T) {
	note := func() contracts.TSchema { return contracts.Obj(contracts.Prop("text", contracts.Str())) }
	store := func(t testing.TB) *contracts.Contract {
		return local(t, "store", 1, noParams(), contracts.Mem("notes", contracts.Keyed(note())), contracts.Mem("n", contracts.Int()))
	}
	noteTagged := func() contracts.TSchema {
		return contracts.Obj(contracts.Prop("text", contracts.Str()), contracts.Prop("tag", contracts.Str()))
	}

	t.Run("changes, adds and removes a local contract's keyed members, each version fingerprinted", func(t *testing.T) {
		base := store(t)
		edit := func(e *contracts.LocalEditor) *contracts.LocalEditor {
			return e.Keyed("notes", noteTagged()).AddKeyed("drafts", note())
		}
		v2 := must(evolveLocal(base, edit))
		v3 := must(evolveLocal(v2, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.RemoveKeyed("drafts") }))
		versions := dataOf(t, v3).Versions
		var got [][]string
		for _, version := range versions {
			got = append(got, keys(version.Variants.Value(contracts.LocalVariant).Keyed))
		}
		if want := [][]string{{"notes"}, {"notes", "drafts"}, {"notes"}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("keyed members = %v, want %v", got, want)
		}
		wantChanges := []contracts.Change{
			{Variant: "members", Member: "notes", Side: contracts.SideKeyed},
			{Variant: "members", Member: "drafts", Side: contracts.SideKeyed},
		}
		if !reflect.DeepEqual(versions[1].Changes, wantChanges) {
			t.Fatalf("changes = %v", versions[1].Changes)
		}
		prints := map[string]bool{}
		for _, version := range versions {
			prints[version.Fingerprint] = true
		}
		if len(prints) != 3 {
			t.Fatalf("%d distinct fingerprints, want 3", len(prints))
		}
		// The same edits elsewhere give the same fingerprint (an old plugin's copy).
		again := must(evolveLocal(store(t), edit))
		if dataOf(t, again).Versions[1].Fingerprint != versions[1].Fingerprint {
			t.Fatal("the same edits gave another fingerprint")
		}
	})

	t.Run("refuses an unknown keyed member, one declared already, and a kind change", func(t *testing.T) {
		_, err := evolveLocal(store(t), func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.Keyed("missing", note()) })
		wantError(t, err, "no such keyed member")
		_, err = evolveLocal(store(t), func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.AddKeyed("n", note()) })
		wantError(t, err, "already declared")
		_, err = evolveLocal(store(t), func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.RemoveKeyed("missing") })
		wantError(t, err, "no such keyed member")
	})
}
