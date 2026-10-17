package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// agent-session.ts:1005 _buildBoundaryContext: `llmMessages = convertToLlm(projection.messages)` and canContinue is derived from those messages (:1006-1020).
// convertToLlm only maps message roles; it does not drop errored or aborted assistant turns or add placeholder tool results, which Pig's provider-level
// NormalizeMessages (transform-messages) does later for the request. The preview an extension sees must therefore hold every projected message.
// mutation-checked: running agent.NormalizeMessages before ConvertToLLM in coding/session_boundaries.go projectBoundaryContext fails both cases.
func TestBoundaryContextLLMMessagesAreConvertToLlmOfTheProjectionWithoutNormalization(t *testing.T) {
	usage := &ai.Usage{}
	user := func(text string, at int64) agent.AgentMessage {
		return agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: at}}
	}
	for _, tc := range []struct {
		name         string
		history      []agent.AgentMessage
		wantRoles    []string
		wantContinue bool
	}{
		{
			"an errored assistant turn is kept, so the context ends on an assistant message and cannot continue",
			[]agent.AgentMessage{
				user("hello", 1),
				{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonError, ErrorMessage: "boom", Usage: usage, Provider: "faux", ModelID: "faux-1", Timestamp: 2}},
			},
			[]string{"user", "assistant"}, false,
		},
		{
			"a tool call without a result gets no placeholder result",
			[]agent.AgentMessage{
				user("read it", 1),
				{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "c1", Name: "read", Arguments: ai.JsonObject{"path": "a.go"}}}, StopReason: ai.StopReasonToolUse, Usage: usage, Provider: "faux", ModelID: "faux-1", Timestamp: 2}},
				user("never mind", 3),
			},
			[]string{"user", "assistant", "user"}, true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel()})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sess.Close() }()
			for _, message := range tc.history {
				if _, err := sess.inner.AppendMessage(message); err != nil {
					t.Fatal(err)
				}
			}
			preview, _, _, err := sess.projectBoundaryContext(nil, icodingagent.EventTurnEnd)
			if err != nil {
				t.Fatal(err)
			}
			roles := make([]string, len(preview.LLMMessages))
			for i, message := range preview.LLMMessages {
				roles[i] = message.MessageRole()
			}
			if len(roles) != len(tc.wantRoles) {
				t.Fatalf("llmMessages roles %v, want %v", roles, tc.wantRoles)
			}
			for i := range roles {
				if roles[i] != tc.wantRoles[i] {
					t.Fatalf("llmMessages roles %v, want %v", roles, tc.wantRoles)
				}
			}
			if preview.CanContinue != tc.wantContinue {
				t.Fatalf("canContinue = %v, want %v", preview.CanContinue, tc.wantContinue)
			}
		})
	}
}
