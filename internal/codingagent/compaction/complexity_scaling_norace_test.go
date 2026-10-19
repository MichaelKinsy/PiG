//go:build !race

package compaction

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// Complexity gates for compaction and context-usage estimation.

func scalingBranch(n int) []codingagent.SessionEntry {
	s := codingagent.NewSession("scaling", "/tmp/scaling")
	for i := range n {
		call := "call_" + strconv.Itoa(i)
		usage := &ai.Usage{Input: 1000 + i, Output: 50, TotalTokens: 1050 + i}
		for _, message := range []agent.AgentMessage{
			{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: fmt.Sprintf("question %d about the repository layout", i)}}, Timestamp: int64(i)}},
			{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.ToolCall{ID: call, Name: "read", Arguments: ai.JsonObject{"path": fmt.Sprintf("file%d.go", i)}}}, StopReason: ai.StopReasonToolUse, Usage: usage, Timestamp: int64(i)}},
			{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: call, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "package main\n\nfunc main() {}\n"}}, Timestamp: int64(i)}},
			{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("answer %d: one file per upstream module", i)}}, StopReason: ai.StopReasonStop, Usage: usage, Timestamp: int64(i)}},
		} {
			if _, err := s.AppendMessage(message); err != nil {
				panic(err)
			}
		}
	}
	return s.GetBranch()
}

func TestScalingCompactionAndContextEstimate(t *testing.T) {
	settings := CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 2000}
	// prepareCompaction projects the branch once and walks it backwards once for the cut point.
	testscaling.Check(t, testscaling.Case{Name: "compaction prepareCompaction at history n", Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
		branch := scalingBranch(n)
		return func() {
			if PrepareCompaction(branch, settings) == nil {
				panic("nothing to compact")
			}
		}
	}})
	testscaling.Check(t, testscaling.Case{Name: "compaction findCutPoint at history n", Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
		branch := scalingBranch(n)
		return func() { FindCutPoint(branch, 0, len(branch), settings.KeepRecentTokens) }
	}})
	// The summary request serializes every summarized message once.
	testscaling.Check(t, testscaling.Case{Name: "compaction serializeConversation at history n", Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
		messages := agent.ConvertToLLM(codingagent.ProjectSessionPath(scalingBranch(n)).Messages)
		return func() { SerializeConversation(messages) }
	}})
	// Context usage projects the branch (linear) and then estimates only the messages after the last assistant usage.
	testscaling.Check(t, testscaling.Case{Name: "context usage estimate at history n", Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
		branch := scalingBranch(n)
		return func() { EstimateProjectedContextTokens(codingagent.ProjectSessionPath(branch), branch) }
	}})
	testscaling.Check(t, testscaling.Case{Name: "estimateContextTokens over projected messages at history n", Size: 100, Bound: testscaling.Constant, Prepare: func(n int) func() {
		messages := codingagent.ProjectSessionPath(scalingBranch(n)).Messages
		return func() { EstimateContextTokens(messages) }
	}})
	// The branch summary walks the abandoned branch newest first within the token budget; collecting the messages must not copy the list again for each one.
	testscaling.Check(t, testscaling.Case{Name: "branch summary PrepareBranchEntries at history n", Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
		branch := scalingBranch(n)
		return func() {
			if _, err := PrepareBranchEntries(branch, 1_000_000_000); err != nil {
				panic(err)
			}
		}
	}})
}
