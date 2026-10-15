package ai

import (
	"math/rand/v2"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// orderFuzzKeys are object keys whose enumeration order differs from their byte order or from the text: array indices (hoisted ascending),
// integer-like keys that are not indices, keys a path must escape, U+FFFD and non-ASCII keys.
var orderFuzzKeys = []string{"a", "b", "z", "A", "0", "1", "2", "10", "01", "-1", "1.5", "4294967294", "4294967295", "a/b", "~0", "é", "\ufffd", ""}

// randomArgumentsText writes a JSON object with members in random order, repeated keys included (JSON.parse keeps the first position and
// the last value), nested objects and arrays, empty ones, and the scalars DecodedFormBlock admits.
func randomArgumentsText(rng *rand.Rand, depth int, b *strings.Builder) {
	b.WriteByte('{')
	for i := range rng.IntN(7) {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Quote(orderFuzzKeys[rng.IntN(len(orderFuzzKeys))]))
		b.WriteByte(':')
		randomArgumentValue(rng, depth, b)
	}
	b.WriteByte('}')
}

func randomArgumentValue(rng *rand.Rand, depth int, b *strings.Builder) {
	choice := rng.IntN(9)
	if depth > 4 {
		choice %= 5
	}
	switch choice {
	case 0:
		b.WriteString("null")
	case 1:
		b.WriteString("true")
	case 2:
		b.WriteString(strconv.Itoa(rng.IntN(100) - 50))
	case 3:
		b.WriteString("2.5")
	case 4:
		b.WriteString(strconv.Quote(orderFuzzKeys[rng.IntN(len(orderFuzzKeys))]))
	case 5, 6:
		randomArgumentsText(rng, depth+1, b)
	default:
		b.WriteByte('[')
		for i := range rng.IntN(4) {
			if i > 0 {
				b.WriteByte(',')
			}
			randomArgumentValue(rng, depth+1, b)
		}
		b.WriteByte(']')
	}
}

// mutateArguments adds and removes members after decoding, so the recorded order names keys the object no longer has and misses keys it
// gained, as a tool call edited in Go does.
func mutateArguments(rng *rand.Rand, value any) {
	switch value := value.(type) {
	case map[string]any:
		for key, member := range value {
			if rng.IntN(5) == 0 {
				delete(value, key)
				continue
			}
			mutateArguments(rng, member)
		}
		if rng.IntN(3) == 0 {
			value[orderFuzzKeys[rng.IntN(len(orderFuzzKeys))]] = map[string]any{"9": 1.0, "x": []any{}, "3": map[string]any{}}
		}
	case []any:
		for _, item := range value {
			mutateArguments(rng, item)
		}
	}
}

// The member order DecodedFormBlock records from the walk that writes a tool call's arguments is the order the token parse of the written text
// (readSchemaObjectOrder of ArgumentsJSON, the former path) records, for every arguments object DecodedFormBlock admits.
func TestWrittenSchemaObjectOrderMatchesTheParsedText(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 12))
	checked := 0
	for i := range 20000 {
		var text strings.Builder
		randomArgumentsText(rng, 0, &text)
		var call ToolCall
		if err := call.SetArgumentsJSON([]byte(text.String())); err != nil {
			t.Fatalf("%s: %v", text.String(), err)
		}
		switch i % 3 {
		case 1:
			mutateArguments(rng, map[string]any(call.Arguments))
		case 2:
			call.argumentOrder = nil // built in Go: no recorded order
		}
		written, err := call.ArgumentsJSON()
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := readSchemaObjectOrder(written)
		if err != nil {
			t.Fatal(err)
		}
		if walked := writtenSchemaObjectOrder(call.Arguments, call.argumentOrder); !reflect.DeepEqual(walked, parsed) {
			t.Fatalf("arguments %s (from %s): walk records %q, the parsed text %q", written, text.String(), walked, parsed)
		}
		call.ID, call.Name = "c", "n"
		block, ok := DecodedFormBlock(call)
		if !ok {
			continue
		}
		checked++
		former, err := NewDecodedToolCall(call.ID, call.Name, call.ThoughtSignature, call.Namespace, cloneDecodedObject(call.Arguments), written)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(block, former) {
			t.Fatalf("arguments %s: DecodedFormBlock %#v, the parsed text %#v", written, block, former)
		}
	}
	if checked < 10000 {
		t.Fatalf("DecodedFormBlock admitted only %d calls", checked)
	}
}
