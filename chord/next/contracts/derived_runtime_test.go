package contracts_test

import (
	"reflect"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Runtime ports of upstream cases whose type-level half (expectTypeOf, @ts-expect-error) Go cannot express: the Go
// forms of Implementation, VariantImplementation, ParamsOf, HostAddress and the evolve history are untyped
// (*delta.JsonObject params, map[string]MethodFunction members, []VersionData), so what upstream's types computed from
// a contract is asserted here from the contract data a factory, a caller and an accepts table are checked against.

func TestDerivedShapes(t *testing.T) {
	// contract.test.ts "derived types › Implementation: one variant with its members, state watchables and methods taking
	// args and a context".
	t.Run("Implementation: one variant with its members, state members and methods taking args", func(t *testing.T) {
		notes := notesContract(t)
		if got := must(contracts.VariantOrder(notes)); !reflect.DeepEqual(got, []string{"editor", "reader"}) {
			t.Fatalf("variants = %v", got)
		}
		editor := currentVariants(t, notes).Value("editor")
		if got := keys(editor.State); !reflect.DeepEqual(got, []string{"notes"}) {
			t.Fatalf("editor state = %v", got)
		}
		sameAs(t, editor.State.Value("notes").Normal, js(t, `{"type":"array","items":{"type":"string"}}`))
		if got := keys(editor.Methods); !reflect.DeepEqual(got, []string{"add", "clear"}) {
			t.Fatalf("editor methods = %v", got)
		}
		add, clear := editor.Methods.Value("add"), editor.Methods.Value("clear")
		sameAs(t, add.Args.Normal, js(t, `{"additionalProperties":false,"properties":{"text":{"type":"string"}},"required":["text"],"type":"object"}`))
		sameAs(t, add.Result.Normal, js(t, `{"type":"string"}`))
		if contracts.TakesNoArgs(add) {
			t.Fatal("add takes args")
		}
		// A method without arguments omits them, and returns null.
		if !contracts.TakesNoArgs(clear) {
			t.Fatal("clear takes args")
		}
		sameAs(t, clear.Result.Normal, js(t, `{"type":"null"}`))
		// The reader has the state member alone.
		reader := currentVariants(t, notes).Value("reader")
		if got := contracts.VariantNamesOf(reader); !reflect.DeepEqual(got.State, []string{"notes"}) || len(got.Methods) != 0 {
			t.Fatalf("reader members = %+v", got)
		}
	})

	// contract.test.ts "derived types › Implementation of a local contract is flat".
	t.Run("Implementation of a local contract is flat", func(t *testing.T) {
		info := local(t, "info", 1, noParams(),
			contracts.Mem("title", contracts.Str()), contracts.Mem("blob", contracts.Bytes()),
			contracts.Mem("save", contracts.NewMethod(contracts.Str())))
		if got := must(contracts.VariantOrder(info)); !reflect.DeepEqual(got, []string{contracts.LocalVariant}) {
			t.Fatalf("variants = %v", got)
		}
		members := currentVariants(t, info).Value(contracts.LocalVariant)
		if got := keys(members.State); !reflect.DeepEqual(got, []string{"title", "blob"}) {
			t.Fatalf("state = %v", got)
		}
		if !members.State.Value("blob").Bytes || members.State.Value("title").Bytes {
			t.Fatal("blob is bytes state, title is not")
		}
		if got := keys(members.Methods); !reflect.DeepEqual(got, []string{"save"}) {
			t.Fatalf("methods = %v", got)
		}
		save := members.Methods.Value("save")
		sameAs(t, save.Args.Normal, js(t, `{"type":"string"}`))
		sameAs(t, save.Result.Normal, js(t, `{"type":"null"}`))
		if contracts.TakesNoArgs(save) {
			t.Fatal("save takes args")
		}
		counts := local(t, "counts", 1, noParams(),
			contracts.Mem("reset", contracts.NewMethod()),
			contracts.Mem("total", contracts.NewMethod(contracts.TSchema{}, contracts.Int())),
			contracts.Mem("maybe", contracts.NewMethod(contracts.Union([]contracts.TSchema{contracts.Nul(), contracts.Str()}))))
		methods := currentVariants(t, counts).Value(contracts.LocalVariant).Methods
		sameAs(t, methods.Value("total").Result.Normal, js(t, `{"type":"integer"}`))
		sameAs(t, methods.Value("reset").Result.Normal, js(t, `{"type":"null"}`))
		// Only a `null` argument schema is omitted; a union with null is an argument.
		if !contracts.TakesNoArgs(methods.Value("reset")) || !contracts.TakesNoArgs(methods.Value("total")) || contracts.TakesNoArgs(methods.Value("maybe")) {
			t.Fatal("takesNoArgs is wrong for reset, total or maybe")
		}
	})

	// contract.test.ts "derived types › ParamsOf: the params object".
	t.Run("ParamsOf: the params object", func(t *testing.T) {
		notes := notesContract(t)
		params := dataOf(t, notes).Params
		if !reflect.DeepEqual(params.Fields, []string{"session", "page"}) {
			t.Fatalf("param fields = %v", params.Fields)
		}
		sameAs(t, params.Normal, js(t, `[["session",{"type":"string"}],["page",{"type":"integer"}]]`))
		ctx := chordctx.Background()
		_, err := data.CheckParams(notes, delta.JsonObjectOf("session", "s", "page", 1.0), ctx)
		wantNoError(t, err)
		for name, bad := range map[string]*delta.JsonObject{
			"page is a string":    delta.JsonObjectOf("session", "s", "page", "1"),
			"session is a number": delta.JsonObjectOf("session", 1.0, "page", 1.0),
			"page is missing":     delta.JsonObjectOf("session", "s"),
			"an extra field":      delta.JsonObjectOf("session", "s", "page", 1.0, "extra", 1.0),
		} {
			_, err := data.CheckParams(notes, bad, ctx)
			if chordErr, ok := data.IsChordErrorCode(err, data.CodeInvalid); !ok || chordErr.Reason != data.ReasonParams {
				t.Errorf("%s: error = %v, want invalid/params", name, err)
			}
		}
	})
}

func TestDeclarationTypeChecksAtRuntime(t *testing.T) {
	// declare.test.ts "declaration checks › refuse a remote declaration without a host address at the type level".
	t.Run("refuse a remote declaration without a host address", func(t *testing.T) {
		_, err := contracts.Remote("n", contracts.RemoteDeclaration{Version: 1, Params: noParams(),
			Variants: []contracts.VariantEntry{contracts.Var("a", contracts.VariantDeclaration{})}})
		wantError(t, err, "n.hostAddress: expected a function of the params")
	})
}

func TestHostAddressParams(t *testing.T) {
	// host-kind.test.ts "hostAddress › is typed by the contract's params": the params of a hostAddress are a
	// *delta.JsonObject, so a read of an undeclared field (`id`, where the params have `session`) compiles; the
	// address it makes is then refused where it is routed.
	t.Run("is typed by the contract's params: an address from an undeclared param is refused where routed", func(t *testing.T) {
		contract := must(contracts.Remote("app.typed", contracts.RemoteDeclaration{
			Version: 1,
			Params:  props(contracts.Prop("session", contracts.Str())),
			HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
				return session.Address(delta.JsonObjectOf("id", params.Value("id"))), nil
			},
			Variants: []contracts.VariantEntry{variant("v", nil)},
		}))
		kindParams := must(contracts.KindParams(session))
		address, err := dataOf(t, contract).HostAddress(delta.JsonObjectOf("session", "s"))
		wantNoError(t, err)
		_, err = data.CheckAddress("session", kindParams, address.Params, chordctx.Background())
		if chordErr, ok := data.IsChordErrorCode(err, data.CodeInvalid); !ok || chordErr.Reason != data.ReasonHostAddress {
			t.Fatalf("error = %v, want invalid/host_address", err)
		}
		// The declared param routes.
		good := session.Address(delta.JsonObjectOf("id", "s"))
		_, err = data.CheckAddress("session", kindParams, good.Params, chordctx.Background())
		wantNoError(t, err)
	})
}

