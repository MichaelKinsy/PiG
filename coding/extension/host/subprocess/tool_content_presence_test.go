package subprocess

import (
	"encoding/json"
	"net"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Every SDK sends the same current content-block wire shape. The host must
// preserve empty text separately from no text when adapting a tool response.
func TestToolResponsePreservesEmptyTextPresence(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		present   bool
		content   []RenderToolContent
	}{
		{"text block", `{"content":[{"type":"text","text":""}]}`, true, []RenderToolContent{{Type: "text", Text: ""}}},
		{"string", `{"content":""}`, true, []RenderToolContent{{Type: "text", Text: ""}}},
		{"signed text", `{"content":[{"type":"text","text":"","textSignature":"signed"}]}`, true, []RenderToolContent{{Type: "text", Text: "", TextSignature: "signed"}}},
		{"empty array", `{"content":[]}`, false, []RenderToolContent{}},
		{"image only", `{"content":[{"type":"image","data":"aW1n","mimeType":"image/png"}]}`, false, []RenderToolContent{{Type: "image", Data: "aW1n", MimeType: "image/png"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hostEnd, peer := net.Pipe()
			conn := NewConn("content", hostEnd)
			conn.Start(t.Context())
			t.Cleanup(func() { _ = peer.Close(); _ = conn.Close("test done") })
			host := NewHost(t.TempDir())
			defer host.Shutdown("test done")
			managed := withConn(&managedExt{config: ExtConfig{Name: "content"}, host: host}, conn)
			handler := host.makeToolExecuteFunc(managed, "content")
			type outcome struct {
				result any
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := handler(t.Context(), "call", json.RawMessage(`{}`), nil)
				done <- outcome{result, err}
			}()
			request := readLivenessEnvelope(t, peer)
			writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Result: json.RawMessage(tc.raw)}})
			got := <-done
			if got.err != nil {
				t.Fatal(got.err)
			}
			result, ok := got.result.(agent.AgentToolResult)
			present := false
			for _, block := range result.Content {
				if _, text := block.(ai.TextContent); text {
					present = true
				}
			}
			if !ok || present != tc.present {
				t.Fatalf("tool result=%#v, want present=%t", got.result, tc.present)
			}
			rendered := renderToolResultPayload(result)
			if !reflect.DeepEqual(rendered.Content, tc.content) {
				t.Fatalf("render content=%#v, want %#v", rendered.Content, tc.content)
			}
			data, err := json.Marshal(rendered)
			if err != nil {
				t.Fatal(err)
			}
			var wire, source map[string]any
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.raw), &source); err != nil {
				t.Fatal(err)
			}
			want := source["content"]
			if text, ok := want.(string); ok {
				want = []any{map[string]any{"type": "text", "text": text}}
			}
			if !reflect.DeepEqual(wire["content"], want) {
				t.Fatalf("render wire=%s, want content %#v", data, want)
			}
		})
	}
}
