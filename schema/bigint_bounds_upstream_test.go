package schema_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// pi: packages/schema/src/builders.ts BigintBounds (minimum, maximum, exclusiveMinimum, exclusiveMaximum, multipleOf, each a bigint).

// BigintBounds writes the keywords of its set fields after the type, in the order Pi declares the members, and BigInt accepts them.
func TestBigintBoundsWriteTheirKeywordsInPisOrder(t *testing.T) {
	bounds := schema.BigintBounds{Minimum: bigint(0), Maximum: bigint(100), ExclusiveMinimum: bigint(-1), ExclusiveMaximum: bigint(101), MultipleOf: bigint(5)}
	got := schema.BigInt(bounds.Options()...)
	want := chordjson.ObjectOf("type", "bigint", "minimum", bigint(0), "maximum", bigint(100), "exclusiveMinimum", bigint(-1), "exclusiveMaximum", bigint(101), "multipleOf", bigint(5))
	if !exactJSON(got, want) {
		t.Fatalf("BigInt(bounds) = %s", marshalled(got))
	}
	if only := schema.BigInt(schema.BigintBounds{MultipleOf: bigint(3)}.Options()...); !exactJSON(only, chordjson.ObjectOf("type", "bigint", "multipleOf", bigint(3))) {
		t.Fatalf("unset bounds are written: %s", marshalled(only))
	}
	if none := schema.BigInt(schema.BigintBounds{}.Options()...); !exactJSON(none, chordjson.ObjectOf("type", "bigint")) {
		t.Fatalf("empty bounds = %s", marshalled(none))
	}
	v := mustCompile(t, schema.BigInt(schema.BigintBounds{Minimum: bigint(0), MultipleOf: bigint(5)}.Options()...))
	for value, ok := range map[any]bool{bigint(10): true, bigint(7): false, bigint(-5): false} {
		if got := v.Check(value); got != ok {
			t.Errorf("Check(%v) = %v, want %v", value, got, ok)
		}
	}
}
