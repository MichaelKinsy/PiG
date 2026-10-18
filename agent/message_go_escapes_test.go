package agent

import (
	"bytes"
	stdjson "encoding/json"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// equalUpToGoEscapes accepts an encoding/json text only where canonicalizing it gives the JSON.stringify text, and accepts the escapes encoding/json writes for characters JSON.stringify writes raw.
func TestEqualUpToGoEscapesOnlyAcceptsTheCanonicalSpelling(t *testing.T) {
	cases := []struct {
		encoded, canonical string
		want               bool
	}{
		{`{"a":"<b>"}`, `{"a":"<b>"}`, true},
		{`{"a":"\u003cb\u003e \u0026"}`, `{"a":"<b> &"}`, true},
		{`{"a":"\u2028\u2029"}`, "{\"a\":\"\u2028\u2029\"}", true},
		{`{"a":"\\u003c"}`, `{"a":"\\u003c"}`, true},
		{`{"a":"\\u003c"}`, `{"a":"\\<"}`, false},
		{`{"a":"\u003c"}`, `{"a":">"}`, false},
		{`{"a":"\u003cb"}`, `{"a":"<"}`, false},
		{`{"a":"<"}`, `{"a":"<b"}`, false},
		{`{"a":"\u0008"}`, `{"a":"\b"}`, false},
		{`{"a":1}`, `{"a":2}`, false},
		{`1`, `12`, false},
		{`12`, `1`, false},
	}
	for _, c := range cases {
		if got := equalUpToGoEscapes([]byte(c.encoded), []byte(c.canonical)); got != c.want {
			t.Errorf("equalUpToGoEscapes(%s, %s) = %v, want %v", c.encoded, c.canonical, got, c.want)
		}
	}

	alphabet := []string{"a", "<", ">", "&", "\u2028", "\u2029", "\\", "\"", "\n", "\b", "\x01", "é", "\\u003c", "😀"}
	rng := rand.New(rand.NewSource(7))
	accepted := 0
	for range 4000 {
		value := map[string]any{}
		for range rng.Intn(3) + 1 {
			var key, text strings.Builder
			for range rng.Intn(4) {
				key.WriteString(alphabet[rng.Intn(len(alphabet))])
			}
			for range rng.Intn(8) {
				text.WriteString(alphabet[rng.Intn(len(alphabet))])
			}
			value[key.String()] = []any{text.String(), rng.Intn(100)}
		}
		encoded, err := stdjson.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := jsonstringify.Canonicalize(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if equalUpToGoEscapes(encoded, canonical) {
			accepted++
		}
		// The fast path may decline, never disagree: whenever it accepts, the encoding canonicalizes to the text.
		for _, other := range [][]byte{canonical, append(bytes.Clone(canonical[:len(canonical)-1]), ' ', '}')} {
			if equalUpToGoEscapes(encoded, other) && !bytes.Equal(canonical, other) {
				t.Fatalf("accepted %s for %s, which canonicalizes to %s", encoded, other, canonical)
			}
		}
	}
	if accepted == 0 {
		t.Fatal("no generated encoding was accepted")
	}
}

// canonicalSystemMessageText is the canonical JSON text of a system message whose tools section is section.
func canonicalSystemMessageText(t *testing.T, section string) []byte {
	t.Helper()
	value := section
	message := AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText(""), Sections: ai.OrderedSections{{Name: "tools", Value: &value}}, Timestamp: 1}}
	encoded, err := message.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jsonstringify.Canonicalize(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

// A system message whose text holds characters encoding/json escapes (the <tools> section of every system prompt) keeps no source and decodes to the message that encodes back to the text.
func TestSystemMessageWithEscapedCharactersDecodesWithoutCanonicalizing(t *testing.T) {
	escaped := canonicalSystemMessageText(t, strings.Repeat("<tools>\n- read & write\n</tools>\n", 20))
	plain := canonicalSystemMessageText(t, strings.Repeat("(tools)\n- read + write\n(/tools)\n", 20))
	if !bytes.Contains(escaped, []byte("<tools>")) {
		t.Fatalf("the canonical text escapes <: %.80s", escaped)
	}
	for _, data := range [][]byte{escaped, plain} {
		var message AgentMessage
		if err := message.UnmarshalJSON(data); err != nil {
			t.Fatal(err)
		}
		if message.System == nil || message.source != nil {
			t.Fatalf("decoding %.60s kept a source", data)
		}
		encoded, err := message.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := jsonstringify.Canonicalize(encoded); !bytes.Equal(got, data) {
			t.Fatalf("the decoded message encodes as %s, want %s", got, data)
		}
	}
}
