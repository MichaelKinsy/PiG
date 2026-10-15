package rules

import "testing"

// TestS13rResultStructAndS5gErrorValue: an upstream function that returns the Result data closes on the Go struct Result[V, E]{Ok, Value,
// Error}; a lookalike struct (other field names, other order, a non-bool Ok) does not. An upstream function that returns Error closes on one
// Go error result, not on no result or on a value besides the error.
func TestS13rResultStructAndS5gErrorValue(t *testing.T) {
	scope := checkCall(t, `
type Result[V, E any] struct {
	Ok    bool
	Value V
	Error E
}
type Lookalike[V, E any] struct {
	Ok    bool
	Err   E
	Value V
}
type Flag[V, E any] struct {
	Ok    int
	Value V
	Error E
}

func Make() Result[string, string]               { return Result[string, string]{} }
func MakeWrong() Result[int, string]             { return Result[int, string]{} }
func MakeLook() Lookalike[string, string]        { return Lookalike[string, string]{} }
func MakeFlag() Flag[string, string]             { return Flag[string, string]{} }
func Plain() (Result[string, string], error)     { return Result[string, string]{}, nil }
func Wrap() error                                { return nil }
func WrapNothing()                               {}
func WrapValue() (string, error)                 { return "", nil }
`)
	resultAlias := func(name string) (string, bool) {
		if name == "Result" {
			return "{ ok: true; value: TValue } | { ok: false; error: TError }", true
		}
		return "", false
	}
	for _, tc := range []struct {
		name, returns, fn string
		want              Tri
	}{
		{"Result struct", "Result<string, string>", "Make", Yes},
		{"type argument disagrees", "Result<string, string>", "MakeWrong", No},
		{"other field names", "Result<string, string>", "MakeLook", No},
		{"non-bool Ok", "Result<string, string>", "MakeFlag", No},
		{"Result struct beside an error", "Result<string, string>", "Plain", No},
		{"Error as the error value", "Error", "Wrap", Yes},
		{"Error with no result", "Error", "WrapNothing", No},
		{"Error beside a value", "Error", "WrapValue", No},
	} {
		env := testEnv(scope)
		env.Alias = resultAlias
		got := Signature(env, call(tc.returns), sigOf(t, scope, tc.fn))
		if (got.OK == Yes) != (tc.want == Yes) { // only a pass closes a row; No and undecided both leave it a gap
			t.Errorf("%s: %v (%s), want %v", tc.name, got.OK, got.Why, tc.want)
		}
	}
}

// TestS13rLookalikeNamedResult: a struct that is itself named Result is still not the Result data unless its fields are Ok bool, Value
// and Error in that order. Each lookalike lives in its own scope, so the name check cannot be what rejects it.
func TestS13rLookalikeNamedResult(t *testing.T) {
	resultAlias := func(name string) (string, bool) {
		if name == "Result" {
			return "{ ok: true; value: TValue } | { ok: false; error: TError }", true
		}
		return "", false
	}
	for _, tc := range []struct{ name, src string }{
		{"other field names", `type Result[V, E any] struct {
	Ok    bool
	Err   E
	Value V
}
func Make() Result[string, string] { return Result[string, string]{} }`},
		{"other field order", `type Result[V, E any] struct {
	Ok    bool
	Error E
	Value V
}
func Make() Result[string, string] { return Result[string, string]{} }`},
		{"non-bool Ok", `type Result[V, E any] struct {
	Ok    int
	Value V
	Error E
}
func Make() Result[string, string] { return Result[string, string]{} }`},
		{"extra field", `type Result[V, E any] struct {
	Ok    bool
	Value V
	Error E
	Extra int
}
func Make() Result[string, string] { return Result[string, string]{} }`},
	} {
		scope := checkCall(t, tc.src)
		env := testEnv(scope)
		env.Alias = resultAlias
		if got := Signature(env, call("Result<string, string>"), sigOf(t, scope, "Make")); got.OK == Yes {
			t.Errorf("%s: closed (%s), want a gap", tc.name, got.Why)
		}
	}
}
