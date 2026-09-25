package pico3

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestBoundedSlicesOversizedChunksAndCapsLines(t *testing.T) {
	tail := NewBounded(10, 100, "tail")
	tail.Push([]byte("0123456789ABCDEFGHIJ"))
	equal(t, []any{tail.Text(), tail.DroppedBytes}, []any{"ABCDEFGHIJ", 10}, "tail")
	head := NewBounded(10, 100, "head")
	head.Push([]byte("0123456789ABCDEFGHIJ"))
	equal(t, []any{head.Text(), head.DroppedBytes}, []any{"0123456789", 10}, "head")
	lines := NewBounded(1000, 2, "head")
	lines.Push([]byte("a\nb\nc\nd\n"))
	equal(t, []any{lines.Text(), lines.DroppedLines}, []any{"a\nb\n", 2}, "head lines")
	tailLines := NewBounded(1000, 2, "tail")
	for _, line := range []string{"a\n", "b\n", "c\n", "d\n"} {
		tailLines.Push([]byte(line))
	}
	equal(t, []any{tailLines.Text(), tailLines.DroppedLines}, []any{"c\nd\n", 2}, "tail lines")
}

func toolResultEntry(t *testing.T, env *testEnv) Entry {
	t.Helper()
	for _, entry := range env.entries() {
		if entry.Kind == "pi.tool_result" {
			return entry
		}
	}
	t.Fatal("no tool result")
	return Entry{}
}

func textBlocks(message JsonObject) []JsonObject {
	var blocks []JsonObject
	for _, block := range arr(message, "content") {
		if object := asObject(block); str(object, "type") == "text" {
			blocks = append(blocks, object)
		}
	}
	return blocks
}

func TestToolStreamingBoundsStreamAndStoredResult(t *testing.T) {
	declaration := &ToolDeclaration{
		Name: "x", Parameters: toolSchema,
		Output: &ToolOutput{MaxBytes: new(20), MaxLines: new(3), Retain: "tail"},
		Execute: func(ctx context.Context, _ JsonValue, api *ToolApi) (ToolResult, error) {
			for index := range 10 {
				if err := api.Stream(fmt.Appendf(nil, "line %d\n", index)); err != nil {
					return ToolResult{}, err
				}
			}
			// ToolProgress exposes only the free fields, so identity fields
			// cannot be forged; upstream copies them into a free-field view and
			// ignores assignments.
			err := api.Progress(ctx, func(slot *ToolProgress) { slot.Progress = new("p") })
			return ToolResult{}, err
		},
	}
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{declaration}})
	env.wait(env.send(env.root, "tool:x"))
	result := toolResultEntry(t, env)
	text := str(textBlocks(result.Model[0])[0], "text")
	if len(text) > 20 || len(strings.Split(text, "\n")) > 4 || !strings.HasSuffix(text, "line 9\n") {
		t.Fatalf("bounded text = %q", text)
	}
	truncated := obj(result.Data, "truncated")
	if numberOr(truncated["bytes"], 0) <= 0 || numberOr(truncated["lines"], 0) <= 0 {
		t.Fatalf("truncated = %#v", truncated)
	}
	equal(t, str(asObject(arr(result.Data, "diagnostics")[0]), "code"), "truncated", "diagnostic")
	equal(t, string(mustJSON(obj(env.sticky(), "turn"))), `{"tools":[]}`, "turn")
}

func TestReturnedTextBlocksShareOneOutputBudget(t *testing.T) {
	declaration := &ToolDeclaration{
		Name: "aggregate", Parameters: toolSchema,
		Output: &ToolOutput{MaxBytes: new(10), MaxLines: new(100), Retain: "tail"},
		Execute: func(context.Context, JsonValue, *ToolApi) (ToolResult, error) {
			return ToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "abcdefgh"}, ai.TextContent{Text: "ijklmnop"}, ai.TextContent{Text: "qrstuvwx"}}}, nil
		},
	}
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{declaration}})
	env.wait(env.send(env.root, "tool:aggregate"))
	result := toolResultEntry(t, env)
	equal(t, textBlocks(result.Model[0]), []JsonObject{{"type": "text", "text": "opqrstuvwx"}}, "text blocks")
	equal(t, obj(result.Data, "truncated")["bytes"], 14, "truncated bytes")
}

