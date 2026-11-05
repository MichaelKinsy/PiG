package contracts_test

import (
	"reflect"
	"testing"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/contracts/host-kind.test.ts. The `hostAddress` typing case is ported as a runtime
// test in derived_runtime_test.go (TestHostAddressParams): a HostAddress takes an untyped *delta.JsonObject.

var (
	session = mustKind("session", contracts.Prop("id", contracts.Str()))
	shard   = mustKind("shard",
		contracts.Prop("region", contracts.OneOf("eu", "us")),
		contracts.Prop("n", contracts.Int(contracts.Min(0))))
)

func TestHostKinds(t *testing.T) {
	t.Run("make addresses: the kind, the params and a key built like an identity key", func(t *testing.T) {
		if got := server.Address(nil); !reflect.DeepEqual(got, contracts.Address{Kind: "server", Params: delta.NewJsonObject(0), Key: "server"}) {
			t.Fatalf("server address = %+v", got)
		}
		id := delta.JsonObjectOf("id", "S1")
		if got := session.Address(id); !reflect.DeepEqual(got, contracts.Address{Kind: "session", Params: id, Key: "session|S1"}) {
			t.Fatalf("session address = %+v", got)
		}
		if got := shard.Address(delta.JsonObjectOf("region", "eu", "n", 3.0)).Key; got != "shard|eu|3" {
			t.Fatalf("shard key = %q", got)
		}
		// Escaped like identity keys: two different param lists never make one key.
		escaped := session.Address(delta.JsonObjectOf("id", "a|b")).Key
		if other := shard.Address(delta.JsonObjectOf("region", "a", "n", "b")).Key; escaped == other {
			t.Fatalf("keys collide: %q", escaped)
		}
		if escaped != `session|a\|b` {
			t.Fatalf("key = %q", escaped)
		}
	})

	t.Run("make keys from the values as JavaScript's String does", func(t *testing.T) {
		kind := mustKind("k", contracts.Prop("a", contracts.Str()), contracts.Prop("b", contracts.Str()), contracts.Prop("c", contracts.Str()))
		values := delta.JsonObjectOf("a", 1.5, "b", nil, "c", true)
		if got := kind.Address(values).Key; got != "k|1.5|null|true" {
			t.Fatalf("key = %q", got)
		}
		// An absent field is String(undefined).
		if got := kind.Address(delta.JsonObjectOf("a", "x")).Key; got != "k|x|undefined|undefined" {
			t.Fatalf("key = %q", got)
		}
	})

	t.Run("follow the params policy of contracts at declaration", func(t *testing.T) {
		_, err := contracts.NewHostKind("k", contracts.Prop("id", contracts.Opt(contracts.Str())))
		wantError(t, err, "k.params.id: a param is")
		_, err = contracts.NewHostKind("")
		wantError(t, err, `A host kind's name "": expected lowercase [a-z0-9@/._~-]`)
		_, err = contracts.NewHostKind("Session")
		wantError(t, err, "expected lowercase")
		_, err = contracts.NewHostKind("a:b")
		wantError(t, err, "expected lowercase")
		_, err = contracts.NewHostKind("client")
		wantError(t, err, `"client" is reserved for hosts without an address`)
		if kind := must(contracts.NewHostKind("@pi/session.v2_~-")); kind.Kind != "@pi/session.v2_~-" {
			t.Fatalf("kind = %q", kind.Kind)
		}
	})

	t.Run("declare params: fields in order, the closed object schema and the normal form", func(t *testing.T) {
		params := must(contracts.KindParams(shard))
		if !reflect.DeepEqual(params.Fields, []string{"region", "n"}) {
			t.Fatalf("fields = %v", params.Fields)
		}
		sameJSON(t, params.Schema, `{"type":"object","properties":{"region":{"anyOf":[{"type":"string","const":"eu"},{"type":"string","const":"us"}]},"n":{"type":"integer","minimum":0}},"required":["region","n"],"additionalProperties":false}`)
		if got := stringify(t, params.Normal); got != `[["region",{"anyOf":[{"const":"eu","type":"string"},{"const":"us","type":"string"}]}],["n",{"minimum":0,"type":"integer"}]]` {
			t.Fatalf("normal = %s", got)
		}
	})

	t.Run("refuse a hand-built host kind", func(t *testing.T) {
		_, err := contracts.KindParams(&contracts.HostKind{Kind: "fake"})
		wantError(t, err, "fake: not a declared host kind")
		_, err = contracts.KindParams(nil)
		wantError(t, err, "not a declared host kind")
	})

	t.Run("check an address's params where it is routed, with the kind's compiled check: invalid/host_address", func(t *testing.T) {
		params := must(contracts.KindParams(session))
		ctx := chordctx.Background()
		address, err := data.CheckAddress("session", params, delta.JsonObjectOf("id", "S1"), ctx)
		wantNoError(t, err)
		if address.Key != "session|S1" {
			t.Fatalf("key = %q", address.Key)
		}
		for name, bad := range map[string]any{
			"a number": delta.JsonObjectOf("id", 1.0),
			"missing":  delta.NewJsonObject(0),
			"extra":    delta.JsonObjectOf("id", "S1", "extra", 1.0),
			"null":     nil,
		} {
			_, err := data.CheckAddress("session", params, bad, ctx)
			chordErr, ok := data.IsChordErrorCode(err, data.CodeInvalid)
			if !ok || chordErr.Reason != data.ReasonHostAddress {
				t.Errorf("%s: error = %v, want invalid/host_address", name, err)
			}
		}
	})

	t.Run("keep their params data only while the kind is referenced", func(t *testing.T) {
		kind, params := func() (weak.Pointer[contracts.HostKind], weak.Pointer[contracts.ParamsData]) {
			temp := must(contracts.NewHostKind("temp", contracts.Prop("id", contracts.Str())))
			return weak.Make(temp), weak.Make(must(contracts.KindParams(temp)))
		}()
		nexttest.ExpectCollected(t, kind)
		nexttest.ExpectCollected(t, params)
	})
}

func TestHostAddress(t *testing.T) {
	variants := variant("v", state(contracts.Prop("n", contracts.Int())))
	byParams := func(params *delta.JsonObject) (contracts.Address, error) {
		return session.Address(delta.JsonObjectOf("id", params.Value("session"))), nil
	}
	declare := func(address contracts.HostAddress) *contracts.Contract {
		return must(contracts.Remote("app.placed", contracts.RemoteDeclaration{
			Version:     1,
			Params:      props(contracts.Prop("session", contracts.Str())),
			HostAddress: address,
			Variants:    []contracts.VariantEntry{variants},
		}))
	}

	t.Run("is not part of the fingerprint and is carried by evolve", func(t *testing.T) {
		onSession := declare(byParams)
		onServer := declare(serverAddress)
		if onSession.Fingerprint != onServer.Fingerprint {
			t.Fatalf("fingerprints differ: %s %s", onSession.Fingerprint, onServer.Fingerprint)
		}
		same := func(contract *contracts.Contract) {
			t.Helper()
			address := dataOf(t, contract).HostAddress
			if reflect.ValueOf(address).Pointer() != reflect.ValueOf(byParams).Pointer() {
				t.Fatal("the contract does not carry the declared hostAddress")
			}
			got := must(address(delta.JsonObjectOf("session", "S9")))
			if got.Key != "session|S9" {
				t.Fatalf("address = %+v", got)
			}
		}
		same(onSession)
		next := must(contracts.Evolve(onSession, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.AddState("v", "m", contracts.Int())
		}))
		same(next)
	})
}
