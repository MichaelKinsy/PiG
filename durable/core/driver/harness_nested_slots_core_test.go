// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// The pi.live.nestedTools slots of nested calls on the core path, ported from Pi 42a3497d harness-nested-tools.test.ts.

// opener closes ch once; a test registers it as a cleanup after its harness, so a failed assertion releases a blocked
// tool before the harness closes instead of hanging.
func opener(ch chan struct{}) func() {
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

func liveCore(t *testing.T, h harness.Harness, root harness.Conversation) *harness.LiveState {
	t.Helper()
	live, err := durable.Snapshot[harness.LiveState](t.Context(), h, harness.LiveDoc, root.Id())
	if err != nil {
		t.Fatal(err)
	}
	return live
}

// harness-nested-tools.test.ts:847 "commits only the status of a nested call made with progress: false, and keeps its output in
// the result" (tool.ts:271 streams, :421-424 publishProgress commits nothing when disabled).
func TestCoreANestedCallMadeWithProgressFalseCommitsOnlyItsStatus(t *testing.T) {
	running, release := make(chan struct{}), make(chan struct{})
	var result durable.NestedToolExecutionResult
	quiet := false
	h, root, sub := nestedCoreHarness(t,
		nestedCoreTool("chatty", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			api.Output("line\n")
			api.Diagnostic(durable.ToolDiagnostic{Severity: "info", Message: "note"})
			if err := api.Details(ctx, map[string]any{"step": float64(1)}); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			close(running)
			<-release
			return durable.ToolExecutionResult{}, nil
		}),
		nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			var err error
			result, err = api.ExecuteTool(ctx, "chatty", map[string]any{}, durable.NestedCallOptions{Progress: &quiet})
			return durable.ToolExecutionResult{}, err
		}))
	open := opener(release)
	t.Cleanup(open)
	// Details returned before this, so a progress commit would have landed already.
	<-running
	live := liveCore(t, h, root)
	if live == nil || len(live.NestedTools) != 1 {
		t.Fatalf("live = %+v", live)
	}
	slot := live.NestedTools[0]
	if slot.CallId != "c1/1" || slot.Status != harness.ToolSlotRunning || slot.Output != nil || slot.Details != nil || slot.Diagnostics != nil {
		t.Fatalf("slot = %s, want only its status", mustJSONCore(t, slot))
	}
	open()
	nestedCoreWait(t, sub, durable.SubmissionDone)
	if result.StructuredOutput != "line\n" || mustJSONCore(t, result.Details) != `{"step":1}` || len(result.Diagnostics) != 1 || result.Diagnostics[0].Message != "note" {
		t.Fatalf("result = %s", mustJSONCore(t, result))
	}
}

// harness-nested-tools.test.ts:936 "shows a nested call's arguments in its slot: as made, then, unbounded, as it runs with them"
// (tool.ts:157-161).
func TestCoreANestedSlotShowsTheArgumentsTheCallRunsWith(t *testing.T) {
	hookEntered, hookRelease := make(chan struct{}), make(chan struct{})
	running, release := make(chan struct{}), make(chan struct{})
	large := strings.Repeat("x", 64*1024)
	extension := &durable.Extension{
		Name: "nested",
		Tools: []*durable.ToolRegistration{
			nestedCoreTool("slow", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				close(running)
				<-release
				return durable.ToolExecutionResult{}, nil
			}),
			nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				_, err := api.ExecuteTool(ctx, "slow", map[string]any{"text": "asked"})
				return durable.ToolExecutionResult{}, err
			}),
		},
		Hooks: []durable.HookRegistration{{Task: harness.ToolTask.AnyDefinition().Name, Handlers: &harness.ToolHooks{
			BeforeTool: func(_ context.Context, call harness.ToolHookCall, _ harness.HookApi) (*harness.BeforeToolResult, error) {
				if call.Parent == nil {
					return nil, nil
				}
				close(hookEntered)
				<-hookRelease
				return &harness.BeforeToolResult{Arguments: map[string]any{"text": large}}, nil
			},
		}}},
	}
	h, root, sub := nestedCoreHarnessWith(t, extension)
	openHook, open := opener(hookRelease), opener(release)
	t.Cleanup(open)
	t.Cleanup(openHook)
	slotArgs := func() (harness.ToolSlotStatus, any) {
		live := liveCore(t, h, root)
		if live == nil || len(live.NestedTools) != 1 {
			t.Fatalf("live = %+v", live)
		}
		arguments, _ := live.NestedTools[0].Arguments.(durable.JsonObject)
		return live.NestedTools[0].Status, arguments.Value("text")
	}
	<-hookEntered
	if status, text := slotArgs(); status != harness.ToolSlotPending || text != "asked" {
		t.Fatalf("before the hook: %s %v", status, text)
	}
	openHook()
	<-running
	if status, text := slotArgs(); status != harness.ToolSlotRunning || text != large {
		t.Fatalf("running: %s, arguments of %d bytes", status, len(text.(string)))
	}
	open()
	nestedCoreWait(t, sub, durable.SubmissionDone)
}

