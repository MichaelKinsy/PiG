package contracts_test

import (
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/test/contracts/declare.test.ts.
//
// Designed out (no Go input: a declaration is a typed struct): a contract name that is no string; an unknown
// declaration or variant key; a method placed under `state`; a schema placed under `methods` (the Go form is a nil
// *Method, covered below); an unknown key of a local declaration, and a host address in one (a LocalDeclaration has no
// such field); a keyed member under a remote variant's state (VariantDeclaration.State holds []Property of Schema, and
// *KeyedMember is not a Schema: a compile error), so the @ts-expect-error variants of those tests have nothing to
// refuse at runtime. The missing host address is a runtime test (TestDeclarationTypeChecksAtRuntime).
//
// A nil slice is upstream's absent field (`missing params`, `no variants`); an empty non-nil one is `{}`.

func TestDeclarationChecks(t *testing.T) {
	note := func() contracts.Schema { return contracts.Obj(contracts.Prop("text", contracts.Str())) }

	t.Run("accept a remote and a local declaration", func(t *testing.T) {
		notes := remote(t, "notes", 1, props(contracts.Prop("session", contracts.Str())),
			variant("editor", state(contracts.Prop("notes", contracts.Arr(note()))),
				contracts.Meth("add", contracts.NewMethod(note(), contracts.Str())),
				contracts.Meth("clear", contracts.NewMethod())),
			variant("reader", state(contracts.Prop("notes", contracts.Arr(note())))))
		if notes.Kind != contracts.KindRemote || notes.Name != "notes" || notes.Version != 1 {
			t.Fatalf("notes = %+v", notes)
		}
		if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(notes.Fingerprint) {
			t.Fatalf("fingerprint = %q", notes.Fingerprint)
		}
		info := local(t, "app.session-info", 3, noParams(),
			contracts.Mem("init", contracts.Obj(contracts.Prop("title", contracts.Str()))),
			contracts.Mem("clear", contracts.NewMethod()))
		if info.Kind != contracts.KindLocal || info.Name != "app.session-info" || info.Version != 3 {
			t.Fatalf("info = %+v", info)
		}
	})

	// Each case declares a valid hostAddress unless it checks that one, so the check it names is the one reached.
	a := func() []contracts.VariantEntry { return []contracts.VariantEntry{variant("a", nil)} }
	for _, tc := range []struct {
		name        string
		contract    string
		declaration contracts.RemoteDeclaration
		message     string
	}{
		{"empty name", "", contracts.RemoteDeclaration{Version: 1, Params: noParams(), HostAddress: serverAddress, Variants: a()}, `A contract name "": expected lowercase`},
		{"an uppercase name", "Notes", contracts.RemoteDeclaration{Version: 1, Params: noParams(), HostAddress: serverAddress, Variants: a()}, "expected lowercase"},
		{"a name with a colon", "a:b", contracts.RemoteDeclaration{Version: 1, Params: noParams(), HostAddress: serverAddress, Variants: a()}, "expected lowercase"},
		{"version zero", "n", contracts.RemoteDeclaration{Version: 0, Params: noParams(), HostAddress: serverAddress, Variants: a()}, "n.version: expected a positive integer"},
		{"fractional version", "n", contracts.RemoteDeclaration{Version: 1.5, Params: noParams(), HostAddress: serverAddress, Variants: a()}, "n.version: expected a positive integer"},
		{"unsafe version", "n", contracts.RemoteDeclaration{Version: 1 << 53, Params: noParams(), HostAddress: serverAddress, Variants: a()}, "n.version: expected a positive integer"},
		{"a missing host address", "n", contracts.RemoteDeclaration{Version: 1, Params: noParams(), Variants: a()}, "n.hostAddress: expected a function of the params"},
		{"no variants", "n", contracts.RemoteDeclaration{Version: 1, Params: noParams(), HostAddress: serverAddress, Variants: []contracts.VariantEntry{}}, "n.variants: at least one variant is required"},
		{"missing variants", "n", contracts.RemoteDeclaration{Version: 1, Params: noParams(), HostAddress: serverAddress}, "n.variants: expected an object"},
		{"missing params", "n", contracts.RemoteDeclaration{Version: 1, HostAddress: serverAddress, Variants: a()}, "n.params: expected an object"},
		{"a nil method under methods", "n", contracts.RemoteDeclaration{Version: 1, Params: noParams(), HostAddress: serverAddress, Variants: []contracts.VariantEntry{
			variant("a", nil, contracts.Meth("m", nil))}}, "n.a.methods.m: expected method(...)"},
		{"bad state schema", "n", contracts.RemoteDeclaration{Version: 1, Params: noParams(), HostAddress: serverAddress, Variants: []contracts.VariantEntry{
			variant("a", state(contracts.Prop("s", raw(t, `{"not":{}}`))))}}, `n.a.state.s: unsupported keyword "not"`},
		{"open arguments", "n", contracts.RemoteDeclaration{Version: 1, Params: noParams(), HostAddress: serverAddress, Variants: []contracts.VariantEntry{
			variant("a", nil, contracts.Meth("m", contracts.NewMethod(tbObject())))}}, "n.a.methods.m.args: argument objects must be closed"},
		{"bad result", "n", contracts.RemoteDeclaration{Version: 1, Params: noParams(), HostAddress: serverAddress, Variants: []contracts.VariantEntry{
			variant("a", nil, contracts.Meth("m", contracts.NewMethod(contracts.Schema{}, raw(t, `{"not":{}}`))))}}, `n.a.methods.m.result: unsupported keyword "not"`},
	} {
		t.Run("remote refuses "+tc.name, func(t *testing.T) {
			_, err := contracts.Remote(tc.contract, tc.declaration)
			wantError(t, err, tc.message)
		})
	}

	for _, tc := range []struct {
		name        string
		declaration contracts.LocalDeclaration
		message     string
	}{
		{"bad member schema", contracts.LocalDeclaration{Version: 1, Params: noParams(), Members: []contracts.LocalEntry{contracts.Mem("s", raw(t, `{"not":{}}`))}}, `i.members.s: unsupported keyword "not"`},
		{"open arguments", contracts.LocalDeclaration{Version: 1, Params: noParams(), Members: []contracts.LocalEntry{contracts.Mem("m", contracts.NewMethod(tbObject()))}}, "i.members.m.args: argument objects must be closed"},
		{"missing members", contracts.LocalDeclaration{Version: 1, Params: noParams()}, "i.members: expected an object"},
		{"a nil member", contracts.LocalDeclaration{Version: 1, Params: noParams(), Members: []contracts.LocalEntry{contracts.Mem("m", nil)}}, "i.members.m: expected a schema, method(...) or keyed(...)"},
	} {
		t.Run("local refuses "+tc.name, func(t *testing.T) {
			_, err := contracts.Local("i", tc.declaration)
			wantError(t, err, tc.message)
		})
	}

	t.Run("does not check state for closed objects", func(t *testing.T) {
		_, err := contracts.Local("i", contracts.LocalDeclaration{Version: 1, Params: noParams(), Members: []contracts.LocalEntry{
			contracts.Mem("s", tbObject(contracts.Prop("a", contracts.Str())))}})
		wantNoError(t, err)
	})

	t.Run("declare in the order JavaScript enumerates an object: index keys first, a repeated name keeps its place", func(t *testing.T) {
		contract := local(t, "o", 1, noParams(),
			contracts.Mem("b", contracts.Str()), contracts.Mem("2", contracts.Str()), contracts.Mem("a", contracts.Str()),
			contracts.Mem("1", contracts.Str()), contracts.Mem("b", contracts.Int()))
		members := currentVariants(t, contract).Value(contracts.LocalVariant).State
		if got := keys(members); len(got) != 4 || got[0] != "1" || got[1] != "2" || got[2] != "b" || got[3] != "a" {
			t.Fatalf("members = %v", got)
		}
		if got := stringify(t, members.Value("b").Normal); got != `{"type":"integer"}` {
			t.Fatalf("the last declaration of a repeated name wins: %s", got)
		}
	})
}

func TestDeclarationMembersAcrossVariants(t *testing.T) {
	declare := func(variants ...contracts.VariantEntry) error {
		_, err := declareRemote("n", 1, noParams(), variants...)
		return err
	}

	t.Run("D11 refuses a name that is state in one variant and a method in another", func(t *testing.T) {
		err := declare(
			variant("a", state(contracts.Prop("x", contracts.Str()))),
			variant("b", nil, contracts.Meth("x", contracts.NewMethod())))
		wantError(t, err, `n.b.methods.x: "x" is a state member; its kind never changes`)
	})

	t.Run("D11 refuses one name as state and method within a variant", func(t *testing.T) {
		err := declare(variant("a", state(contracts.Prop("x", contracts.Str())), contracts.Meth("x", contracts.NewMethod())))
		wantError(t, err, `n.a.methods.x: "x" is a state member`)
	})

	t.Run("D11 refuses a method with different signatures in two variants", func(t *testing.T) {
		err := declare(
			variant("a", nil, contracts.Meth("m", contracts.NewMethod(contracts.Str()))),
			variant("b", nil, contracts.Meth("m", contracts.NewMethod(contracts.Int()))))
		wantError(t, err, "n.b.methods.m: a method has the same signature in every variant")
		err = declare(
			variant("a", nil, contracts.Meth("m", contracts.NewMethod(contracts.Str(), contracts.Str()))),
			variant("b", nil, contracts.Meth("m", contracts.NewMethod(contracts.Str(contracts.Max(3)), contracts.Str()))))
		wantError(t, err, "n.b.methods.m: a method has the same signature in every variant")
	})

	t.Run("D11 allows state schemas to differ per variant (redaction)", func(t *testing.T) {
		wantNoError(t, declare(
			variant("editor", state(contracts.Prop("notes", contracts.Arr(contracts.Obj(contracts.Prop("text", contracts.Str()), contracts.Prop("author", contracts.Str()))))),
				contracts.Meth("m", contracts.NewMethod(contracts.Str()))),
			variant("reader", state(contracts.Prop("notes", contracts.Arr(contracts.Obj(contracts.Prop("text", contracts.Str()))))),
				contracts.Meth("m", contracts.NewMethod(contracts.Str())))))
	})
}

func TestReservedMemberNames(t *testing.T) {
	for _, name := range []string{"then", "release", "variant", "state", "retry"} {
		t.Run("refuses "+name+" in remote, local and evolve", func(t *testing.T) {
			_, err := declareRemote("n", 1, noParams(), variant("v", state(contracts.Prop(name, contracts.Str()))))
			wantError(t, err, `n.v.state.`+name+`: "`+name+`" is reserved: a reference has it`)
			_, err = contracts.Local("n", contracts.LocalDeclaration{Version: 1, Params: noParams(), Members: []contracts.LocalEntry{contracts.Mem(name, contracts.NewMethod())}})
			wantError(t, err, `n.members.`+name+`: "`+name+`" is reserved: a reference has it`)
			base := local(t, "n", 1, noParams())
			_, err = contracts.Evolve(base, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.AddState(name, contracts.Str()) })
			wantError(t, err, `n v2.members.`+name+`: "`+name+`" is reserved: a reference has it`)
		})
	}
}

