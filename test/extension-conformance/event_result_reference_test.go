package extensionconformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// decide adds a handler that returns a result to the event on the reference extension. The Runner reads the pointer results of the events it
// merges (tool_call, tool_result, message_end, cache_warming_decision) through a pointer, and the cancel results of the session_before_* events and the
// input result through the value, as the handlers it loads from an SDK return them.
func decide[T, R any](r *eventReference, event string, pointer bool, handler func(context.Context, T) (R, error)) func() {
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	r.mu.Unlock()
	r.ext.AddEventHandler(event, id, func(args ...any) (any, error) {
		result, err := handler(context.Background(), args[0].(T))
		if pointer {
			return &result, err
		}
		return result, err
	})
	return func() { r.ext.RemoveEventHandler(event, id) }
}

func (r *eventReference) OnSessionBeforeSwitch(h func(context.Context, extension.SessionBeforeSwitchEvent) (extension.SessionBeforeSwitchResult, error)) func() {
	return decide(r, "session_before_switch", false, h)
}

func (r *eventReference) OnSessionBeforeFork(h func(context.Context, extension.SessionBeforeForkEvent) (extension.SessionBeforeForkResult, error)) func() {
	return decide(r, "session_before_fork", false, h)
}

func (r *eventReference) OnSessionBeforeCompact(h func(context.Context, extension.SessionBeforeCompactEvent) (extension.SessionBeforeCompactResult, error)) func() {
	return decide(r, "session_before_compact", false, h)
}

func (r *eventReference) OnSessionBeforeTree(h func(context.Context, extension.SessionBeforeTreeEvent) (extension.SessionBeforeTreeResult, error)) func() {
	return decide(r, "session_before_tree", false, h)
}

func (r *eventReference) OnToolCall(h func(context.Context, extension.ToolCallEvent) (extension.ToolCallEventResult, error)) func() {
	return decide(r, "tool_call", true, h)
}

func (r *eventReference) OnToolResult(h func(context.Context, extension.ToolResultEvent) (extension.ToolResultEventResult, error)) func() {
	return decide(r, "tool_result", true, h)
}

func (r *eventReference) OnInput(h func(context.Context, extension.InputEvent) (extension.InputEventResult, error)) func() {
	return decide(r, "input", false, h)
}

func (r *eventReference) OnMessageEnd(h func(context.Context, extension.MessageEndEvent) (extension.MessageEndEventResult, error)) func() {
	return decide(r, "message_end", true, h)
}

func (r *eventReference) OnCacheWarmingDecision(h func(context.Context, extension.CacheWarmingDecisionEvent) (extension.CacheWarmingDecisionEventResult, error)) func() {
	return decide(r, "cache_warming_decision", true, h)
}

func (r *eventReference) OnBeforeProviderRequest(h func(context.Context, extension.BeforeProviderRequestEvent) (extension.BeforeProviderRequestEventResult, error)) func() {
	return decide(r, "before_provider_request", false, h)
}

func (r *eventReference) OnContext(h func(context.Context, extension.ContextEvent) (extension.ContextEventResult, error)) func() {
	return decide(r, "context", true, h)
}

func (r *eventReference) OnContextWithSystem(h func(context.Context, extension.ContextWithSystemEvent) (extension.ContextEventResult, error)) func() {
	return decide(r, "context_with_system", true, h)
}

func (r *eventReference) OnBeforeAgentStart(h func(context.Context, extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error)) func() {
	return decide(r, "before_agent_start", true, h)
}

func (r *eventReference) OnResourcesDiscover(h func(context.Context, extension.ResourcesDiscoverEvent) (extension.ResourcesDiscoverResult, error)) func() {
	return decide(r, "resources_discover", true, h)
}

func (r *eventReference) OnUserBash(h func(context.Context, extension.UserBashEvent) (extension.UserBashEventResult, error)) func() {
	return decide(r, "user_bash", true, h)
}

