package pico3

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These cases port packages/agent/test/harness/pico3/recovery.test.ts.
// Close joins live invocations without writing terminal outcomes; reopening the
// JSONL directory exercises the same durable phases as upstream's crash helper.
func TestRecoveryGenerationRequesting(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{backend: "jsonl", models: newFake(fakeOptions{respond: echoScript, gate: gate})})
	input := must(env.root.Send(bg, SendInput{Content: "A", RequestId: "r"}))
	env.untilPhase("pi.generation", "requesting")
	gate.Arrivals(t, 1)
	env.crash()
	models := newFake(fakeOptions{respond: echoScript})
	next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir, models: models})
	same := must(next.root.Send(bg, SendInput{Content: "dup", RequestId: "r"}))
	equal(t, same.Id, input.Id, "request id survives reopen")
	equal(t, next.wait(same).Status, InputDone, "input outcome")
	entries := next.entries()
	equal(t, entryKinds(entries), "user system usage assistant", "transcript")
	equal(t, entries[2].Data, JsonObject{"attempt": 1, "error": "interrupted"}, "interrupted usage")
	equal(t, models.Calls(), 1, "one recovered request")
}

func TestRecoveryGenerationPrepared(t *testing.T) {
	gate := &testGate{}
	var calls atomic.Int64
	hooks := &hooksByKind{generation: &GenerationHooks{BeforeRequest: func(ctx context.Context, _ BeforeRequestInput, _ BeforeRequestInfo) (*BeforeRequestInput, error) {
		calls.Add(1)
		return nil, gate.Wait(ctx)
	}}}
	env := openEnv(t, openOptions{backend: "jsonl", hooks: hooks})
	input := env.send(env.root, "A")
	env.untilPhase("pi.generation", "prepared")
	gate.Arrivals(t, 1)
	env.crash()
	gate.Open()
	next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir, hooks: hooks})
	next.idle()
	equal(t, calls.Load(), int64(2), "hook reruns")
	counts := map[string]int{}
	for _, entry := range next.entries() {
		counts[entry.Kind]++
	}
	equal(t, counts["pi.system"], 1, "prepared system is retained")
	equal(t, counts["pi.usage"], 0, "prepared is not in-flight")
	equal(t, next.input(input.Id).Status, InputDone, "input outcome")
}

func TestRecoveryGenerationRetrying(t *testing.T) {
	models := newFake(fakeOptions{respond: func(messages []JsonObject, call int) fakeResponse {
		if call == 0 {
			return errorResponse("overloaded")
		}
		return echoScript(messages, call)
	}})
	env := openEnv(t, openOptions{backend: "jsonl", models: models, root: &RootSpec{Rewindable: JsonObject{"model": testModel}, Sticky: JsonObject{"retry": JsonObject{"enabled": true, "maxRetries": 3, "baseDelayMs": 300}}}})
	input := env.send(env.root, "A")
	task := env.untilPhase("pi.generation", "retrying")
	deadline := numberOr(task.Checkpoint["untilMs"], 0)
	env.crash()
	nextModels := newFake(fakeOptions{respond: echoScript})
	next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir, models: nextModels})
	next.idle()
	if float64(time.Now().UnixMilli()) < deadline {
		t.Fatal("did not wait out durable deadline")
	}
	equal(t, next.input(input.Id).Status, InputDone, "input outcome")
	equal(t, nextModels.Calls(), 1, "retry count")
	equal(t, entryKinds(next.entries()), "user system usage assistant", "transcript")
}

func TestRecoveryToolStarted(t *testing.T) {
	for _, replay := range []string{"safe", "unsafe"} {
		t.Run(replay, func(t *testing.T) {
			gate := &testGate{}
			tool := newTool("x", toolOptions{replay: replay, gate: gate})
			env := openEnv(t, openOptions{backend: "jsonl", tools: []*ToolDeclaration{tool.ToolDeclaration}})
			input := env.send(env.root, "tool:x")
			env.untilPhase("pi.tool", "started")
			gate.Arrivals(t, 1)
			env.crash()
			replacement := newTool("x", toolOptions{replay: replay})
			next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir, tools: []*ToolDeclaration{replacement.ToolDeclaration}})
			next.idle()
			equal(t, next.input(input.Id).Status, InputDone, "input outcome")
			var result *Entry
			for _, entry := range next.entries() {
				if entry.Kind == "pi.tool_result" {
					result = &entry
				}
			}
			want, calls := "x(x)", int64(1)
			if replay == "unsafe" {
				want, calls = "interrupted", 0
			}
			equal(t, replacement.calls.Load(), calls, "replay calls")
			if !strings.Contains(contentOf(result), want) {
				t.Fatalf("tool result %s lacks %q", contentOf(result), want)
			}
			equal(t, entryKinds(next.entries()), "user system assistant tool_result assistant", "transcript")
		})
	}
}

