// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// hookAPI is the HookApi a hook sees; these hooks read only its identity.
type hookAPI struct {
	harness.HookApi
	task durable.TaskId
	conv durable.ConversationId
}

func (a hookAPI) TaskId() durable.TaskId                 { return a.task }
func (a hookAPI) ConversationId() durable.ConversationId { return a.conv }

func hookHost(t *testing.T, core *funcCore, handlers map[int]any) *sqlhost.Host {
	t.Helper()
	db, err := sqlhost.OpenDB(t.Context(), filepath.Join(t.TempDir(), "s.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	hooks := &driver.Hooks{
		Lookup: func(i int) (any, bool) { h, ok := handlers[i]; return h, ok },
		API: func(task durable.TaskId, conv durable.ConversationId) harness.HookApi {
			return hookAPI{task: task, conv: conv}
		},
	}
	host := sqlhost.New(sqlhost.Options{
		Core: core, DB: db, OwnDB: true, Clock: func() float64 { return 5000 },
		Handlers: map[abi.EffectKind]sqlhost.Handler{abi.EffectHook: hooks.Handler()},
	})
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	return host
}

// hookRun sends one hook effect to the driver and returns the completion the core received.
func hookRun(t *testing.T, name string, handlers map[int]any, payload string) abi.Event {
	t.Helper()
	core := &funcCore{respond: func(ev abi.Event) *abi.Step {
		if ev.Kind != abi.EventOpen {
			return nil
		}
		body := `{"name":"` + name + `","handler":0,"taskId":7,"conversationId":3,"payload":` + payload + `}`
		return &abi.Step{Effects: []abi.Effect{{ID: 1, Kind: abi.EffectHook, Payload: []byte(body)}}}
	}}
	host := hookHost(t, core, handlers)
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventOpen, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "hook_done", func() bool { return len(core.events(abi.EventHookDone)) == 1 || host.Err() != nil })
	if host.Err() != nil {
		t.Fatalf("host discarded: %v", host.Err())
	}
	return core.events(abi.EventHookDone)[0]
}

const userMsg = `{"role":"user","content":"hi","timestamp":1}`

