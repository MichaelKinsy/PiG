package pico3

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func diagnosticCode(entry Entry) string {
	return str(asObject(arr(entry.Data, "diagnostics")[0]), "code")
}

func TestToolAdmissionRejectsUnofferedMissingAndInvalidArguments(t *testing.T) {
	for _, mode := range []string{"not-offered", "missing", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			tool := newTool("x", toolOptions{})
			options := openOptions{tools: []*ToolDeclaration{tool.ToolDeclaration}}
			expected := "not_offered"
			switch mode {
			case "not-offered":
				options.root = &RootSpec{Rewindable: JsonObject{"model": testModel, "selectedTools": []any{}}}
			case "missing":
				options.tools = nil
				options.root = &RootSpec{Rewindable: JsonObject{"model": testModel, "selectedTools": []any{"x"}}}
			case "invalid":
				expected = "invalid_arguments"
				options.models = newFake(fakeOptions{respond: func(messages []JsonObject, _ int) fakeResponse {
					if str(lastMessage(messages), "role") == "toolResult" {
						return textResponse("ok")
					}
					return fakeResponse{toolCalls: []fakeToolCall{{name: "x", arguments: JsonObject{"v": 42}}}}
				}})
			}
			env := openEnv(t, options)
			env.wait(env.send(env.root, "tool:x"))
			equal(t, tool.calls.Load(), int64(0), "not invoked")
			equal(t, diagnosticCode(toolResultEntry(t, env)), expected, "diagnostic")
		})
	}
}

func TestBeforeToolBlocksRewritesAndRejectsIdentityChanges(t *testing.T) {
	for _, mode := range []string{"block", "rewrite", "identity", "throw"} {
		t.Run(mode, func(t *testing.T) {
			tool := newTool("x", toolOptions{})
			env := openEnv(t, openOptions{tools: []*ToolDeclaration{tool.ToolDeclaration}, hooks: &hooksByKind{tool: &ToolHooks{BeforeTool: func(_ context.Context, call JsonObject, _ *BeforeToolApi) (*BeforeToolResult, error) {
				switch mode {
				case "block":
					return &BeforeToolResult{Block: new("no")}, nil
				case "throw":
					return nil, errors.New("hook boom")
				case "rewrite":
					call["arguments"] = JsonObject{"v": "rewritten"}
				case "identity":
					call["name"] = "other"
				}
				return &BeforeToolResult{Call: call}, nil
			}}}})
			env.wait(env.send(env.root, "tool:x"))
			result := toolResultEntry(t, env)
			if mode == "rewrite" {
				equal(t, tool.calls.Load(), int64(1), "invoked")
				if !strings.Contains(contentOf(&result), "x(rewritten)") {
					t.Fatalf("rewritten output: %s", contentOf(&result))
				}
			} else {
				equal(t, tool.calls.Load(), int64(0), "blocked")
				equal(t, diagnosticCode(result), "blocked", "diagnostic")
			}
		})
	}
}

func TestAfterToolCanReplaceThrownResult(t *testing.T) {
	tool := newTool("x", toolOptions{throws: "kaboom"})
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{tool.ToolDeclaration}, hooks: &hooksByKind{tool: &ToolHooks{AfterTool: func(_ context.Context, _ JsonObject, result ToolResult, _ AfterToolInfo) (*ToolResult, error) {
		if !result.IsError {
			return nil, errors.New("throw did not produce error result")
		}
		result.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: "softened"}}
		result.IsError = false
		return &result, nil
	}}}})
	env.wait(env.send(env.root, "tool:x"))
	result := toolResultEntry(t, env)
	equal(t, contentOf(&result), `[{"text":"softened","type":"text"}]`, "replacement")
	equal(t, result.Model[0]["isError"], false, "error cleared")
}

func TestToolHeadTailOutputBoundsRecordTruncation(t *testing.T) {
	var lines []string
	for i := range 500 {
		lines = append(lines, fmt.Sprintf("line%d", i))
	}
	big := strings.Join(lines, "\n")
	for _, retain := range []string{"head", "tail"} {
		t.Run(retain, func(t *testing.T) {
			tool := newTool("big", toolOptions{output: &ToolOutput{MaxBytes: new(2000), MaxLines: new(100), Retain: retain}, result: &ToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: big}}}})
			env := openEnv(t, openOptions{tools: []*ToolDeclaration{tool.ToolDeclaration}})
			env.wait(env.send(env.root, "tool:big"))
			result := toolResultEntry(t, env)
			text := str(asObject(arr(result.Model[0], "content")[0]), "text")
			got := strings.Split(text, "\n")
			equal(t, len(got), 100, "lines")
			want := "line0"
			if retain == "tail" {
				want = "line400"
			}
			equal(t, got[0], want, "retained edge")
			equal(t, obj(result.Data, "truncated")["lines"], 400, "dropped lines")
			equal(t, diagnosticCode(result), "truncated", "diagnostic")
		})
	}
}

