package extension

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Pi packages/coding-agent/src/core/extensions/runner.ts:952-985 createToolContext and loader.ts:376-379 appendEntry: a tool context answers Tools() with the callable tools (an empty list when none are
// bound), appends entries through the bound action (failing with notInitialized when none is bound), and runs nested calls through the bound executor with the call's id and either the calling
// tool's signal or the options' own (`options.signal ?? signal`). Each of them first asserts the context is still active.
// mutation-checked: dropping the nil checks on the three actions, the nested-call error handling, or the signal choice fails it.
func TestToolContextActionsFollowTheBoundHostActions(t *testing.T) {
	stale := errors.New("this extension instance is stale")
	active := func() error { return nil }
	newContext := func(assert func() error, actions ToolActions) *ToolContext {
		return NewToolContext(NewContext("/cwd", nil, assert, ContextActions{}), "call-7", context.Background(), actions)
	}
	t.Run("tools", func(t *testing.T) {
		tools, err := newContext(active, ToolActions{}).Tools()
		if err != nil || tools == nil || len(tools) != 0 {
			t.Errorf("unbound Tools() = %v, %v, want an empty non-nil list", tools, err)
		}
		bound := []AgentTool{{Name: "a"}, {Name: "b"}}
		tools, err = newContext(active, ToolActions{GetCallableTools: func() []AgentTool { return bound }}).Tools()
		if err != nil || len(tools) != 2 {
			t.Errorf("bound Tools() = %v, %v, want the bound list", tools, err)
		}
		if _, err := newContext(func() error { return stale }, ToolActions{}).Tools(); !errors.Is(err, stale) {
			t.Errorf("stale Tools() error = %v, want %v", err, stale)
		}
	})
	t.Run("appendEntry", func(t *testing.T) {
		if err := newContext(active, ToolActions{}).AppendEntry("kind", 1); !errors.Is(err, ErrRuntimeNotInitialized) {
			t.Errorf("unbound AppendEntry error = %v, want ErrRuntimeNotInitialized", err)
		}
		var gotType string
		var gotData any
		failure := errors.New("log failed")
		context := newContext(active, ToolActions{AppendEntry: func(customType string, data any) error {
			gotType, gotData = customType, data
			return failure
		}})
		if err := context.AppendEntry("kind", map[string]any{"a": 1}); !errors.Is(err, failure) || gotType != "kind" || gotData == nil {
			t.Errorf("bound AppendEntry = %v with %q, %v, want the action's error and arguments", err, gotType, gotData)
		}
		if err := newContext(func() error { return stale }, ToolActions{AppendEntry: func(string, any) error { t.Error("a stale context ran the action"); return nil }}).AppendEntry("kind", 1); !errors.Is(err, stale) {
			t.Errorf("stale AppendEntry error = %v, want %v", err, stale)
		}
	})
	t.Run("executeTool", func(t *testing.T) {
		if _, err := newContext(active, ToolActions{}).ExecuteTool("t", nil, nil); !errors.Is(err, errNestedCallsUnavailable) {
			t.Errorf("unbound ExecuteTool error = %v, want the unavailable error", err)
		}
		type seen struct {
			own       bool
			callID    string
			name      string
			raw       string
			hadSignal bool
		}
		var got seen
		actions := ToolActions{ExecuteTool: func(ctx context.Context, callID, name string, raw json.RawMessage, options ExecuteToolOptions) (AgentToolCallOutcome, error) {
			got = seen{own: OwnsSignal(ctx), callID: callID, name: name, raw: string(raw), hadSignal: options.Signal != nil}
			return AgentToolCallOutcome{IsError: true}, nil
		}}
		outcome, err := newContext(active, actions).ExecuteTool("read", map[string]any{"path": "a.txt"}, nil)
		if err != nil || !outcome.IsError || got.own || got.callID != "call-7" || got.name != "read" || got.raw != `{"path":"a.txt"}` {
			t.Errorf("default call: outcome %+v, %v, saw %+v", outcome, err, got)
		}
		own := t.Context()
		if _, err := newContext(active, actions).ExecuteTool("read", nil, &ExecuteToolOptions{Signal: own}); err != nil || !got.own || got.hadSignal {
			t.Errorf("call with its own signal: err %v, saw %+v, want the own-signal context and no Signal option forwarded", err, got)
		}
		if _, err := newContext(active, actions).ExecuteTool("read", func() {}, nil); err == nil {
			t.Error("unencodable arguments were accepted")
		}
		if _, err := newContext(func() error { return stale }, actions).ExecuteTool("read", nil, nil); !errors.Is(err, stale) {
			t.Errorf("stale ExecuteTool error = %v, want %v", err, stale)
		}
	})
}
