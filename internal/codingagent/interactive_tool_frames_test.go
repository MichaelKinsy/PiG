package codingagent

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi requests a frame at tool_execution_start and at tool_execution_end (interactive-mode.ts:3754 and 3773, this.ui.requestRender()), and
// requestRender coalesces the requests of one event-loop turn into one frame, no sooner than 16 ms after the frame before it
// (tui.ts:1033-1084). A tool that starts and ends in one turn of the owner loop therefore writes nothing while its events are handled,
// and one frame afterwards.
func TestToolExecutionStartAndEndShareOneThrottledFrame(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, term := newTickRenderProbe(t, mode)
				settle := func() string {
					time.Sleep(100 * time.Millisecond)
					synctest.Wait()
					m.drainMainLoopOnce()
					return term.take()
				}
				settle()
				m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "a", ToolName: "read", Args: json.RawMessage(`{"path":"a.txt"}`)})
				m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "a", ToolName: "read", Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "FINAL"}}}})
				if wrote := term.take(); wrote != "" {
					t.Fatalf("tool_execution_start and tool_execution_end wrote %d bytes while they were handled, want their frame left to the throttle: %q", len(wrote), wrote)
				}
				frame := settle()
				if !strings.Contains(widthx.StripAnsi(frame), "a.txt") {
					t.Fatalf("the throttled frame does not show the tool card: %q", frame)
				}
				if again := settle(); again != "" {
					t.Fatalf("a second frame followed the tool's frame: %q", again)
				}
			})
		})
	}
}
