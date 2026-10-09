package extensionconformance

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/compactiontypes"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// eventPayloadCase is one pi.on overload (packages/coding-agent/src/core/extensions/types.ts:1558-1642): the production Runner dispatch that
// delivers the event, the payload an extension handler in every SDK must receive (Pi's event interface member for member, as JSON),
// and, for an overload whose handler returns a result, the result the handler returns and what the host does with it.
type eventPayloadCase struct {
	event string
	// wireKeys names members the host serializes through a wire form other than the Go struct's JSON; modelIDs states their identity instead.
	wireKeys []string
	modelIDs map[string]string
	// sent is the event the host serializes to the handler; emit overrides the plain Runner.Emit dispatch for an event that has its own Emit method.
	sent extension.ExtensionEvent
	emit func(context.Context, *harness) (any, error)
	// members are the event interface's literal members (type, identifiers, reasons) Pi names; the whole received payload must also equal the event the host
	// serialized, so an SDK that drops, renames or rewrites any other member fails.
	members map[string]any
	// probes handlers subscribe (default one) and each returns the next of results; check receives what the dispatch returned to the emitter and the payloads
	// the handlers received, in order, so it can tell which handlers the dispatch still called.
	probes int
	// calls is how many of the probes the dispatch calls (default one).
	calls   int
	results []string
	check   func(*testing.T, any, []map[string]any)
}

func jsonValue(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %T: %v", value, err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	return out
}

