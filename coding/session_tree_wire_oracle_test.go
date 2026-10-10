package coding

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// wireCapturingProvider records the transcript of every request it receives.
type wireCapturingProvider struct {
	mu       sync.Mutex
	requests [][]ai.Message
}

func (*wireCapturingProvider) ID() string { return "wire-capture" }
func (p *wireCapturingProvider) Stream(_ context.Context, transcript ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.requests = append(p.requests, transcript.Messages())
	p.mu.Unlock()
	message := sessionTestMessage(p.ID(), "ok", ai.StopReasonStop, "")
	return newSessionTestStream(
		ai.StartEvent{Partial: message},
		ai.DoneEvent{Reason: ai.StopReasonStop, Message: message},
	), nil
}
func (*wireCapturingProvider) Close() error { return nil }

// transcriptToolFlowError returns "" when the transcript satisfies the rule
// every provider enforces (Anthropic Messages: "unexpected tool_use_id in
// tool_result blocks ... must have a corresponding tool_use block in the
// previous message"): the tool results directly after an assistant message
// answer each of its tool calls exactly once, and no tool result appears
// anywhere else.
func transcriptToolFlowError(wire []ai.Message) string {
	for i := 0; i < len(wire); i++ {
		switch message := wire[i].(type) {
		case ai.AssistantMessage:
			pending := map[string]bool{}
			for _, block := range message.Content {
				if call, ok := block.(ai.ToolCall); ok {
					pending[call.ID] = true
				}
			}
			j := i + 1
			for ; j < len(wire); j++ {
				result, ok := wire[j].(ai.ToolResultMessage)
				if !ok {
					break
				}
				if !pending[result.ToolCallID] {
					return fmt.Sprintf("wire[%d]: tool result %q has no tool call in the previous assistant message", j, result.ToolCallID)
				}
				delete(pending, result.ToolCallID)
			}
			for id := range pending {
				return fmt.Sprintf("wire[%d]: tool call %q is not answered directly after its assistant message", i, id)
			}
			i = j - 1
		case ai.ToolResultMessage:
			return fmt.Sprintf("wire[%d]: tool result %q does not follow an assistant message", i, message.ToolCallID)
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
// branch summary, sends a new prompt and checks the transcript the provider
// receives.
func TestNavigateTreeEveryPointSendsValidToolFlow(t *testing.T) {
	probe := newTreeTestSession(t)
	entryCount := len(buildWireOracleTree(t, probe))
	for index := range entryCount {
		for _, summarize := range []bool{false, true} {
			t.Run(fmt.Sprintf("entry%02d/summarize=%v", index, summarize), func(t *testing.T) {
				provider := &wireCapturingProvider{}
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
				if len(provider.requests) != 1 {
					t.Fatalf("provider requests = %d, want 1", len(provider.requests))
				}
				if problem := transcriptToolFlowError(provider.requests[0]); problem != "" {
					t.Fatalf("%s\ntranscript: %v", problem, provider.requests[0])
				}
			})
		}
	}
}
