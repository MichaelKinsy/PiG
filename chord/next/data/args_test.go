package data_test

import (
	"context"
	"strconv"
	"testing"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest/contractstest"
)

// Ports packages/chord/test/next/data/args.test.ts.
//
// Designed out (type level, no Go runtime form): the expectTypeOf assertions of "returns current-version arguments as
// passed" (CheckedArgs<C, M> and ArgsOf are Value in Go) and, for "types a call replacement's arguments as
// values" (CheckedArgs.Call is the contracts.CallFunc field type; the compiler rejects another signature), the
// type check in typelevel_test.go. The WeakRef of the mapping function in "memory" is a weak pointer to a value the mapping
// closure captures: a Go func value has no weak reference.

var (
	note   = contracts.Obj(contracts.Prop("text", contracts.Str()))
	noteV2 = contracts.Obj(contracts.Prop("text", contracts.Str()), contracts.Prop("tag", contracts.Str()))
)

func toV2Mapping(calls *int) contracts.Mapping {
	return func(old delta.JsonValue) (delta.JsonValue, error) {
		*calls++
		next := old.(*delta.JsonObject).Clone()
		next.Set("tag", "v1")
		return next, nil
	}
}

func resultToOld(current delta.JsonValue) (delta.JsonValue, error) {
	return strconv.FormatFloat(current.(float64), 'f', -1, 64), nil
}

func attachFunc(calls *int) contracts.CallFunc {
	return func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
		*calls++
		return "attached", nil
	}
}

func bind(t testing.TB, contract *contracts.Contract, accepts contracts.AcceptsTable) *data.Provider {
	t.Helper()
	provider, err := data.BindProvider(contract, accepts)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func evolveRemote(t testing.TB, prev *contracts.Contract, edit func(*contracts.RemoteEditor) *contracts.RemoteEditor) *contracts.Contract {
	t.Helper()
	contract, err := contracts.Evolve(prev, edit)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func evolveLocal(t testing.TB, prev *contracts.Contract, edit func(*contracts.LocalEditor) *contracts.LocalEditor) *contracts.Contract {
	t.Helper()
	contract, err := contracts.Evolve(prev, edit)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func entries[T any](pairs ...contracts.Entry[T]) []contracts.Entry[T] { return pairs }

type argsFixture struct {
	notesV1, notesV2, notesV3 *contracts.Contract
	accepts                   contracts.AcceptsTable
	provider                  *data.Provider
	// toV2Calls and attachCalls count the calls of the mappings, as upstream's vi.fn does.
	toV2Calls, attachCalls *int
}

func newArgsFixture(t *testing.T) *argsFixture {
	t.Helper()
	notesV1 := mustRemote(t, "notes", contracts.RemoteDeclaration{
		Version:     1,
		Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
		HostAddress: contractstest.ServerAddress,
		Variants: []contracts.VariantEntry{
			contracts.Var("editor", contracts.VariantDeclaration{
				State: []contracts.Property{contracts.Prop("notes", contracts.Arr(note))},
				Methods: []contracts.MethodEntry{
					contracts.Meth("add", contracts.NewMethod(note, contracts.Str())),
					contracts.Meth("clear", contracts.NewMethod()),
					contracts.Meth("attach", contracts.NewMethod(contracts.Obj(contracts.Prop("data", contracts.Bytes(contracts.Max(4)))))),
				},
			}),
			contracts.Var("reader", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("notes", contracts.Arr(note))}}),
		},
	})
	notesV2 := evolveRemote(t, notesV1, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
		return e.Args("add", noteV2).Result("add", contracts.Int())
	})
	notesV3 := evolveRemote(t, notesV2, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
		return e.RemoveMethod("attach")
	})
	toV2Calls, attachCalls := new(int), new(int)
	toV2, attach := toV2Mapping(toV2Calls), attachFunc(attachCalls)
	accepts := contracts.AcceptsTable{
		{Name: "1", Value: contracts.VersionMappings{Methods: entries(
			contracts.Entry[contracts.MethodMapping]{Name: "add", Value: contracts.MethodMapping{Args: toV2, Result: resultToOld}},
			contracts.Entry[contracts.MethodMapping]{Name: "attach", Value: contracts.MethodMapping{Call: attach}},
		)}},
		{Name: "2", Value: contracts.VersionMappings{Methods: entries(
			contracts.Entry[contracts.MethodMapping]{Name: "attach", Value: contracts.MethodMapping{Refuse: true}},
		)}},
	}
	return &argsFixture{notesV1: notesV1, notesV2: notesV2, notesV3: notesV3, accepts: accepts, provider: bind(t, notesV3, accepts), toV2Calls: toV2Calls, attachCalls: attachCalls}
}