func TestKeyedMembers(t *testing.T) {
	note := func() contracts.Schema { return contracts.Obj(contracts.Prop("text", contracts.Str())) }

	t.Run("declare a keyed member in a local contract: its own kind and a node of its own in the fingerprint", func(t *testing.T) {
		keyed := local(t, "app.store", 1, noParams(), contracts.Mem("notes", contracts.Keyed(note())), contracts.Mem("add", contracts.NewMethod()))
		plain := local(t, "app.store", 1, noParams(), contracts.Mem("notes", note()), contracts.Mem("add", contracts.NewMethod()))
		variant := currentVariants(t, keyed).Value(contracts.LocalVariant)
		if k, s := keys(variant.Keyed), keys(variant.State); len(k) != 1 || k[0] != "notes" || len(s) != 0 {
			t.Fatalf("keyed = %v, state = %v", k, s)
		}
		if keyed.Fingerprint == plain.Fingerprint {
			t.Fatal("a keyed member and a state member share a fingerprint")
		}
		// Evolving the contract carries its keyed members over unchanged (an edit changes them: slice 7b).
		next := must(contracts.Evolve(keyed, func(e *contracts.LocalEditor) *contracts.LocalEditor {
			return e.AddMethod("clear", contracts.Str(), contracts.Str())
		}))
		if !currentVariants(t, next).Value(contracts.LocalVariant).Keyed.Has("notes") {
			t.Fatal("evolve dropped the keyed member")
		}
	})
}

