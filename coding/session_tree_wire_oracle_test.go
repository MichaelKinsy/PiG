package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// anthropicWireServer answers every request with a short Anthropic text reply and
// records each request body.
func anthropicWireServer(t *testing.T) (*httptest.Server, func() [][]byte) {
	t.Helper()
	var mu sync.Mutex
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"message_start","message":{"id":"m","usage":{"input_tokens":1,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		} {
			kind, _, _ := strings.Cut(strings.TrimPrefix(event, `{"type":"`), `"`)
			_, _ = io.WriteString(w, "event: "+kind+"\ndata: "+event+"\n\n")
		}
	}))
	t.Cleanup(server.Close)
	return server, func() [][]byte {
		mu.Lock()
		defer mu.Unlock()
		return append([][]byte(nil), bodies...)
	}
}

// anthropicToolFlowError returns "" when an Anthropic Messages request
// satisfies the rule the API enforces ("unexpected tool_use_id in tool_result
// blocks ... must have a corresponding tool_use block in the previous
// message"): the tool_result blocks of a message answer exactly the tool_use
// blocks of the message before it, and every tool_use is answered in the next
// message.
func anthropicToolFlowError(body []byte) string {
	var request struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return "decode request: " + err.Error()
	}
	type blocks struct{ uses, results []string }
	parsed := make([]blocks, len(request.Messages))
	for index, message := range request.Messages {
		var content []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			ToolUseID string `json:"tool_use_id"`
		}
		if json.Unmarshal(message.Content, &content) != nil {
			continue
		}
		for _, block := range content {
			switch block.Type {
			case "tool_use":
				parsed[index].uses = append(parsed[index].uses, block.ID)
			case "tool_result":
				parsed[index].results = append(parsed[index].results, block.ToolUseID)
			}
		}
	}
	for index, current := range parsed {
		var previousUses []string
		if index > 0 {
			previousUses = parsed[index-1].uses
		}
		for position, id := range current.results {
			if !slices.Contains(previousUses, id) {
				return fmt.Sprintf("messages.%d.content.%d: unexpected tool_use_id %q in tool_result blocks", index, position, id)
			}
		}
		for _, id := range current.uses {
			if index+1 >= len(parsed) || !slices.Contains(parsed[index+1].results, id) {
				return fmt.Sprintf("messages.%d: tool_use %q has no tool_result in the next message", index, id)
			}
		}
	}
	return ""
}

type wireTreeBuilder struct {
	t    *testing.T
	sess *Session
	ids  []string
}

func (b *wireTreeBuilder) add(message agent.AgentMessage) string {
	b.t.Helper()
	id, err := b.sess.inner.AppendMessage(message)
	if err != nil {
		b.t.Fatal(err)
	}
	b.ids = append(b.ids, id)
	return id
}

func (b *wireTreeBuilder) user(text string) string {
	return b.add(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}})
}

func (b *wireTreeBuilder) text(text string) string {
	return b.add(agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, StopReason: ai.StopReasonStop,
	}})
}

func (b *wireTreeBuilder) calls(stop ai.StopReason, ids ...string) string {
	blocks := make([]ai.AssistantContentBlock, 0, len(ids))
	for _, id := range ids {
		blocks = append(blocks, ai.ToolCall{ID: id, Name: "read", Arguments: ai.JsonObject{"path": id}})
	}
	return b.add(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: blocks, StopReason: stop}})
}

func (b *wireTreeBuilder) result(id string) string {
	return b.add(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
		Role: agent.RoleToolResult, ToolCallID: id, ToolName: "read",
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "result " + id}},
	}})
}

func (b *wireTreeBuilder) forkAt(id string) {
	b.t.Helper()
	if err := b.sess.inner.SetLeafID(&id); err != nil {
		b.t.Fatal(err)
	}
}

// buildWireOracleTree builds a forked tree whose branches hold parallel tool
// calls, single tool calls and an errored turn that still has a persisted
// result, and returns every entry id in creation order.
func buildWireOracleTree(t *testing.T, sess *Session) []string {
	b := &wireTreeBuilder{t: t, sess: sess}
	b.user("start")
	a1 := b.calls(ai.StopReasonToolUse, "c1", "c2", "c3")
	r1 := b.result("c1")
	b.result("c2")
	b.result("c3")
	b.text("done reading")
	u2 := b.user("next")
	b.calls(ai.StopReasonToolUse, "d1")
	b.result("d1")
	b.text("done again")

	b.forkAt(r1)
	b.user("retry after c1")
	b.calls(ai.StopReasonToolUse, "e1", "e2")
	b.result("e1")
	b.result("e2")
	b.text("done retry")

	b.forkAt(a1)
	b.user("other idea")
	b.text("other answer")

	b.forkAt(u2)
	b.calls(ai.StopReasonError, "x1")
	b.result("x1")
	b.text("recovered")
	return b.ids
}

// Pi builds the context from the session path, converts it, and
// transformMessages closes every pending tool call at the next user turn, so a
// navigation point between a tool call and one of its results never leaves a
// tool_result without its tool_use. Pig normalizes before converting; this
// drives /tree navigation to every entry of a forked tree, with and without a
// branch summary, sends a new prompt through the Anthropic provider and checks
// the request it serializes.
func TestNavigateTreeEveryPointSendsValidToolFlow(t *testing.T) {
	probe := newTreeTestSession(t)
	entryCount := len(buildWireOracleTree(t, probe))
	for index := range entryCount {
		for _, summarize := range []bool{false, true} {
			t.Run(fmt.Sprintf("entry%02d/summarize=%v", index, summarize), func(t *testing.T) {
				server, bodies := anthropicWireServer(t)
				provider := ai.NewAnthropicProvider(ai.AnthropicConfig{Model: "wire-oracle-model", BaseURL: server.URL, APIKey: "k", ProviderID: "anthropic"})
				sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModelWithProvider(provider), SkipBuiltinTools: true})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = sess.Close() })
				sess.completer = &promptCapturingCompleter{summary: "abandoned branch summary"}
				ids := buildWireOracleTree(t, sess)

				if _, err := sess.NavigateTree(context.Background(), ids[index], NavigateTreeOptions{Summarize: summarize}); err != nil {
					t.Fatalf("NavigateTree(%s): %v", ids[index], err)
				}
				if _, err := sess.Send(context.Background(), "prepare for compaction"); err != nil {
					t.Fatalf("Send: %v", err)
				}
				sent := bodies()
				if len(sent) != 1 {
					t.Fatalf("provider requests = %d, want 1", len(sent))
				}
				if problem := anthropicToolFlowError(sent[0]); problem != "" {
					t.Fatalf("%s\nrequest: %s", problem, sent[0])
				}
			})
		}
	}
}