// refused is the ChordError a refused check returns.
func refused(t *testing.T, checked data.CheckedArgs, err error) *data.ChordError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a refusal, got %+v", checked)
	}
	chordErr, ok := data.IsChordError(err)
	if !ok {
		t.Fatalf("unexpected error %v", err)
	}
	return chordErr
}

func requireArgsRefusal(t *testing.T, err *data.ChordError, code data.ChordErrorCode, reason data.ChordErrorReason, path string) {
	t.Helper()
	if err.Code != code || err.Reason != reason {
		t.Fatalf("refusal = %s/%s %q, want %s/%s", err.Code, err.Reason, err.Message, code, reason)
	}
	if path != "" {
		nexttest.RequireJSONEqual(t, err.Details, delta.JsonObjectOf("path", path))
	}
}

func checkArgs(t *testing.T, provider *data.Provider, method string, raw any, version int) (data.CheckedArgs, *data.ChordError) {
	t.Helper()
	checked, err := data.CheckArgs(provider, method, raw, version, chordctx.Background())
	if err == nil {
		return checked, nil
	}
	chordErr, ok := data.IsChordError(err)
	if !ok {
		t.Fatalf("unexpected error %v", err)
	}
	return checked, chordErr
}

func TestBindProvider(t *testing.T) {
	fixture := newArgsFixture(t)

	t.Run("checks the accepts table once and lists the accepted versions", func(t *testing.T) {
		if fixture.provider.Contract != fixture.notesV3 {
			t.Fatal("provider.Contract is not the bound contract")
		}
		want := []contracts.AcceptedVersion{
			{Version: 1, Fingerprint: fixture.notesV1.Fingerprint},
			{Version: 2, Fingerprint: fixture.notesV2.Fingerprint},
		}
		if got := fixture.provider.Accepted; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("Accepted = %+v, want %+v", got, want)
		}
		_, err := data.BindProvider(fixture.notesV3, contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{}}})
		if err == nil || err.Error() != "notes accepts[1].methods.add: missing mapping" {
			t.Fatalf("BindProvider error = %v", err)
		}
	})

	t.Run("compiles nothing and calls no mapping", func(t *testing.T) {
		counts := nexttest.Counted(t, func(context.Context) {
			if _, err := data.BindProvider(fixture.notesV3, fixture.accepts); err != nil {
				t.Error(err)
			}
		})
		if counts != (nexttest.Counts{}) {
			t.Fatalf("counts = %+v", counts)
		}
		if *fixture.toV2Calls != 0 {
			t.Fatalf("toV2 called %d times", *fixture.toV2Calls)
		}
	})
}

func TestBinds(t *testing.T) {
	fixture := newArgsFixture(t)
	provider := fixture.provider
	if provider.Accepts == nil || len(provider.Accepts) != len(fixture.accepts) {
		t.Fatalf("Accepts = %+v", provider.Accepts)
	}
	for _, c := range []struct {
		version     int
		fingerprint string
		want        bool
	}{
		{fixture.notesV3.Version, fixture.notesV3.Fingerprint, true},
		{fixture.notesV3.Version, fixture.notesV1.Fingerprint, false},
		{1, fixture.notesV1.Fingerprint, true},
		{2, fixture.notesV2.Fingerprint, true},
		{1, fixture.notesV2.Fingerprint, false},
		{9, fixture.notesV1.Fingerprint, false},
	} {
		if got := data.Binds(provider, c.version, c.fingerprint); got != c.want {
			t.Errorf("Binds(%d, %.8s) = %v, want %v", c.version, c.fingerprint, got, c.want)
		}
	}
}

