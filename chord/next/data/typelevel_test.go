package data_test

import (
	"context"
	"reflect"
	"runtime"
	"testing"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports the type-level cases of packages/chord/test/next/data: args.test.ts "types a call replacement's arguments as
// values", errors.test.ts "is a tagged union: a wrong pairing is a type error" and validator.test.ts "leave nothing in
// TypeBox's build accumulators".

func TestCallReplacementArgumentsAreValues(t *testing.T) {
	var checked data.CheckedArgs
	want := reflect.TypeOf(func(args data.Value, ctx context.Context) (data.Value, error) { return nil, nil })
	if got := reflect.TypeOf(checked.Call); !got.ConvertibleTo(want) || got.Kind() != reflect.Func || got.NumIn() != 2 || got.In(0) != want.In(0) || got.In(1) != want.In(1) || got.NumOut() != 2 || got.Out(0) != want.Out(0) || got.Out(1) != want.Out(1) {
		t.Fatalf("CheckedArgs.Call has type %v, want %v", got, want)
	}
}

// Go compiles a wrong code/reason pairing in ChordErrorInit (the compiler does not enforce the tagged union), and
// upstream's constructor does not check it at runtime either: it keeps the fields as given. ChordErrorFromJson is where
// the pairing is checked (TestChordError).
func TestChordErrorConstructorKeepsAWrongPairing(t *testing.T) {
	for _, init := range []data.ChordErrorInit{
		{Code: data.CodeInvalid, Message: "m"},
		{Code: data.CodeGone, Reason: data.ReasonArgs, Message: "m"},
		{Code: data.CodeUnavailable, Reason: data.ReasonPart, Message: "m"},
	} {
		err := data.NewChordError(init, nil)
		if err.Code != init.Code || err.Reason != init.Reason || err.Message != "m" {
			t.Errorf("NewChordError(%+v) = %+v", init, err)
		}
		if _, parseErr := data.ChordErrorFromJson(err.ToJSON()); parseErr == nil {
			t.Errorf("ChordErrorFromJson accepted the wrong pairing %+v", init)
		}
	}
}

// Compiling keeps no state outside the validator: two owners compiled in sequence are each collected, with their
// validators, once dropped; the only module-level holder is the weak-keyed cache, which the owner's collection evicts.
func TestValidatorCompileLeavesNoBuildState(t *testing.T) {
	type refs struct {
		owner     weak.Pointer[contracts.Snapshot]
		validator weak.Pointer[data.Validator]
	}
	compileOne := func(schema contracts.TSchema) refs {
		owner := argumentsSnapshot(t, schema, "m")
		validator, err := data.ValidatorFor(owner, chordctx.Background())
		if err != nil {
			t.Fatal(err)
		}
		return refs{weak.Make(owner), weak.Make(validator)}
	}
	first := compileOne(contracts.Obj(contracts.Prop("blob", contracts.Bytes()), contracts.Prop("n", contracts.Int())))
	second := compileOne(contracts.Obj(contracts.Prop("blob", contracts.Bytes())))
	nexttest.ExpectCollected(t, first.owner, second.owner)
	runtime.GC()
	nexttest.ExpectCollected(t, first.validator, second.validator)
}
