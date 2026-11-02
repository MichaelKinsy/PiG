package data_test

import (
	"context"
	"testing"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest/contractstest"
)

// Ports packages/chord/test/next/data/params.test.ts.
//
// Designed out: the expectTypeOf assertion of "returns the caller's own object ..." (CheckedParams.Params is the
// caller's value as `any`; Go has no per-contract params type) and the two @ts-expect-error assertions of "local
// objects take no principal key, by type" (IdentityKey takes its principal key variadically; nothing refuses one for
// a local contract).

func mustRemote(t testing.TB, name string, declaration contracts.RemoteDeclaration) *contracts.Contract {
	t.Helper()
	contract, err := contracts.Remote(name, declaration)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func mustLocal(t testing.TB, name string, declaration contracts.LocalDeclaration) *contracts.Contract {
	t.Helper()
	contract, err := contracts.Local(name, declaration)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func session(t testing.TB) *contracts.Contract {
	return mustRemote(t, "session", contracts.RemoteDeclaration{
		Version: 1,
		Params: contracts.Params{
			contracts.Prop("id", contracts.Str(contracts.Min(1))),
			contracts.Prop("mode", contracts.OneOf("chat", "agent")),
			contracts.Prop("page", contracts.Int(contracts.Min(0))),
			contracts.Prop("open", contracts.Bool()),
		},
		HostAddress: contractstest.ServerAddress,
		Variants:    []contracts.VariantEntry{contracts.Var("owner", contracts.VariantDeclaration{})},
	})
}

func validSession() *delta.JsonObject {
	return delta.JsonObjectOf("id", "S1", "mode", "chat", "page", 2.0, "open", true)
}

// refusal is the `invalid/params` error of a refused check.
func refusal(t *testing.T, contract *contracts.Contract, raw any) *checkFailure {
	t.Helper()
	_, err := data.CheckParams(contract, raw, chordctx.Background())
	if err == nil {
		t.Fatal("expected a refusal")
	}
	chordErr, ok := data.IsChordErrorCode(err, data.CodeInvalid)
	if !ok {
		t.Fatalf("unexpected error %v", err)
	}
	return &checkFailure{Reason: chordErr.Reason, Message: chordErr.Message, Details: chordErr.Details}
}

func requireRefusal(t *testing.T, contract *contracts.Contract, raw any, path string) {
	t.Helper()
	if failed := refusal(t, contract, raw); detailsPath(failed) != path {
		t.Fatalf("refusal = %+v, want path %q", failed, path)
	}
}

func TestCheckParams(t *testing.T) {
	Session := session(t)

	t.Run("returns the caller's own object and the key of the field values in declared order", func(t *testing.T) {
		valid := validSession()
		checked, err := data.CheckParams(Session, valid, chordctx.Background())
		if err != nil {
			t.Fatal(err)
		}
		if checked.Params != valid || checked.Key != "S1|chat|2|true" {
			t.Fatalf("checked = %+v", checked)
		}
	})

	t.Run("gives the same key whatever the property order", func(t *testing.T) {
		checked, err := data.CheckParams(Session, delta.JsonObjectOf("open", true, "page", 2.0, "mode", "chat", "id", "S1"), chordctx.Background())
		if err != nil || checked.Key != "S1|chat|2|true" {
			t.Fatalf("checked = %+v, %v", checked, err)
		}
	})

	t.Run("refuses missing, extra and wrong fields at their paths", func(t *testing.T) {
		missing := validSession()
		missing.Delete("id")
		failed := refusal(t, Session, missing)
		if failed.Reason != data.ReasonParams || len(failed.Message) < 11 || failed.Message[:11] != "params/id: " {
			t.Fatalf("missing = %+v", failed)
		}
		nexttest.RequireJSONEqual(t, failed.Details, delta.JsonObjectOf("path", "/id"))

		extra := validSession()
		extra.Set("extra", 1.0)
		failed = refusal(t, Session, extra)
		if len(failed.Message) < 14 || failed.Message[:14] != "params/extra: " || detailsPath(failed) != "/extra" {
			t.Fatalf("extra = %+v", failed)
		}

		wrong := validSession()
		wrong.Set("page", "2")
		failed = refusal(t, Session, wrong)
		if failed.Message != "params/page: must be integer" || detailsPath(failed) != "/page" {
			t.Fatalf("wrong = %+v", failed)
		}

		failed = refusal(t, Session, "S1")
		if failed.Message != "params: must be object" || detailsPath(failed) != "/" {
			t.Fatalf("not an object = %+v", failed)
		}
	})

	t.Run("enforces literal membership and constraints", func(t *testing.T) {
		with := func(key string, value any) *delta.JsonObject {
			object := validSession()
			object.Set(key, value)
			return object
		}
		requireRefusal(t, Session, with("mode", "other"), "/mode")
		requireRefusal(t, Session, with("id", ""), "/id")
		requireRefusal(t, Session, with("page", -1.0), "/page")
		requireRefusal(t, Session, with("page", 1.5), "/page")
	})

	t.Run("escapes the separator: no two params join to one key", func(t *testing.T) {
		Pair := mustLocal(t, "pair", contracts.LocalDeclaration{
			Version: 1,
			Params:  contracts.Params{contracts.Prop("a", contracts.Str()), contracts.Prop("b", contracts.Str())},
			Members: []contracts.LocalEntry{},
		})
		var keys []string
		for _, raw := range [][2]string{{"x|y", "z"}, {"x", "y|z"}, {"x\\", "|z"}, {"x\\|", "z"}} {
			checked, err := data.CheckParams(Pair, delta.JsonObjectOf("a", raw[0], "b", raw[1]), chordctx.Background())
			if err != nil {
				t.Fatal(err)
			}
			keys = append(keys, checked.Key)
		}
		seen := map[string]bool{}
		for _, key := range keys {
			seen[key] = true
		}
		if len(seen) != len(keys) || keys[0] != "x\\|y|z" {
			t.Fatalf("keys = %q", keys)
		}
	})

	t.Run("counts one compilation per contract, then one validation per get; a failure adds one walk", func(t *testing.T) {
		Fresh := mustLocal(t, "fresh", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())}, Members: []contracts.LocalEntry{}})
		check := func(raw any) func(context.Context) {
			return func(ctx context.Context) { _, _ = data.CheckParams(Fresh, raw, ctx) }
		}
		if got, want := nexttest.Counted(t, check(delta.JsonObjectOf("id", "a"))), (nexttest.Counts{Validations: 1, Compilations: 1}); got != want {
			t.Fatalf("first = %+v, want %+v", got, want)
		}
		if got, want := nexttest.Counted(t, check(delta.JsonObjectOf("id", "b"))), (nexttest.Counts{Validations: 1}); got != want {
			t.Fatalf("second = %+v, want %+v", got, want)
		}
		if got, want := nexttest.Counted(t, check(delta.JsonObjectOf("id", 1.0))), (nexttest.Counts{Walks: 1, Validations: 1}); got != want {
			t.Fatalf("failed = %+v, want %+v", got, want)
		}
	})

	t.Run("does not retain a dropped contract's params validator", func(t *testing.T) {
		contractRef, validatorRef := func() (weak.Pointer[contracts.Contract], weak.Pointer[data.Validator]) {
			contract := mustLocal(t, "gc", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())}, Members: []contracts.LocalEntry{}})
			if _, err := data.CheckParams(contract, delta.JsonObjectOf("id", "a"), chordctx.Background()); err != nil {
				t.Fatal(err)
			}
			contractData, err := contracts.ContractDataOf(contract)
			if err != nil {
				t.Fatal(err)
			}
			validator, err := data.ValidatorFor(contractData.Params, chordctx.Background())
			if err != nil {
				t.Fatal(err)
			}
			return weak.Make(contract), weak.Make(validator)
		}()
		nexttest.ExpectCollected(t, contractRef)
		nexttest.ExpectCollected(t, validatorRef)
	})
}

func TestIdentityKey(t *testing.T) {
	t.Run("joins name, key and principal key, each escaped", func(t *testing.T) {
		Session := session(t)
		if got, want := data.IdentityKey(Session, "S1|chat|2|true", `{"kind":"user"}`), `session|S1\|chat\|2\|true|{"kind":"user"}`; got != want {
			t.Fatalf("IdentityKey = %s, want %s", got, want)
		}
	})

	t.Run("local objects take no principal key", func(t *testing.T) {
		Info := mustLocal(t, "info", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}})
		if got := data.IdentityKey(Info, ""); got != "info|" {
			t.Fatalf("IdentityKey = %q", got)
		}
	})
}