func TestCheckArgs(t *testing.T) {
	fixture := newArgsFixture(t)
	provider := fixture.provider

	t.Run("returns current-version arguments as passed: no copy, never mapped", func(t *testing.T) {
		raw := delta.JsonObjectOf("text", "t", "tag", "x")
		checked, err := checkArgs(t, provider, "add", raw, 3)
		if err != nil {
			t.Fatal(err)
		}
		if checked.Kind != data.CheckedMethod || checked.Args != any(raw) || checked.Result != nil || checked.Call != nil {
			t.Fatalf("checked = %+v", checked)
		}
		if *fixture.toV2Calls != 0 {
			t.Fatalf("toV2 called %d times", *fixture.toV2Calls)
		}
	})

	t.Run("checks an old version against the old schema, then maps directly to the current shape", func(t *testing.T) {
		checked, err := checkArgs(t, provider, "add", delta.JsonObjectOf("text", "t"), 1)
		if err != nil {
			t.Fatal(err)
		}
		if checked.Kind != data.CheckedMethod {
			t.Fatalf("kind = %s", checked.Kind)
		}
		nexttest.RequireJSONEqual(t, checked.Args, delta.JsonObjectOf("text", "t", "tag", "v1"))
		mapped, mapErr := checked.Result(5.0)
		if mapErr != nil || mapped != "5" {
			t.Fatalf("Result(5) = %v, %v", mapped, mapErr)
		}
		_, refusal := checkArgs(t, provider, "add", delta.JsonObjectOf("text", "t", "tag", "x"), 1)
		requireArgsRefusal(t, refused(t, checked, refusal), data.CodeInvalid, data.ReasonArgs, "/tag")
		// Version 2 already has the current args: checked with that schema, not mapped.
		checked, err = checkArgs(t, provider, "add", delta.JsonObjectOf("text", "t", "tag", "x"), 2)
		if err != nil || checked.Kind != data.CheckedMethod || checked.Result != nil {
			t.Fatalf("v2 checked = %+v, %v", checked, err)
		}
		nexttest.RequireJSONEqual(t, checked.Args, delta.JsonObjectOf("text", "t", "tag", "x"))
	})

	t.Run("returns the old arguments with the call replacement; never invokes it", func(t *testing.T) {
		raw := delta.JsonObjectOf("data", make([]byte, 2))
		checked, err := checkArgs(t, provider, "attach", raw, 1)
		if err != nil {
			t.Fatal(err)
		}
		if checked.Kind != data.CheckedCall || checked.Args != any(raw) || checked.Call == nil || checked.Result != nil {
			t.Fatalf("checked = %+v", checked)
		}
		if *fixture.attachCalls != 0 {
			t.Fatal("the call replacement was invoked")
		}
		if result, callErr := checked.Call(nil, chordctx.Background()); callErr != nil || result != "attached" {
			t.Fatalf("Call = %v, %v", result, callErr)
		}
	})

	t.Run("refuses unknown fields, nested ones too, and checks bytes by type and length", func(t *testing.T) {
		checked, err := checkArgs(t, provider, "add", delta.JsonObjectOf("text", "t", "tag", "x", "extra", 1.0), 3)
		requireArgsRefusal(t, refused(t, checked, err), data.CodeInvalid, data.ReasonArgs, "/extra")
		checked, err = checkArgs(t, provider, "attach", delta.JsonObjectOf("data", []any{1.0, 2.0}), 1)
		requireArgsRefusal(t, refused(t, checked, err), data.CodeInvalid, data.ReasonArgs, "/data")
		checked, err = checkArgs(t, provider, "attach", delta.JsonObjectOf("data", make([]byte, 5)), 1)
		requireArgsRefusal(t, refused(t, checked, err), data.CodeInvalid, data.ReasonArgs, "/data")
		nested := mustLocal(t, "nested", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
			contracts.Mem("m", contracts.NewMethod(contracts.Obj(contracts.Prop("a", contracts.Obj(contracts.Prop("b", contracts.Str())))))),
		}})
		nestedProvider := bind(t, nested, nil)
		raw := delta.JsonObjectOf("a", delta.JsonObjectOf("b", "x", "c", 1.0))
		checked, err = checkArgs(t, nestedProvider, "m", raw, 1)
		requireArgsRefusal(t, refused(t, checked, err), data.CodeInvalid, data.ReasonArgs, "/a/c")
	})

	t.Run("takes null for no-argument methods", func(t *testing.T) {
		checked, err := checkArgs(t, provider, "clear", nil, 3)
		if err != nil || checked.Kind != data.CheckedMethod || checked.Args != nil || checked.Result != nil {
			t.Fatalf("checked = %+v, %v", checked, err)
		}
		checked, err = checkArgs(t, provider, "clear", delta.NewJsonObject(0), 3)
		requireArgsRefusal(t, refused(t, checked, err), data.CodeInvalid, data.ReasonArgs, "/")
	})

	t.Run("checks local callers too", func(t *testing.T) {
		info := mustLocal(t, "info", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
			contracts.Mem("save", contracts.NewMethod(contracts.Obj(contracts.Prop("title", contracts.Str())), contracts.Nul())),
		}})
		local1 := bind(t, info, nil)
		checked, err := checkArgs(t, local1, "save", delta.JsonObjectOf("title", "t"), 1)
		if err != nil {
			t.Fatal(err)
		}
		nexttest.RequireJSONEqual(t, checked.Args, delta.JsonObjectOf("title", "t"))
		checked, err = checkArgs(t, local1, "save", delta.JsonObjectOf("title", 1.0), 1)
		if refusal := refused(t, checked, err); refusal.Message != "args/title: must be string" {
			t.Fatalf("message = %q", refusal.Message)
		}
	})

	t.Run("refuses an unknown method, an unaccepted version and a refused method", func(t *testing.T) {
		checked, err := checkArgs(t, provider, "missing", nil, 3)
		refusal := refused(t, checked, err)
		nexttest.RequireJSONEqual(t, refusal.ToJSON(), delta.JsonObjectOf("code", "invalid", "reason", "member", "message", `notes v3 has no method "missing"`))
		checked, err = checkArgs(t, provider, "attach", nil, 3)
		requireArgsRefusal(t, refused(t, checked, err), data.CodeInvalid, data.ReasonMember, "")
		checked, err = checkArgs(t, provider, "add", delta.JsonObjectOf("text", "t"), 4)
		nexttest.RequireJSONEqual(t, refused(t, checked, err).ToJSON(), delta.JsonObjectOf("code", "contract_mismatch", "message", "notes v4 is not accepted"))
		bare := bind(t, fixture.notesV3, nil)
		checked, err = checkArgs(t, bare, "add", delta.JsonObjectOf("text", "t"), 1)
		requireArgsRefusal(t, refused(t, checked, err), data.CodeContractMismatch, "", "")
		checked, err = checkArgs(t, provider, "attach", delta.JsonObjectOf("data", []byte{}), 2)
		nexttest.RequireJSONEqual(t, refused(t, checked, err).ToJSON(), delta.JsonObjectOf("code", "contract_mismatch", "message", `notes v2 refuses "attach"`))
	})

	t.Run("compiles lazily, once per accepted version and method; equal schema objects share a validator", func(t *testing.T) {
		v1 := mustLocal(t, "lazy", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
			contracts.Mem("m", contracts.NewMethod(contracts.Obj(contracts.Prop("a", contracts.Str())))),
			contracts.Mem("clear", contracts.NewMethod()),
		}})
		v2 := evolveLocal(t, v1, func(e *contracts.LocalEditor) *contracts.LocalEditor {
			return e.Args("m", contracts.Obj(contracts.Prop("a", contracts.Str()), contracts.Prop("b", contracts.Str())))
		})
		fresh := bind(t, v2, contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{Methods: entries(
			contracts.Entry[contracts.MethodMapping]{Name: "m", Value: contracts.MethodMapping{Args: func(old delta.JsonValue) (delta.JsonValue, error) {
				next := old.(*delta.JsonObject).Clone()
				next.Set("b", "")
				return next, nil
			}}},
		)}}})
		calls := []func(ctx context.Context){
			func(ctx context.Context) { _, _ = data.CheckArgs(fresh, "clear", nil, 2, ctx) },
			func(ctx context.Context) { _, _ = data.CheckArgs(fresh, "clear", nil, 2, ctx) },
			// Version 1 shares the unchanged `clear` schema object with version 2.
			func(ctx context.Context) { _, _ = data.CheckArgs(fresh, "clear", nil, 1, ctx) },
			func(ctx context.Context) { _, _ = data.CheckArgs(fresh, "m", delta.JsonObjectOf("a", "x"), 1, ctx) },
			func(ctx context.Context) { _, _ = data.CheckArgs(fresh, "m", delta.JsonObjectOf("a", "x"), 1, ctx) },
			func(ctx context.Context) {
				_, _ = data.CheckArgs(fresh, "m", delta.JsonObjectOf("a", "x", "b", "y"), 2, ctx)
			},
		}
		var compilations []int
		for _, call := range calls {
			compilations = append(compilations, nexttest.Counted(t, call).Compilations)
		}
		want := []int{1, 0, 0, 1, 0, 1}
		for index := range want {
			if compilations[index] != want[index] {
				t.Fatalf("compilations = %v, want %v", compilations, want)
			}
		}
	})

	t.Run("counts one validation per call and no walk", func(t *testing.T) {
		counts := nexttest.Counted(t, func(ctx context.Context) {
			_, _ = data.CheckArgs(provider, "add", delta.JsonObjectOf("text", "t"), 1, ctx)
		})
		if want := (nexttest.Counts{Validations: 1}); counts != want {
			t.Fatalf("counts = %+v, want %+v", counts, want)
		}
	})
}

