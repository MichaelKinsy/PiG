package codingagent

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi 014873d7 test/codemode-renderer.test.ts "shows the elapsed time while the script runs and the total once it finished" (#10708):
// the codemode row shows `Elapsed` from the first partial update and `Took` with the recorded duration once it ends, and the
// elapsed clock is not shown after the result is final.
func TestCodemodeRowShowsElapsedWhileRunningAndTookOnceFinished(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		def := baseToolDefinition("codemode")
		def.RenderCall, def.RenderResult = CodemodeRenderers.RenderCall, CodemodeRenderers.RenderResult
		f := toolComponent(t, "codemode", "tool-codemode-timer", map[string]any{"code": ""}, &def)
		f.mode.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: f.id, ToolName: f.name, Args: json.RawMessage(`{"code":""}`)})
		f.mode.handleAgentEvent(agent.ToolExecutionUpdateEvent{ToolCallID: f.id, ToolName: f.name, PartialResult: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{}, Details: map[string]any{"calls": []any{}}}})
		time.Sleep(3 * time.Second)
		f.card.Invalidate()
		assertToolContains(t, f.plain(120), "Elapsed 3.0s")

		f.mode.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: f.id, ToolName: f.name, Result: agent.AgentToolResult{Details: map[string]any{"calls": []any{}}}, DurationMs: new(int64(5100))})
		final := f.plain(120)
		assertToolContains(t, final, "Took 5.1s")
		if strings.Contains(final, "Elapsed") {
			t.Fatalf("a finished row still shows Elapsed: %q", final)
		}
	})
}