func TestEvolveHistory(t *testing.T) {
	note := contracts.Obj(contracts.Prop("text", contracts.Str()))
	// evolve.test.ts "long histories › the types track every step with its changes".
	t.Run("the history tracks every step with its changes", func(t *testing.T) {
		v1 := remote(t, "notes", 1, props(contracts.Prop("session", contracts.Str())),
			variant("editor", state(contracts.Prop("notes", contracts.Arr(note)), contracts.Prop("count", contracts.Int())),
				contracts.Meth("add", contracts.NewMethod(note, contracts.Str())), contracts.Meth("clear", contracts.NewMethod())),
			variant("reader", state(contracts.Prop("notes", contracts.Arr(note))), contracts.Meth("add", contracts.NewMethod(note, contracts.Str()))))
		v2 := must(contracts.Evolve(v1, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.State("editor", "count", contracts.Str()).AddMethod("reader", "clear", contracts.Nul(), contracts.Nul())
		}))
		versions := dataOf(t, v2).Versions
		if len(versions) != 2 {
			t.Fatalf("history length = %d", len(versions))
		}
		want := []contracts.Change{
			{Variant: "editor", Member: "count", Side: contracts.SideState},
			{Variant: "reader", Member: "clear", Side: contracts.SideArgs},
			{Variant: "reader", Member: "clear", Side: contracts.SideResult},
		}
		if !reflect.DeepEqual(versions[1].Changes, want) {
			t.Fatalf("step 2 changes = %v, want %v", versions[1].Changes, want)
		}
		if len(versions[0].Changes) != 0 {
			t.Fatalf("a base has changes: %v", versions[0].Changes)
		}
		// Step 1 keeps the old schema of count; step 2 has the new one.
		sameAs(t, versions[0].Variants.Value("editor").State.Value("count").Normal, js(t, `{"type":"integer"}`))
		sameAs(t, versions[1].Variants.Value("editor").State.Value("count").Normal, js(t, `{"type":"string"}`))
	})
}