func TestMemory(t *testing.T) {
	t.Run("a dropped binding releases its dispatch table, mappings, schemas and validators after the last call", func(t *testing.T) {
		type token struct{ _ int }
		contractRef, providerRef, mappingRef := func() (weak.Pointer[contracts.Contract], weak.Pointer[data.Provider], weak.Pointer[token]) {
			v1 := mustLocal(t, "gc", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
				contracts.Mem("m", contracts.NewMethod(contracts.Obj(contracts.Prop("a", contracts.Str())))),
			}})
			contract := evolveLocal(t, v1, func(e *contracts.LocalEditor) *contracts.LocalEditor {
				return e.Args("m", contracts.Obj(contracts.Prop("a", contracts.Str()), contracts.Prop("b", contracts.Str())))
			})
			captured := &token{}
			mapping := func(old delta.JsonValue) (delta.JsonValue, error) {
				_ = captured
				next := old.(*delta.JsonObject).Clone()
				next.Set("b", "")
				return next, nil
			}
			bound, err := data.BindProvider(contract, contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{Methods: entries(
				contracts.Entry[contracts.MethodMapping]{Name: "m", Value: contracts.MethodMapping{Args: mapping}},
			)}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := data.CheckArgs(bound, "m", delta.JsonObjectOf("a", "x"), 1, chordctx.Background()); err != nil {
				t.Fatal(err)
			}
			return weak.Make(contract), weak.Make(bound), weak.Make(captured)
		}()
		nexttest.ExpectCollected(t, providerRef)
		nexttest.ExpectCollected(t, contractRef)
		nexttest.ExpectCollected(t, mappingRef)
	})
}
