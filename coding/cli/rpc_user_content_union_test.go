package cli

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// RPC sends the Session message without rewriting user content. Expectations come from raw source fixtures, not from an already decoded/marshaled Session message.
func TestRPCUserContentRetainsStringAndArrayVariants(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"empty-string", `""`, `""`},
		{"text", `"hello"`, `"hello"`},
		{"unicode-scalars", `"é😀中"`, `"é😀中"`},
		{"empty-blocks", `[]`, `[]`},
		{"text-block", `[{"type":"text","text":"hello"}]`, `[{"type":"text","text":"hello"}]`},
		{"high-surrogate", `"a\ud800b"`, `"a\ud800b"`},
		{"low-surrogate", `"a\udfffb"`, `"a\udfffb"`},
		{"paired-surrogates", `"\ud83d\ude00"`, `"😀"`},
		{"replacement-scalar", `"a�b"`, `"a�b"`},
		{"literal-escape", `"\\ud800"`, `"\\ud800"`},
		{"surrogate-block", `[{"type":"text","text":"a\ud800b"}]`, `[{"type":"text","text":"a\ud800b"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var message agent.AgentMessage
			if err := json.Unmarshal([]byte(`{"role":"user","content":`+tc.input+`,"timestamp":123}`), &message); err != nil {
				t.Fatal(err)
			}
			wire, err := rpcAgentMessage(message)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Role      string
				Content   json.RawMessage
				Timestamp int64
			}
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if got.Role != "user" || got.Timestamp != 123 || string(got.Content) != tc.want {
				t.Fatalf("RPC=%s, want unchanged content=%s", encoded, tc.want)
			}
		})
	}
}
