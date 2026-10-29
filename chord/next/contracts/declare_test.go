package contracts_test

import (
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/test/next/contracts/declare.test.ts.
//
// Designed out (no Go input: a declaration is a typed struct): a contract name that is no string; an unknown
// declaration or variant key; a method placed under `state`; a schema placed under `methods` (the Go form is a nil
// *Method, covered below); an unknown key of a local declaration, and a host address in one (a LocalDeclaration has no
// such field); a keyed member under a remote variant's state (VariantDeclaration.State holds []Property of TSchema, and
// *KeyedMember is not a TSchema: a compile error), so the @ts-expect-error variants of those tests have nothing to
// refuse at runtime. The missing host address is a runtime test (TestDeclarationTypeChecksAtRuntime).
//
// A nil slice is upstream's absent field (`missing params`, `no variants`); an empty non-nil one is `{}`.

func TestDeclarationChecks(t *testing.T) {
	note := func() contracts.TSchema { return contracts.Obj(contracts.Prop("text", contracts.Str())) }

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
			variant("a", nil, contracts.Meth("m", contracts.NewMethod(contracts.TSchema{}, raw(t, `{"not":{}}`))))}}, `n.a.methods.m.result: unsupported keyword "not"`},
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
	note := func() contracts.TSchema { return contracts.Obj(contracts.Prop("text", contracts.Str())) }

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
		// Evolving the contract carries its keyed members over unchanged (their versions are slice 7's).
		next := must(contracts.Evolve(keyed, func(e *contracts.LocalEditor) *contracts.LocalEditor {
			return e.AddMethod("clear", contracts.Str(), contracts.Str())
		}))
		if !currentVariants(t, next).Value(contracts.LocalVariant).Keyed.Has("notes") {
			t.Fatal("evolve dropped the keyed member")
		}
	})
}