func TestQueriesMembers(t *testing.T) {
	note := func() contracts.Schema { return contracts.Obj(contracts.Prop("text", contracts.Str())) }
	args := func() []contracts.Property {
		return []contracts.Property{
			contracts.Prop("ofBoard", contracts.Obj(contracts.Prop("board", contracts.Str()))),
			contracts.Prop("one", contracts.Obj(contracts.Prop("id", contracts.Int()))),
		}
	}

	t.Run("declare named queries in a local contract: their own kind, each query's argument schema in the fingerprint", func(t *testing.T) {
		store := local(t, "app.store", 1, noParams(), contracts.Mem("notes", contracts.Queries(note(), args())), contracts.Mem("add", contracts.NewMethod()))
		variant := currentVariants(t, store).Value(contracts.LocalVariant)
		if got := keys(variant.Queries.Value("notes").Args); len(got) != 2 || got[0] != "ofBoard" || got[1] != "one" {
			t.Fatalf("queries = %v", got)
		}
		if got := keys(variant.State); len(got) != 0 {
			t.Fatalf("state = %v", got)
		}
		if kind := dataOf(t, store).Kinds["notes"]; kind != contracts.MemberKindQueries {
			t.Fatalf("the kind of notes = %q", kind)
		}
		other := args()
		other[1] = contracts.Prop("one", contracts.Obj(contracts.Prop("id", contracts.Str())))
		changed := local(t, "app.store", 1, noParams(), contracts.Mem("notes", contracts.Queries(note(), other)), contracts.Mem("add", contracts.NewMethod()))
		if changed.Fingerprint == store.Fingerprint {
			t.Fatal("a changed argument schema left the fingerprint")
		}
		// A contract without one keeps its fingerprint: queries_fingerprint_test.go pins it against Pi.
		// Evolving the contract carries its queries members over unchanged (their edits are a later slice's).
		next := must(contracts.Evolve(store, func(e *contracts.LocalEditor) *contracts.LocalEditor {
			return e.AddMethod("clear", contracts.Str(), contracts.Str())
		}))
		if !currentVariants(t, next).Value(contracts.LocalVariant).Queries.Has("notes") {
			t.Fatal("evolve dropped the queries member")
		}
		_, err := contracts.Evolve(store, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.AddState("notes", contracts.Str()) })
		wantError(t, err, "already declared")
	})

	t.Run("refuse argument schemas that are not schemas or not closed", func(t *testing.T) {
		// Upstream's `5 as never` is no Go input (a query's arguments are Schemas); an opt(...) is the Go case of a value that is no schema.
		_, err := contracts.Local("app.store", contracts.LocalDeclaration{Version: 1, Params: noParams(), Members: []contracts.LocalEntry{
			contracts.Mem("notes", contracts.Queries(note(), []contracts.Property{contracts.Prop("one", contracts.Opt(contracts.Str()))})),
		}})
		wantError(t, err, "app.store.members.notes.args.one: expected a schema")
		_, err = contracts.Local("app.store", contracts.LocalDeclaration{Version: 1, Params: noParams(), Members: []contracts.LocalEntry{
			contracts.Mem("notes", contracts.Queries(note(), []contracts.Property{contracts.Prop("one", tbObject())})),
		}})
		wantError(t, err, "app.store.members.notes.args.one: argument objects must be closed")
		_, err = contracts.Local("app.store", contracts.LocalDeclaration{Version: 1, Params: noParams(), Members: []contracts.LocalEntry{
			contracts.Mem("notes", contracts.Queries(raw(t, `{"not":{}}`), []contracts.Property{})),
		}})
		wantError(t, err, `app.store.members.notes.item: unsupported keyword "not"`)
	})

	t.Run("record their kind: a reserved name is refused, and so are a nil member and absent arguments", func(t *testing.T) {
		_, err := contracts.Local("app.store", contracts.LocalDeclaration{Version: 1, Params: noParams(), Members: []contracts.LocalEntry{
			contracts.Mem("then", contracts.Queries(note(), []contracts.Property{})),
		}})
		wantError(t, err, `app.store.members.then: "then" is reserved: a reference has it`)
		_, err = contracts.Local("app.store", contracts.LocalDeclaration{Version: 1, Params: noParams(), Members: []contracts.LocalEntry{
			contracts.Mem("notes", (*contracts.QueriesMember)(nil)),
		}})
		wantError(t, err, "app.store.members.notes: expected a schema, method(...) or keyed(...)")
		_, err = contracts.Local("app.store", contracts.LocalDeclaration{Version: 1, Params: noParams(), Members: []contracts.LocalEntry{
			contracts.Mem("notes", contracts.Queries(note(), nil)),
		}})
		wantError(t, err, "app.store.members.notes.args: expected an object")
	})
}

