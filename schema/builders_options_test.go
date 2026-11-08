package schema_test

import (
	"math/big"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// Options are applied in call order, so the members of a schema follow the order the caller wrote them, as the members of the options object Pi's builders spread do.
func TestBuilderOptionsWriteMembersInCallOrder(t *testing.T) {
	cases := []struct {
		got  schema.Schema
		want string
	}{
		{schema.Str(schema.Description("d"), schema.MinLength(1), schema.Pattern("^a")), `{"type":"string","description":"d","minLength":1,"pattern":"^a"}`},
		{schema.Str(schema.Pattern("^a"), schema.MinLength(1), schema.Description("d")), `{"type":"string","pattern":"^a","minLength":1,"description":"d"}`},
		{schema.Num(schema.Maximum(9), schema.Title("t"), schema.Minimum(0)), `{"type":"number","maximum":9,"title":"t","minimum":0}`},
		{schema.Arr(schema.Int(), schema.UniqueItems(true), schema.MinItems(1)), `{"type":"array","items":{"type":"integer"},"uniqueItems":true,"minItems":1}`},
		{schema.Obj(schema.Properties{schema.Prop("a", schema.Str())}, schema.Description("d"), schema.Open(), schema.Title("t")), `{"type":"object","properties":{"a":{"type":"string"}},"required":["a"],"description":"d","title":"t"}`},
	}
	for _, c := range cases {
		text, err := chordjson.Marshal(c.got)
		if err != nil {
			t.Fatal(err)
		}
		if string(text) != c.want {
			t.Errorf("got %s\nwant %s", text, c.want)
		}
	}
}

// A misplaced option does not compile, which is the Go counterpart of the per-builder option types of Pi's builders.ts: MinItems is an option of Arr, Open of Obj, Pattern of Str. The compiler enforces the parameter types; this test pins which builders each option fits, so a widened option shows up as a failure here.
func TestBuilderOptionsFitOnlyTheirBuilders(t *testing.T) {
	type fits struct{ str, number, bigInt, array, bytes, object, annotation bool }
	of := func(option any) fits {
		var f fits
		_, f.str = option.(schema.StrOption)
		_, f.number = option.(schema.NumberOption)
		_, f.bigInt = option.(schema.BigIntOption)
		_, f.array = option.(schema.ArrayOption)
		_, f.bytes = option.(schema.BytesOption)
		_, f.object = option.(schema.ObjectOption)
		_, f.annotation = option.(schema.AnnotationOption)
		return f
	}
	every := fits{true, true, true, true, true, true, true}
	for name, c := range map[string]struct {
		option any
		want   fits
	}{
		"Title":            {schema.Title("t"), every},
		"Description":      {schema.Description("d"), every},
		"Examples":         {schema.Examples(1), every},
		"Deprecated":       {schema.Deprecated(true), every},
		"Default":          {schema.Default(1), every},
		"MinLength":        {schema.MinLength(1), fits{str: true, bytes: true}},
		"MaxLength":        {schema.MaxLength(1), fits{str: true, bytes: true}},
		"Pattern":          {schema.Pattern("a"), fits{str: true}},
		"Format":           {schema.Format("email"), fits{str: true}},
		"Minimum":          {schema.Minimum(0), fits{number: true}},
		"Maximum":          {schema.Maximum(0), fits{number: true}},
		"ExclusiveMinimum": {schema.ExclusiveMinimum(0), fits{number: true}},
		"ExclusiveMaximum": {schema.ExclusiveMaximum(0), fits{number: true}},
		"MultipleOf":       {schema.MultipleOf(1), fits{number: true}},
		"BigMinimum":          {schema.BigMinimum(big.NewInt(0)), fits{bigInt: true}},
		"BigMaximum":          {schema.BigMaximum(big.NewInt(0)), fits{bigInt: true}},
		"BigExclusiveMinimum": {schema.BigExclusiveMinimum(big.NewInt(0)), fits{bigInt: true}},
		"BigExclusiveMaximum": {schema.BigExclusiveMaximum(big.NewInt(0)), fits{bigInt: true}},
		"BigMultipleOf":       {schema.BigMultipleOf(big.NewInt(1)), fits{bigInt: true}},
		"MinItems":         {schema.MinItems(1), fits{array: true}},
		"MaxItems":         {schema.MaxItems(1), fits{array: true}},
		"UniqueItems":      {schema.UniqueItems(true), fits{array: true}},
		"Open":             {schema.Open(), fits{object: true}},
	} {
		if got := of(c.option); got != c.want {
			t.Errorf("%s fits %+v, want %+v", name, got, c.want)
		}
	}
}