func eventPayloadCases() []eventPayloadCase {
	user := map[string]any{"role": "user", "content": "hello", "timestamp": float64(1)}
	assistant := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "hi"}}, "timestamp": float64(2)}
	// A cancel from the first handler is the dispatch's result and the second handler never runs (runner.ts emit: a cancelling session_before_* result
	// is returned at once, types.ts:1474-1495).
	cancelled := func(t *testing.T, got any, received []map[string]any) {
		t.Helper()
		if !strings.Contains(string(payloadJSON(t, got)), `"first"`) || strings.Contains(string(payloadJSON(t, got)), `"second"`) {
			t.Fatalf("the dispatch did not return the first handler's cancel: %s", payloadJSON(t, got))
		}
		if len(received) != 1 {
			t.Fatalf("a handler ran after the cancel: %d payloads", len(received))
		}
	}
	twoCancels := []string{`{"cancel":true,"tag":"first"}`, `{"cancel":true,"tag":"second"}`}
	labelEntry := sessionentry.DecodeSessionEntry(json.RawMessage(`{"type":"label","id":"e1","parentId":null,"timestamp":"2026-01-01T00:00:00.000Z","targetId":"e0","label":"mark"}`))
	// Pi's CompactionPreparation (compaction.ts:772-788) as an extension receives it: FileOperations sets travel as sorted arrays.
	preparation := extension.CompactionPreparation{FirstKeptEntryID: "e2", MessagesToSummarize: []agent.AgentMessage{}, TurnPrefixMessages: []agent.AgentMessage{},
		IsSplitTurn: true, TokensBefore: 900, PreviousSummary: "earlier", FileOps: compactiontypes.FileOperations{Read: map[string]struct{}{"b.go": {}, "a.go": {}}},
		Settings: compactiontypes.CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000}}
	preparationJSON := map[string]any{"firstKeptEntryId": "e2", "messagesToSummarize": []any{}, "turnPrefixMessages": []any{}, "isSplitTurn": true, "tokensBefore": float64(900),
		"previousSummary": "earlier", "fileOps": map[string]any{"read": []any{"a.go", "b.go"}, "written": []any{}, "edited": []any{}},
		"settings": map[string]any{"enabled": true, "reserveTokens": float64(16384), "keepRecentTokens": float64(20000)}}
	tree := map[string]any{"targetId": "t1", "oldLeafId": nil, "commonAncestorId": nil, "entriesToSummarize": []any{}, "userWantsSummary": true, "label": "lbl"}
	return []eventPayloadCase{
		{
			event:   "message_start",
			sent:    extension.MessageStartEvent{Type: "message_start", Message: wireAgentMessage(user)},
			members: map[string]any{"type": "message_start", "message": user},
		},
		{
			event: "message_update",
			sent: extension.MessageUpdateEvent{Type: "message_update", Message: wireAgentMessage(assistant),
				AssistantMessageEvent: ai.TextDeltaEvent{ContentIndex: 0, Delta: "hi"}},
			members: map[string]any{"type": "message_update"},
		},
		{
			// MessageEndEventResult.message replaces the finalized message and keeps the role (types.ts:1463-1466, runner.ts emitMessageEnd).
			event: "message_end",
			sent:  extension.MessageEndEvent{Type: "message_end", Message: wireAgentMessage(user)},
			emit: func(ctx context.Context, h *harness) (any, error) {
				return h.runner.EmitMessageEnd(ctx, extension.MessageEndEvent{Type: "message_end", Message: wireAgentMessage(user)})
			},
			members: map[string]any{"type": "message_end", "message": user},
			// The second handler sees the first one's replacement and the last replacement is the result (runner.ts emitMessageEnd).
			probes:  2,
			calls:   2,
			results: []string{`{"message":{"role":"user","content":"first","timestamp":1}}`, `{"message":{"role":"user","content":"second","timestamp":1}}`},
			check: func(t *testing.T, got any, received []map[string]any) {
				t.Helper()
				want := map[string]any{"role": "user", "content": "second", "timestamp": float64(1)}
				if replacement := jsonValue(t, got); !reflect.DeepEqual(replacement, want) {
					t.Fatalf("message_end replacement = %v, want %v", replacement, want)
				}
				if len(received) != 2 || !reflect.DeepEqual(received[1]["message"], map[string]any{"role": "user", "content": "first", "timestamp": float64(1)}) {
					t.Fatalf("the second message_end handler did not receive the first one's replacement: %v", received)
				}
			},
		},
		{
			// Pi's ModelSelectEvent is { type, model, previousModel, source } (types.ts:1106-1111).
			event:   "model_select",
			sent:    extension.ModelSelectEvent{Type: "model_select", Model: &ai.Model{ID: "next-model"}, PreviousModel: &ai.Model{ID: "prior-model"}, Source: "cycle"},
			members: map[string]any{"type": "model_select", "source": "cycle"},
			// The host serializes a model through its provider wire form, not the Go struct's JSON, so only the identity members are compared.
			wireKeys: []string{"model", "previousModel"},
			modelIDs: map[string]string{"model": "next-model", "previousModel": "prior-model"},
		},
		{
			// A handler returning {trusted: "yes"} decides the project (types.ts:686-708, runner.ts emitProjectTrustEvent). The fixtures leave the "/probe" cwd undecided.
			event: "project_trust",
			sent:  extension.ProjectTrustEvent{Type: "project_trust", Cwd: "/probe"},
			emit: func(ctx context.Context, h *harness) (any, error) {
				result, _, err := inproc.EmitProjectTrust(h.runner, ctx, extension.ProjectTrustEvent{Type: "project_trust", Cwd: "/probe"}, extension.ProjectTrustContext{})
				return result, err
			},
			members: map[string]any{"type": "project_trust", "cwd": "/probe"},
			// An undecided handler falls through to the next; the first decisive result wins and later handlers do not run.
			probes:  3,
			calls:   2,
			results: []string{`{"trusted":"undecided"}`, `{"trusted":"yes","remember":true}`, `{"trusted":"no"}`},
			check: func(t *testing.T, got any, received []map[string]any) {
				t.Helper()
				if want := map[string]any{"trusted": "yes", "remember": true}; !reflect.DeepEqual(jsonValue(t, got), want) {
					t.Fatalf("project_trust decision = %s, want %v", payloadJSON(t, got), want)
				}
				if len(received) != 2 {
					t.Fatalf("project_trust handlers called = %d, want 2 (the decisive one stops the dispatch)", len(received))
				}
			},
		},
		{
			event:   "provider_stream_event",
			sent:    extension.ProviderStreamEvent{Type: "provider_stream_event", Provider: "prov-x", API: "api-y", Model: "model-z", Data: map[string]any{"delta": []any{float64(1), "two"}}},
			members: map[string]any{"type": "provider_stream_event", "provider": "prov-x", "api": "api-y", "model": "model-z", "data": map[string]any{"delta": []any{float64(1), "two"}}},
		},
		{
			// ResourcesDiscoverResult paths reach the emitter (types.ts:711-722, runner.ts emitResourcesDiscover).
			event: "resources_discover",
			sent:  extension.ResourcesDiscoverEvent{Type: "resources_discover", Cwd: "/probe-cwd", Reason: "reload"},
			emit: func(ctx context.Context, h *harness) (any, error) {
				return h.runner.EmitResourcesDiscover(ctx, "/probe-cwd", "reload")
			},
			members: map[string]any{"type": "resources_discover", "cwd": "/probe-cwd", "reason": "reload"},
			// Every handler contributes, in registration order.
			probes:  2,
			calls:   2,
			results: []string{`{"skillPaths":["/skills/a"],"promptPaths":["/prompts/b"],"themePaths":["/themes/c"]}`, `{"skillPaths":["/skills/d"]}`},
			check: func(t *testing.T, got any, received []map[string]any) {
				t.Helper()
				aggregate, ok := got.(*extension.ResourcesDiscoverAggregateResult)
				if !ok || aggregate == nil {
					t.Fatalf("resources_discover returned %T", got)
				}
				paths := func(list []extension.AttributedResourcePath) []string {
					var out []string
					for _, p := range list {
						out = append(out, p.Path)
					}
					return out
				}
				if got := [][]string{paths(aggregate.SkillPaths), paths(aggregate.PromptPaths), paths(aggregate.ThemePaths)}; !reflect.DeepEqual(got, [][]string{{"/skills/a", "/skills/d"}, {"/prompts/b"}, {"/themes/c"}}) {
					t.Fatalf("resources_discover paths = %v", got)
				}
			},
		},
		{
			event: "session_before_compact",
			sent: extension.SessionBeforeCompactEvent{Type: "session_before_compact", Preparation: preparation,
				BranchEntries: []extension.SessionEntry{labelEntry}, CustomInstructions: "be brief", Reason: "overflow", WillRetry: true},
			members: map[string]any{"type": "session_before_compact", "customInstructions": "be brief", "reason": "overflow", "willRetry": true,
				"preparation": preparationJSON, "branchEntries": []any{map[string]any{"type": "label", "id": "e1", "parentId": nil, "timestamp": "2026-01-01T00:00:00.000Z", "targetId": "e0", "label": "mark"}}},
			probes: 2, results: twoCancels,
			check: cancelled,
		},
		{
			event:   "session_before_fork",
			sent:    extension.SessionBeforeForkEvent{Type: "session_before_fork", EntryID: "entry-7", Position: "before"},
			members: map[string]any{"type": "session_before_fork", "entryId": "entry-7", "position": "before"},
			probes:  2, results: twoCancels,
			check: cancelled,
		},
		{
			event:   "session_before_switch",
			sent:    extension.SessionBeforeSwitchEvent{Type: "session_before_switch", Reason: "resume", TargetSessionFile: "/next.jsonl"},
			members: map[string]any{"type": "session_before_switch", "reason": "resume", "targetSessionFile": "/next.jsonl"},
			probes:  2, results: twoCancels,
			check: cancelled,
		},
		{
			event:   "session_before_tree",
			sent:    extension.SessionBeforeTreeEvent{Type: "session_before_tree", Preparation: extension.TreePreparation{TargetID: "t1", EntriesToSummarize: []extension.SessionEntry{}, UserWantsSummary: true, Label: "lbl"}},
			members: map[string]any{"type": "session_before_tree", "preparation": tree},
			probes:  2, results: twoCancels,
			check: cancelled,
		},
		{
			event: "session_compact",
			sent: extension.SessionCompactEvent{Type: "session_compact", CompactionEntry: extension.CompactionEntry{SessionEntryBase: sessionentry.SessionEntryBase{Type: "compaction", ID: "c1", Timestamp: "2026-01-01T00:00:00.000Z"}, Summary: "s", FirstKeptEntryID: "e2", TokensBefore: 900},
				FromExtension: true, Reason: "threshold", WillRetry: false},
			members: map[string]any{"type": "session_compact", "fromExtension": true, "reason": "threshold", "willRetry": false,
				"compactionEntry": map[string]any{"type": "compaction", "id": "c1", "parentId": nil, "timestamp": "2026-01-01T00:00:00.000Z", "summary": "s", "firstKeptEntryId": "e2", "tokensBefore": float64(900), "fromHook": false}},
		},
		{
			event:   "session_compact_failed",
			sent:    extension.SessionCompactFailedEvent{Type: "session_compact_failed", Reason: "manual", ErrorMessage: "boom", Aborted: false, WillRetry: false, FromExtension: true},
			members: map[string]any{"type": "session_compact_failed", "reason": "manual", "errorMessage": "boom", "aborted": false, "willRetry": false, "fromExtension": true},
		},
		{
			event:   "session_info_changed",
			sent:    extension.SessionInfoChangedEvent{Type: "session_info_changed", Name: "renamed"},
			members: map[string]any{"type": "session_info_changed", "name": "renamed"},
		},
		{
			event:   "session_shutdown",
			sent:    extension.SessionShutdownEvent{Type: "session_shutdown", Reason: "fork", TargetSessionFile: "/fork.jsonl"},
			members: map[string]any{"type": "session_shutdown", "reason": "fork", "targetSessionFile": "/fork.jsonl"},
		},
		{
			event:   "agent_start",
			sent:    extension.AgentStartEvent{Type: "agent_start"},
			members: map[string]any{"type": "agent_start"},
		},
		{
			event:   "agent_end",
			sent:    extension.AgentEndEvent{Type: "agent_end", Messages: []extension.AgentMessage{wireAgentMessage(user)}},
			members: map[string]any{"type": "agent_end", "messages": []any{user}},
		},
		{
			// AgentSettledEvent.aborted is whether the run ended because it was aborted (types.ts:1005-1009).
			event:   "agent_settled",
			sent:    extension.AgentSettledEvent{Type: "agent_settled", Aborted: true},
			members: map[string]any{"type": "agent_settled", "aborted": true},
		},
		{
			event: "mcp_servers_change",
			sent: extension.McpServersChangeEvent{Type: "mcp_servers_change", Servers: []extension.RegisteredMcpServer{
				{Name: "srv", ExtensionPath: "/ext/a.ts", Config: extension.McpServerConfig{Type: "http", URL: "https://mcp.invalid/mcp"}}}},
			members: map[string]any{"type": "mcp_servers_change"},
		},
		{
			event:   "after_provider_response",
			sent:    extension.AfterProviderResponseEvent{Type: "after_provider_response", Status: 207, Headers: map[string]string{"x-probe": "received"}},
			members: map[string]any{"type": "after_provider_response", "status": float64(207), "headers": map[string]any{"x-probe": "received"}},
		},
		{
			// The value a handler returns replaces the request payload for the next handler and the request (types.ts BeforeProviderRequestEventResult = unknown, runner.ts emitBeforeProviderRequest).
			event: "before_provider_request",
			sent:  extension.BeforeProviderRequestEvent{Type: "before_provider_request", Payload: map[string]any{"model": "m", "n": float64(1)}},
			emit: func(ctx context.Context, h *harness) (any, error) {
				return h.runner.EmitBeforeProviderRequest(ctx, map[string]any{"model": "m", "n": float64(1)})
			},
			members: map[string]any{"type": "before_provider_request", "payload": map[string]any{"model": "m", "n": float64(1)}},
			probes:  2, calls: 2,
			results: []string{`{"model":"first"}`, `{"model":"second"}`},
			check: func(t *testing.T, got any, received []map[string]any) {
				t.Helper()
				if want := map[string]any{"model": "second"}; !reflect.DeepEqual(jsonValue(t, got), want) {
					t.Fatalf("before_provider_request payload = %s, want %v", payloadJSON(t, got), want)
				}
				if len(received) != 2 || !reflect.DeepEqual(received[1]["payload"], map[string]any{"model": "first"}) {
					t.Fatalf("the second handler did not receive the first one's replacement: %v", received)
				}
			},
		},
		{
			// The headers the handlers leave on the outgoing request replace it; a null value deletes one (runner.ts emitBeforeProviderHeaders). Node mutates event.headers in place as Pi does, the other SDKs return the new map; the host sees the same result.
			event: "before_provider_headers",
			sent:  extension.BeforeProviderHeadersEvent{Type: "before_provider_headers", Headers: extension.ProviderHeaders{"a": new("1"), "b": nil}},
			emit: func(ctx context.Context, h *harness) (any, error) {
				return h.runner.EmitBeforeProviderHeaders(ctx, extension.ProviderHeaders{"a": new("1"), "b": nil})
			},
			members: map[string]any{"type": "before_provider_headers", "headers": map[string]any{"a": "1", "b": nil}},
			results: []string{`{"a":"1","c":"3"}`},
			check: func(t *testing.T, got any, received []map[string]any) {
				t.Helper()
				if want := map[string]any{"a": "1", "c": "3"}; !reflect.DeepEqual(jsonValue(t, got), want) {
					t.Fatalf("before_provider_headers result = %s, want %v", payloadJSON(t, got), want)
				}
			},
		},
		{
			// ContextEventResult.messages replaces the messages the next handler and the request see (types.ts:1415, runner.ts emitContext).
			event: "context",
			sent:  extension.ContextEvent{Type: "context", Messages: []extension.AgentMessage{wireAgentMessage(user)}},
			emit: func(ctx context.Context, h *harness) (any, error) {
				return h.runner.EmitContext(ctx, []extension.AgentMessage{wireAgentMessage(user)})
			},
			members: map[string]any{"type": "context", "messages": []any{user}},
			probes:  2, calls: 2,
			results: []string{`{"messages":[{"role":"user","content":"first","timestamp":1}]}`, `{"messages":[{"role":"user","content":"second","timestamp":1}]}`},
			check: func(t *testing.T, got any, received []map[string]any) {
				t.Helper()
				if want := []any{map[string]any{"role": "user", "content": "second", "timestamp": float64(1)}}; !reflect.DeepEqual(jsonValue(t, got), want) {
					t.Fatalf("context messages = %s, want %v", payloadJSON(t, got), want)
				}
				if len(received) != 2 || !reflect.DeepEqual(received[1]["messages"], []any{map[string]any{"role": "user", "content": "first", "timestamp": float64(1)}}) {
					t.Fatalf("the second context handler did not receive the first one's messages: %v", received)
				}
			},
		},
		{
			event: "context_with_system",
			sent:  extension.ContextWithSystemEvent{Type: "context_with_system", Messages: []extension.AgentMessage{wireAgentMessage(user)}},
			emit: func(ctx context.Context, h *harness) (any, error) {
				return h.runner.EmitContextWithSystem(ctx, []extension.AgentMessage{wireAgentMessage(user)})
			},
			members: map[string]any{"type": "context_with_system", "messages": []any{user}},
			probes:  2, calls: 2,
			results: []string{`{"messages":[{"role":"user","content":"first","timestamp":1}]}`, `{"messages":[{"role":"user","content":"second","timestamp":1}]}`},
			check: func(t *testing.T, got any, received []map[string]any) {
				t.Helper()
				if want := []any{map[string]any{"role": "user", "content": "second", "timestamp": float64(1)}}; !reflect.DeepEqual(jsonValue(t, got), want) {
					t.Fatalf("context_with_system messages = %s, want %v", payloadJSON(t, got), want)
				}
				if len(received) != 2 || !reflect.DeepEqual(received[1]["messages"], []any{map[string]any{"role": "user", "content": "first", "timestamp": float64(1)}}) {
					t.Fatalf("the second handler did not receive the first one's messages: %v", received)
				}
			},
		},
		{
			// InputEventResult: a transform feeds the next handler, handled ends the dispatch (types.ts:1156-1159, runner.ts emitInput).
			event: "input",
			sent:  extension.InputEvent{Type: "input", Text: "hello", Images: []extension.ImageContent{{Data: "aGk=", MimeType: "image/png"}}, Source: "interactive", StreamingBehavior: "steer"},
			emit: func(ctx context.Context, h *harness) (any, error) {
				return h.runner.EmitInput(ctx, "hello", []extension.ImageContent{{Data: "aGk=", MimeType: "image/png"}}, "interactive", "steer")
			},
			members: map[string]any{"type": "input", "text": "hello", "source": "interactive", "streamingBehavior": "steer"},
			probes:  3, calls: 2,
			results: []string{`{"action":"transform","text":"first"}`, `{"action":"handled"}`, `{"action":"transform","text":"never"}`},
			check: func(t *testing.T, got any, received []map[string]any) {
				t.Helper()
				if _, ok := got.(extension.InputEventResultHandled); !ok {
					t.Fatalf("input dispatch result = %#v, want handled", got)
				}
				if len(received) != 2 || received[1]["text"] != "first" {
					t.Fatalf("input handlers: the second must see the first one's transform and the handled result must stop the third: %v", received)
				}
			},
		},
		{
			// The last action a handler supplies is the decision (cache-warmer.ts CacheWarmingDecisionEventResult, runner.ts emit).
			event: "cache_warming_decision",
			sent:  extension.CacheWarmingDecisionEvent{Type: "cache_warming_decision", WarmCost: 0.05, MissCost: 0.5, ContinuationProbability: 0.15, Action: extension.CacheWarmingActionStop},
			emit: func(ctx context.Context, h *harness) (any, error) {
				return h.runner.EmitCacheWarmingDecision(ctx, extension.CacheWarmingDecisionEvent{Type: "cache_warming_decision", WarmCost: 0.05, MissCost: 0.5, ContinuationProbability: 0.15, Action: extension.CacheWarmingActionStop})
			},
			members: map[string]any{"type": "cache_warming_decision", "warmCost": 0.05, "missCost": 0.5, "continuationProbability": 0.15, "action": "stop"},
			probes:  2, calls: 2,
			results: []string{`{"action":"stop"}`, `{"action":"warm"}`},
			check: func(t *testing.T, got any, received []map[string]any) {
				t.Helper()
				if got != extension.CacheWarmingActionWarm {
					t.Fatalf("cache_warming_decision = %v, want the last action a handler supplied (the default is stop)", got)
				}
			},
		},
		{
			// BeforeAgentStartEventResult: a message accumulates, systemPrompt replaces the prompt every later handler sees (types.ts:1468-1473, runner.ts emitBeforeAgentStart).
			event:    "before_agent_start",
			sent:     extension.BeforeAgentStartEvent{Type: "before_agent_start", Prompt: "do it", Images: []extension.ImageContent{{Data: "aGk=", MimeType: "image/png"}}},
			wireKeys: []string{"systemPrompt", "systemPromptOptions"},
			emit: func(ctx context.Context, h *harness) (any, error) {
				return h.runner.EmitBeforeAgentStart(ctx, "do it", []extension.ImageContent{{Data: "aGk=", MimeType: "image/png"}}, extension.BuildSystemPromptOptions{Cwd: "/w", CustomPrompt: "base"})
			},
			members: map[string]any{"type": "before_agent_start", "prompt": "do it"},
			probes:  2, calls: 2,
			results: []string{`{"systemPrompt":"first-prompt","message":{"customType":"x","content":"c","display":true}}`, `{"message":{"customType":"y","content":"d","display":false}}`},
			check: func(t *testing.T, got any, received []map[string]any) {
				t.Helper()
				combined, ok := got.(*extension.BeforeAgentStartCombinedResult)
				if !ok || combined == nil || combined.SystemPrompt == nil || *combined.SystemPrompt != "first-prompt" || len(combined.Messages) != 2 {
					t.Fatalf("before_agent_start combined result = %s", payloadJSON(t, got))
				}
				if first, _ := received[0]["systemPrompt"].(string); !strings.HasPrefix(first, "base") {
					t.Fatalf("the first handler's systemPrompt = %q, want the prompt rendered from the options", first)
				}
				if len(received) != 2 || received[1]["systemPrompt"] != "first-prompt" {
					t.Fatalf("the second handler did not see the first one's systemPrompt override: %v", received)
				}
			},
		},
	}
}

func payloadJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %T: %v", value, err)
	}
	return raw
}

// TestConformance_EventPayloadAndResultAcrossSDKs pins what Pi's `pi.on(event, handler)` delivers and honours for the events of
// this table (types.ts:686-833, 1047-1113, 1463-1490, 1569-1642): a handler registered after connecting in every SDK (Go, Node, Node
// packed, Rust, Python, fused Go) receives Pi's event interface, member for member, from the production Runner dispatch, and the result
// it returns (a cancel, a replacement message, a trust decision, resource paths) reaches the emitter as the Runner defines it. The
// payload and the result are produced by the host and the extension's own logic, so an SDK that drops a member, renames it or ignores the result fails.
func TestConformance_EventPayloadAndResultAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	cases := eventPayloadCases()
	for _, tc := range sdkHarnessCases() {
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
			payloads := func(event string) []map[string]any {
				var found []map[string]any
				prefix := "event_payload:" + event + ":"
				for _, n := range *h.notify {
					if rest, ok := strings.CutPrefix(n, prefix); ok {
						var payload map[string]any
						if err := json.Unmarshal([]byte(strings.TrimSuffix(rest, ":info")), &payload); err != nil {
							t.Fatalf("%s: payload %q: %v", event, rest, err)
						}
						found = append(found, payload)
					}
				}
				return found
			}
			for _, c := range cases {
				t.Run(c.event, func(t *testing.T) {
					for range max(c.probes, 1) {
						runConformanceCommandArgs(t, h, "event_probe_on", c.event)
					}
					for _, result := range c.results {
						runConformanceCommandArgs(t, h, "event_probe_result", c.event+" "+result)
					}
					before := len(payloads(c.event))
					emit := c.emit
					if emit == nil {
						emit = func(ctx context.Context, h *harness) (any, error) { return h.runner.Emit(ctx, c.sent) }
					}
					got, err := emit(ctx, h)
					if err != nil {
						t.Fatalf("emit %s: %v", c.event, err)
					}
					// Emit returns after every handler it calls has returned, and a handler notifies before it returns.
					pollUntilConformance(t, 5*time.Second, c.event+" never reached the handler registered after connecting", func() bool {
						return len(payloads(c.event)) >= before+max(c.calls, 1)
					})
					// A command round trip on the extension's connection returns after the notifications sent before it, so a handler the dispatch wrongly
					// called after the last expected one has reported by now.
					runConformanceCommandArgs(t, h, "event_probe_off", "no-such-event")
					received := payloads(c.event)[before]
					for member, want := range c.members {
						if !reflect.DeepEqual(received[member], want) {
							t.Fatalf("%s member %q = %s, want %s (payload %s)", c.event, member, payloadJSON(t, received[member]), payloadJSON(t, want), payloadJSON(t, received))
						}
					}
					// The abort signal Pi's before-events carry (types.ts:777, 836) is the dispatch context's cancellation, not a serialized member.
					delete(received, "signal")
					want := jsonValue(t, c.sent).(map[string]any)
					for member, id := range c.modelIDs {
						if model, _ := received[member].(map[string]any); model["id"] != id {
							t.Fatalf("%s member %q = %s, want model %q", c.event, member, payloadJSON(t, received[member]), id)
						}
					}
					for _, key := range c.wireKeys {
						delete(received, key)
						delete(want, key)
					}
					if !reflect.DeepEqual(received, want) {
						t.Fatalf("%s payload = %s, want the serialized event %s", c.event, payloadJSON(t, received), payloadJSON(t, want))
					}
					if c.check != nil {
						c.check(t, got, payloads(c.event)[before:])
					}
				})
			}
		})
	}
}
