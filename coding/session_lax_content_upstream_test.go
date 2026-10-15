package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func laxMessageEntry(t *testing.T, fields map[string]any) icodingagent.SessionEntry {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	var base icodingagent.SessionEntryBase
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}
	return sessionentry.DecodeSessionEntry(raw)
}

func laxRoleContent(t *testing.T, message agent.AgentMessage) []any {
	t.Helper()
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	return []any{fields["role"], fields["content"]}
}

func TestUpstreamLaxExtensionContent(t *testing.T) {
	var result []any
	// .upstream/v0.87.1/packages/coding-agent/test/suite/lax-message-content.test.ts:29
	t.Run("normalizes tool results from untyped tools that omit content", func(t *testing.T) {
		registered := extension.RegisteredTool{Definition: extension.ToolDefinition{
			Name: "web_search", Label: "Web Search", Description: "Custom tool that returns a result without content",
			Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
			Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
				return agent.AgentToolResult{Details: map[string]any{}}, nil
			},
		}}
		tools, diagnostics := BridgeNewRunnerTools([]extension.RegisteredTool{registered})
		if len(diagnostics) != 0 {
			t.Fatal(diagnostics)
		}
		h := newRecoveryHarness(t, harnessOptions{tools: tools}, fauxToolCall("web_search"), fauxReply("done", ai.StopReasonStop, 0))
		if _, err := h.session.Send(t.Context(), "search something"); err != nil {
			t.Fatal(err)
		}
		messages := laxMessagesWithRole(h.session, agent.RoleToolResult)
		if len(messages) != 1 {
			t.Fatalf("tool results=%d want 1", len(messages))
		}
		got := laxRoleContent(t, messages[0])
		if !reflect.DeepEqual(got, []any{"toolResult", []any{}}) {
			t.Fatalf("tool result=%#v", got)
		}
		pending := len(h.provider.responses) - h.provider.callCount()
		if pending != 0 {
			t.Fatalf("pending responses=%d want 0", pending)
		}
		result = append(result, []any{[]any{got}, pending})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/lax-message-content.test.ts:62
	t.Run("normalizes null content in message_end extension replacements", func(t *testing.T) {
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"message_end": {func(args ...any) (any, error) {
			message := args[0].(extension.MessageEndEvent).Message
			if message.Assistant == nil {
				return nil, nil
			}
			raw, err := json.Marshal(message)
			if err != nil {
				return nil, err
			}
			var replacement map[string]any
			if err := json.Unmarshal(raw, &replacement); err != nil {
				return nil, err
			}
			replacement["content"] = nil
			if raw, err = json.Marshal(replacement); err != nil {
				return nil, err
			}
			var value extension.AgentMessage
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			return &extension.MessageEndEventResult{Message: &value}, nil
		}}}}
		h := newRecoveryHarness(t, harnessOptions{extension: ext}, fauxReply("hello", ai.StopReasonStop, 0))
		if _, err := h.session.Send(t.Context(), "hi"); err != nil {
			t.Fatal(err)
		}
		messages := laxMessagesWithRole(h.session, agent.RoleAssistant)
		if len(messages) != 1 {
			t.Fatalf("assistant messages=%d want 1", len(messages))
		}
		got := laxRoleContent(t, messages[0])
		if !reflect.DeepEqual(got, []any{"assistant", []any{}}) {
			t.Fatalf("assistant=%#v", got)
		}
		result = append(result, []any{got})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/lax-message-content.test.ts:86
	t.Run("normalizes null content in custom messages from extensions", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{})
		if err := h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "test", Content: nil, Display: false}, nil); err != nil {
			t.Fatal(err)
		}
		messages := laxMessagesWithRole(h.session, agent.RoleCustom)
		if len(messages) != 1 {
			t.Fatalf("custom messages=%d want 1", len(messages))
		}
		got := laxRoleContent(t, messages[0])
		if !reflect.DeepEqual(got, []any{"custom", []any{}}) {
			t.Fatalf("custom=%#v", got)
		}
		result = append(result, []any{got})
	})
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("SESSION_LAX_EXTENSIONS %s\n", encoded)
}

func laxMessagesWithRole(session *Session, role string) []agent.AgentMessage {
	var messages []agent.AgentMessage
	for _, message := range session.Messages() {
		if message.Role() == role {
			messages = append(messages, message)
		}
	}
	return messages
}

func TestUpstreamLaxSessionEntryContent(t *testing.T) {
	messageEntry := func(message map[string]any) icodingagent.SessionEntry {
		return laxMessageEntry(t, map[string]any{"type": "message", "id": "entry-1", "parentId": nil, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "message": message})
	}
	var result []any
	// .upstream/v0.87.1/packages/coding-agent/test/suite/lax-message-content.test.ts:105
	t.Run("normalizes null or missing content when loading session message entries", func(t *testing.T) {
		messages := []map[string]any{
			{"role": "user", "content": nil, "timestamp": time.Now().UnixMilli()},
			{"role": "assistant", "content": nil, "api": "openai-completions", "provider": "openai", "model": "test-model", "usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}, "stopReason": "stop", "timestamp": time.Now().UnixMilli()},
			{"role": "toolResult", "toolCallId": "call_1", "toolName": "web_search", "isError": false, "timestamp": time.Now().UnixMilli()},
		}
		var rows []any
		for _, bad := range messages {
			projected := icodingagent.SessionEntryToContextMessages(messageEntry(bad))
			if len(projected) == 0 {
				t.Fatalf("no projected message for %v", bad)
			}
			got := laxRoleContent(t, projected[0])
			want := []any{bad["role"], []any{}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("role/content=%#v want %#v", got, want)
			}
			rows = append(rows, got)
		}
		result = append(result, rows)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/lax-message-content.test.ts:140
	t.Run("normalizes null content when loading custom message entries", func(t *testing.T) {
		entry := laxMessageEntry(t, map[string]any{"type": "custom_message", "id": "entry-1", "parentId": nil, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "customType": "test", "content": nil, "display": false})
		projected := icodingagent.SessionEntryToContextMessages(entry)
		if len(projected) == 0 {
			t.Fatal("no projected custom message")
		}
		got := laxRoleContent(t, projected[0])
		want := []any{"custom", []any{}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("role/content=%#v want %#v", got, want)
		}
		result = append(result, got)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/lax-message-content.test.ts:156
	t.Run("keeps valid message content untouched when loading session entries", func(t *testing.T) {
		projected := icodingagent.SessionEntryToContextMessages(messageEntry(map[string]any{"role": "user", "content": "hello", "timestamp": time.Now().UnixMilli()}))
		if len(projected) == 0 {
			t.Fatal("no projected user message")
		}
		got := laxRoleContent(t, projected[0])
		want := []any{"user", "hello"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("role/content=%#v want %#v", got, want)
		}
		result = append(result, got)
	})
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("SESSION_LAX_ENTRIES %s\n", encoded)
}
