package ai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// streamingJSONOracleDocuments are the documents the parser is compared on: the vectors of Pi's test (partial-json and fixJson), the
// malformed texts, every JSONTestSuite document, and seeded random documents with random whitespace. Each is cut into pieces at several
// sizes, and the parser's changes and snapshot after every piece must equal Pi's StreamingJsonParser's.
func streamingJSONOracleDocuments(t *testing.T) []string {
	t.Helper()
	documents := []string{
		`"`, `["`, `{"": "`, "n", "t", "f", "0", "-1.25e+4", "-1.25e+", "-1.25e", `" \x12`, "I", "-I", "N",
		"", "nul", "fals", "12.2", "-12", "-", "2.5e", "2.5e-", "2.5E3", "12.34e", "5e", `"abc`, `"value with \"quoted\" text and \\ escape`,
		`"value with \`, `"\u`, `"\u12`, `"text \u00`, `{"a":"\u12`, "[", "[[1], [2", `[["1"], ["2`, "[[false], [nu", "[[{}], [{", "[1, ", "[[], 123",
		`{"key":`, `{"a": {"b": 1}, "c": {"d": 2`, `{"ke`, `{"k1": 1, "k2`, `{"k1": 1, "k2":`, `{"key": "value"  `, "[1, [2, 3, [",
		`{"key": 123, "key2": {"subKey":`, `{"key": [1, 2, {`, `[1, 2, {"key": "value",`, `{"a": {"b": ["c", {"d": "e",`, `{"a": 1, "b": "`,
		"{\n  \"a\": [\n    {\n      \"a1\": \"v1\",\n      \"a2\": \"v2\"\n    }\n  ],\n  \"b\": [\n    {\n      \"b1\": \"n",
		`{"type":"div","children":[{"type":"Card","props":{}`, "12.", "12.e",
		`{"a":1 "b":2}`, `{"a" 1, "b": 2}`, "{a:1, 'b': 2}", `{"a":1,,"b":2,}`, "[1 2, , 3,]", `{"a":[1,2}, "b":3]`, "]]]}}}{",
		`{"a":NaN,"b":Infinity,"c":-Infinity,"d":undefined,"e":1}`, `{"a":01,"b":1.,"c":.5,"d":+1,"e":0x10,"f":1e}`,
		`{"a":"x"} trailing garbage {"b":2}`, `{"a": tru, "b": nulll, "c": falsey}`, `{"a":{"b":{"c":`, `{"a\": 1}`, `"unterminated \`,
		"// comment\n{\"a\":1}", "\x00{\x01\"a\"\x02:\x031}", `{"a":"x"}}}]]]`,
		`{"a":"\ud83d\ude00","b":"\ud83d","c":"\ude00","d":"\ud83dx","e":"\u00e9\u12","f":"é😀"}`, `{"__proto__":{"x":1},"__proto__":2,"a":1,"a":{"b":2},"1":1,"0":0,"a":3}`,
		`{"o":` + strings.Repeat(`{"a":`, 40) + `1` + strings.Repeat(`}`, 40) + `}`, `{"big":[` + strings.Repeat(`1,`, 3000) + `2]}`,
		`{"n":1e999,"m":-0,"p":0.1e-5,"q":123456789012345678901234567890,"r":[-0,0.5e1]}`,
	}
	suite, err := os.ReadFile(filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "test", "data", "json-test-suite.json"))
	if err != nil {
		t.Fatal(err)
	}
	var files struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(suite, &files); err != nil {
		t.Fatal(err)
	}
	for _, name := range slices.Sorted(maps_keys(files.Files)) {
		text, err := base64.StdEncoding.DecodeString(files.Files[name])
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, string(text))
	}
	random := rand.New(rand.NewSource(11))
	for _, spaced := range []bool{true, false} {
		for range 150 {
			var value any
			if random.Intn(5) < 4 {
				value = map[string]any{"path": randomJSONString(random), "args": randomJSONValue(random, 0)}
			} else {
				value = randomJSONValue(random, 0)
			}
			documents = append(documents, renderRandomJSON(value, random, spaced))
		}
	}
	// Raw control characters and invalid escapes inside strings, which the parser keeps and shows while streaming.
	malformed := []string{"\n", "\t", "\x00", `\d`, `\x41`, `\u12`, `\uZZZZ`, `\u00e9`, `\\`, `\q`, "a", " ", "é"}
	for round := range 120 {
		var content strings.Builder
		for range 6 {
			content.WriteString(malformed[random.Intn(len(malformed))])
		}
		documents = append(documents, fmt.Sprintf(`{"path":"p","content":"%s","n":%d}`, content.String(), round))
	}
	// Random corruption of valid documents.
	for i := range 150 {
		runes := []rune(documents[len(documents)-300+i%150])
		for range 1 + random.Intn(3) {
			if len(runes) == 0 {
				break
			}
			at := random.Intn(len(runes))
			switch random.Intn(3) {
			case 0:
				runes = slices.Delete(runes, at, at+1)
			case 1:
				runes = slices.Insert(runes, at, []rune(`{}[]",:\ x1e`)[random.Intn(12)])
			default:
				runes[at] = []rune(`{}[]",:\ x1e`)[random.Intn(12)]
			}
		}
		documents = append(documents, string(runes))
	}
	// A document that is not UTF-8 reaches the oracle with every bad byte replaced by U+FFFD (what encoding/json writes); give the
	// parser the same text.
	for i, document := range documents {
		documents[i] = replaceInvalidUTF8Bytes(document)
	}
	return documents
}

func replaceInvalidUTF8Bytes(text string) string {
	if utf8.ValidString(text) {
		return text
	}
	var out strings.Builder
	for len(text) > 0 {
		r, size := utf8.DecodeRuneInString(text)
		if r == utf8.RuneError && size == 1 {
			out.WriteRune(utf8.RuneError)
		} else {
			out.WriteString(text[:size])
		}
		text = text[size:]
	}
	return out.String()
}

func maps_keys[K comparable, V any](m map[K]V) func(func(K) bool) {
	return func(yield func(K) bool) {
		for key := range m {
			if !yield(key) {
				return
			}
		}
	}
}

var randomStringPieces = []string{"a", "b", "x y", " ", "\n", "\t", `\`, `"`, "/", "é", "😀", "\u0001", "\u001f", "\u2028", "{", "]", ","}

func randomJSONString(random *rand.Rand) string {
	var text strings.Builder
	for range random.Intn(6) {
		text.WriteString(randomStringPieces[random.Intn(len(randomStringPieces))])
	}
	return text.String()
}

func randomJSONValue(random *rand.Rand, depth int) any {
	roll := random.Float64()
	if depth > 3 || roll < 0.3 {
		switch random.Intn(10) {
		case 0, 1:
			return randomJSONString(random)
		case 2:
			return float64(random.Intn(2000) - 1000)
		case 3:
			return random.Float64() * 100
		case 4:
			return 1e21
		case 5:
			return -5e-8
		case 6:
			return math.Copysign(0, -1)
		case 7:
			return true
		case 8:
			return false
		}
		return nil
	}
	if roll < 0.65 {
		members := &orderedRandomObject{}
		for range random.Intn(4) {
			members.set(randomJSONString(random), randomJSONValue(random, depth+1))
		}
		return members
	}
	items := make([]any, random.Intn(4))
	for i := range items {
		items[i] = randomJSONValue(random, depth+1)
	}
	return items
}

// orderedRandomObject is a random object whose members keep the order they were generated in.
type orderedRandomObject struct {
	keys   []string
	values map[string]any
}

func (object *orderedRandomObject) set(key string, value any) {
	if object.values == nil {
		object.values = map[string]any{}
	}
	if _, exists := object.values[key]; !exists {
		object.keys = append(object.keys, key)
	}
	object.values[key] = value
}

func renderRandomJSON(value any, random *rand.Rand, spaced bool) string {
	space := func() string {
		if !spaced {
			return ""
		}
		return []string{"", "", " ", "\n  ", "\t", "\r\n"}[random.Intn(6)]
	}
	switch value := value.(type) {
	case []any:
		parts := make([]string, len(value))
		for i, item := range value {
			parts[i] = renderRandomJSON(item, random, spaced)
		}
		return "[" + space() + strings.Join(parts, space()+","+space()) + space() + "]"
	case *orderedRandomObject:
		parts := make([]string, len(value.keys))
		for i, key := range value.keys {
			encoded, _ := json.Marshal(key)
			parts[i] = string(encoded) + space() + ":" + space() + renderRandomJSON(value.values[key], random, spaced)
		}
		return "{" + space() + strings.Join(parts, space()+","+space()) + space() + "}"
	case float64:
		if value == 0 && math.Signbit(value) {
			return "-0"
		}
		return strconv.FormatFloat(value, 'g', -1, 64)
	case string:
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// streamingJSONPieces cuts text into pieces of size bytes, never inside a UTF-8 character.
func streamingJSONPieces(text string, size int) []string {
	if text == "" {
		return []string{""}
	}
	var pieces []string
	for len(text) > 0 {
		end := min(size, len(text))
		for end < len(text) && !isRuneStart(text[end]) {
			end++
		}
		pieces = append(pieces, text[:end])
		text = text[end:]
	}
	return pieces
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

type streamingJSONExpected struct {
	Changes  string  `json:"changes"`
	Snapshot *string `json:"snapshot"`
	Defined  bool    `json:"defined"`
}

// streamingJSONText is JSON.stringify of a snapshot with the oracle's replacer: -0 and non-finite numbers as strings.
func streamingJSONText(t *testing.T, value any, order schemaObjectOrder, path string) string {
	t.Helper()
	switch value := value.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(value)
	case string:
		encoded, err := marshalSchemaWithOrder(value, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	case float64:
		switch {
		case value == 0 && math.Signbit(value):
			return `"-0"`
		case math.IsInf(value, 1):
			return `"Infinity"`
		case math.IsInf(value, -1):
			return `"-Infinity"`
		}
		encoded, err := marshalSchemaWithOrder(value, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	case []any:
		parts := make([]string, len(value))
		for i, item := range value {
			parts[i] = streamingJSONText(t, item, order, schemaPath(path, strconv.Itoa(i)))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		keys := orderedSchemaKeys(value, order, path)
		parts := make([]string, len(keys))
		for i, key := range keys {
			encoded, err := marshalSchemaWithOrder(key, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			parts[i] = string(encoded) + ":" + streamingJSONText(t, value[key], order, schemaPath(path, key))
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	t.Fatalf("unexpected snapshot value %T", value)
	return ""
}

// streamingChangesText is the changes as JSON text in the oracle's form, decoded so both sides normalize alike (a lone surrogate
// decodes to U+FFFD on both).
func streamingChangesText(t *testing.T, changes []JSONChange) any {
	t.Helper()
	quote := func(text string) string {
		encoded, err := marshalSchemaWithOrder(text, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	parts := make([]string, 0, len(changes))
	for _, change := range changes {
		path := make([]string, len(change.Path))
		for i, element := range change.Path {
			if key, ok := element.(string); ok {
				path[i] = quote(key)
			} else {
				path[i] = strconv.Itoa(element.(int))
			}
		}
		entry := `{"kind":` + quote(change.Kind) + `,"path":[` + strings.Join(path, ",") + `],`
		if change.Kind == jsonChangeSet {
			entry += `"value":` + streamingJSONText(t, change.Value, nil, "")
		} else {
			entry += `"text":` + quote(change.Text)
		}
		parts = append(parts, entry+"}")
	}
	var normalized any
	if err := json.Unmarshal([]byte("["+strings.Join(parts, ",")+"]"), &normalized); err != nil {
		t.Fatal(err)
	}
	return normalized
}

// StreamingJSONParser (streaming-json.ts) against the pinned Pi: changes and snapshot after every piece, at several piece sizes.
func TestStreamingJSONParserMatchesPi(t *testing.T) {
	documents := streamingJSONOracleDocuments(t)
	var probes [][]string
	var origins []string
	for _, text := range documents {
		for _, size := range []int{len(text) + 1, 1, 2, 3, 7, 29} {
			probes = append(probes, streamingJSONPieces(text, size))
			origins = append(origins, fmt.Sprintf("%.80q @%d", text, size))
		}
	}
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "utils", "streaming-json.ts")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/streaming_json_parser.mjs", source)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]streamingJSONExpected
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	failures := 0
	for i, pieces := range probes {
		var parser StreamingJSONParser
		for k, piece := range pieces {
			changes := parser.Append(piece)
			snapshot := parser.Snapshot()
			wantChanges := expected[i][k]
			var want any
			if err := json.Unmarshal([]byte(wantChanges.Changes), &want); err != nil {
				t.Fatal(err)
			}
			gotChanges := streamingChangesText(t, changes)
			if want == nil {
				want = []any{}
			}
			if gotChanges == nil {
				gotChanges = []any{}
			}
			gotSnapshot := "null"
			if snapshot.Defined {
				gotSnapshot = streamingJSONText(t, snapshot.Value, snapshot.Order, "")
			}
			wantSnapshot := "null"
			if wantChanges.Snapshot != nil {
				wantSnapshot = *wantChanges.Snapshot
			}
			if gotSnapshot != wantSnapshot || snapshot.Defined != wantChanges.Defined || !reflect.DeepEqual(gotChanges, want) {
				if failures++; failures <= 8 {
					t.Errorf("%s piece %d %q:\n  changes got  %v\n  changes want %v\n  snapshot got  %s (defined %v)\n  snapshot want %s (defined %v)",
						origins[i], k, piece, gotChanges, want, gotSnapshot, snapshot.Defined, wantSnapshot, wantChanges.Defined)
				}
				break
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