func TestThrowingBeforeToolHookBlocksTheCallBeforeStarted(t *testing.T) {
	calls := 0
	declaration := &ToolDeclaration{
		Name: "x", Parameters: toolSchema,
		Execute: func(context.Context, JsonValue, *ToolApi) (ToolResult, error) {
			calls++
			return ToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ran"}}}, nil
		},
	}
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{declaration}, hooks: &hooksByKind{tool: &ToolHooks{
		BeforeTool: func(context.Context, JsonObject, *BeforeToolApi) (*BeforeToolResult, error) {
			return nil, errors.New("nope")
		},
	}}})
	env.wait(env.send(env.root, "tool:x"))
	equal(t, calls, 0, "calls")
	if model := string(mustJSON(toolResultEntry(t, env).Model)); !strings.Contains(model, "blocked: hook threw") {
		t.Fatalf("model = %s", model)
	}
	if tool := firstOfKind(t, env.tasks(), "pi.tool"); tool.Checkpoint != nil {
		t.Fatalf("checkpoint = %#v", tool.Checkpoint)
	}
}

func quickKind(t *testing.T, name string, result func(task Task) JsonValue) *Kind {
	t.Helper()
	return must(DefineTask(Kind{
		Name: name,
		Initial: func(context.Context, Task, *Runtime) (Step, error) {
			return Step{Next: Checkpoint{"phase": "x"}}, nil
		},
		Phases: map[string]PhaseHandler{"x": func(_ context.Context, task Task, _ *Runtime) (Step, error) {
			return done(Completed(result(task))), nil
		}},
		Abort: func(context.Context, Task, *Runtime) (AbortClosure, error) {
			return func(context.Context, *Tx, Task) (JsonValue, error) { return nil, nil }, nil
		},
	}))
}

func createBackground(t *testing.T, env *testEnv, kind *Kind, input JsonValue) TaskRef {
	t.Helper()
	conversation := Id(1)
	return must(HostCommit(bg, env.root, func(_ context.Context, tx *Tx) (TaskRef, error) {
		return tx.CreateTask(kind, input, TaskOptions{ConversationId: &conversation, Background: true})
	}))
}

func TestTerminalTasksStayReadableAfterRetirementAndReopen(t *testing.T) {
	counter := quickKind(t, "n", func(task Task) JsonValue { return JsonObject{"n": asObject(task.Input)["n"]} })
	env := openEnv(t, openOptions{backend: "jsonl", taskKinds: []*Kind{counter}})
	first := createBackground(t, env, counter, JsonObject{"n": 1})
	must(env.h.WaitForTask(bg, first.Id))
	for index := 2; index <= 150; index++ {
		must(env.h.WaitForTask(bg, createBackground(t, env, counter, JsonObject{"n": index}).Id))
	}
	equal(t, resultOf(must(env.h.GetTask(bg, first.Id))), JsonObject{"n": 1}, "first result")
	terminal := 0
	for _, task := range env.tasks() {
		if task.Status == TaskTerminal {
			terminal++
		}
	}
	equal(t, terminal, 150, "terminal tasks")
	plugin := createBackground(t, env, Kinds.Plugin, JsonObject{"handler": "none", "input": nil})
	must(env.h.WaitForTask(bg, plugin.Id))
	dir := env.dir
	env.crash()
	for _, file := range must(os.ReadDir(dir)) {
		if strings.HasPrefix(file.Name(), "task-") {
			t.Fatalf("live-only sidecar %s survived", file.Name())
		}
	}
	env = openEnv(t, openOptions{dir: dir, backend: "jsonl", taskKinds: []*Kind{counter}})
	firstTask := must(env.h.GetTask(bg, first.Id))
	equal(t, resultOf(firstTask), JsonObject{"n": 1}, "reopened result")
	equal(t, must(env.h.WaitForTask(bg, first.Id)).Status, TaskTerminal, "waitForTask")
	equal(t, must(env.h.GetTask(bg, plugin.Id)).Outcome.Status, OutcomeFailed, "plugin outcome")
	equal(t, len(tasksOfKind(env.tasks(), "n")), 150, "reopened tasks")
	if firstTask.Checkpoint != nil {
		t.Fatalf("terminal checkpoint = %#v", firstTask.Checkpoint)
	}
}

func TestBashToolStreamsAndReportsExit(t *testing.T) {
	declaration := BashTool(&ToolOutput{MaxBytes: new(5), MaxLines: new(10), Retain: "tail"})
	cwd := t.TempDir()
	models := newFake(fakeOptions{respond: func(messages []JsonObject, _ int) fakeResponse {
		if str(lastMessage(messages), "role") == "toolResult" {
			return textResponse("done")
		}
		return fakeResponse{toolCalls: []fakeToolCall{{name: "bash", arguments: JsonObject{"command": "printf abcdefgh; exit 7", "cwd": cwd}}}}
	}})
	env := openEnv(t, openOptions{models: models, tools: []*ToolDeclaration{declaration}})
	env.wait(env.send(env.root, "run"))
	env.idle()
	result := toolResultEntry(t, env)
	equal(t, str(textBlocks(result.Model[0])[0], "text"), "defgh", "bounded subprocess stdout")
	equal(t, result.Model[0]["isError"], true, "nonzero exit")
	equal(t, obj(result.Data, "details")["exitCode"], 7, "process exit code")
}
