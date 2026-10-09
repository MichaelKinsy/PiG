package coding

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// SessionManager projection and convertToLlm retain a valid user string, including when a later poisoned assistant is removed before a model switch.
func TestResumedSessionPreservesUserStringThroughNextPrompt(t *testing.T) {
	for _, reason := range []ai.StopReason{ai.StopReasonStop, ai.StopReasonError, ai.StopReasonAborted} {
		t.Run(string(reason), func(t *testing.T) {
			h := newRecoveryHarness(t, harnessOptions{}, fauxReply("new answer", ai.StopReasonStop, 0))
			if _, err := h.session.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserText("hello"), Timestamp: 1}}); err != nil {
				t.Fatal(err)
			}
			previous := &agent.AssistantMessage{Role: agent.RoleAssistant, API: ai.APIOpenAICompletions, Provider: "openai", ModelID: "old-model", Content: []ai.AssistantContentBlock{}, Usage: &ai.Usage{}, StopReason: reason, Timestamp: 2}
			if reason != ai.StopReasonStop {
				previous.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: "orphan", Name: "read", Arguments: ai.JsonObject{"path": "old"}}}
				previous.ErrorMessage = "old interrupted turn"
			}
			if _, err := h.session.Inner().AppendMessage(agent.AgentMessage{Assistant: previous}); err != nil {
				t.Fatal(err)
			}
			if reason != ai.StopReasonStop {
				if _, err := h.session.Inner().AppendMessage(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "orphan", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "orphaned result"}}, Timestamp: 3}}); err != nil {
					t.Fatal(err)
				}
			}
			resumed, err := NewSession(h.session.services, SessionOptions{Model: h.session.Agent().Model(), SkipBuiltinTools: true, ResumePath: h.session.Path()})
			if err != nil {
				t.Fatal(err)
			}
			drainSessionEvents(t, resumed)
			// Resume resolves the saved provider registration; select the capturing provider for the new turn explicitly.
			if err := resumed.SetModel(h.session.Agent().Model()); err != nil {
				t.Fatal(err)
			}
			users := laxMessagesWithRole(resumed, agent.RoleUser)
			if len(users) != 1 {
				t.Fatalf("resumed users=%#v", users)
			}
			want := []any{"user", "hello"}
			if got := laxRoleContent(t, users[0]); !reflect.DeepEqual(got, want) {
				t.Fatalf("resumed role/content=%#v", got)
			}
			if _, err := resumed.Send(t.Context(), "next"); err != nil {
				t.Fatal(err)
			}
			h.provider.mu.Lock()
			requests := append([]string(nil), h.provider.requests...)
			h.provider.mu.Unlock()
			if len(requests) != 1 {
				t.Fatalf("requests=%v", requests)
			}
			var messages []map[string]any
			if err := json.Unmarshal([]byte(requests[0]), &messages); err != nil {
				t.Fatal(err)
			}
			var contents []any
			for _, message := range messages {
				if message["role"] == "user" {
					contents = append(contents, message["content"])
				}
				if reason != ai.StopReasonStop && message["role"] == "toolResult" {
					t.Fatal("poisoned orphan reached new provider")
				}
			}
			if !reflect.DeepEqual(contents, []any{"hello", []any{map[string]any{"type": "text", "text": "next"}}}) {
				t.Fatalf("provider user contents=%#v", contents)
			}
			if reason == ai.StopReasonStop {
				encoded, err := json.Marshal([]any{"user", contents[0]})
				if err != nil {
					t.Fatal(err)
				}
				fmt.Printf("SESSION_USER_STRING_CONTEXT %s\n", encoded)
			}
			for _, entry := range resumed.Inner().GetEntries() {
				message, ok := entry.(icodingagent.MessageEntry)
				if ok && message.Message.User != nil && message.Message.User.Timestamp == 1 {
					if got := laxRoleContent(t, message.Message); !reflect.DeepEqual(got, want) {
						t.Fatalf("persisted role/content=%#v", got)
					}
					return
				}
			}
			t.Fatal("original user lost after next prompt")
		})
	}
}
