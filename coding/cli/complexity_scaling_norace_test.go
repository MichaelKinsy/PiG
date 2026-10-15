//go:build !race

package cli

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// Complexity gate for the state the host pushes to a subprocess extension before each event it dispatches (subprocess host.go pushStateTo), with the Session actions every mode binds.
func TestScalingExtensionEventStatePush(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	// The snapshot carries getContextUsage. The Session reuses its estimate until the log changes, so an event while the log stands still, such as a message_update per streamed delta, costs O(1) in the history; Pi computes the usage only when an extension calls ctx.getContextUsage() (agent-session.ts getContextUsage). Before the reuse each event projected the whole branch.
	testscaling.Check(t, testscaling.Case{Name: "extension event state push at history n", Size: 50, Bound: testscaling.Constant, Prepare: func(n int) func() {
		manager, err := coding.NewInMemorySessionManager(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		for i := range n {
			call := "call_" + strconv.Itoa(i)
			usage := &ai.Usage{Input: 1000 + i, Output: 50, TotalTokens: 1050 + i}
			for _, message := range []agent.AgentMessage{
				{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: fmt.Sprintf("question %d", i)}}, Timestamp: int64(i)}},
				{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.ToolCall{ID: call, Name: "read", Arguments: ai.JsonObject{"path": "f.go"}}}, StopReason: ai.StopReasonToolUse, Usage: usage, Timestamp: int64(i)}},
				{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: call, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "package main\n"}}, Timestamp: int64(i)}},
				{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("answer %d", i)}}, StopReason: ai.StopReasonStop, Usage: usage, Timestamp: int64(i)}},
			} {
				if _, err := manager.AppendMessage(message); err != nil {
					t.Fatal(err)
				}
			}
		}
		model := &ai.Model{ID: "scaling-model", DisplayName: "scaling", Capabilities: ai.ModelCapabilities{ContextWindow: 10_000_000}}
		session, err := coding.NewSession(services, coding.SessionOptions{Model: model, SessionManager: manager, ResourceLoader: coding.NoResources, SkipBuiltinTools: true, SystemPrompt: "Test"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		})
		bridge := subprocess.NewUIBridge(nil)
		bindSessionExtensionActions(nil, bridge, func() *coding.Session { return session }, extension.ContextActions{}, nil)
		// The first event after a change of the log computes the estimate.
		bridge.Snapshot(nil, 0, false)
		return func() { bridge.Snapshot(nil, 0, false) }
	}})
}
