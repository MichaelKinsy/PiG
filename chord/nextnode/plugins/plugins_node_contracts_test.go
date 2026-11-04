package plugins

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Not upstream's (docs/plan/chord-plugins-node-evaluator.md 10.2): a contract crosses to the Node host as data and the host's
// Pi declares its own contract of it. The oracle is the fingerprint: Pi's `materialize` in the Node host and the Go package's
// `Materialize` hash the same normal form, so the contract the Node host rebuilds must hash to the Go contract's.

var pcItemContract = mustContract(contracts.Local("test.item", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("label", contracts.Str())},
}))

// cdAddress is the host address of the remote contracts of this file's table: it is not part of a fingerprint.
func cdAddress(*delta.JsonObject) (contracts.Address, error) { return workerKind.Address(nil), nil }

// cdContracts is every contract kind and member kind the contracts package declares: the fixtures' `Index`, `Notes` and `Item`,
// a local contract of every schema constructor and of state, method and keyed members, a remote contract with several
// variants and every kind of params field, an evolved contract, and raw schemas (`$defs` and `$ref`, annotations, `enum`,
// `const`, a bytes leaf, an `Opt` field).
func cdContracts(t *testing.T) map[string]*contracts.Contract {
	t.Helper()
	out := map[string]*contracts.Contract{"Index": pcIndexContract, "Notes": notesContract, "Item": pcItemContract}

	allSchemas := mustContract(contracts.Local("test.every-schema", contracts.LocalDeclaration{
		Version: 3,
		Params: contracts.Params{
			contracts.Prop("id", contracts.Str(contracts.Min(1), contracts.Max(64), contracts.Pattern("^[a-z]+$"))),
			contracts.Prop("n", contracts.Int(contracts.Min(0), contracts.Max(9))),
			contracts.Prop("on", contracts.Bool()),
			contracts.Prop("mode", contracts.OneOf("read", "write")),
		},
		Members: []contracts.LocalEntry{
			contracts.Mem("text", contracts.Str(contracts.Default("x"))),
			contracts.Mem("number", contracts.Num(contracts.Min(-1.5), contracts.Max(1e3))),
			contracts.Mem("integer", contracts.Int(contracts.Default(7))),
			contracts.Mem("flag", contracts.Bool(contracts.Default(true))),
			contracts.Mem("nothing", contracts.Nul()),
			contracts.Mem("exactly", contracts.Lit("a")),
			contracts.Mem("either", contracts.Union([]contracts.Schema{contracts.Str(), contracts.Int()})),
			contracts.Mem("one", contracts.OneOf(1, 2, 3)),
			contracts.Mem("list", contracts.Arr(contracts.Str(), contracts.Min(1), contracts.Max(5))),
			contracts.Mem("record", contracts.Map(contracts.Int())),
			contracts.Mem("shape", contracts.Obj(
				contracts.Prop("b", contracts.Int()), contracts.Prop("a", contracts.Opt(contracts.Str())), contracts.Prop("10", contracts.Bool()),
			)),
			contracts.Mem("anything", contracts.Unknown()),
			contracts.Mem("blob", contracts.Bytes(contracts.Min(1), contracts.Max(16))),
			contracts.Mem("noargs", contracts.NewMethod()),
			contracts.Mem("argsonly", contracts.NewMethod(contracts.Obj(contracts.Prop("k", contracts.Str())))),
			contracts.Mem("both", contracts.NewMethod(contracts.Arr(contracts.Int()), contracts.Str())),
			contracts.Mem("resultonly", contracts.NewMethod(contracts.Schema{}, contracts.Num())),
			contracts.Mem("docs", contracts.Keyed(contracts.Obj(contracts.Prop("title", contracts.Str())))),
			contracts.Mem("blobs", contracts.Keyed(contracts.Bytes())),
		},
	}))
	out["every schema constructor, every member kind, every params kind"] = allSchemas

	raw := func(pairs ...any) contracts.Schema { return contracts.Schema{Node: delta.JsonObjectOf(pairs...)} }
	out["raw schemas"] = mustContract(contracts.Local("test.raw", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("tree", raw(
				"$ref", "#/$defs/node",
				"$defs", delta.JsonObjectOf("node", delta.JsonObjectOf(
					"type", "object",
					"properties", delta.JsonObjectOf("value", delta.JsonObjectOf("type", "integer"), "next", delta.JsonObjectOf("$ref", "#/$defs/node")),
					"required", []any{"value"},
					"additionalProperties", false,
				)),
			)),
			contracts.Mem("annotated", raw("type", "string", "title", "dropped", "description", "dropped", "examples", []any{"b", "a"})),
			contracts.Mem("enumerated", raw("enum", []any{"b", "a", float64(2), delta.JsonObjectOf("z", 1.0, "a", 2.0)})),
			contracts.Mem("constant", raw("const", delta.JsonObjectOf("y", []any{1.0, 2.0}, "x", nil))),
			contracts.Mem("bounded", raw("type", "number", "exclusiveMinimum", 0.0, "multipleOf", 0.25)),
			contracts.Mem("boolean schema", contracts.Schema{Node: true}),
		},
	}))

	out["remote, two variants, every params kind"] = mustContract(contracts.Remote("test.remote-variants", contracts.RemoteDeclaration{
		Version: 2,
		Params: contracts.Params{
			contracts.Prop("who", contracts.Str()), contracts.Prop("n", contracts.Int()),
			contracts.Prop("kind", contracts.OneOf("a", "b")), contracts.Prop("on", contracts.Bool()),
		},
		HostAddress: cdAddress,
		Variants: []contracts.VariantEntry{
			contracts.Var("admin", contracts.VariantDeclaration{
				State:   []contracts.Property{contracts.Prop("label", contracts.Str()), contracts.Prop("secret", contracts.Obj(contracts.Prop("k", contracts.Str())))},
				Methods: []contracts.MethodEntry{contracts.Meth("add", contracts.NewMethod(contracts.Int(), contracts.Int())), contracts.Meth("reset", contracts.NewMethod())},
			}),
			contracts.Var("user", contracts.VariantDeclaration{
				State:   []contracts.Property{contracts.Prop("label", contracts.Str())},
				Methods: []contracts.MethodEntry{contracts.Meth("add", contracts.NewMethod(contracts.Int(), contracts.Int()))},
			}),
		},
	}))

	out["remote, no params and a variant with no members"] = mustContract(contracts.Remote("test.remote-empty", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{}, HostAddress: cdAddress,
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{})},
	}))

	evolved, err := contracts.Evolve(pcIndexContract, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.AddState("size", contracts.Int()).AddMethod("rename", contracts.Str(), contracts.Str())
	})
	if err != nil {
		t.Fatal(err)
	}
	out["local, evolved to version 2"] = evolved
	remoteEvolved, err := contracts.Evolve(notesContract, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
		return e.AddState("main", "extra", contracts.Bool())
	})
	if err != nil {
		t.Fatal(err)
	}
	out["remote, evolved to version 2"] = remoteEvolved
	return out
}

