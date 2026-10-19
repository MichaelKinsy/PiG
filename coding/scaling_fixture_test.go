package coding

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// scalingDeltaProvider streams one text answer in a fixed number of deltas. Its own cost is linear in the deltas and does not read the transcript, so a gate measures the Session.
// A positive durationMs is the answer's durationMs. The event stream measures a response's duration with a monotonic clock only when the message has none (ai/event_stream.go timeLocked, event-stream.ts:127), so a test that compares the requests of two runs pins it: two runs of one provider otherwise differ by whole milliseconds on a loaded host.
type scalingDeltaProvider struct {
	deltas     int
	durationMs int64
}

func (p scalingDeltaProvider) ID() string   { return "scaling" }
func (p scalingDeltaProvider) Close() error { return nil }

func (p scalingDeltaProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	full := strings.Repeat("word ", p.deltas)
	partial := func(text string) *ai.AssistantMessage {
		return &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, API: "scaling", Provider: "scaling", Model: "scaling-model", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
	}
	stream := ai.NewAssistantMessageEventStream()
	push := func(event ai.AssistantMessageEvent) {
		if err := stream.Push(event); err != nil {
			panic(err)
		}
	}
	push(ai.StartEvent{Partial: partial("")})
	push(ai.TextStartEvent{ContentIndex: 0, Partial: partial("")})
	for i := range p.deltas {
		push(ai.TextDeltaEvent{ContentIndex: 0, Delta: "word ", Partial: partial(full[:5*(i+1)])})
	}
	push(ai.TextEndEvent{ContentIndex: 0, Content: full, Partial: partial(full)})
	final := partial(full)
	final.Usage = ai.Usage{Input: 1000, Output: 50, TotalTokens: 1050}
	if p.durationMs > 0 {
		final.DurationMs = &p.durationMs
	}
	push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: final})
	return stream, nil
}

func scalingSessionManager(t *testing.T, n int) *SessionManager {
	manager, err := NewInMemorySessionManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		call := "call_" + strconv.Itoa(i)
		usage := &ai.Usage{Input: 1000 + i, Output: 50, TotalTokens: 1050 + i}
		for _, message := range []agent.AgentMessage{
			{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: fmt.Sprintf("question %d about the repository layout", i)}}, Timestamp: int64(i)}},
			{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.ToolCall{ID: call, Name: "read", Arguments: ai.JsonObject{"path": fmt.Sprintf("file%d.go", i)}}}, StopReason: ai.StopReasonToolUse, Usage: usage, Provider: "scaling", ModelID: "scaling-model", Timestamp: int64(i)}},
			{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: call, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "package main\n\nfunc main() {}\n"}}, Timestamp: int64(i)}},
			{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("answer %d: one file per upstream module", i)}}, StopReason: ai.StopReasonStop, Usage: usage, Provider: "scaling", ModelID: "scaling-model", Timestamp: int64(i)}},
		} {
			if _, err := manager.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
		}
	}
	return manager
}