func TestHostKeyedMembers(t *testing.T) {
	meta := func() contracts.Schema { return contracts.Obj(contracts.Prop("n", contracts.Int())) }

	t.Run("carry their host kind in the data and the fingerprint; string-keyed ones carry none", func(t *testing.T) {
		byAddress := local(t, "store", 1, noParams(), contracts.Mem("meta", contracts.Keyed(meta(), session)))
		byString := local(t, "store", 1, noParams(), contracts.Mem("meta", contracts.Keyed(meta())))
		member := func(contract *contracts.Contract) *contracts.KeyedSnapshot {
			return currentVariants(t, contract).Value(contracts.LocalVariant).Keyed.Value("meta")
		}
		if member(byAddress).Key != session {
			t.Fatal("the host-keyed member does not carry its kind")
		}
		if member(byString).Key != nil {
			t.Fatal("the string-keyed member carries a kind")
		}
		if byAddress.Fingerprint == byString.Fingerprint {
			t.Fatal("a host-keyed and a string-keyed member share a fingerprint")
		}
	})

	t.Run("keep the fingerprint of a string-keyed member as it was before host keys", func(t *testing.T) {
		byString := local(t, "store", 1, noParams(), contracts.Mem("notes", contracts.Keyed(contracts.Obj(contracts.Prop("text", contracts.Str())))))
		if byString.Fingerprint != "bf6ff30e1483251d" {
			t.Fatalf("fingerprint = %s", byString.Fingerprint)
		}
	})

	t.Run("refuse a kind that was not declared", func(t *testing.T) {
		_, err := contracts.Local("store", contracts.LocalDeclaration{Version: 1, Params: noParams(), Members: []contracts.LocalEntry{
			contracts.Mem("meta", contracts.Keyed(contracts.Obj(), &contracts.HostKind{Kind: "x"})),
		}})
		wantError(t, err, "x: not a declared host kind")
	})
}