func TestRecoveryBeforeTool(t *testing.T) {
	gate := &testGate{}
	var calls atomic.Int64
	hooks := &hooksByKind{tool: &ToolHooks{BeforeTool: func(ctx context.Context, _ JsonObject, _ *BeforeToolApi) (*BeforeToolResult, error) {
		calls.Add(1)
		return nil, gate.Wait(ctx)
	}}}
	tool := newTool("x", toolOptions{})
	env := openEnv(t, openOptions{backend: "jsonl", hooks: hooks, tools: []*ToolDeclaration{tool.ToolDeclaration}})
	input := env.send(env.root, "tool:x")
	env.untilPhase("pi.tool", "")
	gate.Arrivals(t, 1)
	env.crash()
	gate.Open()
	replacement := newTool("x", toolOptions{})
	next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir, hooks: hooks, tools: []*ToolDeclaration{replacement.ToolDeclaration}})
	next.idle()
	equal(t, calls.Load(), int64(2), "hook reruns")
	equal(t, replacement.calls.Load(), int64(1), "tool runs once")
	equal(t, next.input(input.Id).Status, InputDone, "input outcome")
}

func TestRecoveryPostTools(t *testing.T) {
	gate := &testGate{}
	var calls atomic.Int64
	hooks := &hooksByKind{postTools: &PostToolsHooks{AfterTools: func(ctx context.Context, _ Id, _ []Id, _ HookApi) error { calls.Add(1); return gate.Wait(ctx) }}}
	env := openEnv(t, openOptions{backend: "jsonl", hooks: hooks, tools: []*ToolDeclaration{newTool("x", toolOptions{}).ToolDeclaration}})
	input := env.send(env.root, "tool:x")
	gate.Arrivals(t, 1)
	live := env.liveTasks()
	equal(t, len(live), 1, "one live task")
	equal(t, live[0].Kind, "pi.post_tools", "posttools is live")
	env.crash()
	// Keep the recovery hook blocked while inspecting the reopened durable set.
	next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir, hooks: hooks, tools: []*ToolDeclaration{newTool("x", toolOptions{}).ToolDeclaration}})
	gate.Arrivals(t, 2)
	var atOpen []string
	for _, task := range next.tasks() {
		if task.Kind != "pi.generation" {
			atOpen = append(atOpen, strings.TrimPrefix(task.Kind, "pi.")+":"+string(task.Status))
		}
	}
	slices.Sort(atOpen)
	equal(t, atOpen, []string{"post_tools:running", "tool:terminal"}, "retained dependent tasks")
	gate.Open()
	next.idle()
	equal(t, calls.Load(), int64(2), "hook reruns")
	equal(t, next.input(input.Id).Status, InputDone, "input outcome")
	equal(t, entryKinds(next.entries()), "user system assistant tool_result assistant", "transcript")
	var post Id
	for _, task := range next.tasks() {
		if task.Kind == "pi.post_tools" {
			post = task.Id
		}
	}
	continuations := 0
	for _, task := range next.tasks() {
		if task.Kind == "pi.generation" && task.Id > post {
			continuations++
		}
	}
	equal(t, continuations, 1, "one continuation")
}

