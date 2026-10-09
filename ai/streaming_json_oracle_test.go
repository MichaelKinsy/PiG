package ai

// pi: packages/ai/src/utils/json-parse.ts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type streamingJSONAnswer struct {
	Repair string `json:"repair"`
	Parse  struct {
		Ok  any  `json:"ok"`
		Err bool `json:"err"`
	} `json:"parse"`
	Stream any     `json:"stream"`
	Text   *string `json:"text"`
}

// jsonTokens lists a JSON text's tokens in order, numbers by value, so two texts compare by member order and value but not by number spelling.
func jsonTokens(raw []byte) []string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var tokens []string
	for {
		token, err := decoder.Token()
		if err != nil {
			return tokens
		}
		if number, ok := token.(json.Number); ok {
			value, _ := number.Float64()
			token = math.Float64bits(value)
		}
		tokens = append(tokens, fmt.Sprintf("%T:%v", token, token))
	}
}

// streamingJSONInputs cut well-formed documents at every kind of boundary (inside a string, an escape, a \u escape, a number, a literal, between
// members) and corrupt others the way providers do: raw control characters and invalid escapes inside strings, a trailing backslash, single
// quotes, unquoted keys, trailing commas, NaN and Infinity, signs and exponents without digits, leading zeros, duplicate keys, deep nesting.
func streamingJSONInputs() []string {
	random := rand.New(rand.NewSource(20260934))
	scalars := []string{`1`, `-1`, `0`, `1.5`, `-0`, `1e5`, `1E-5`, `123456789012345678901234567890`, `1e400`, `-1e400`, `1e-400`, `Infinity`, `-Infinity`, `NaN`, `0.1`, `true`, `false`, `null`, `""`, `"a"`, `"é😀"`, `"a\nb"`, `"\u00e9"`, `"\ud83d\ude00"`, `"\ud83d"`, `"q\"q"`, `"\\"`, `"a\/b"`, `"tab\there"`}
	var gen func(depth int) string
	gen = func(depth int) string {
		switch r := random.Intn(6); {
		case r < 3 || depth == 0:
			return scalars[random.Intn(len(scalars))]
		case r < 5:
			var parts []string
			for range random.Intn(4) {
				key := []string{"a", "b", "path", "k\"q", "é", "1", "10", "2", "__proto__", "e", ""}[random.Intn(11)]
				parts = append(parts, `"`+strings.ReplaceAll(key, `"`, `\"`)+`":`+[]string{"", " "}[random.Intn(2)]+gen(depth-1))
			}
			return "{" + strings.Join(parts, ","+[]string{"", " ", "\n"}[random.Intn(3)]) + "}"
		default:
			var parts []string
			for range random.Intn(4) {
				parts = append(parts, gen(depth-1))
			}
			return "[" + strings.Join(parts, ",") + "]"
		}
	}
	corruptions := []string{"\x00", "\x01", "\t", "\n", "\r", "\x1f", "\\x", "\\a", "\\", "\\u12", "\\u12G4", "\\ud800", "'", ",", ",}", ",]", "NaN", "Infinity", "-", ".", "e", "tru", "nul", "fals", "}", "]", ":", "01", "\u2028", "\ufeff", "\x7f", "\\\n"}
	var inputs []string
	for range 1800 {
		text := gen(1 + random.Intn(4))
		if random.Intn(3) != 0 && !strings.HasPrefix(text, "{") {
			text = `{"a":` + text + `,"b":` + gen(2) + `}`
		}
		switch random.Intn(5) {
		case 0, 1:
			runes := []rune(text)
			text = string(runes[:random.Intn(len(runes)+1)])
		case 2:
			runes := []rune(text)
			at := random.Intn(len(runes) + 1)
			text = string(runes[:at]) + corruptions[random.Intn(len(corruptions))] + string(runes[at:])
			if random.Intn(2) == 0 {
				runes = []rune(text)
				text = string(runes[:random.Intn(len(runes)+1)])
			}
		}
		inputs = append(inputs, text)
	}
	inputs = append(inputs, "1e", "12e", "1e5x", "-", "-1", "1.", "5e+", "0e", "-e", "1E", "1e5", "1e5,", "-1e", "e", "1 ", " 1 e", `{"e":1E`, `{"e":-`, `{"e":1e`, `{"name":1E-`, `{"e":1.5E`, `[1e5,-`, `{"e":[1E`, `{"a":1,"e":2,"b":3E`, `{"e":"x","b":1E5`, "", " ", "\n", "{", "[", `{"a"`, `{"a":`, `{"a":1`, `{"a":1,`, `{"a":"x`, `{"a":"x\`, `{"a":"x\u00`, `{"a":[1,{"b":`, `"str`, `12`, `null`, `tru`, `[1,2`, `{"a":1}{"b":2}`, `{"a":1} x`, `  {"a": 1}  `, "{"+strings.Repeat(`"a":{`, 100),
		// A repeated key whose second value throws keeps the first value and its member order; integer-like keys enumerate first.
		`{"b":{"z":1,"y":2},"a":1,"b":`, `{"b":{"z":1,"y":2},"a":1,"b":-`, `{"b":1,"a":2,"10":3,"2":`, `{"a":-0,"b":[-0,{"c":-0}]}`)
	return inputs
}

// Pi's installed json-parse.js runs in Node against the same texts: repairJson's rewrite, parseJsonWithRepair's value or failure, and
// parseStreamingJson's result must match. Go's streaming result is a tool-argument object, so Pi's non-object results (array, number, string,
// null, true) are expected as an empty object.
func TestStreamingJSONMatchesPiOnSeededInput(t *testing.T) {
	inputs := streamingJSONInputs()
	payload, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/streaming_json.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []streamingJSONAnswer
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(inputs) {
		t.Fatalf("Pi answered %d of %d inputs", len(expected), len(inputs))
	}
	// Pi keeps Infinity and NaN in memory and writes null when it serializes them; Go cannot hold one in a tool-argument object (see nonFiniteToNull),
	// so Pi's non-finite markers are compared as null.
	var nullNonFinite func(value any) any
	nullNonFinite = func(value any) any {
		switch typed := value.(type) {
		case map[string]any:
			if len(typed) == 1 && typed["$num"] != nil {
				return nil
			}
			for key, item := range typed {
				typed[key] = nullNonFinite(item)
			}
		case []any:
			for i, item := range typed {
				typed[i] = nullNonFinite(item)
			}
		}
		return value
	}
	decode := func(value any) any {
		raw, _ := json.Marshal(value)
		var out any
		_ = json.Unmarshal(raw, &out)
		return out
	}
	var repaired, parsed, objects, differing int
	report := func(i int, what string, got, want any) {
		if differing++; differing <= 12 {
			t.Errorf("%s of %q differs from Pi:\n  Pig %v\n  Pi  %v", what, inputs[i], got, want)
		}
	}
	for i, text := range inputs {
		want := expected[i]
		if got := repairJSON(text); got != want.Repair {
			report(i, "repairJSON", got, want.Repair)
		} else if got != text {
			repaired++
		}
		value, err := ParseJSONWithRepair[any](text)
		switch {
		case strings.Contains(text, "e400"):
			// encoding/json rejects an out-of-range number that JSON.parse reads as an infinity; the streaming parser below reads it as Pi does.
		case (err != nil) != want.Parse.Err:
			report(i, "ParseJSONWithRepair failure", err, want.Parse.Err)
		case err == nil && !reflect.DeepEqual(decode(value), want.Parse.Ok):
			report(i, "ParseJSONWithRepair", decode(value), want.Parse.Ok)
		case err == nil:
			parsed++
		}
		wantStream := want.Stream
		if object, isObject := wantStream.(map[string]any); isObject && object["$num"] == nil && object["$undefined"] == nil {
			wantStream = nullNonFinite(wantStream)
			objects++
		} else {
			wantStream = map[string]any{}
		}
		if got := decode(ParseStreamingJson(text)); !reflect.DeepEqual(got, wantStream) {
			report(i, "ParseStreamingJson", got, wantStream)
		} else if want.Text != nil && strings.HasPrefix(*want.Text, "{") {
			// DeepEqual ignores member order and -0: the call's serialized arguments must list Pi's members in Pi's order with Pi's numbers.
			var call ToolCall
			call.SetStreamingArguments(text)
			serialized, err := call.ArgumentsJSON()
			if err != nil || !slices.Equal(jsonTokens(serialized), jsonTokens([]byte(*want.Text))) {
				report(i, "ParseStreamingJson serialized", string(serialized), *want.Text)
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d differences from Pi in %d inputs", differing, len(inputs))
	}
	if repaired < 40 || parsed < 400 || objects < 600 {
		t.Errorf("inputs are one-sided: %d repaired, %d parsed, %d object results of %d", repaired, parsed, objects, len(inputs))
	}
}
