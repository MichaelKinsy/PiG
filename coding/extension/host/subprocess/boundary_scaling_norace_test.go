//go:build !race

package subprocess

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// The boundary scaling audit: what the host encodes for an extension on each event whose payload grows with the length of a stream (M) or of the session history (H). encodeEventForWire is the encoding makeEventHandler runs for every handler of an extension on every event (host.go: marshalEventWire of boundaryEventWire(wireEventPayload(event))). The wire bytes are the same for every SDK and for isolated and packed cells: each extension has its own socket and its own frames either way. The slopes are measured at n and 10n by testscaling (allocations and bytes gated, wall time reported). Pi hands an in-process handler the same objects by reference, so a row that grows with M or H per call is a cost PiG pays on the wire and Pi does not.
func encodeEventForWire(event any) int {
	encoded, err := marshalEventWire(boundaryEventWire(wireEventPayload(event), nil))
	if err != nil {
		panic(err)
	}
	return len(encoded)
}

var wireSink int

func historyMessages(h int) []agent.AgentMessage {
	messages := make([]agent.AgentMessage, 0, 2*h)
	for i := range h {
		call := fmt.Sprintf("call_%d", i)
		messages = append(messages,
			agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.ToolCall{ID: call, Name: "read", Arguments: ai.JsonObject{"path": fmt.Sprintf("file%d.go", i)}}}, StopReason: ai.StopReasonToolUse}},
			agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: call, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "package main\n\nfunc main() {}\n"}}, Timestamp: int64(i)}},
		)
	}
	return messages
}

func historyEntries(h int) []extension.SessionEntry {
	entries := make([]extension.SessionEntry, h)
	for i := range h {
		parent := "null"
		if i > 0 {
			parent = fmt.Sprintf("%q", fmt.Sprintf("e%d", i-1))
		}
		raw := fmt.Sprintf(`{"type":"message","id":"e%d","parentId":%s,"timestamp":"2026-10-11T00:00:00.000Z","message":{"role":"user","content":"message %d with some text","timestamp":%d}}`, i, parent, i, i)
		entries[i] = sessionentry.DecodeSessionEntry(json.RawMessage(raw))
	}
	return entries
}

// tool_execution_update carries the accumulated partialResult: a tool that prints M bytes in n chunks sends n events whose partial results grow to M bytes. The host sends the first update of a call whole and every later one as the operations that turn the previous payload into it (tool_update_delta.go), so the n updates cost O(n) in all, as a message_update stream does.
func TestBoundaryScalingToolExecutionUpdateStream(t *testing.T) {
	testscaling.Check(t, testscaling.Case{Name: "tool_execution_update stream of n output chunks (accumulated partialResult)", Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
		chunk := strings.Repeat("output line of a long running command\n", 2)
		return func() {
			streams := &toolUpdateStreams{}
			conn := &Conn{}
			var text strings.Builder
			for range n {
				text.WriteString(chunk)
				partial := map[string]any{"content": []any{map[string]any{"type": "text", "text": text.String()}}}
				event := extension.ToolExecutionUpdateEvent{Type: "tool_execution_update", ToolCallID: "c1", ToolName: "bash", Args: map[string]any{"command": "make"}, PartialResult: partial}
				prepared, args, stream, inStream, err := streams.prepare(conn, event, event, nil)
				if err != nil || !inStream {
					panic(fmt.Sprint("tool_execution_update left the stream: ", err))
				}
				wireSink += len(args) + len(stream.Ops)
				streams.sent(conn, prepared, nil)
			}
		}
	}})
}

// One event whose payload is the session history grows linearly with H per call; a handler registered K times (or K extensions) pays K times, because each handler's call encodes the event again.
func TestBoundaryScalingHistoryEvents(t *testing.T) {
	testscaling.Check(t, testscaling.Case{Name: "context event at history n (one handler call)", Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
		event := extension.ContextEvent{Type: "context", Messages: historyMessages(n)}
		return func() { wireSink += encodeEventForWire(event) }
	}})
	testscaling.Check(t, testscaling.Case{Name: "agent_end at n messages of the run (one handler call)", Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
		event := extension.AgentEndEvent{Type: "agent_end", Messages: historyMessages(n)}
		return func() { wireSink += encodeEventForWire(event) }
	}})
	testscaling.Check(t, testscaling.Case{Name: "before_provider_request payload at history n (one handler call)", Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
		messages := make([]any, 0, n)
		for i := range n {
			messages = append(messages, map[string]any{"role": "user", "content": fmt.Sprintf("message %d with some text", i)})
		}
		event := extension.BeforeProviderRequestEvent{Type: "before_provider_request", Payload: map[string]any{"model": "m", "messages": messages, "stream": true}}
		return func() { wireSink += encodeEventForWire(event) }
	}})
	testscaling.Check(t, testscaling.Case{Name: "session_before_compact at branch n entries (one handler call)", Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
		event := extension.SessionBeforeCompactEvent{Type: "session_before_compact", BranchEntries: historyEntries(n), Reason: "threshold"}
		return func() { wireSink += encodeEventForWire(event) }
	}})
	testscaling.Check(t, testscaling.Case{Name: "session_before_tree at n entries to summarize (one handler call)", Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
		event := extension.SessionBeforeTreeEvent{Type: "session_before_tree", Preparation: extension.TreePreparation{TargetID: "e0", EntriesToSummarize: historyEntries(n)}}
		return func() { wireSink += encodeEventForWire(event) }
	}})
	testscaling.Check(t, testscaling.Case{Name: "session_tree at history n", Size: 100, Bound: testscaling.Constant, Prepare: func(n int) func() {
		_ = historyEntries(n)
		leaf := fmt.Sprintf("e%d", n)
		event := extension.SessionTreeEvent{Type: "session_tree", NewLeafID: &leaf, OldLeafID: &leaf}
		return func() { wireSink += encodeEventForWire(event) }
	}})
}

// Events about one message or one tool call grow with that message's or result's size M, once per call: linear in M.
func TestBoundaryScalingMessageSizeEvents(t *testing.T) {
	text := func(n int) string { return strings.Repeat("0123456789", n) }
	testscaling.Check(t, testscaling.Case{Name: "message_end of an assistant message of n*10 bytes", Size: 1000, Bound: testscaling.Linear, Prepare: func(n int) func() {
		message := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: text(n)}}, StopReason: ai.StopReasonStop}}
		event := extension.MessageEndEvent{Type: "message_end", Message: message}
		return func() { wireSink += encodeEventForWire(event) }
	}})
	testscaling.Check(t, testscaling.Case{Name: "tool_execution_end with a result of n*10 bytes", Size: 1000, Bound: testscaling.Linear, Prepare: func(n int) func() {
		result := map[string]any{"content": []any{map[string]any{"type": "text", "text": text(n)}}}
		event := extension.ToolExecutionEndEvent{Type: "tool_execution_end", ToolCallID: "c1", ToolName: "bash", Result: result}
		return func() { wireSink += encodeEventForWire(event) }
	}})
	testscaling.Check(t, testscaling.Case{Name: "turn_end without a boundary state, tool results of n*10 bytes", Size: 1000, Bound: testscaling.Linear, Prepare: func(n int) func() {
		result := agent.ToolResultMessage{Role: "toolResult", ToolCallID: "c1", ToolName: "bash", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text(n)}}}
		event := extension.TurnEndEvent{Type: "turn_end", Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", StopReason: ai.StopReasonStop}}, ToolResults: []extension.ToolResultMessage{result}}
		return func() { wireSink += encodeEventForWire(event) }
	}})
}
