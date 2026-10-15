package driver_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Pi source: packages/durable/src/harness/types.ts ToolHooks.afterTool (:652-658): `HookResult<ToolExecutionResult>`: a hook that returns nothing keeps the result (the host receives
// `null`), a returned result replaces it, and a throw is a thrown hook outcome. The Go member drops Pi's trailing `context` argument because its `ctx` is the hook context.
// mutation-checked: `&&` for `||` in the nil-result/error check of the afterTool dispatch fails it (a nil result would be dereferenced).
func TestAfterToolHookKeepsTheResultOrFailsAsThrown(t *testing.T) {
	const payload = `{"call":{"id":"c","name":"t","arguments":{}},"result":{"output":[{"type":"text","text":"ok"}]}}`
	replaced := durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "x"}}}
	for _, tc := range []struct {
		name  string
		hook  func(context.Context, harness.ToolHookCall, durable.ToolExecutionResult, harness.HookApi) (*durable.ToolExecutionResult, error)
		phase uint8
		want  string
	}{
		{"no result keeps the original", func(context.Context, harness.ToolHookCall, durable.ToolExecutionResult, harness.HookApi) (*durable.ToolExecutionResult, error) {
			return nil, nil
		}, abi.OutcomeResult, `null`},
		{"an error with no result is thrown", func(context.Context, harness.ToolHookCall, durable.ToolExecutionResult, harness.HookApi) (*durable.ToolExecutionResult, error) {
			return nil, errors.New("after failed")
		}, abi.OutcomeThrown, `{"name":"Error","message":"after failed"}`},
		{"an error beats a returned result", func(context.Context, harness.ToolHookCall, durable.ToolExecutionResult, harness.HookApi) (*durable.ToolExecutionResult, error) {
			return &replaced, errors.New("after failed")
		}, abi.OutcomeThrown, `{"name":"Error","message":"after failed"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := hookRun(t, "afterTool", map[int]any{0: &harness.ToolHooks{AfterTool: tc.hook}}, payload)
			if ev.Phase != tc.phase || string(ev.Payload) != tc.want {
				t.Fatalf("hook_done = outcome %d %s, want outcome %d %s", ev.Phase, ev.Payload, tc.phase, tc.want)
			}
		})
	}
}