// harness-nested-tools.test.ts:1020 "finds each nested call's slot after slots in the middle were removed" (live.ts:161-175
// toolSlot's binary search, :187-196 removeNestedSlots).
func TestCoreFindsEachNestedSlotAfterSlotsInTheMiddleWereRemoved(t *testing.T) {
	before, checked := make(chan struct{}), make(chan struct{})
	var slots []harness.NestedToolSlot
	h, _, sub := nestedCoreHarness(t, echoCoreTool(),
		nestedCoreTool("inner", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			for _, text := range []string{"m1", "m2"} {
				if _, err := api.ExecuteTool(ctx, "echo", map[string]any{"text": text}); err != nil {
					return durable.ToolExecutionResult{}, err
				}
			}
			return durable.ToolExecutionResult{}, nil
		}),
		nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			// n2 makes m1 and m2 and settles, which removes them from between n2 and n3.
			for _, call := range []struct {
				name string
				args map[string]any
			}{{"echo", map[string]any{"text": "n1"}}, {"inner", map[string]any{}}, {"echo", map[string]any{"text": "n3"}}, {"echo", map[string]any{"text": "n4"}}} {
				if _, err := api.ExecuteTool(ctx, call.name, call.args); err != nil {
					return durable.ToolExecutionResult{}, err
				}
			}
			live, err := durable.Snapshot[harness.LiveState](ctx, api, harness.LiveDoc, api.ConversationId())
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if live != nil {
				slots = live.NestedTools
			}
			close(before)
			<-checked
			return durable.ToolExecutionResult{}, nil
		}))
	open := opener(checked)
	t.Cleanup(open)
	<-before
	var got []string
	for _, slot := range slots {
		if slot.Status != harness.ToolSlotDone || slot.Summary == nil || slot.Summary.IsError || slot.Summary.DurationMs == nil {
			t.Fatalf("slot %s is not done with a summary", mustJSONCore(t, slot))
		}
		got = append(got, slot.CallId)
	}
	if strings.Join(got, " ") != "c1/1 c1/2 c1/3 c1/4" {
		t.Fatalf("slots = %v", got)
	}
	open()
	nestedCoreWait(t, sub, durable.SubmissionDone)
	_ = h
}

// harness-nested-tools.test.ts:1114 "shows a finished nested call as a done slot with its summary until its caller settles"
// (tool.ts:773-795 summaryOf, errorTextOf).
func TestCoreAFinishedNestedCallIsADoneSlotWithItsSummaryUntilItsCallerSettles(t *testing.T) {
	checked, release := make(chan struct{}), make(chan struct{})
	usage := &ai.Usage{Input: 1, Output: 2, TotalTokens: 3, Cost: ai.UsageCost{Input: 0.1, Output: 0.2, Total: 0.3}}
	h, root, sub := nestedCoreHarness(t, echoCoreTool(),
		// Diagnostics win over output for the error text, which is cut to 500 characters.
		nestedCoreTool("verbose", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "ignored"}}, IsError: new(true),
				Diagnostics: []durable.ToolDiagnostic{{Severity: "error", Message: strings.Repeat("e", 600)}}}, nil
		}),
		nestedCoreTool("paid", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Usage: usage}, nil
		}),
		// An error with mixed content: programs get the list, the summary its text.
		nestedCoreTool("broken", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "bad image"}, ai.ImageContent{Data: "AAAA", MimeType: "image/png"}}, IsError: new(true)}, nil
		}),
		nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			for _, call := range []struct {
				name string
				args map[string]any
			}{{"echo", map[string]any{"text": "a"}}, {"missing", map[string]any{}}, {"paid", map[string]any{}}, {"broken", map[string]any{}}, {"verbose", map[string]any{}}} {
				if _, err := api.ExecuteTool(ctx, call.name, call.args); err != nil {
					return durable.ToolExecutionResult{}, err
				}
			}
			close(checked)
			<-release
			return durable.ToolExecutionResult{}, nil
		}))
	open := opener(release)
	t.Cleanup(open)
	<-checked
	live := liveCore(t, h, root)
	if live == nil || len(live.NestedTools) != 5 {
		t.Fatalf("live = %+v", live)
	}
	type want struct {
		duration bool
		summary  string
	}
	for i, expected := range []want{
		{true, `{"isError":false}`},
		{false, `{"isError":true,"error":"Tool missing is not available"}`},
		{true, `{"isError":false,"usage":` + mustJSONCore(t, usage) + `}`},
		{true, `{"isError":true,"error":"bad image"}`},
		{true, `{"isError":true,"error":"` + strings.Repeat("e", 500) + `"}`},
	} {
		slot := live.NestedTools[i]
		if slot.Status != harness.ToolSlotDone || slot.Summary == nil || (slot.Summary.DurationMs != nil) != expected.duration {
			t.Fatalf("slot %d = %s", i, mustJSONCore(t, slot))
		}
		summary := *slot.Summary
		summary.DurationMs = nil
		if got := mustJSONCore(t, summary); got != expected.summary {
			t.Fatalf("slot %d summary = %s, want %s", i, got, expected.summary)
		}
	}
	open()
	nestedCoreWait(t, sub, durable.SubmissionDone)
	if after := liveCore(t, h, root); after != nil && (after.Run != nil || after.Generation != nil || after.Tools != nil || after.NestedTools != nil || after.Compactions != nil) {
		t.Fatalf("live after = %s, want {}", mustJSONCore(t, after))
	}
}
