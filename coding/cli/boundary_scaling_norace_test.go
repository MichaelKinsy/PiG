//go:build !race

package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/rpcclient"
	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// The boundary scaling audit, RPC and JSON event lines: the bytes pig writes per agent event as the stream (M) or the history (H) grows. Pi writes the same lines (the parity scenarios rpc/* and json/* compare them byte for byte), so a quadratic row here is Pi's too. The measured cost is rpcAgentEvent plus rpcclient.SerializeJsonLine of each frame it returns, the work subscribeRPCEvents and writeJSONLine do per event.
var rpcLineSink int

func encodeRPCEvent(event agent.AgentEvent) {
	lines, err := rpcAgentEvent(event)
	if err != nil {
		panic(err)
	}
	for _, line := range lines {
		encoded, err := rpcclient.SerializeJsonLine(line)
		if err != nil {
			panic(err)
		}
		rpcLineSink += len(encoded)
	}
}

// Pi's rpc message_update drops the cumulative partial and sends the delta (rpcAssistantEventFields): a stream of n deltas writes O(n) bytes.
func TestBoundaryScalingRPCMessageUpdateStream(t *testing.T) {
	testscaling.Check(t, testscaling.Case{Name: "rpc message_update lines of an n-delta message", Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
		full := strings.Repeat("word ", n)
		return func() {
			for i := range n {
				partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: full[:5*(i+1)]}}, StopReason: ai.StopReasonStop}
				message := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: partial.Content, StopReason: ai.StopReasonStop}}
				encodeRPCEvent(agent.MessageUpdateEvent{Message: message, AssistantMessageEvent: ai.TextDeltaEvent{ContentIndex: 0, Delta: "word ", Partial: partial}})
			}
		}
	}})
}

// tool_execution_update carries the complete-so-far result (agent.ToolExecutionUpdateEvent.PartialResult: upstream's onUpdate contract, agent-loop.ts:778-786): a command that prints n chunks writes O(n^2) bytes in rpc and json mode, as Pi does.
func TestBoundaryScalingRPCToolExecutionUpdateStream(t *testing.T) {
	testscaling.Check(t, testscaling.Case{Name: "rpc tool_execution_update lines of an n-chunk command output", Size: 100, Bound: testscaling.Quadratic, Prepare: func(n int) func() {
		chunk := strings.Repeat("output line of a long running command\n", 2)
		return func() {
			var text strings.Builder
			for range n {
				text.WriteString(chunk)
				encodeRPCEvent(agent.ToolExecutionUpdateEvent{ToolCallID: "c1", ToolName: "bash", Args: json.RawMessage(`{"command":"make"}`), PartialResult: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text.String()}}}})
			}
		}
	}})
}

// agent_end carries every message of the run, once: linear in the messages.
func TestBoundaryScalingRPCAgentEnd(t *testing.T) {
	testscaling.Check(t, testscaling.Case{Name: "rpc agent_end line of an n-message run", Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
		messages := make([]agent.AgentMessage, 0, 2*n)
		for i := range n {
			call := fmt.Sprintf("call_%d", i)
			messages = append(messages,
				agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.ToolCall{ID: call, Name: "read", Arguments: ai.JsonObject{"path": fmt.Sprintf("file%d.go", i)}}}, StopReason: ai.StopReasonToolUse}},
				agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: call, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "package main\n"}}, Timestamp: int64(i)}})
		}
		return func() { encodeRPCEvent(agent.AgentEndEvent{Messages: messages}) }
	}})
}

// message_end and tool_execution_end carry the finished message or result once: linear in its size.
func TestBoundaryScalingRPCSingleMessageEvents(t *testing.T) {
	text := func(n int) string { return strings.Repeat("0123456789", n) }
	testscaling.Check(t, testscaling.Case{Name: "rpc message_end line of a message of n*10 bytes", Size: 1000, Bound: testscaling.Linear, Prepare: func(n int) func() {
		message := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: text(n)}}, StopReason: ai.StopReasonStop}}
		return func() { encodeRPCEvent(agent.MessageEndEvent{Message: message}) }
	}})
	testscaling.Check(t, testscaling.Case{Name: "rpc tool_execution_end line of a result of n*10 bytes", Size: 1000, Bound: testscaling.Linear, Prepare: func(n int) func() {
		result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text(n)}}}
		return func() {
			encodeRPCEvent(agent.ToolExecutionEndEvent{ToolCallID: "c1", ToolName: "bash", Result: result})
		}
	}})
}