func TestRecoveryCollapseSummarizing(t *testing.T) {
	gate := &testGate{}
	respond := func(messages []JsonObject, call int) fakeResponse {
		for _, message := range messages {
			if str(message, "role") == "user" && strings.HasPrefix(fmt.Sprint(message["content"]), "Summarize") {
				return textResponse("the summary")
			}
		}
		return echoScript(messages, call)
	}
	env := openEnv(t, openOptions{backend: "jsonl", models: newFake(fakeOptions{respond: respond, gate: gate}), root: &RootSpec{Rewindable: JsonObject{"model": testModel, "keepRecent": 10}, Sticky: JsonObject{"retry": JsonObject{"enabled": true, "maxRetries": 3, "baseDelayMs": 1}}}})
	gate.Open()
	for _, text := range []string{"one", "two", "three"} {
		env.wait(env.send(env.root, text))
	}
	gate.Close()
	id := must(env.root.Collapse(bg, nil))
	env.untilPhase("pi.collapse", "summarizing")
	gate.Arrivals(t, 4)
	env.crash()
	next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir, models: newFake(fakeOptions{respond: respond})})
	task := next.untilTerminal(id)
	equal(t, task.Outcome.Status, OutcomeCompleted, "collapse outcome")
	var summary *Entry
	for _, entry := range next.entries() {
		if entry.Kind == "pi.summary" {
			summary = &entry
		}
	}
	equal(t, contentOf(summary), "the summary ", "summary content")
	if summary == nil || summary.Head == nil || *summary.Head <= 0 {
		t.Fatal("summary has no retained head")
	}
}

func TestRecoveryJobUnknown(t *testing.T) {
	for _, rerun := range []bool{true, false} {
		t.Run(fmt.Sprint(rerun), func(t *testing.T) {
			host := newFakeHost()
			env := openEnv(t, openOptions{backend: "jsonl", processHost: host})
			ref := createTestTask(t, env, Kinds.Job, JsonObject{"command": "x", "args": []any{}, "cwd": "/", "notify": rerun, "rerun": rerun})
			task := env.untilPhase("pi.job", "running")
			key := str(task.Checkpoint, "key")
			equal(t, host.Starts(), 1, "initial starts")
			env.crash()
			replacement := newFakeHost()
			next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir, processHost: replacement})
			if !rerun {
				equal(t, str(failureOfTask(next.untilTerminal(ref.Id)), "reason"), "interrupted", "unknown process failure")
				return
			}
			next.untilPhase("pi.job", "running")
			deadline := time.Now().Add(time.Second)
			for replacement.Starts() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			equal(t, replacement.Starts(), 1, "same process restarted")
			equal(t, must(replacement.Status(bg, key)).Status, "running", "same key")
			replacement.exit(key, 0)
			terminal := next.untilTerminal(ref.Id)
			equal(t, terminal.Outcome.Status, OutcomeCompleted, "job outcome")
			equal(t, terminal.Outcome.Result, JsonObject{"exitCode": 0, "occurrences": 1, "stdout": "out " + key, "stderr": ""}, "job completion")
			equal(t, entryKinds(next.entries()), "notice", "idle notification")
		})
	}
}

func failureOfTask(task Task) JsonObject { return failureOf(&task) }

func TestRecoveryMarkedTask(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{backend: "jsonl", models: newFake(fakeOptions{respond: echoScript, gate: gate})})
	input := env.send(env.root, "A")
	task := env.untilPhase("pi.generation", "requesting")
	gate.Arrivals(t, 1)
	must(env.h.MarkTask(bg, task.Id))
	env.crash()
	models := newFake(fakeOptions{respond: echoScript})
	next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir, models: models})
	equal(t, next.untilTerminal(task.Id).Outcome.Status, OutcomeAborted, "marked task aborts")
	equal(t, models.Calls(), 0, "no recovered request")
	equal(t, next.input(input.Id).Reason, "aborted", "input aborted")
}

func TestRecoveryMemoryMatchesJSONL(t *testing.T) {
	var results []string
	for _, backend := range []string{"memory", "jsonl"} {
		env := openEnv(t, openOptions{backend: backend, tools: []*ToolDeclaration{newTool("a", toolOptions{}).ToolDeclaration}})
		for _, text := range []string{"tool:a", "plain", "tool:a"} {
			env.wait(env.send(env.root, text))
		}
		env.idle()
		var outcomes []string
		for _, task := range env.tasks() {
			outcomes = append(outcomes, task.Kind+":"+string(task.Outcome.Status))
		}
		results = append(results, entryKinds(env.entries())+"|"+strings.Join(outcomes, ","))
		env.close()
	}
	equal(t, results[0], results[1], "memory/jsonl transcript and outcomes")
}
