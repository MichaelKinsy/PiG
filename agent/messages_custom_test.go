package agent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Upstream convertToLlm (coding-agent/src/core/messages.ts) maps every
// "custom" message to a user message: string content becomes a one-element
// text block array, and block-array content passes through unchanged. The
// bashExecution, branchSummary, and compactionSummary roles likewise become a
// single text block, never a bare string, so provider converters emit the
// array form on the wire.
func TestConvertToLLM_CustomAndSummaryRolesMatchUpstreamBlockShape(t *testing.T) {
	image := ai.ImageContent{Data: "aGk=", MimeType: "image/png"}
	cases := []struct {
		name   string
		custom map[string]any
		want   ai.UserContent
	}{
		{
			name:   "custom string",
			custom: map[string]any{"role": RoleCustom, "customType": "note", "content": "hello", "timestamp": int64(7)},
			want:   ai.UserContentBlocks{ai.TextContent{Text: "hello"}},
		},
		{
			name:   "custom empty string",
			custom: map[string]any{"role": RoleCustom, "customType": "note", "content": "", "timestamp": int64(7)},
			want:   ai.UserContentBlocks{ai.TextContent{Text: ""}},
		},
		{
			name: "custom typed blocks",
			custom: map[string]any{"role": RoleCustom, "customType": "note", "timestamp": int64(7),
				"content": []ai.UserContentBlock{ai.TextContent{Text: "a"}, image}},
			want: ai.UserContentBlocks{ai.TextContent{Text: "a"}, image},
		},
		{
			name: "custom decoded JSON blocks",
			custom: map[string]any{"role": RoleCustom, "customType": "note", "timestamp": float64(7),
				"content": []any{
					map[string]any{"type": "text", "text": "a"},
					map[string]any{"type": "image", "data": "aGk=", "mimeType": "image/png"},
				}},
			want: ai.UserContentBlocks{ai.TextContent{Text: "a"}, image},
		},
		{
			name:   "custom empty block array",
			custom: map[string]any{"role": RoleCustom, "customType": "note", "timestamp": int64(7), "content": []any{}},
			want:   ai.UserContentBlocks{},
		},
		{
			name:   "compaction summary",
			custom: map[string]any{"role": RoleCompactionSummary, "summary": "s", "timestamp": int64(7)},
			want:   ai.UserContentBlocks{ai.TextContent{Text: CompactionSummaryContextText("s")}},
		},
		{
			name:   "branch summary",
			custom: map[string]any{"role": RoleBranchSummary, "summary": "s", "timestamp": int64(7)},
			want:   ai.UserContentBlocks{ai.TextContent{Text: BranchSummaryContextText("s")}},
		},
		{
			name:   "bash execution",
			custom: map[string]any{"role": RoleBashExecution, "command": "ls", "output": "", "timestamp": int64(7)},
			want:   ai.UserContentBlocks{ai.TextContent{Text: "Ran `ls`\n(no output)"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := convertToLLM([]AgentMessage{{Custom: tc.custom}}, nil)
			want := []ai.Message{ai.UserMessage{Content: tc.want, Timestamp: 7}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("convertToLLM = %#v, want %#v", got, want)
			}
		})
	}
}

// A block-array custom message reaches the provider request. Before the fix
// the openai-completions payload lost the message entirely.
func TestAgentLoop_CustomBlockContentReachesProviderRequest(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	a := NewAgent(AgentOptions{Model: scriptedModel(provider)})
	a.SetMessages([]AgentMessage{{Custom: map[string]any{
		"role": RoleCustom, "customType": "note", "display": true, "timestamp": int64(1),
		"content": []any{map[string]any{"type": "text", "text": "from extension"}},
	}}})

	mustSend(t, a, "Hello")

	if got := userTexts(provider.request(1).transcript); !reflect.DeepEqual(got, []string{"from extension", "Hello"}) {
		t.Fatalf("request user texts = %v, want the custom block content before the prompt", got)
	}
}
