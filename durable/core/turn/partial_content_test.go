package turn

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// A partial message shows nothing until it has content (generation.ts streams only partials with content). hasContent reads that
// from the partial's text; it is held to the parsed object's answer: the last content member is an array with an element.
func TestHasContentAgreesWithTheParsedPartial(t *testing.T) {
	for _, text := range []string{
		`{"role":"assistant","content":[],"stopReason":"stop"}`, `{"role":"assistant","content":[{"type":"text","text":"he"}]}`,
		`{"content":[]}`, `{"content":[ ]}`, `{"content":[1]}`, `{"content":[[]]}`, `{"content":[null]}`, `{"content":{}}`, `{"content":"x"}`,
		`{"content":null}`, `{"content":0}`, `{}`, `{"a":{"content":[1]}}`, `{"a":[{"content":[1]}],"content":[]}`, `{"content":[1],"content":[]}`,
		`{"content":[],"content":[1]}`, `{"content":[1],"content":"x"}`, `{"con\u0074ent":[1]}`, `{"content ":[1]}`, `{"Content":[1]}`,
		`{"a":"\"content\":[1]","content":[]}`, `{"a":"}","content":[1]}`, `{"content":["]"]}`, "{\"\xff\":1,\"content\":[1]}", ` { "content" : [ 1 ] } `,
	} {
		v, err := jv.ParseString(text)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		content, _ := arrayField(v.(*jv.Object), "content")
		if got, want := hasContent(jv.Raw(text)), len(content) > 0; got != want {
			t.Errorf("hasContent(%q) = %v, want %v", text, got, want)
		}
	}
}
