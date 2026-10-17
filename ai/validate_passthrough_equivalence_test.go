package ai

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// passthroughText writes a JSON object as a model might: mostly compact, sometimes with whitespace, escapes JSON.stringify
// would not write, non-shortest numbers, integer-like or repeated keys, nesting and lone surrogates.
func passthroughText(r *rand.Rand, depth int) string {
	var b strings.Builder
	keys := []string{"path", "content", "n", "s", "o", "1", "edits", "oldText", "newText", "x"}
	b.WriteByte('{')
	for i := range r.IntN(4) {
		if i > 0 {
			b.WriteByte(',')
		}
		if r.IntN(12) == 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strconv.Quote(keys[r.IntN(len(keys))]))
		b.WriteByte(':')
		b.WriteString(passthroughValue(r, depth))
	}
	b.WriteByte('}')
	return b.String()
}

func passthroughValue(r *rand.Rand, depth int) string {
	switch k := r.IntN(10); {
	case k == 0 && depth < 3:
		return passthroughText(r, depth+1)
	case k == 1 && depth < 3:
		return "[" + passthroughValue(r, depth+1) + "," + passthroughValue(r, depth+1) + "]"
	case k == 2:
		return []string{"3", "3.0", "-0", "1e21", "1E5", "0.000001", "1e-7", "12345678901234567", "2.5", "-4"}[r.IntN(10)]
	case k == 3:
		return []string{"true", "false", "null"}[r.IntN(3)]
	default:
		pieces := []string{"a", "line\\n", "\\u0041", "\\/", "é", "\\u00e9", "\\ud800", "\\uD800", "\\t", "\\u001f", "\\u001F", "\\\"", "😀", "\\ud83d\\ude00", "<&>", "\u2028"}
		var s strings.Builder
		s.WriteByte('"')
		for range r.IntN(5) {
			s.WriteString(pieces[r.IntN(len(pieces))])
		}
		s.WriteByte('"')
		return s.String()
	}
}

// Whenever validation hands back the model's own bytes, they are exactly what re-encoding the validated value in the
// arguments' member order writes, which is what the previous code returned. Schemas cover TypeBox Convert, JSON-schema
// coercion, optional nulls and the wrapped-array relocation.
func TestValidateToolArgumentsPassthroughMatchesReencoding(t *testing.T) {
	schemas := []string{
		`{}`,
		`{"type":"object","properties":{"n":{"type":"number"},"s":{"type":"string"},"o":{"type":"string"}}}`,
		`{"~kind":"Object","type":"object","properties":{"path":{"~kind":"String","type":"string"},"content":{"~kind":"String","type":"string"}}}`,
		`{"~kind":"Object","type":"object","properties":{"edits":{"~kind":"Array","type":"array","items":{"~kind":"Object","type":"object","properties":{"oldText":{"~kind":"String","type":"string"},"newText":{"~kind":"String","type":"string"}}}},"n":{"~kind":"Number","type":"number"}}}`,
		`{"type":"object","additionalProperties":{"type":"string"}}`,
	}
	r := rand.New(rand.NewPCG(7, 11))
	passed, reencoded := 0, 0
	for i := range 20000 {
		schema := json.RawMessage(schemas[i%len(schemas)])
		args := json.RawMessage(passthroughText(r, 0))
		got, err := ValidateToolArgumentsJSON("t", schema, args)
		if err != nil || len(got) == 0 || &got[0] != &args[0] {
			reencoded++
			continue
		}
		passed++
		var value any
		if err := sdkjson.Unmarshal(args, &value); err != nil {
			t.Fatal(err)
		}
		order, err := readSchemaObjectOrder(args)
		if err != nil {
			t.Fatal(err)
		}
		want, err := marshalInRecordedOrder(value, order)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("schema %s args %s: passed through, but re-encoding writes %s", schema, args, want)
		}
	}
	if passed < 1000 || reencoded < 1000 {
		t.Fatalf("passed through %d and re-encoded %d of 20000; the generator should exercise both", passed, reencoded)
	}
}
