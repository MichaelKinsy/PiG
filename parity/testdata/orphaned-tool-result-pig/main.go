// Command orphaned-tool-result-pig drives pig's production poisoned-history path
// for the D48 parity scenario: it normalizes a conversation whose only tool_use
// for a persisted tool-result lived in an aborted assistant turn, converts the
// survivors to provider messages, and streams them through the real openai
// provider against a hermetic endpoint. It then prints the tool_call_ids the
// serialized request actually carries. pig's NormalizeMessages strips the
// orphaned tool-result, so the request carries none.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func poisoned() []agent.AgentMessage {
	return []agent.AgentMessage{
		{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "run it"}}}},
		{Assistant: &agent.AssistantMessage{
			Role:         "assistant",
			Content:      []ai.AssistantContentBlock{ai.ToolCall{ID: "call_x", Name: "bash", Arguments: ai.JsonObject{"command": "expr 20 + 22"}}},
			StopReason:   ai.StopReasonError,
			ErrorMessage: "connection reset",
		}},
		{ToolResult: &agent.ToolResultMessage{
			Role: agent.RoleToolResult, ToolCallID: "call_x", ToolName: "bash",
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "42\n"}},
		}},
	}
}

// toWire mirrors convertToLLM's user/assistant/tool-result mapping for the fixture
// shapes above. The behavior under test is the strip in NormalizeMessages; this
// only renders the survivors so the provider can serialize them.
func toWire(msgs []agent.AgentMessage) []ai.Message {
	out := make([]ai.Message, 0, len(msgs))
	for _, m := range msgs {
		switch {
		case m.User != nil:
			out = append(out, ai.UserMessage{Content: ai.UserContentBlocks(m.User.Content), Timestamp: m.User.Timestamp})
		case m.Assistant != nil:
			content := m.Assistant.Content
			if len(content) == 0 {
				content = []ai.AssistantContentBlock{ai.TextContent{Text: ""}}
			}
			out = append(out, ai.AssistantMessage{
				Content: content, StopReason: m.Assistant.StopReason,
				ErrorMessage: m.Assistant.ErrorMessage, Timestamp: m.Assistant.Timestamp,
			})
		case m.ToolResult != nil:
			out = append(out, ai.ToolResultMessage{
				ToolCallID: m.ToolResult.ToolCallID, ToolName: m.ToolResult.ToolName,
				Content: m.ToolResult.Content, IsError: m.ToolResult.IsError,
				Timestamp: m.ToolResult.Timestamp,
			})
		}
	}
	return out
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	wire := toWire(agent.NormalizeMessages(poisoned(), nil))
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: srv.URL, APIKey: "sk-test", Model: "gpt-4o-mini", ProviderID: "openai"})
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: wire}), ai.StreamOptions{})
	if err != nil {
		return err
	}
	for range stream.Events(context.Background()) {
	}
	if len(body) == 0 {
		return fmt.Errorf("no request body captured")
	}
	fmt.Printf("tool_call_ids_in_request: %s\n", toolCallIDs(body))
	return nil
}

// toolCallIDs extracts the tool_call_id of every role:"tool" message in an
// openai chat-completions request body, sorted for determinism.
func toolCallIDs(body []byte) string {
	var req struct {
		Messages []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "[unparseable]"
	}
	ids := []string{}
	for _, m := range req.Messages {
		if m.Role == "tool" && m.ToolCallID != "" {
			ids = append(ids, m.ToolCallID)
		}
	}
	sort.Strings(ids)
	return fmt.Sprintf("%v", ids)
}
