package sdk

import (
	"encoding/json"
	"strings"
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// TestSerializeConversationKeepsUnpairedSurrogates: a JavaScript string can hold an unpaired surrogate, which this SDK keeps as WTF-8. Pi's
// serializeConversation (compaction/utils.ts:120-155) counts it as one UTF-16 unit, JSON.stringify writes it as its \uXXXX escape, and the
// truncateForSummary slice that cuts a pair keeps the high surrogate. The expected strings are Node's output for the same messages.
func TestSerializeConversationKeepsUnpairedSurrogates(t *testing.T) {
	for _, tc := range []struct{ message, want string }{
		{`{"role":"toolResult","content":"` + strings.Repeat("a", 1999) + `😀"}`, `"[Tool result]: ` + strings.Repeat("a", 1999) + `\ud83d\n\n[... 1 more characters truncated]"`},
		{`{"role":"assistant","content":[{"type":"toolCall","name":"t","arguments":{"s":"\udc00","k\ud83d":1}}]}`, `"[Assistant tool calls]: t(s=\"\\udc00\", k\ud83d=1)"`},
		{`{"role":"assistant","content":[{"type":"toolCall","name":"t","arguments":"\ud83d😀"}]}`, `"[Assistant tool calls]: t(0=\"\\ud83d\", 1=\"\\ud83d\", 2=\"\\ude00\")"`},
	} {
		var want string
		if err := sdkjson.Unmarshal([]byte(tc.want), &want); err != nil {
			t.Fatal(err)
		}
		got, err := SerializeConversation([]json.RawMessage{json.RawMessage(tc.message)})
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%.80s:\n got %q\nwant %q", tc.message, got, want)
		}
	}
}