// ContractDataOf is the normal form the fingerprint hashes: the data hashes to the contract's fingerprint in Go, without a
// Node host.
func TestContractDataIsTheNormalFormTheFingerprintHashes(t *testing.T) {
	for name, contract := range cdContracts(t) {
		data, err := ContractDataOf(contract)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if data.Name != contract.Name || data.Version != contract.Version || data.Fingerprint != contract.Fingerprint {
			t.Fatalf("%s: data = %+v, contract = %s v%d %s", name, data, contract.Name, contract.Version, contract.Fingerprint)
		}
		hashed, err := contracts.FingerprintOf(data.Normal)
		if err != nil {
			t.Fatal(err)
		}
		if hashed != contract.Fingerprint {
			t.Fatalf("%s: the normal form hashes to %s, the contract's fingerprint is %s", name, hashed, contract.Fingerprint)
		}
	}
}

func TestContractDataRefusesAValueThatIsNoDeclaredContract(t *testing.T) {
	copied := *pcIndexContract
	if _, err := ContractDataOf(&copied); err == nil {
		t.Fatal("a copy of a contract was accepted")
	}
	if _, err := ContractDataOf(nil); err == nil {
		t.Fatal("nil was accepted")
	}
}

// The Node host rebuilds every contract (control `describe`, node/contracts.mjs `contractOf`) with the same kind, name,
// version and fingerprint: its Pi's `materialize` and the Go package's hash one normal form.
func TestNodeRebuildsEveryContractWithTheSameFingerprint(t *testing.T) {
	node, _, _ := loadOn(t, handmade(t, "@test/p", "worker", emptyFacet))
	for name, contract := range cdContracts(t) {
		got, err := node.Describe(contract, background)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := ContractDescription{Kind: contract.Kind, Name: contract.Name, Version: contract.Version, Fingerprint: contract.Fingerprint}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the Node host rebuilt %+v, want %+v", name, got, want)
		}
	}
}
