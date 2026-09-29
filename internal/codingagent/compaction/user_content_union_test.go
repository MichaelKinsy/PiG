package compaction

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi core/messages.ts:163-168 wraps custom strings, but :185-188 retains ordinary user strings.
func TestSummaryConversionPreservesUserUnionAndWrapsCustomStrings(t *testing.T) {
	for _, content := range []any{"hello", ai.UserText("hello"), json.RawMessage(`"hello"`)} {
		got := convertToLlm([]agent.AgentMessage{{Custom: map[string]any{"role": agent.RoleCustom, "content": content}}})
		want := ai.UserContentBlocks{ai.TextContent{Text: "hello"}}
		if len(got) != 1 || !reflect.DeepEqual(got[0].(ai.UserMessage).Content, want) {
			t.Fatalf("custom %T became %#v, want blocks %#v", content, got, want)
		}
	}
	for _, content := range []ai.UserContent{ai.UserText(""), ai.UserText("hello"), ai.UserContentBlocks{}, ai.UserContentBlocks{ai.TextContent{Text: "hello"}}} {
		got := convertToLlm([]agent.AgentMessage{{User: &agent.UserMessage{Role: agent.RoleUser, Content: content}}})
		if len(got) != 1 || !reflect.DeepEqual(got[0].(ai.UserMessage).Content, content) {
			t.Fatalf("user content %#v became %#v", content, got)
		}
	}
}