func TestChangesAfter(t *testing.T) {
	// history.test.ts "type-level history › collects every change after a version": the changes after a version are the
	// union of the changes of every later step; an accepts table needs exactly a mapping for each of them.
	v9 := local(t, "h", 9, noParams(), contracts.Mem("s", contracts.Str()), contracts.Mem("t", contracts.Str()))
	v10 := must(evolveLocal(v9, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("s", contracts.Int()) }))
	v11 := must(evolveLocal(v10, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("t", contracts.Int()) }))
	mapping := func(members ...string) contracts.VersionMappings {
		var state []contracts.Entry[contracts.StateMapping]
		for _, member := range members {
			state = append(state, stateMapping(member, stateFn()))
		}
		return contracts.VersionMappings{State: state}
	}

	t.Run("after 9: both later changes need a mapping", func(t *testing.T) {
		_, err := contracts.CheckAccepts(v11, table(version("9", mapping("s", "t"))))
		wantNoError(t, err)
		_, err = contracts.CheckAccepts(v11, table(version("9", mapping("s"))))
		wantError(t, err, "h accepts[9].state.t: missing mapping")
		_, err = contracts.CheckAccepts(v11, table(version("9", mapping("t"))))
		wantError(t, err, "h accepts[9].state.s: missing mapping")
	})
	t.Run("after 10: the later change alone", func(t *testing.T) {
		_, err := contracts.CheckAccepts(v11, table(version("10", mapping("t"))))
		wantNoError(t, err)
		_, err = contracts.CheckAccepts(v11, table(version("10", mapping("s", "t"))))
		wantError(t, err, "h accepts[10].state.s: unnecessary mapping")
	})
	t.Run("after 11: nothing", func(t *testing.T) {
		_, err := contracts.CheckAccepts(v11, table(version("11", mapping())))
		wantError(t, err, "h accepts[11]: not an older version")
	})
}
