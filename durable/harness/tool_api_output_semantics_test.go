package harness

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
)

// packages/durable/src/harness/tool.ts:194-196 api.output(chunk, skipped): a Uint8Array chunk takes the skipped marker like a string (OutputBuffer.push), and the
// harness reads the retained output when the result omits content.
func TestToolApiOutputBytesAcceptTheSkippedMarker(t *testing.T) {
	setup := chatSetup(t)
	addTool(t, setup.Registry, tlTool("tailed", func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		api.Output([]byte("dropped\n"))
		api.Output([]byte("x\ny\n"), env.ShellOutputSkip{Bytes: 8, Newlines: 1, EndsWithNewline: true})
		return durable.ToolExecutionResult{}, nil
	}, func(tool *durable.ToolRegistration) {
		lines := 1
		tool.OutputLimits = &durable.ToolOutputLimits{MaxLines: &lines, Retain: durable.RetainTail}
	}))
	run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"tailed", map[string]any{}, "c1"}), tlDone()}, nil)
	tlExpectText(t, tlResultText(tlResults(run.entries)[0]), "y\n|<harness>\n[warn] Output truncated to its end: 3 lines, 18 bytes dropped\n</harness>")
	mustClose(t, run.harness)
}

// tool.ts:194-196 api.output rejects once the tool call has ended (assertLive) and accepts only string or Uint8Array chunks.
func TestToolApiOutputRejectsLateCallsAndForeignChunks(t *testing.T) {
	setup := chatSetup(t)
	var api durable.ToolExecutionApi
	var foreign any
	addTool(t, setup.Registry, tlTool("probe", func(_ context.Context, _ any, toolApi durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		api = toolApi
		func() {
			defer func() { foreign = recover() }()
			toolApi.Output(42)
		}()
		return durable.ToolExecutionResult{}, nil
	}))
	run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"probe", map[string]any{}, "c1"}), tlDone()}, nil)
	if err, ok := foreign.(error); !ok || err.Error() != "tool output chunk is int, not a string or bytes" {
		t.Fatalf("Output(42) panicked with %v, want the chunk-type error", foreign)
	}
	var skipped any
	addTool(t, setup.Registry, tlTool("headed", func(_ context.Context, _ any, toolApi durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		func() {
			defer func() { skipped = recover() }()
			toolApi.Output("x", env.ShellOutputSkip{Bytes: 1})
		}()
		return durable.ToolExecutionResult{}, nil
	}))
	headed := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"headed", map[string]any{}, "c1"}), tlDone()}, nil)
	if err, ok := skipped.(error); !ok || err.Error() != "Skipped output requires tail retention" {
		t.Fatalf("skipped output under head retention panicked with %v, want the tail-retention error", skipped)
	}
	mustClose(t, headed.harness)
	var late any
	func() {
		defer func() { late = recover() }()
		api.Output("late")
	}()
	if late == nil {
		t.Fatal("Output after the tool call ended did not panic")
	}
	mustClose(t, run.harness)
}

// generation.ts:450 runtime.hooks.each("afterResponse", ...): a generation hook set that leaves afterResponse unset is skipped without a report; the
// hooks that set it still run.
func TestAfterResponseSkipsHookSetsWithoutIt(t *testing.T) {
	setup := chatSetup(t)
	addHooks(t, setup.Registry, GenerationTask.Erased(), &GenerationHooks{OnYield: func(context.Context, ai.AssistantMessage, HookApi) (*YieldContinue, error) { return nil, nil }})
	seen := &syncList[string]{}
	addHooks(t, setup.Registry, GenerationTask.Erased(), &GenerationHooks{AfterResponse: func(_ context.Context, message ai.AssistantMessage, _ HookApi) error {
		text, _ := textOf(message)
		seen.add(text)
		return nil
	}})
	run := tlRun(t, setup, []ai.FauxResponseStep{ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.AssistantMessage, error) {
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("hello")}}.AssistantMessage(), nil
	})}, nil)
	if reports := setup.Reports.all(); len(reports) != 0 {
		t.Fatalf("hook reports = %v, want none", reports)
	}
	if got := seen.all(); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("afterResponse saw %v, want [hello]", got)
	}
	if run.status != durable.SubmissionDone {
		t.Fatalf("status %s, want done", run.status)
	}
	mustClose(t, run.harness)
}

// scheduler.ts:1170-1172 memo(name, context) reads through #read: a memo read after the invocation ended rejects instead of answering from the live record.
func TestToolApiMemoRejectsOnceTheInvocationEnded(t *testing.T) {
	setup := chatSetup(t)
	var kept durable.ToolExecutionApi
	addTool(t, setup.Registry, tlTool("probe", func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		kept = api
		return durable.ToolExecutionResult{}, nil
	}))
	run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"probe", map[string]any{}, "c1"}), tlDone()}, nil)
	if value, ok, err := kept.Memo(context.Background(), "pick"); err == nil {
		t.Fatalf("Memo after the tool invocation ended = %v, %v, want an error", value, ok)
	}
	mustClose(t, run.harness)
}

// types.ts DocumentReader.snapshotAsOf(doc, at, ...scope) reached through a tool's api: the entry and the scope are forwarded to the Session, which reports an entry
// that is not visible from the conversation by its id.
func TestToolApiSnapshotAsOfForwardsTheEntryAndScope(t *testing.T) {
	setup := chatSetup(t)
	var gotErr error
	addTool(t, setup.Registry, tlTool("probe", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		var reader durable.DocumentReader = api
		_, gotErr = reader.SnapshotAsOfErased(ctx, LiveDoc, 999, api.ConversationId())
		return durable.ToolExecutionResult{}, nil
	}))
	run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"probe", map[string]any{}, "c1"}), tlDone()}, nil)
	if gotErr == nil || gotErr.Error() != "Entry 999 is not visible from conversation 1" {
		t.Fatalf("SnapshotAsOfErased error = %v, want the entry-not-visible error naming entry 999 and conversation 1", gotErr)
	}
	mustClose(t, run.harness)
}
