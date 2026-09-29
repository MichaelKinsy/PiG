package coding

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/src/core/agent-session.ts:1493-1508 consumes the just-finished assistant saved by message_end, not an older assistant selected from the edited projection.
func TestUpstreamSessionBoundariesOmittedLatestDoesNotRetryHistoricalError(t *testing.T) {
	handled := false
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"turn_end": {func(args ...any) (any, error) {
		if handled {
			return nil, nil
		}
		handled = true
		event := args[0].(extension.TurnEndEvent)
		return boundaryDrafts(false, extension.SessionBoundaryDraft{Type: "context_edit", TargetID: event.MessageEntryID}), nil
	}}}}
	h := newBoundaryHarness(t, harnessOptions{extension: ext, settings: `{"compaction":{"enabled":false},"retry":{"enabled":true,"maxRetries":1,"baseDelayMs":1}}`}, boundaryReply("current done", ai.StopReasonStop, 0), boundaryReply("must not retry history", ai.StopReasonStop, 0))
	if _, err := h.session.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: BuildUserContent("historical input", nil), Timestamp: time.Now().UnixMilli() - 5}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.session.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Provider: "faux", ModelID: "faux-1", Content: []ai.AssistantContentBlock{ai.TextContent{Text: ""}}, Usage: &ai.Usage{}, StopReason: ai.StopReasonError, ErrorMessage: "overloaded_error", Timestamp: time.Now().UnixMilli() - 4}}); err != nil {
		t.Fatal(err)
	}
	h.session.RefreshContext()
	boundaryPrompt(t, h, "current input")
	if h.provider.callCount() != 1 || len(boundaryEvents[agent.AutoRetryStartEvent](h)) != 0 {
		t.Errorf("retried history: calls=%d retry=%v", h.provider.callCount(), boundaryEvents[agent.AutoRetryStartEvent](h))
	}
	retained := projectionContains(h, "overloaded_error")
	if !retained {
		t.Error("historical error was omitted instead of the current response")
	}
	if os.Getenv("PIG_BOUNDARY_PROBE") == "1" && !t.Failed() {
		fmt.Println("SESSION_BOUNDARY_BRIDGE " + boundaryJSON(t, []any{h.provider.callCount(), retained}))
	}
}
