package extensionconformance

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

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
		// Agent lifecycle and message events (types.ts AgentStartEvent :932, AgentEndEvent :937, AgentSettledEvent :1005, MessageStartEvent :1047, MessageEndEvent :1060).
		{"agent_start", genericEmit(extension.AgentStartEvent{Type: "agent_start"}), `{"type":"agent_start"}`},
		{"agent_end", genericEmit(extension.AgentEndEvent{Type: "agent_end", Messages: []extension.AgentMessage{wireAgentMessage(map[string]any{"role": "user", "content": "hello", "timestamp": float64(1700000000000)})}}),
			`{"type":"agent_end","messages":[{"role":"user","content":"hello","timestamp":1700000000000}]}`},
		{"agent_settled", genericEmit(extension.AgentSettledEvent{Type: "agent_settled", Aborted: true}), `{"type":"agent_settled","aborted":true}`},
		// aborted is a required boolean (types.ts:1008): false stays on the wire.
		{"agent_settled", genericEmit(extension.AgentSettledEvent{Type: "agent_settled", Aborted: false}), `{"type":"agent_settled","aborted":false}`},
		{"message_start", genericEmit(extension.MessageStartEvent{Type: "message_start", Message: wireAgentMessage(map[string]any{"role": "user", "content": "hello", "timestamp": float64(1700000000000)})}),
			`{"type":"message_start","message":{"role":"user","content":"hello","timestamp":1700000000000}}`},
		{"message_end", func(ctx context.Context, h *harness) error {
			_, err := h.runner.EmitMessageEnd(ctx, extension.MessageEndEvent{Type: "message_end", Message: wireAgentMessage(map[string]any{"role": "user", "content": "hello", "timestamp": float64(1700000000000)})})
			return err
		}, `{"type":"message_end","message":{"role":"user","content":"hello","timestamp":1700000000000}}`},
		// Session lifecycle events (types.ts SessionInfoChangedEvent :750, SessionCompactFailedEvent :795, SessionShutdownEvent :810). An empty name is `undefined` in Pi, so the member is omitted.
		{"session_info_changed", genericEmit(extension.SessionInfoChangedEvent{Type: "session_info_changed", Name: "My session"}), `{"type":"session_info_changed","name":"My session"}`},
		{"session_shutdown", genericEmit(extension.SessionShutdownEvent{Type: "session_shutdown", Reason: "resume", TargetSessionFile: "/next.jsonl"}), `{"type":"session_shutdown","reason":"resume","targetSessionFile":"/next.jsonl"}`},
		{"session_compact_failed", genericEmit(extension.SessionCompactFailedEvent{Type: "session_compact_failed", Reason: "overflow", ErrorMessage: "too big", Aborted: true, WillRetry: true, FromExtension: true}),
			`{"type":"session_compact_failed","reason":"overflow","errorMessage":"too big","aborted":true,"willRetry":true,"fromExtension":true}`},
		// Provider and context events (types.ts ContextWithSystemEvent :881, BeforeProviderHeadersEvent :897, AfterProviderResponseEvent :903). A null header value asks the host to delete that header (ProviderHeaders is Record<string, string | null>).
		{"context_with_system", func(ctx context.Context, h *harness) error {
			_, err := h.runner.EmitContextWithSystem(ctx, []extension.AgentMessage{wireAgentMessage(map[string]any{"role": "user", "content": "hello", "timestamp": float64(1700000000000)})})
			return err
		}, `{"type":"context_with_system","messages":[{"role":"user","content":"hello","timestamp":1700000000000}]}`},
		{"before_provider_headers", func(ctx context.Context, h *harness) error {
			_, err := h.runner.EmitBeforeProviderHeaders(ctx, extension.ProviderHeaders{"x-keep": new("1"), "x-drop": nil})
			return err
		}, `{"type":"before_provider_headers","headers":{"x-keep":"1","x-drop":null}}`},
		{"after_provider_response", genericEmit(extension.AfterProviderResponseEvent{Type: "after_provider_response", Status: 429, Headers: map[string]string{"retry-after": "3"}}),
			`{"type":"after_provider_response","status":429,"headers":{"retry-after":"3"}}`},
		// Boundary events (types.ts BoundaryState :987, TurnEndEvent :1037, AgentBeforeSettleEvent :1000): entries, continue, context preview and outcome reach the handler unchanged; the turn_end fields keep their names.
		{"turn_end", genericEmit(extension.TurnEndEvent{
			BoundaryState: boundaryStateFixture(), Type: "turn_end", TurnIndex: 2,
			Message:     wireAgentMessage(map[string]any{"role": "user", "content": "hi", "timestamp": float64(1700000000000)}),
			ToolResults: []extension.ToolResultMessage{}, MessageEntryID: "assistant-entry", ToolResultEntryIds: []string{"tool-entry"},
		}), `{"type":"turn_end","turnIndex":2,"message":{"role":"user","content":"hi","timestamp":1700000000000},"toolResults":[],"messageEntryId":"assistant-entry","toolResultEntryIds":["tool-entry"],` + boundaryStateJSON + `}`},
		// session_compact carries a CompactionEntry (types.ts :784, session-manager.ts CompactionEntry); provider_stream_event carries the provider's opaque data (types.ts ProviderStreamEvent).
		{"session_compact", func(ctx context.Context, h *harness) error {
			var entry extension.CompactionEntry
			if err := json.Unmarshal([]byte(compactionEntryJSON), &entry); err != nil {
				return err
			}
			return genericEmit(extension.SessionCompactEvent{Type: "session_compact", CompactionEntry: entry, FromExtension: true, Reason: "overflow", WillRetry: true})(ctx, h)
		}, `{"type":"session_compact","compactionEntry":` + compactionEntryJSON + `,"fromExtension":true,"reason":"overflow","willRetry":true}`},
		{"provider_stream_event", genericEmit(extension.ProviderStreamEvent{Type: "provider_stream_event", Provider: "anthropic", API: "anthropic-messages", Model: "m1", Data: map[string]any{"k": []any{1, "two", nil}}}),
			`{"type":"provider_stream_event","provider":"anthropic","api":"anthropic-messages","model":"m1","data":{"k":[1,"two",null]}}`},
		// message_update pairs the growing assistant message with the provider's delta event (types.ts MessageUpdateEvent); the delta is opaque to the host, so it must arrive as written.
		{"message_update", func(ctx context.Context, h *harness) error {
			var message map[string]any
			if err := json.Unmarshal([]byte(assistantMessageJSON), &message); err != nil {
				return err
			}
			return genericEmit(extension.MessageUpdateEvent{Type: "message_update", Message: wireAgentMessage(message),
				AssistantMessageEvent: map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "x", "partial": message}})(ctx, h)
		}, `{"type":"message_update","message":` + assistantMessageJSON + `,"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"x","partial":` + assistantMessageJSON + `}}`},
		// model_select carries Pi's model object (runner.ts:609; ai types.ts Model :1132: id, name, api, provider, baseUrl, reasoning, input, cost, contextWindow, maxTokens); unset optional members such as headers, compat and thinkingLevelMap are absent.
		{"model_select", genericEmit(extension.ModelSelectEvent{Type: "model_select", Model: payloadModel("m2"), PreviousModel: payloadModel("m1"), Source: "cycle"}),
			`{"type":"model_select","model":` + payloadModelJSON("m2") + `,"previousModel":` + payloadModelJSON("m1") + `,"source":"cycle"}`},
		// Events whose handlers can answer (types.ts ResourcesDiscoverEvent :53, InputEvent, ContextEvent, BeforeProviderRequestEvent, CacheWarmingDecisionEvent, SessionBeforeSwitch/Fork/TreeEvent): the probe answers
		// nothing, so the host's emit chain must still deliver each payload to the handler unchanged.
		{"resources_discover", genericEmit(extension.ResourcesDiscoverEvent{Type: "resources_discover", Cwd: "/work/project", Reason: "reload"}),
			`{"type":"resources_discover","cwd":"/work/project","reason":"reload"}`},
		{"input", genericEmit(extension.InputEvent{Type: "input", Text: "hello", Source: "interactive", StreamingBehavior: "followUp",
			Images: []extension.ImageContent{{Data: "aW1n", MimeType: "image/png"}}}),
			`{"type":"input","text":"hello","images":[{"type":"image","data":"aW1n","mimeType":"image/png"}],"source":"interactive","streamingBehavior":"followUp"}`},
		{"context", genericEmit(extension.ContextEvent{Type: "context", Messages: []extension.AgentMessage{wireAgentMessage(map[string]any{"role": "user", "content": "hi", "timestamp": float64(1700000000000)})}}),
			`{"type":"context","messages":[{"role":"user","content":"hi","timestamp":1700000000000}]}`},
		{"before_provider_request", genericEmit(extension.BeforeProviderRequestEvent{Type: "before_provider_request", Payload: map[string]any{"model": "m1", "messages": []any{map[string]any{"role": "user", "content": "x"}}, "temperature": 0.5, "stop": nil}}),
			`{"type":"before_provider_request","payload":{"model":"m1","messages":[{"role":"user","content":"x"}],"temperature":0.5,"stop":null}}`},
		{"cache_warming_decision", genericEmit(extension.CacheWarmingDecisionEvent{Type: "cache_warming_decision", WarmCost: 0.25, MissCost: 1.5, ContinuationProbability: 0.75, Action: extension.CacheWarmingActionWarm}),
			`{"type":"cache_warming_decision","warmCost":0.25,"missCost":1.5,"continuationProbability":0.75,"action":"warm"}`},
		{"session_before_switch", genericEmit(extension.SessionBeforeSwitchEvent{Type: "session_before_switch", Reason: "resume", TargetSessionFile: "/sessions/a.jsonl"}),
			`{"type":"session_before_switch","reason":"resume","targetSessionFile":"/sessions/a.jsonl"}`},
		{"session_before_fork", genericEmit(extension.SessionBeforeForkEvent{Type: "session_before_fork", EntryID: "e1", Position: "at"}),
			`{"type":"session_before_fork","entryId":"e1","position":"at"}`},
		{"session_before_tree", genericEmit(extension.SessionBeforeTreeEvent{Type: "session_before_tree", Preparation: extension.TreePreparation{
			TargetID: "t1", OldLeafID: new("o1"), EntriesToSummarize: []extension.SessionEntry{}, UserWantsSummary: true, CustomInstructions: "be brief", ReplaceInstructions: true, Label: "branch"}}),
			`{"type":"session_before_tree","preparation":{"targetId":"t1","oldLeafId":"o1","commonAncestorId":null,"entriesToSummarize":[],"userWantsSummary":true,"customInstructions":"be brief","replaceInstructions":true,"label":"branch"}}`},
		// fileOps are JavaScript Sets in Pi (session-manager compaction types): the host sends their members as arrays, and the probe reports Node's Set the same way.
		{"session_before_compact", func(ctx context.Context, h *harness) error {
			var preparation extension.CompactionPreparation
			if err := json.Unmarshal([]byte(strings.Replace(compactionPreparationJSON, `"fileOps":{"read":["a.txt"],"written":[],"edited":["b.go"]}`, `"fileOps":{"read":{"a.txt":{}},"written":{},"edited":{"b.go":{}}}`, 1)), &preparation); err != nil {
				return err
			}
			return genericEmit(extension.SessionBeforeCompactEvent{Type: "session_before_compact", Preparation: preparation, BranchEntries: []extension.SessionEntry{},
				CustomInstructions: "keep names", Reason: "threshold", WillRetry: true})(ctx, h)
		}, `{"type":"session_before_compact","preparation":` + compactionPreparationJSON + `,"branchEntries":[],"customInstructions":"keep names","reason":"threshold","willRetry":true}`},
		// before_agent_start (Pi 1.2.0 types.ts:932-957 BeforeAgentStartUserEvent): messageType, the run's prompt and images, current system prompt and the options it was built from.
		{"before_agent_start", func(ctx context.Context, h *harness) error {
			_, err := h.runner.EmitBeforeAgentStart(ctx, extension.UserPromptTrigger("hello", []extension.ImageContent{{Data: "aW1n", MimeType: "image/png"}}), extension.BuildSystemPromptOptions{CustomPrompt: "custom", SelectedTools: []string{"read", "bash"}, PromptGuidelines: []string{"be brief"}, Cwd: "/work/project"})
			return err
		}, `{"type":"before_agent_start","messageType":"user","prompt":"hello","images":[{"type":"image","data":"aW1n","mimeType":"image/png"}],"systemPrompt":"custom\n\n<cwd>\n/work/project\n</cwd>",` +
			`"systemPromptOptions":{"appendSystemPrompt":"","contextFiles":[],"customPrompt":"custom","cwd":"/work/project","hiddenTools":[],"promptGuidelines":["be brief"],"sections":{},"selectedTools":["read","bash"],"skills":[],"toolGuidelines":{},"toolSnippets":{}}}`},
		{"agent_before_settle", genericEmit(extension.AgentBeforeSettleEvent{Type: "agent_before_settle", BoundaryState: *boundaryStateFixture()}),
			`{"type":"agent_before_settle",` + boundaryStateJSON + `}`},
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
					pollUntilConformance(t, c.event+": the handler never reported its payload", func() bool {
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
					// Pi's session_before_compact and session_before_tree events carry an AbortSignal (types.ts :780, :836). The Node SDK passes it through as the
					// object it is, which serializes to {}; the other SDKs have no event member for it, and their handler's request cancellation (ctx.Done(),
					// is_cancelled) is the same signal in the language's own form.
					if object, ok := got.(map[string]any); ok && strings.HasPrefix(tc.name, "subprocess-node") {
						if signal, present := object["signal"]; present && reflect.DeepEqual(signal, map[string]any{}) {
							delete(object, "signal")
						}
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

// boundaryStateFixture is a BoundaryState with one draft of each shape the fixtures can tell apart: a custom entry with data, and a compaction that keeps nothing (firstKeptEntryId null is not omitted).
func boundaryStateFixture() *extension.BoundaryState {
	return &extension.BoundaryState{
		Entries: []extension.SessionBoundaryDraft{
			{Type: extension.SessionBoundaryDraftCustom, CustomType: "note", Data: map[string]any{"n": 1}},
		},
		Continue: true,
		Context: extension.BoundaryContextPreview{
			ContextEntries:  []extension.ProjectedSessionEntry{},
			ContextMessages: []extension.AgentMessage{wireAgentMessage(map[string]any{"role": "user", "content": "hi", "timestamp": float64(1700000000000)})},
			LLMMessages:     []ai.Message{},
			PendingMessages: []extension.AgentMessage{},
			CanContinue:     true,
		},
		Outcome: extension.AgentActivityCompleted,
	}
}

const boundaryStateJSON = `"entries":[{"type":"custom","customType":"note","data":{"n":1}}],"continue":true,` +
	`"context":{"contextEntries":[],"contextMessages":[{"role":"user","content":"hi","timestamp":1700000000000}],"llmMessages":[],"pendingMessages":[],"canContinue":true},"outcome":"completed"`

const compactionEntryJSON = `{"type":"compaction","id":"c1","parentId":"p1","timestamp":"2026-10-10T00:00:00.000Z","summary":"s","firstKeptEntryId":"k1","tokensBefore":1234,"details":{"b":1,"a":2},"fromHook":true}`

const assistantMessageJSON = `{"role":"assistant","content":[{"type":"text","text":"x"}],"api":"anthropic-messages","provider":"anthropic","model":"m1",` +
	`"usage":{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"totalTokens":10,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1700000000000}`

func payloadModel(id string) *ai.Model {
	provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Model: id})
	return &ai.Model{ID: id, DisplayName: "Model " + id, Provider: provider, Input: []string{"text", "image"},
		Capabilities: ai.ModelCapabilities{ContextWindow: 4000, MaxOutputTokens: 1000},
		ProviderMeta: ai.ProviderMetadata{ProviderID: "faux", API: "anthropic-messages", BaseURL: "https://example.test"}}
}

func payloadModelJSON(id string) string {
	return `{"id":"` + id + `","name":"Model ` + id + `","api":"anthropic-messages","provider":"faux","baseUrl":"https://example.test","reasoning":false,` +
		`"input":["text","image"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":4000,"maxTokens":1000}`
}

const compactionPreparationJSON = `{"firstKeptEntryId":"k1","messagesToSummarize":[],"turnPrefixMessages":[],"isSplitTurn":true,"tokensBefore":1234,"previousSummary":"before",` +
	`"fileOps":{"read":["a.txt"],"written":[],"edited":["b.go"]},"settings":{"enabled":true,"reserveTokens":16384,"keepRecentTokens":20000}}`
