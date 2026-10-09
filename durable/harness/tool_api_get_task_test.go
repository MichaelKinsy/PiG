package harness

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// packages/durable/src/harness/types.ts:200 ToolExecutionApi.getTask(id): the record of a task, or undefined when absent. A running tool reads its own task and
// sees it running; an unknown id is absent.
// Pi: packages/durable/src/harness/harness.ts:242 (getTask)
func TestToolExecutionApiGetTask(t *testing.T) {
	chat := openCompaction(t)
	var own *durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
	var missing *durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
	addTool(t, chat.setup.Registry, new(durable.ToolRegistration{ToolSchema: ai.ToolSchema{Name: "probe", Description: "probe", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}, Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		var err error
		if own, err = api.GetTask(ctx, api.TaskId()); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if missing, err = api.GetTask(ctx, durable.TaskId(987654321)); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "probed"}}}, nil
	}}))
	if err := chat.root.Configure(testContext, AgentChange{Tools: SetTo(ToolChange{Exact: true, List: toolsNamed(t, chat.setup, "probe")})}); err != nil {
		t.Fatal(err)
	}
	chat.faux.pushAgent(scriptFixedToolCall("probe"), scriptAnswer("done"))
	input := submitInput(t, chat.root, "probe")
	expectSettled(t, must(input.Wait(testContext)), durable.SubmissionDone, "")
	if own == nil || own.State.Status != durable.TaskRunning {
		t.Fatalf("own task record = %+v, want a running task", own)
	}
	if missing != nil {
		t.Fatalf("unknown task id returned %+v, want nil", missing)
	}
	chat.close(t)
}

func scriptFixedToolCall(name string) scriptStep {
	return fixed(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall(name, map[string]any{}, &ai.FauxToolCallOptions{ID: ""})}, StopReason: "toolUse"})
}

// packages/durable/src/harness/types.ts:192 ToolExecutionApi.memo(name): a tool reads a durable memo of its call's task; it is absent until memoCandidate stores one,
// and the first stored candidate wins over later ones.
func TestToolExecutionApiMemoReadsTheDurableMemoOfTheToolTask(t *testing.T) {
	chat := openCompaction(t)
	var before, after, winner durable.JsonValue
	var presentBefore, presentAfter bool
	addTool(t, chat.setup.Registry, new(durable.ToolRegistration{ToolSchema: ai.ToolSchema{Name: "memo", Description: "memo", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}, Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		var err error
		if before, presentBefore, err = api.Memo(ctx, "pick"); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if _, err = api.MemoCandidate(ctx, "pick", "first"); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if winner, err = api.MemoCandidate(ctx, "pick", "second"); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if after, presentAfter, err = api.Memo(ctx, "pick"); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "memoized"}}}, nil
	}}))
	if err := chat.root.Configure(testContext, AgentChange{Tools: SetTo(ToolChange{Exact: true, List: toolsNamed(t, chat.setup, "memo")})}); err != nil {
		t.Fatal(err)
	}
	chat.faux.pushAgent(scriptFixedToolCall("memo"), scriptAnswer("done"))
	input := submitInput(t, chat.root, "memo")
	expectSettled(t, must(input.Wait(testContext)), durable.SubmissionDone, "")
	if presentBefore || before != nil {
		t.Fatalf("memo before any candidate = %v/%v", before, presentBefore)
	}
	if winner != "first" || after != "first" || !presentAfter {
		t.Fatalf("memo after candidates = %v (winner %v, present %v), want the first candidate", after, winner, presentAfter)
	}
	chat.close(t)
}
