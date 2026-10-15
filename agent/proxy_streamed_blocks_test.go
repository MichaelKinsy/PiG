package agent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// However a block is cut into deltas, the converter holds what Pi's proxy holds after `content.text += delta` and `partialJson += delta; content.arguments = parseStreamingJson(partialJson) || {}` (proxy.ts:272, 304, 342-343): the concatenation and the arguments parsed from the whole text so far, at every delta. The arguments compare as JSON.stringify writes them, so member order (the model's, not sorted) and a non-object or falsy value count. A block that starts again at the same index starts from empty.
func TestStreamProxyDeltasEqualConcatenationAndWholeTextParse(t *testing.T) {
	type stream struct{ kind, text string }
	streams := []stream{
		{"text", "plain text é 😀 with\nlines"},
		{"thinking", "thinking \u2028 text"},
		// Members out of sorted order at both levels, so a parse that loses the model's order differs.
		{"toolcall", " \n" + `{"path":"a.ts","content":"line \"one\"\n\ttwo é 😀 \\ end","n":[1.5,true,null],"z":{"y":1,"b":{"q":2,"a":3}},"a":"last"}`},
		{"toolcall", ` [1, {"b":1,"a":2}, "x"]`},
		{"toolcall", `"a streamed string é"`},
		{"toolcall", `12345`},
		{"toolcall", `null`},
	}
	for _, shape := range streams {
		for _, step := range []int{1, 3, 7, 64} {
			converter := &proxyEventConverter{partial: newProxyPartial(proxyTestModel()), toolJSON: map[int]string{}}
			for round := range 2 {
				start := ProxyAssistantMessageEvent{Type: ProxyAssistantMessageEventType(shape.kind + "_start"), ContentIndex: 0, ID: "call", ToolName: "write"}
				if _, err := converter.process(start); err != nil {
					t.Fatal(err)
				}
				var so strings.Builder
				for offset := 0; offset < len(shape.text); offset += step {
					delta := shape.text[offset:min(offset+step, len(shape.text))]
					so.WriteString(delta)
					if _, err := converter.process(ProxyAssistantMessageEvent{Type: ProxyAssistantMessageEventType(shape.kind + "_delta"), ContentIndex: 0, Delta: delta}); err != nil {
						t.Fatal(err)
					}
					var got, want string
					switch block := converter.partial.Content[0].(type) {
					case ai.TextContent:
						got, want = block.Text, so.String()
					case ai.ThinkingContent:
						got, want = block.Thinking, so.String()
					case ai.ToolCall:
						var whole ai.ToolCall
						whole.SetStreamingArguments(so.String())
						whole.DefaultFalsyArguments()
						gotJSON, err := block.ArgumentsJSON()
						if err != nil {
							t.Fatal(err)
						}
						wantJSON, err := whole.ArgumentsJSON()
						if err != nil {
							t.Fatal(err)
						}
						got, want = string(gotJSON), string(wantJSON)
						if converter.toolJSON[0] != so.String() {
							t.Fatalf("%q step %d: partialJson %q, want %q", shape.text, step, converter.toolJSON[0], so.String())
						}
					}
					if got != want {
						t.Fatalf("%s %q step %d round %d after %q: %s, want %s", shape.kind, shape.text, step, round, so.String(), got, want)
					}
				}
			}
		}
	}
}