// TestAdmission ports `admission (slice 14 §2.1)` of declare.test.ts. Designed out: the `expectTypeOf<AdmittedOf<...>>`
// lines (TypeScript types only: a Go factory receives the value as delta.JsonValue), and `untyped.local(... admission)`
// refused as an unknown key (a LocalDeclaration has no such field).
func TestAdmission(t *testing.T) {
	variants := []contracts.VariantEntry{variant("a", state(contracts.Prop("n", contracts.Int())))}
	declare := func(admission *contracts.Schema) (*contracts.Contract, error) {
		return contracts.Remote("doc", contracts.RemoteDeclaration{Version: 1, Params: noParams(), HostAddress: serverAddress, Admission: admission, Variants: variants})
	}
	schema := func(properties ...contracts.Property) *contracts.Schema {
		schema := contracts.Obj(properties...)
		return &schema
	}

	t.Run("is never part of the fingerprint, and has its own that a reload's staging compares", func(t *testing.T) {
		plain := must(declare(nil))
		admitted := must(declare(schema(contracts.Prop("mode", contracts.Str()))))
		if admitted.Fingerprint != plain.Fingerprint {
			t.Fatalf("fingerprints %s and %s differ", admitted.Fingerprint, plain.Fingerprint)
		}
		other := must(declare(schema(contracts.Prop("mode", contracts.Int()))))
		if dataOf(t, admitted).Admission.Fingerprint == dataOf(t, other).Admission.Fingerprint {
			t.Fatal("two admission schemas share a fingerprint")
		}
		if dataOf(t, plain).Admission != nil {
			t.Fatal("a contract without an admission has one")
		}
	})

	t.Run("is JSON only", func(t *testing.T) {
		_, err := declare(schema(contracts.Prop("data", contracts.Bytes())))
		wantError(t, err, "doc.admission: an admission's value is JSON only (no bytes())")
	})

	t.Run("is replaced by evolve's Admission(schema), the last edit of a version; the wire fingerprint stays an edit's", func(t *testing.T) {
		v1 := must(declare(schema(contracts.Prop("mode", contracts.Str()))))
		v2 := must(contracts.Evolve(v1, func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.AddState("a", "m", contracts.Int()) }))
		v3 := must(contracts.Evolve(v2, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.Admission(contracts.Obj(contracts.Prop("mode", contracts.Str()), contracts.Prop("since", contracts.Int())))
		}))
		// Carried over unchanged by an edit that is not its own.
		if dataOf(t, v2).Admission != dataOf(t, v1).Admission {
			t.Fatal("an edit of the members replaced the admission")
		}
		if dataOf(t, v3).Admission.Fingerprint == dataOf(t, v1).Admission.Fingerprint {
			t.Fatal("Admission(schema) kept the fingerprint")
		}
		// A version whose only edit is its admission has the previous version's members.
		if v3.Fingerprint != v2.Fingerprint {
			t.Fatalf("fingerprints %s and %s differ", v3.Fingerprint, v2.Fingerprint)
		}
		gained := must(contracts.Evolve(must(declare(nil)), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.Admission(contracts.Obj(contracts.Prop("mode", contracts.Str())))
		}))
		if dataOf(t, gained).Admission == nil {
			t.Fatal("a contract without one did not gain one")
		}
	})
}
