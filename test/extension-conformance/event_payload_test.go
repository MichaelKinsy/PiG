package extensionconformance

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestConformance_EventPayloadsReachEverySDKUnchanged pins the payload of the pi.on(event) overloads (extensions/types.ts: SessionStartEvent
// :741, SessionTreeEvent :840, TurnStartEvent :1030, ToolExecutionStartEvent :1066, ToolExecutionUpdateEvent :1076, ToolExecutionEndEvent
// :1087, ThinkingLevelSelectEvent :1114, UserBashEvent :1125, ToolCallEvent :1165, ToolResultEvent :1239, UIPromptStartEvent and
// UIPromptEndEvent) for every SDK, isolated and packed. The production Runner dispatches a fully populated event; the handler each SDK
// registers reports the JSON it received, and the test compares it with the object Pi's TypeScript declaration describes, written out
// by hand: every declared member under its Pi name, an optional member present only when set, and null where Pi's type is `| null`.
func TestConformance_EventPayloadsReachEverySDKUnchanged(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	summaryEntry := new(extension.BranchSummaryEntry)
	if err := json.Unmarshal([]byte(`{"type":"branch_summary","id":"s1","parentId":"n0","timestamp":"2026-01-01T00:00:00.000Z","fromId":"o1","summary":"branch summary","fromHook":true}`), summaryEntry); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		event string
		emit  func(context.Context, *harness) error
		want  string
	}{
		{"session_start", genericEmit(extension.SessionStartEvent{Type: "session_start", Reason: "fork", PreviousSessionFile: "/prev.jsonl"}),
			`{"type":"session_start","reason":"fork","previousSessionFile":"/prev.jsonl"}`},
		{"session_tree", genericEmit(extension.SessionTreeEvent{Type: "session_tree", NewLeafID: new("n1"), OldLeafID: nil, SummaryEntry: summaryEntry, FromExtension: true}),
			`{"type":"session_tree","newLeafId":"n1","oldLeafId":null,"summaryEntry":{"type":"branch_summary","id":"s1","parentId":"n0","timestamp":"2026-01-01T00:00:00.000Z","fromId":"o1","summary":"branch summary","fromHook":true},"fromExtension":true}`},
		{"thinking_level_select", genericEmit(extension.ThinkingLevelSelectEvent{Type: "thinking_level_select", Level: ai.ThinkingHigh, PreviousLevel: ai.ThinkingLow}),
			`{"type":"thinking_level_select","level":"high","previousLevel":"low"}`},
		{"turn_start", genericEmit(extension.TurnStartEvent{Type: "turn_start", TurnIndex: 3, Timestamp: 1700000000123}),
			`{"type":"turn_start","turnIndex":3,"timestamp":1700000000123}`},
		{"tool_execution_start", genericEmit(extension.ToolExecutionStartEvent{Type: "tool_execution_start", ToolCallID: "c1", ToolName: "read", Args: map[string]any{"path": "a"}, ParentToolCallID: "p1"}),
			`{"type":"tool_execution_start","toolCallId":"c1","toolName":"read","args":{"path":"a"},"parentToolCallId":"p1"}`},
		{"tool_execution_update", genericEmit(extension.ToolExecutionUpdateEvent{Type: "tool_execution_update", ToolCallID: "c1", ToolName: "read", Args: map[string]any{"path": "a"}, PartialResult: map[string]any{"content": []any{}}, ParentToolCallID: "p1"}),
			`{"type":"tool_execution_update","toolCallId":"c1","toolName":"read","args":{"path":"a"},"partialResult":{"content":[]},"parentToolCallId":"p1"}`},
		{"tool_execution_end", genericEmit(extension.ToolExecutionEndEvent{Type: "tool_execution_end", ToolCallID: "c1", ToolName: "read", Result: map[string]any{"content": []any{}}, IsError: true, DurationMs: new(int64(42)), ParentToolCallID: "p1"}),
			`{"type":"tool_execution_end","toolCallId":"c1","toolName":"read","result":{"content":[]},"isError":true,"durationMs":42,"parentToolCallId":"p1"}`},
		{"ui_prompt_start", genericEmit(extension.UIPromptStartEvent{Type: "ui_prompt_start", Reason: "ui_prompt", Kind: "select", Title: "Pick"}),
			`{"type":"ui_prompt_start","reason":"ui_prompt","kind":"select","title":"Pick"}`},
		{"ui_prompt_end", genericEmit(extension.UIPromptEndEvent{Type: "ui_prompt_end", Reason: "ui_prompt", Kind: "select"}),
			`{"type":"ui_prompt_end","reason":"ui_prompt","kind":"select"}`},
		{"user_bash", func(ctx context.Context, h *harness) error {
			_, err := h.runner.EmitUserBash(ctx, extension.UserBashEvent{Type: "user_bash", Command: "probe-payload", ExcludeFromContext: true, Cwd: "/work"})
			return err
		}, `{"type":"user_bash","command":"probe-payload","excludeFromContext":true,"cwd":"/work"}`},
		{"tool_call", func(ctx context.Context, h *harness) error {
			_, err := h.runner.EmitToolCall(ctx, extension.CustomToolCallEvent{
				ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "c1", ParentToolCallID: "p1"}, ToolName: "probe", Input: map[string]any{"a": float64(1)},
			})
			return err
		}, `{"type":"tool_call","toolCallId":"c1","parentToolCallId":"p1","toolName":"probe","input":{"a":1}}`},
		{"tool_result", func(ctx context.Context, h *harness) error {
			_, err := h.runner.EmitToolResult(ctx, extension.CustomToolResultEvent{
				ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", ToolCallID: "c1", ParentToolCallID: "p1", Input: map[string]any{"a": float64(1)}, Content: []any{map[string]any{"type": "text", "text": "ok"}}, IsError: true},
				ToolName:            "probe",
			})
			return err
		}, `{"type":"tool_result","toolCallId":"c1","parentToolCallId":"p1","input":{"a":1},"content":[{"type":"text","text":"ok"}],"isError":true,"toolName":"probe"}`},
	}

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			ctx := context.Background()
			for _, c := range cases {
				t.Run(c.event, func(t *testing.T) {
					prefix := "event_payload:" + c.event + ":"
					*h.notify = (*h.notify)[:0]
					runConformanceCommandArgs(t, h, "event_payload_probe", c.event)
					if err := c.emit(ctx, h); err != nil {
						t.Fatalf("emit %s: %v", c.event, err)
					}
					var reported string
					pollUntilConformance(t, 5*time.Second, c.event+": the handler never reported its payload", func() bool {
						for _, n := range *h.notify {
							if rest, ok := strings.CutPrefix(n, prefix); ok {
								reported = strings.TrimSuffix(rest, ":info")
								return true
							}
						}
						return false
					})
					var got, want any
					if err := json.Unmarshal([]byte(reported), &got); err != nil {
						t.Fatalf("%s payload %q is not JSON: %v", c.event, reported, err)
					}
					if err := json.Unmarshal([]byte(c.want), &want); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("%s payload = %s, want %s", c.event, reported, c.want)
					}
				})
			}
		})
	}
}