// mutation-checked: dropping the reads and writes of CompactionHooks.BeforeCompact, GenerationHooks.BeforeRequest, GenerationHooks.OnYield, ToolHooks.AfterTool fails it
func TestHooksReturnTheResultObjectOfEachHook(t *testing.T) {
	block := "no"
	summary := "short"
	for _, tc := range []struct {
		name, payload, want string
		handler             any
	}{
		{"beforeRequest", `{"messages":[` + userMsg + `]}`, `{"messages":[{"role":"user","content":"hi","timestamp":2}]}`, &harness.GenerationHooks{
			BeforeRequest: func(_ context.Context, r harness.GenerationRequest, api harness.HookApi) (*harness.GenerationRequest, error) {
				if len(r.Messages) != 1 || api.TaskId() != 7 || api.ConversationId() != 3 {
					t.Errorf("request %v api %v %v", r, api.TaskId(), api.ConversationId())
				}
				return &harness.GenerationRequest{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 2}}}, nil
			}}},
		{"beforeRequest", `{"messages":[` + userMsg + `]}`, `null`, &harness.GenerationHooks{}},
		{"beforeRequest", `{"messages":[` + userMsg + `]}`, `null`, &harness.GenerationHooks{
			BeforeRequest: func(context.Context, harness.GenerationRequest, harness.HookApi) (*harness.GenerationRequest, error) {
				return nil, nil
			}}},
		{"beforeCompact", `{"compaction":{"reason":"overflow","entries":[],"messages":[],"firstKept":1}}`, `{"decline":true}`, &harness.CompactionHooks{
			BeforeCompact: func(context.Context, harness.CompactionRequest, harness.HookApi) (*harness.CompactionDecision, error) {
				return &harness.CompactionDecision{Decline: true}, nil
			}}},
		{"onYield", `{"answer":{"role":"assistant","content":[],"stopReason":"stop"}}`, `{"continue":"more"}`, &harness.GenerationHooks{
			OnYield: func(context.Context, ai.AssistantMessage, harness.HookApi) (*harness.YieldContinue, error) {
				return &harness.YieldContinue{Continue: ai.UserText("more")}, nil
			}}},
		{"afterTools", `{"assistant":4,"results":[5,6]}`, `null`, &harness.GenerationHooks{
			AfterTools: func(_ context.Context, assistant durable.EntryId, results []durable.EntryId, _ harness.HookApi) error {
				if assistant != 4 || len(results) != 2 || results[1] != 6 {
					t.Errorf("afterTools %v %v", assistant, results)
				}
				return nil
			}}},
		{"beforeTool", `{"call":{"id":"c","name":"t","arguments":{"a":1}}}`, `{"block":"no"}`, &harness.ToolHooks{
			BeforeTool: func(_ context.Context, call ai.ToolCall, _ harness.HookApi) (*harness.BeforeToolResult, error) {
				if call.Name != "t" {
					t.Errorf("call %v", call)
				}
				return &harness.BeforeToolResult{Block: &block}, nil
			}}},
		{"beforeTool", `{"call":{"id":"c","name":"t","arguments":{"a":1}}}`, `{"arguments":{"a":2}}`, &harness.ToolHooks{
			BeforeTool: func(context.Context, ai.ToolCall, harness.HookApi) (*harness.BeforeToolResult, error) {
				return &harness.BeforeToolResult{Arguments: map[string]any{"a": float64(2)}}, nil
			}}},
		{"beforeTool", `{"call":{"id":"c","name":"t","arguments":{"a":1}}}`, `{"arguments":{}}`, &harness.ToolHooks{
			BeforeTool: func(context.Context, ai.ToolCall, harness.HookApi) (*harness.BeforeToolResult, error) {
				// An explicit empty arguments object replaces the call's arguments; only a nil one leaves them.
				return &harness.BeforeToolResult{Arguments: map[string]any{}}, nil
			}}},
		{"afterTool", `{"call":{"id":"c","name":"t","arguments":{}},"result":{"content":[{"type":"text","text":"ok"}],"details":{"k":1}}}`, `{"content":[{"type":"text","text":"changed"}],"details":{"k":1}}`, &harness.ToolHooks{
			AfterTool: func(_ context.Context, _ ai.ToolCall, r durable.ToolExecutionResult, _ harness.HookApi) (*durable.ToolExecutionResult, error) {
				if len(r.Content) != 1 || !r.HasDetails {
					t.Errorf("result %+v", r)
				}
				r.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: "changed"}}
				return &r, nil
			}}},
		{"beforeCompact", `{"compaction":{"reason":"manual","entries":[],"messages":[],"firstKept":9}}`, `{"summary":"short"}`, &harness.CompactionHooks{
			BeforeCompact: func(_ context.Context, r harness.CompactionRequest, _ harness.HookApi) (*harness.CompactionDecision, error) {
				if r.Reason != durable.CompactionManual || r.FirstKept != 9 {
					t.Errorf("request %+v", r)
				}
				return &harness.CompactionDecision{Summary: &summary}, nil
			}}},
		{"prepareArguments", `{"arguments":"[1,2]"}`, `[1,2]`, driver.PrepareArguments(func(args any) (any, error) {
			var out []int
			return out, json.Unmarshal([]byte(args.(string)), &out)
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := hookRun(t, tc.name, map[int]any{0: tc.handler}, tc.payload)
			if ev.Phase != abi.OutcomeResult || string(ev.Payload) != tc.want {
				t.Fatalf("hook_done = outcome %d %s, want %s", ev.Phase, ev.Payload, tc.want)
			}
		})
	}
}

// Pi source: packages/durable/src/harness/types.ts
// mutation-checked: dropping the reads and writes of GenerationHooks.AfterResponse fails it
func TestHookFailureIsAThrownOutcome(t *testing.T) {
	ev := hookRun(t, "afterResponse", map[int]any{0: &harness.GenerationHooks{
		AfterResponse: func(context.Context, ai.AssistantMessage, harness.HookApi) error { return errors.New("audit failed") },
	}}, `{"message":{"role":"assistant","content":[],"stopReason":"stop"}}`)
	if ev.Phase != abi.OutcomeThrown || string(ev.Payload) != `{"name":"Error","message":"audit failed"}` {
		t.Fatalf("hook_done = outcome %d %s", ev.Phase, ev.Payload)
	}
}

func TestHandlerOfTheWrongKindDiscardsTheHost(t *testing.T) {
	core := &funcCore{respond: func(ev abi.Event) *abi.Step {
		if ev.Kind != abi.EventOpen {
			return nil
		}
		return &abi.Step{Effects: []abi.Effect{{ID: 1, Kind: abi.EffectHook, Payload: []byte(`{"name":"beforeTool","handler":0,"taskId":1,"conversationId":1,"payload":{"call":{"id":"c","name":"t","arguments":{}}}}`)}}}
	}}
	host := hookHost(t, core, map[int]any{0: &harness.GenerationHooks{}})
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventOpen, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "discard", func() bool { return host.Err() != nil })
	if !strings.Contains(host.Err().Error(), "hook beforeTool: handler is a *harness.GenerationHooks") {
		t.Fatalf("err = %v", host.Err())
	}
}