func TestToolSlotOutputSuffixAndRetirement(t *testing.T) {
	gate := &testGate{}
	tool := &ToolDeclaration{Name: "p", Parameters: toolSchema, Execute: func(ctx context.Context, _ JsonValue, api *ToolApi) (ToolResult, error) {
		if err := api.Stream([]byte("half")); err != nil {
			return ToolResult{}, err
		}
		if err := api.Progress(ctx, func(slot *ToolProgress) { slot.Progress = new("50%") }); err != nil {
			return ToolResult{}, err
		}
		if err := gate.Wait(ctx); err != nil {
			return ToolResult{}, err
		}
		if err := api.Stream([]byte(" full")); err != nil {
			return ToolResult{}, err
		}
		if err := api.Progress(ctx, func(slot *ToolProgress) { slot.Progress = new("100%") }); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}}, nil
	}}
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{tool}})
	watch := collectWatch(t, env.root)
	input := env.send(env.root, "tool:p")
	gate.Arrivals(t, 1)
	slots := arr(obj(env.sticky(), "turn"), "tools")
	equal(t, len(slots), 1, "one slot")
	equal(t, asObject(slots[0])["callId"], "call_0_0", "call id")
	equal(t, asObject(slots[0])["output"], "half", "partial output")
	gate.Open()
	env.wait(input)
	env.idle()
	equal(t, obj(env.sticky(), "turn"), JsonObject{"tools": []any{}}, "turn retired")
	equal(t, obj(env.sticky(), "tasks"), JsonObject{}, "tasks retired")
	found := false
	for _, envelope := range watch.Envelopes() {
		for _, op := range envelope.Ops {
			if op.Verb() == "a" && string(mustJSON(op.Path())) == `["turn","tools",0,"output"]` && op[2] == " full" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("missing output suffix append")
	}
}

func TestToolArgumentSchemaAndSyntheticFailure(t *testing.T) {
	calls := 0
	tool := &ToolDeclaration{Name: "typed", Parameters: JsonObject{"type": "object", "properties": JsonObject{"n": JsonObject{"type": "number"}, "tags": JsonObject{"type": "array", "items": JsonObject{"type": "string"}}}, "required": []any{"n"}}, Execute: func(_ context.Context, args JsonValue, _ *ToolApi) (ToolResult, error) {
		calls++
		value := asObject(args)
		return ToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("n=%g tags=%d", numberOr(value["n"], 0)*2, len(arr(value, "tags")))}}}, nil
	}}
	models := newFake(fakeOptions{respond: func(messages []JsonObject, _ int) fakeResponse {
		last := lastMessage(messages)
		if str(last, "role") == "user" {
			args := JsonObject{"n": 21, "tags": []any{"a"}}
			if last["content"] == "bad" {
				args = JsonObject{"n": "x"}
			}
			return fakeResponse{toolCalls: []fakeToolCall{{name: "typed", arguments: args}}}
		}
		return textResponse("ok")
	}})
	env := openEnv(t, openOptions{models: models, tools: []*ToolDeclaration{tool}})
	env.wait(env.send(env.root, "good"))
	first := toolResultEntry(t, env)
	if !strings.Contains(contentOf(&first), "n=42 tags=1") {
		t.Fatalf("typed args: %s", contentOf(&first))
	}
	env.wait(env.send(env.root, "bad"))
	var bad Entry
	for _, entry := range env.entries() {
		if entry.Kind == "pi.tool_result" {
			bad = entry
		}
	}
	if !strings.Contains(contentOf(&bad), "invalid arguments:") {
		t.Fatal("missing validation error")
	}
	equal(t, diagnosticCode(bad), "invalid_arguments", "diagnostic")
	equal(t, calls, 1, "bad call not invoked")
	var lastTask Task
	for _, task := range env.tasks() {
		if task.Kind == "pi.tool" {
			lastTask = task
		}
	}
	equal(t, asObject(lastTask.Outcome.Result)["entry"], float64(bad.Id), "synthetic entry linked")
}

func TestToolStreamDefaultsToBoundedTailAndThrottles(t *testing.T) {
	tool := &ToolDeclaration{Name: "stream", Parameters: toolSchema, Output: &ToolOutput{MaxBytes: new(100), Retain: "tail"}, Execute: func(_ context.Context, _ JsonValue, api *ToolApi) (ToolResult, error) {
		for i := range 50 {
			if err := api.Stream(fmt.Appendf(nil, "chunk%d ", i)); err != nil {
				return ToolResult{}, err
			}
		}
		return ToolResult{}, nil
	}}
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{tool}})
	watch := collectWatch(t, env.root)
	env.wait(env.send(env.root, "tool:stream"))
	result := toolResultEntry(t, env)
	text := str(asObject(arr(result.Model[0], "content")[0]), "text")
	if len(text) > 100 || !strings.HasSuffix(text, "chunk49 ") {
		t.Fatalf("tail: %q", text)
	}
	equal(t, diagnosticCode(result), "truncated", "diagnostic")
	flushes := 0
	for _, envelope := range watch.Envelopes() {
		for _, op := range envelope.Ops {
			if op.Verb() == "s" {
				found := false
				for _, part := range op.Path() {
					if part == "output" {
						found = true
					}
				}
				if found {
					flushes++
					break
				}
			}
		}
	}
	if flushes > 2 {
		t.Fatalf("unthrottled output: %d", flushes)
	}
}

func TestToolReturnedOutputCannotEscapeKernelBudget(t *testing.T) {
	tool := newTool("big", toolOptions{output: &ToolOutput{MaxBytes: new(10)}, result: &ToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Repeat("x", 1000)}}}})
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{tool.ToolDeclaration}})
	env.wait(env.send(env.root, "tool:big"))
	result := toolResultEntry(t, env)
	text := str(asObject(arr(result.Model[0], "content")[0]), "text")
	equal(t, len(text), 10, "bounded")
	equal(t, obj(result.Data, "truncated")["bytes"], 990, "dropped bytes")
}