func (r *eventReference) OnTurnEnd(h func(context.Context, extension.TurnEndEvent) (extension.TurnEndEventResult, error)) func() {
	return decide(r, "turn_end", false, h)
}

func (r *eventReference) OnAgentBeforeSettle(h func(context.Context, *extension.AgentBeforeSettleEvent) (extension.AgentBeforeSettleEventResult, error)) func() {
	return decide(r, "agent_before_settle", false, h)
}

// OnProjectTrust adds a trust decision: the Runner passes a project_trust handler the event, its dispatch context and the trust context.
func (r *eventReference) OnProjectTrust(h extension.ProjectTrustHandler) func() {
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	r.mu.Unlock()
	r.ext.AddEventHandler("project_trust", id, func(args ...any) (any, error) {
		return h(context.Background(), args[0].(extension.ProjectTrustEvent), args[2].(extension.ProjectTrustContext))
	})
	return func() { r.ext.RemoveEventHandler("project_trust", id) }
}

// TestConformance_EventResultsReachTheHost pins the result an extension's handler gives the host for the events whose handler decides (extensions/types.ts:1558-1642
// pi.on overloads with SessionBefore*Result, ToolCallEventResult, ToolResultEventResult, InputEventResult, MessageEndEventResult and CacheWarmingDecisionEventResult;
// runner.ts:988-1017 emit and its emitToolCall, emitToolResult, emitInput, emitMessageEnd, emitBeforeProviderRequest, emitContext, emitBeforeAgentStart, emitResourcesDiscover
// emitUserBash, emitBoundary and emitProjectTrustEvent).
// Every SDK returns a result that names a value only the test chose; the host's Runner reads it and returns the merged outcome the host acts on. A Go
// reference of extension.API.OnX returns the same value as a typed result from a handler on a Runner of its own, and the outcome must match.
func TestConformance_EventResultsReachTheHost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	message := wireAgentMessage(map[string]any{"role": "user", "content": "original", "timestamp": float64(1)})
	replaced := wireAgentMessage(map[string]any{"role": "user", "content": "probe-replaced", "timestamp": float64(2)})
	stop := extension.CacheWarmingActionStop
	contextReplaced := wireAgentMessage(map[string]any{"role": "user", "content": "probe-ctx", "timestamp": float64(3)})
	systemPrompt := "probe-system"
	type row struct {
		event string
		// sdkResult is the JSON the SDK's handler returns.
		sdkResult string
		// emit runs the host's production path for the event and returns the outcome.
		emit func(*inproc.Runner) (any, error)
		// field is the dotted path in the outcome's JSON ("" for the whole outcome) and want its compact JSON.
		field, want string
		// register adds the reference handler through the extension.API member for the event.
		register func(extension.API)
	}
	rows := []row{
		{"session_before_switch", `{"cancel":true}`, func(r *inproc.Runner) (any, error) {
			return r.Emit(context.Background(), extension.SessionBeforeSwitchEvent{Type: "session_before_switch", Reason: "new"})
		}, "cancel", `true`, func(api extension.API) {
			api.OnSessionBeforeSwitch(func(context.Context, extension.SessionBeforeSwitchEvent) (extension.SessionBeforeSwitchResult, error) {
				return extension.SessionBeforeSwitchResult{Cancel: true}, nil
			})
		}},
		{"session_before_fork", `{"cancel":true,"skipConversationRestore":true}`, func(r *inproc.Runner) (any, error) {
			return r.Emit(context.Background(), extension.SessionBeforeForkEvent{Type: "session_before_fork", EntryID: "e1", Position: "at"})
		}, "skipConversationRestore", `true`, func(api extension.API) {
			api.OnSessionBeforeFork(func(context.Context, extension.SessionBeforeForkEvent) (extension.SessionBeforeForkResult, error) {
				return extension.SessionBeforeForkResult{Cancel: true, SkipConversationRestore: true}, nil
			})
		}},
		{"session_before_compact", `{"cancel":true}`, func(r *inproc.Runner) (any, error) {
			return r.Emit(context.Background(), extension.SessionBeforeCompactEvent{Type: "session_before_compact", Reason: "manual"})
		}, "cancel", `true`, func(api extension.API) {
			api.OnSessionBeforeCompact(func(context.Context, extension.SessionBeforeCompactEvent) (extension.SessionBeforeCompactResult, error) {
				return extension.SessionBeforeCompactResult{Cancel: true}, nil
			})
		}},
		{"session_before_tree", `{"cancel":true,"label":"probe-label"}`, func(r *inproc.Runner) (any, error) {
			return r.Emit(context.Background(), extension.SessionBeforeTreeEvent{Type: "session_before_tree"})
		}, "label", `"probe-label"`, func(api extension.API) {
			label := "probe-label"
			api.OnSessionBeforeTree(func(context.Context, extension.SessionBeforeTreeEvent) (extension.SessionBeforeTreeResult, error) {
				return extension.SessionBeforeTreeResult{Cancel: true, Label: &label}, nil
			})
		}},
		{"tool_call", `{"block":true,"reason":"probe-reason"}`, func(r *inproc.Runner) (any, error) {
			return r.EmitToolCall(context.Background(), extension.CustomToolCallEvent{ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "call-probe"}, ToolName: "probe", Input: map[string]any{}})
		}, "reason", `"probe-reason"`, func(api extension.API) {
			api.OnToolCall(func(context.Context, extension.ToolCallEvent) (extension.ToolCallEventResult, error) {
				return extension.ToolCallEventResult{Block: true, Reason: "probe-reason"}, nil
			})
		}},
		{"tool_result", `{"isError":true,"content":[{"type":"text","text":"probe-redacted"}]}`, func(r *inproc.Runner) (any, error) {
			return r.EmitToolResult(context.Background(), extension.CustomToolResultEvent{ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", ToolCallID: "call-probe", Input: map[string]any{}, Content: []any{map[string]any{"type": "text", "text": "original"}}}, ToolName: "probe"})
		}, "content.0.text", `"probe-redacted"`, func(api extension.API) {
			isError := true
			api.OnToolResult(func(context.Context, extension.ToolResultEvent) (extension.ToolResultEventResult, error) {
				return extension.ToolResultEventResult{IsError: &isError, Content: []any{map[string]any{"type": "text", "text": "probe-redacted"}}}, nil
			})
		}},
		{"input", `{"action":"transform","text":"probe-transformed"}`, func(r *inproc.Runner) (any, error) {
			return r.EmitInput(context.Background(), "original", nil, "interactive", "")
		}, "text", `"probe-transformed"`, func(api extension.API) {
			api.OnInput(func(context.Context, extension.InputEvent) (extension.InputEventResult, error) {
				return extension.InputEventResultTransform{Text: "probe-transformed"}, nil
			})
		}},
		{"message_end", `{"message":{"role":"user","content":"probe-replaced","timestamp":2}}`, func(r *inproc.Runner) (any, error) {
			return r.EmitMessageEnd(context.Background(), extension.MessageEndEvent{Type: "message_end", Message: message})
		}, "content", `"probe-replaced"`, func(api extension.API) {
			next := replaced
			api.OnMessageEnd(func(context.Context, extension.MessageEndEvent) (extension.MessageEndEventResult, error) {
				return extension.MessageEndEventResult{Message: &next}, nil
			})
		}},
		{"cache_warming_decision", `{"action":"stop"}`, func(r *inproc.Runner) (any, error) {
			return r.EmitCacheWarmingDecision(context.Background(), extension.CacheWarmingDecisionEvent{Type: "cache_warming_decision", WarmCost: 0.05, MissCost: 0.5, ContinuationProbability: 0.15, Action: extension.CacheWarmingActionWarm})
		}, "", `"stop"`, func(api extension.API) {
			api.OnCacheWarmingDecision(func(context.Context, extension.CacheWarmingDecisionEvent) (extension.CacheWarmingDecisionEventResult, error) {
				return extension.CacheWarmingDecisionEventResult{Action: &stop}, nil
			})
		}},
		{"before_provider_request", `{"model":"probe-model"}`, func(r *inproc.Runner) (any, error) {
			return r.EmitBeforeProviderRequest(context.Background(), map[string]any{"model": "original"})
		}, "model", `"probe-model"`, func(api extension.API) {
			api.OnBeforeProviderRequest(func(context.Context, extension.BeforeProviderRequestEvent) (extension.BeforeProviderRequestEventResult, error) {
				return map[string]any{"model": "probe-model"}, nil
			})
		}},
		{"context", `{"messages":[{"role":"user","content":"probe-ctx","timestamp":3}]}`, func(r *inproc.Runner) (any, error) {
			return r.EmitContext(context.Background(), []extension.AgentMessage{message})
		}, "0.content", `"probe-ctx"`, func(api extension.API) {
			api.OnContext(func(context.Context, extension.ContextEvent) (extension.ContextEventResult, error) {
				return extension.ContextEventResult{Messages: []extension.AgentMessage{contextReplaced}}, nil
			})
		}},
		{"context_with_system", `{"messages":[{"role":"user","content":"probe-ctx","timestamp":3}]}`, func(r *inproc.Runner) (any, error) {
			return r.EmitContextWithSystem(context.Background(), []extension.AgentMessage{message})
		}, "0.content", `"probe-ctx"`, func(api extension.API) {
			api.OnContextWithSystem(func(context.Context, extension.ContextWithSystemEvent) (extension.ContextEventResult, error) {
				return extension.ContextEventResult{Messages: []extension.AgentMessage{contextReplaced}}, nil
			})
		}},
		{"before_agent_start", `{"systemPrompt":"probe-system"}`, func(r *inproc.Runner) (any, error) {
			return r.EmitBeforeAgentStart(context.Background(), "prompt", nil, conformanceSystemPromptOptions())
		}, "systemPrompt", `"probe-system"`, func(api extension.API) {
			api.OnBeforeAgentStart(func(context.Context, extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error) {
				return extension.BeforeAgentStartEventResult{SystemPrompt: &systemPrompt}, nil
			})
		}},
		{"resources_discover", `{"skillPaths":["/probe/skill"]}`, func(r *inproc.Runner) (any, error) {
			return r.EmitResourcesDiscover(context.Background(), ".", "startup")
		}, "skillPaths.0.path", `"/probe/skill"`, func(api extension.API) {
			api.OnResourcesDiscover(func(context.Context, extension.ResourcesDiscoverEvent) (extension.ResourcesDiscoverResult, error) {
				return extension.ResourcesDiscoverResult{SkillPaths: []string{"/probe/skill"}}, nil
			})
		}},
		{"user_bash", `{"result":{"output":"probe-out","exitCode":7,"cancelled":false,"truncated":false}}`, func(r *inproc.Runner) (any, error) {
			return r.EmitUserBash(context.Background(), extension.UserBashEvent{Type: "user_bash", Command: "echo probe", Cwd: "."})
		}, "result.output", `"probe-out"`, func(api extension.API) {
			api.OnUserBash(func(context.Context, extension.UserBashEvent) (extension.UserBashEventResult, error) {
				return extension.UserBashEventResult{Result: map[string]any{"output": "probe-out", "exitCode": 7, "cancelled": false, "truncated": false}}, nil
			})
		}},
		{"project_trust", `{"trusted":"yes","remember":true}`, func(r *inproc.Runner) (any, error) {
			result, _, err := inproc.EmitProjectTrust(r, context.Background(), extension.ProjectTrustEvent{Type: "project_trust", Cwd: "/probe"}, extension.ProjectTrustContext{Cwd: "/probe"})
			return result, err
		}, "trusted", `"yes"`, func(api extension.API) {
			api.OnProjectTrust(func(context.Context, extension.ProjectTrustEvent, extension.ProjectTrustContext) (extension.ProjectTrustEventResult, error) {
				return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustYes}, nil
			})
		}},
		{"turn_end", `{"entries":[{"type":"custom","customType":"probe-turn"}],"continue":true}`, func(r *inproc.Runner) (any, error) {
			return r.EmitBoundary(context.Background(), extension.TurnEndEvent{Type: "turn_end", Message: wireAgentMessage(map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "done"}}, "stopReason": "stop"}), ToolResults: []extension.ToolResultMessage{}, ToolResultEntryIds: []string{}, BoundaryState: &extension.BoundaryState{Entries: []extension.SessionBoundaryDraft{}, Outcome: extension.AgentActivityCompleted}}, noBoundaryPreview)
		}, "Entries.0.customType", `"probe-turn"`, func(api extension.API) {
			entries := []extension.SessionBoundaryDraft{{Type: "custom", CustomType: "probe-turn"}}
			api.OnTurnEnd(func(context.Context, extension.TurnEndEvent) (extension.TurnEndEventResult, error) {
				return extension.TurnEndEventResult{Entries: &entries, Continue: new(true)}, nil
			})
		}},
		{"agent_before_settle", `{"entries":[{"type":"custom","customType":"probe-settle"}],"continue":true}`, func(r *inproc.Runner) (any, error) {
			return r.EmitBoundary(context.Background(), &extension.AgentBeforeSettleEvent{Type: "agent_before_settle", BoundaryState: extension.BoundaryState{Entries: []extension.SessionBoundaryDraft{}, Outcome: extension.AgentActivityCompleted}}, noBoundaryPreview)
		}, "Entries.0.customType", `"probe-settle"`, func(api extension.API) {
			entries := []extension.SessionBoundaryDraft{{Type: "custom", CustomType: "probe-settle"}}
			api.OnAgentBeforeSettle(func(context.Context, *extension.AgentBeforeSettleEvent) (extension.AgentBeforeSettleEventResult, error) {
				return extension.AgentBeforeSettleEventResult{Entries: &entries, Continue: new(true)}, nil
			})
		}},
	}
	outcome := func(t *testing.T, row row, runner *inproc.Runner) string {
		t.Helper()
		got, err := row.emit(runner)
		if err != nil {
			t.Fatalf("%s: %v", row.event, err)
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("%s: %v", row.event, err)
		}
		if row.field == "" {
			return string(encoded)
		}
		return jsonField(encoded, row.field)
	}

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
			if h.host == nil {
				t.Skip("no subprocess host for " + tc.name)
			}
			// The SDK's handlers are the only handlers of this runner.
			sdkRunner := inproc.NewRunner(h.host.Extensions(), t.TempDir(), h.host.Runtime())
			t.Cleanup(sdkRunner.Shutdown)
			sdkRunner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
			for _, r := range rows {
				runConformanceCommandArgs(t, h, "event_result_probe", r.event+" "+r.sdkResult)
				pollUntilConformance(t, 5*time.Second, r.event+": the host never received the SDK's subscription", func() bool { return sdkRunner.HasHandlers(r.event) })
				if got := outcome(t, r, sdkRunner); got != r.want {
					t.Errorf("%s: the host read %q from the SDK's result, want %s", r.event, got, r.want)
				}

				reference := extension.Extension{Path: "go-reference", ResolvedPath: "go-reference"}
				reference.InitializeEventHandlers()
				r.register(&eventReference{ext: &reference})
				referenceRunner := inproc.NewRunner([]extension.Extension{reference}, t.TempDir())
				referenceRunner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
				if got := outcome(t, r, referenceRunner); got != r.want {
					t.Errorf("%s: the host read %q from the Go reference's result, want %s", r.event, got, r.want)
				}
				referenceRunner.Shutdown()
			}
		})
	}
}

// noBoundaryPreview projects no context: the rows read the proposal, not the preview.
func noBoundaryPreview([]extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
	return extension.BoundaryContextPreview{}, nil
}
