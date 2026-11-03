package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

type litMode string
type litFlag bool
type litCount int
type litBig int64
type litRatio float64

// Pi contracts/schema.ts:111-113 `lit(value)` is `{ type: typeof value, const: value }` for any string, number or boolean. A Go
// named type of those kinds is the same literal: the type is its JSON type and the const its value.
func TestLitNamedTypesKeepTheirJSONTypeAndValue(t *testing.T) {
	sameJSON(t, contracts.Lit(litMode("a")), `{"type":"string","const":"a"}`)
	sameJSON(t, contracts.Lit(litFlag(true)), `{"type":"boolean","const":true}`)
	sameJSON(t, contracts.Lit(litCount(3)), `{"type":"number","const":3}`)
	sameJSON(t, contracts.Lit(litBig(7)), `{"type":"number","const":7}`)
	sameJSON(t, contracts.Lit(litRatio(1.5)), `{"type":"number","const":1.5}`)
}

func TestOneOfNamedTypesIsAUnionOfTheirLiterals(t *testing.T) {
	sameAs(t, contracts.OneOf(litMode("a"), litMode("b")), delta.JsonObjectOf("anyOf", []any{contracts.Lit("a").Node, contracts.Lit("b").Node}))
}
