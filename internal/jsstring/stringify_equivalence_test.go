package jsstring

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsparity"
)

// foreignMarshaler returns text another encoder wrote: HTML-escaped by encoding/json's rules, with whitespace, negative zero spelled several
// ways, escapes in either hex case and escaped backslashes before escape-like text. The JSON.stringify mode rewrites it while compacting.
type foreignMarshaler struct{ text string }

func (m foreignMarshaler) MarshalJSON() ([]byte, error) { return []byte(m.text), nil }

// htmlMarshaler is a PiG type whose MarshalJSON uses the HTML-escaping json.Marshal, as ai.AssistantMessage does.
type htmlMarshaler struct{ value any }

func (m htmlMarshaler) MarshalJSON() ([]byte, error) { return json.Marshal(m.value) }

// appender implements json.StringifyAppender over a string member; the package's verify mode checks it against its MarshalJSON.
type appender struct{ Text string }

func (a appender) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Text string `json:"text"`
	}{a.Text})
}

// AppendStringify writes the string with jsparity, the primitives PiG's append encoders use; the package's verify mode checks it against
// MarshalJSON.
func (a appender) AppendStringify(dst []byte) ([]byte, error) {
	dst = append(dst, `{"text":`...)
	return append(jsparity.AppendString(dst, a.Text), '}'), nil
}

// An appender built on jsparity.AppendString writes what MarshalJSON writes for every string, ill-formed UTF-8 included (LW-JSON-1: both
// write one raw U+FFFD per maximal subpart, as Node does).
func TestAppenderBuiltOnJsparityMatchesMarshalJSON(t *testing.T) {
	random := rand.New(rand.NewSource(3))
	for range 20000 {
		text := randomEncoderValue(random, 9).(string)
		viaJsparity := `{"text":` + string(jsparity.AppendString(nil, text)) + `}`
		want, err := json.MarshalStringify(struct {
			Text string `json:"text"`
		}{text})
		if err != nil {
			t.Fatal(err)
		}
		if viaJsparity != string(want) {
			t.Fatalf("%q: jsparity %s, MarshalJSON %s", text, viaJsparity, want)
		}
	}
}

type textMarshaler string

func (t textMarshaler) MarshalText() ([]byte, error) { return []byte(t), nil }

type quotedFields struct {
	Text   string      `json:"text,string"`
	Number float64     `json:"number,string"`
	Int    int         `json:"int,omitempty"`
	Num    json.Number `json:"num"`
	Ptr    *string     `json:"ptr,omitempty"`
	Under  any         `json:"<&>\u00e9"`
}

var foreignTexts = []string{
	` { "a" : -0 , "b" : [ -0.0 , -0e5 , 0 , -1 , -1e-400 , 1e-400 , -0.5 ] } `,
	`"\u003c\u003e\u0026\u2028\u2029"`, `"\u003C\u003E\u0026"`, `"\\u003c\\u2028"`, `"\\\u003c"`, `"\u0041\u00e9\ud800\udc00"`,
	`"\u003"`, `-0`, `[-0]`, `{"-0":-0}`, `"-0"`, `"a\u2028b"`, "\"raw \u2028 and \u2029 and <&>\"", `"\/"`, `"\ufffd"`, `"` + strings.Repeat("plain text ", 30) + `\u003c"`,
	`{"bad"`, `[1,]`, `"\x01"`, "\"\x01\"", `nul`, ``,
}

var numbers = []json.Number{"-0", "-0.0", "-0e10", "0", "-1", "1e5", "-1e-400", "-0.000"}

func randomEncoderValue(random *rand.Rand, depth int) any {
	long := []string{"plain ascii text run ", "0123456", "\u2028", "<&>", "\"", "\\", "\n", "\x1f", "é", "😀", "\xed\xa0\x80", "\xed\xa0\xbd\xed\xb8\x80", "\xff", "\xe2\x82"}
	switch kind := random.Intn(14); {
	case depth > 3 || kind < 2:
		return randomStringifyValue(random, depth)
	case kind == 2:
		var text strings.Builder
		for range random.Intn(40) {
			text.WriteString(long[random.Intn(len(long))])
		}
		return text.String()
	case kind == 3:
		return foreignMarshaler{foreignTexts[random.Intn(len(foreignTexts))]}
	case kind == 4:
		return json.RawMessage(foreignTexts[random.Intn(len(foreignTexts)-6)])
	case kind == 5:
		return htmlMarshaler{randomEncoderValue(random, depth+1)}
	case kind == 6:
		return appender{randomEncoderValue(random, 9).(string)}
	case kind == 7:
		return textMarshaler(long[random.Intn(len(long))] + "<\u2028")
	case kind == 8:
		return numbers[random.Intn(len(numbers))]
	case kind == 9:
		text := randomEncoderValue(random, 9).(string)
		var ptr *string
		if random.Intn(2) == 0 {
			ptr = &text
		}
		return quotedFields{Text: text, Number: []float64{math.Copysign(0, -1), 1.5, -2}[random.Intn(3)], Int: random.Intn(3), Num: numbers[random.Intn(len(numbers))], Ptr: ptr, Under: randomEncoderValue(random, depth+1)}
	case kind == 10:
		items := make([]any, random.Intn(4))
		for i := range items {
			items[i] = randomEncoderValue(random, depth+1)
		}
		return items
	case kind == 11:
		members := map[string]any{}
		for range random.Intn(4) {
			members[randomEncoderValue(random, 9).(string)] = randomEncoderValue(random, depth+1)
		}
		return members
	case kind == 12:
		return map[textMarshaler]any{"k<\u2028": randomEncoderValue(random, depth+1), "10": 1, "2": 2}
	default:
		return &struct {
			A any `json:"a"`
			B any `json:"b,omitempty"`
		}{randomEncoderValue(random, depth+1), randomEncoderValue(random, depth+1)}
	}
}

// The JSON.stringify mode writes in one pass exactly what the encoder wrote before it (HTML escaping off, then jsText over the whole output):
// nested and foreign Marshaler output, RawMessage, json.Number, quoted fields, text marshalers, map keys, long strings across the 8-byte scan,
// invalid UTF-8 and WTF-8 surrogates. Errors stay errors. AppendMarshalJSON appends the same bytes after a prefix.
func TestMarshalJSONMatchesTheRewriteEncoding(t *testing.T) {
	for seed := range int64(60000) {
		random := rand.New(rand.NewSource(seed))
		value := randomEncoderValue(random, 0)
		want, wantErr := referenceMarshalJSON(value)
		got, gotErr := MarshalJSON(value)
		if (wantErr == nil) != (gotErr == nil) || !bytes.Equal(got, want) {
			t.Fatalf("seed %d %#v: got %q, %v; want %q, %v", seed, value, got, gotErr, want, wantErr)
		}
		if wantErr != nil {
			if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
				t.Fatalf("seed %d: error %v, want %v", seed, gotErr, wantErr)
			}
			continue
		}
		appended, err := AppendMarshalJSON([]byte("prefix"), value)
		if err != nil || string(appended) != "prefix"+string(want) {
			t.Fatalf("seed %d: AppendMarshalJSON %q, %v; want prefix%q", seed, appended, err, want)
		}
		if length, err := StringifyLength(value); err != nil || length != mustLength(t, value) {
			t.Fatalf("seed %d: StringifyLength %d, %v", seed, length, err)
		}
	}
}

func mustLength(t *testing.T, value any) int {
	t.Helper()
	length, err := referenceStringifyLength(value)
	if err != nil {
		t.Fatal(err)
	}
	return length
}
