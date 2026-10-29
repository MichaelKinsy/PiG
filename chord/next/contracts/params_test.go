package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/test/next/contracts/params.test.ts. The type-level half of each case ("in the types") is
// designed out: the Go declaration takes TSchema values, so the policy is the runtime check alone.

func paramsVariants() contracts.VariantEntry {
	return variant("a", state(contracts.Prop("s", contracts.Str())))
}

func TestParamsPolicy(t *testing.T) {
	t.Run("accepts required str, int, bool and oneOf of strings, with constraints", func(t *testing.T) {
		_, err := declareRemote("n", 1, props(
			contracts.Prop("a", contracts.Str(contracts.Min(1), contracts.Max(9), contracts.Pattern("^x"))),
			contracts.Prop("b", contracts.Int(contracts.Min(0))),
			contracts.Prop("c", contracts.Bool()),
			contracts.Prop("d", contracts.OneOf("chat", "agent")),
		), paramsVariants())
		wantNoError(t, err)
	})

	t.Run("refuses optional fields at declaration", func(t *testing.T) {
		_, err := declareRemote("n", 1, props(contracts.Prop("a", contracts.Opt(contracts.Str()))), paramsVariants())
		wantError(t, err, "n.params.a: a param is a required str(), int(), bool() or oneOf(...) of strings")
	})

	for _, tc := range []struct {
		name   string
		schema contracts.TSchema
	}{
		{"a number", contracts.Num()},
		{"nested objects", contracts.Obj(contracts.Prop("a", contracts.Str()))},
		{"arrays", contracts.Arr(contracts.Str())},
		{"bytes", contracts.Bytes()},
		{"numeric literals", contracts.OneOf(1, 2)},
		{"defaults", raw(t, `{"type":"string","default":"x"}`)},
	} {
		t.Run("refuses "+tc.name+" at declaration", func(t *testing.T) {
			_, err := declareRemote("n", 1, props(contracts.Prop("p", tc.schema)), paramsVariants())
			wantError(t, err, "n.params.p: a param is")
		})
	}
}

func TestJoinKey(t *testing.T) {
	for _, tc := range []struct {
		parts []string
		want  string
	}{
		{[]string{"session", "S1"}, "session|S1"},
		{[]string{"a|b"}, `a\|b`},
		{[]string{`a\`, "b"}, `a\\|b`},
		{[]string{"a", "b|c"}, `a|b\|c`},
		{nil, ""},
	} {
		if got := contracts.JoinKey(tc.parts); got != tc.want {
			t.Errorf("JoinKey(%q) = %q, want %q", tc.parts, got, tc.want)
		}
	}
	if contracts.JoinKey([]string{"a|b", "c"}) == contracts.JoinKey([]string{"a", "b|c"}) {
		t.Error("two different part lists joined to one key")
	}
}
