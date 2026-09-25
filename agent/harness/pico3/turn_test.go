package pico3

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

var bg = context.Background()

// must returns value or panics with err, failing the running test.
func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func equal(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(normalizeNumbers(got), normalizeNumbers(want)) {
		t.Fatalf("%s:\n got: %#v\nwant: %#v", what, got, want)
	}
}

func (env *testEnv) context(conversationId Id) ContextView {
	env.t.Helper()
	return must(HostCommit(bg, env.root, func(_ context.Context, tx *Tx) (ContextView, error) {
		return tx.Context(conversationId, nil)
	}))
}

func (env *testEnv) sticky() JsonObject {
	env.t.Helper()
	return must(env.root.Sticky(bg))
}

func (env *testEnv) rewindable() JsonObject {
	env.t.Helper()
	return must(env.root.Rewindable(bg))
}

func messageRoles(messages []JsonObject) []string {
	roles := make([]string, len(messages))
	for index, message := range messages {
		roles[index] = str(message, "role")
	}
	return roles
}

func inboxModes(sticky JsonObject) []string {
	var modes []string
	for _, item := range arr(sticky, "inbox") {
		modes = append(modes, str(asObject(item), "mode"))
	}
	return modes
}

func tasksOfKind(tasks []Task, kind string) []Task {
	return slices.DeleteFunc(slices.Clone(tasks), func(task Task) bool { return task.Kind != kind })
}

func firstOfKind(t *testing.T, tasks []Task, kind string) *Task {
	t.Helper()
	for index := range tasks {
		if tasks[index].Kind == kind {
			return &tasks[index]
		}
	}
	t.Fatalf("no %s task", kind)
	return nil
}

func inputIds(task Task) []Id { return idList(asObject(task.Input)["inputs"]) }

func noticeEntry(text string) NewEntry {
	return NewEntry{Kind: "pi.notice", Model: []JsonObject{{"role": "user", "content": text, "timestamp": 0}}}
}

func TestOneTurnWithTwoToolsOnBothBackends(t *testing.T) {
	for _, backend := range []string{"memory", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			a, b := newTool("a", toolOptions{}), newTool("b", toolOptions{})
			env := openEnv(t, openOptions{backend: backend, tools: []*ToolDeclaration{a.ToolDeclaration, b.ToolDeclaration}})
			settled := env.wait(env.send(env.root, "tool:a,b"))
			check(t, env.root.WaitForIdle(bg))
			equal(t, settled.Status, "done", "status")
			equal(t, entryKinds(env.entries()), "user system assistant tool_result tool_result assistant", "entries")
			equal(t, []int64{a.calls.Load(), b.calls.Load()}, []int64{1, 1}, "tool calls")
			var summary []string
			for _, task := range env.tasks() {
				summary = append(summary, fmt.Sprintf("%s:%s:%s", task.Kind, task.Status, task.Outcome.Status))
			}
			slices.Sort(summary)
			equal(t, summary, []string{"pi.generation:terminal:completed", "pi.generation:terminal:completed", "pi.post_tools:terminal:completed", "pi.tool:terminal:completed", "pi.tool:terminal:completed"}, "tasks")
			for _, entry := range env.entries() {
				if entry.Id == *settled.Answer {
					equal(t, contentOf(&entry), `[{"text":"after tools ","type":"text"}]`, "answer content")
				}
			}
			equal(t, messageRoles(env.context(1).Messages), []string{"user", "system", "assistant", "toolResult", "toolResult", "assistant"}, "context roles")
			sticky := env.sticky()
			equal(t, sticky["turn"], JsonObject{"tools": []any{}}, "turn")
			equal(t, sticky["tasks"], JsonObject{}, "tasks slot")
		})
	}
}

func TestRequestIdDedupReturnsTheOriginalInput(t *testing.T) {
	env := openEnv(t, openOptions{})
	first := must(env.root.Send(bg, SendInput{Content: "hi", RequestId: "k"}))
	second := must(env.root.Send(bg, SendInput{Content: "other", RequestId: "k"}))
	equal(t, first.Id, second.Id, "input id")
	env.wait(first)
	users := 0
	for _, entry := range env.entries() {
		if entry.Kind == "pi.user" {
			users++
		}
	}
	equal(t, users, 1, "user entries")
}

func TestWhenBusyFollowUpSteerAndReject(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: echoScript, gate: gate})})
	first := env.send(env.root, "A")
	gate.Arrivals(t, 1)
	followUp := env.send(env.root, "F")
	steer := must(env.root.Send(bg, SendInput{Content: "S", WhenBusy: "steer"}))
	if _, err := env.root.Send(bg, SendInput{Content: "R", WhenBusy: "reject"}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "busy") {
		t.Fatalf("reject err = %v", err)
	}
	equal(t, env.input(followUp.Id).Status, "queued", "followUp")
	equal(t, env.input(steer.Id).Status, "queued", "steer")
	equal(t, inboxModes(env.sticky()), []string{"followUp", "steer"}, "inbox")
	gate.Open()
	env.wait(first)
	check(t, env.root.WaitForIdle(bg))
	equal(t, env.input(followUp.Id).Status, "done", "followUp done")
	equal(t, env.input(steer.Id).Status, "done", "steer done")
	equal(t, entryKinds(env.entries()), "user system assistant user user assistant", "entries")
}

func TestSteerJoinsActiveGroupAtPostToolsBoundary(t *testing.T) {
	gate := &testGate{}
	tool := newTool("a", toolOptions{gate: gate})
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{tool.ToolDeclaration}})
	first := env.send(env.root, "tool:a")
	gate.Arrivals(t, 1)
	steer := must(env.root.Send(bg, SendInput{Content: "S", WhenBusy: "steer"}))
	followUp := env.send(env.root, "F")
	gate.Open()
	env.wait(first)
	check(t, env.root.WaitForIdle(bg))
	equal(t, entryKinds(env.entries()), "user system assistant tool_result user assistant user assistant", "entries")
	steerInput, followInput := env.input(steer.Id), env.input(followUp.Id)
	equal(t, []string{steerInput.Status, followInput.Status}, []string{"done", "done"}, "statuses")
	if *steerInput.Entry >= *followInput.Entry {
		t.Fatalf("steer entry %d not before followUp %d", *steerInput.Entry, *followInput.Entry)
	}
	equal(t, *env.input(first.Id).Answer, *steerInput.Answer, "shared answer")
	if *steerInput.Answer == *followInput.Answer {
		t.Fatal("followUp shares the steer answer")
	}
}

func TestFollowUpModesOneAtATimeAndAll(t *testing.T) {
	for _, mode := range []string{"one-at-a-time", "all"} {
		t.Run(mode, func(t *testing.T) {
			gate := &testGate{}
			env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: echoScript, gate: gate}), root: &RootSpec{Rewindable: JsonObject{"model": testModel}, Sticky: JsonObject{"followUpMode": mode}}})
			first := env.send(env.root, "A")
			gate.Arrivals(t, 1)
			f1, f2 := env.send(env.root, "F1"), env.send(env.root, "F2")
			gate.Open()
			env.wait(first)
			check(t, env.root.WaitForIdle(bg))
			generations := tasksOfKind(env.tasks(), "pi.generation")
			if mode == "all" {
				equal(t, inputIds(generations[1]), []Id{f1.Id, f2.Id}, "second group")
			} else {
				equal(t, inputIds(generations[1]), []Id{f1.Id}, "second group")
				equal(t, inputIds(generations[2]), []Id{f2.Id}, "third group")
			}
			equal(t, env.input(f2.Id).Status, "done", "f2")
		})
	}
}

func TestWriteAppendedIdleQueuedBusyPlacedAtBoundary(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: echoScript, gate: gate})})
	idle := must(env.root.Write(bg, noticeEntry("idle note")))
	equal(t, env.input(idle).Status, "done", "idle write")
	first := env.send(env.root, "A")
	gate.Arrivals(t, 1)
	busy := must(env.root.Write(bg, noticeEntry("busy note")))
	equal(t, env.input(busy).Status, "queued", "busy write")
	gate.Open()
	env.wait(first)
	check(t, env.root.WaitForIdle(bg))
	placed := env.input(busy)
	equal(t, placed.Status, "done", "placed write")
	if placed.Answer != nil {
		t.Fatalf("write answered by %d", *placed.Answer)
	}
	equal(t, entryKinds(env.entries()), "notice user system assistant notice", "entries")
}

func TestInputHandleAbort(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: echoScript, gate: gate})})
	first := env.send(env.root, "A")
	gate.Arrivals(t, 1)
	followUp := env.send(env.root, "F")
	equal(t, must(followUp.Abort(bg)), "aborted", "queued abort")
	equal(t, env.input(followUp.Id).Status, "unanswered", "aborted status")
	equal(t, len(arr(env.sticky(), "inbox")), 0, "inbox")
	equal(t, must(first.Abort(bg)), "already_placed", "placed abort")
	if env.input(99999) != nil {
		t.Fatal("unknown input found")
	}
	gate.Open()
	env.wait(first)
}

func TestResetWhileBusyStalesEarlierQueuedInput(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: echoScript, gate: gate})})
	first := env.send(env.root, "A")
	gate.Arrivals(t, 1)
	b1, b2 := env.send(env.root, "B1"), env.send(env.root, "B2")
	check(t, env.root.Reset(bg, nil))
	after := env.send(env.root, "C")
	gate.Open()
	env.wait(first)
	env.wait(after)
	check(t, env.root.WaitForIdle(bg))
	equal(t, []string{env.input(b1.Id).Reason, env.input(b2.Id).Reason}, []string{"stale", "stale"}, "stale reasons")
	equal(t, env.input(after.Id).Status, "done", "fresh input")
	equal(t, entryKinds(env.context(1).Entries), "reset* user system assistant", "context")
}

func TestHandoffControlEndsTurnWithHead(t *testing.T) {
	gate := &testGate{}
	hand := newTool("hand", toolOptions{gate: gate, result: &ToolResult{Control: &ToolControl{Handoff: new("fresh")}}})
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{hand.ToolDeclaration}})
	first := env.send(env.root, "tool:hand")
	gate.Arrivals(t, 1)
	followUp := env.send(env.root, "F")
	gate.Open()
	env.wait(first)
	env.wait(followUp)
	check(t, env.root.WaitForIdle(bg))
	post := firstOfKind(t, env.tasks(), "pi.post_tools")
	equal(t, resultOf(post)["ended"], "handoff", "ended")
	settled := env.input(first.Id)
	equal(t, settled.Status, "done", "status")
	for _, entry := range env.entries() {
		if entry.Kind == "pi.assistant" {
			equal(t, *settled.Answer, entry.Id, "answer")
			break
		}
	}
	equal(t, entryKinds(env.context(1).Entries), "handoff* user system assistant", "context")
}

func TestTerminateControlEndsTurnWithoutHead(t *testing.T) {
	terminate := newTool("t", toolOptions{result: &ToolResult{Control: &ToolControl{Terminate: true}}})
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{terminate.ToolDeclaration}})
	env.wait(env.send(env.root, "tool:t"))
	check(t, env.root.WaitForIdle(bg))
	equal(t, entryKinds(env.entries()), "user system assistant tool_result", "entries")
	equal(t, len(tasksOfKind(env.tasks(), "pi.generation")), 1, "generations")
}

func TestAddToolsControlExtendsSelectedTools(t *testing.T) {
	adder := newTool("t", toolOptions{result: &ToolResult{Control: &ToolControl{AddTools: []string{"extra"}}}})
	extra := newTool("extra", toolOptions{})
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{adder.ToolDeclaration, extra.ToolDeclaration}, root: &RootSpec{Rewindable: JsonObject{"model": testModel, "selectedTools": []any{"t"}}}})
	env.wait(env.send(env.root, "tool:t"))
	equal(t, env.rewindable()["selectedTools"], []any{"t", "extra"}, "selectedTools")
	var systems []Entry
	for _, entry := range env.entries() {
		if entry.Kind == "pi.system" {
			systems = append(systems, entry)
		}
	}
	equal(t, len(systems), 2, "system entries")
	var added []string
	for _, tool := range arr(systems[1].Model[0], "toolsAdded") {
		added = append(added, str(asObject(tool), "name"))
	}
	equal(t, added, []string{"extra"}, "toolsAdded")
	if _, present := systems[1].Model[0]["toolsRemoved"]; present {
		t.Fatal("toolsRemoved present")
	}
	var effective []string
	for _, tool := range EffectiveTools(env.context(1).Messages) {
		effective = append(effective, str(tool, "name"))
	}
	equal(t, effective, []string{"t", "extra"}, "effective tools")
}

func retryRoot(enabled bool, maxRetries int) *RootSpec {
	return &RootSpec{Rewindable: JsonObject{"model": testModel}, Sticky: JsonObject{"retry": JsonObject{"enabled": enabled, "maxRetries": maxRetries, "baseDelayMs": 1}}}
}

func TestRetryableProviderErrorRecordsUsageAndRetries(t *testing.T) {
	models := newFake(fakeOptions{respond: func(messages []JsonObject, call int) fakeResponse {
		if call == 0 {
			return errorResponse("overloaded")
		}
		return echoScript(messages, call)
	}})
	env := openEnv(t, openOptions{models: models, root: retryRoot(true, 3)})
	settled := env.wait(env.send(env.root, "A"))
	equal(t, settled.Status, "done", "status")
	entries := env.entries()
	equal(t, entryKinds(entries), "user system usage assistant", "entries")
	equal(t, models.Calls(), 2, "calls")
	equal(t, entries[2].Data["attempt"], 1, "attempt")
}

func TestRetriesExhaustedFailsWithDisplayOnlyAssistant(t *testing.T) {
	models := newFake(fakeOptions{respond: func([]JsonObject, int) fakeResponse { return errorResponse("overloaded") }})
	env := openEnv(t, openOptions{models: models, root: retryRoot(true, 1)})
	settled := env.wait(env.send(env.root, "A"))
	equal(t, []string{settled.Status, settled.Reason}, []string{"unanswered", "failed"}, "input")
	generation := firstOfKind(t, env.tasks(), "pi.generation")
	equal(t, failureOf(generation)["reason"], "retries_exhausted", "failure")
	equal(t, models.Calls(), 2, "calls")
	entries := env.entries()
	equal(t, entryKinds(entries), "user system usage assistant", "entries")
	for _, message := range env.context(1).Messages {
		if str(message, "role") == "assistant" {
			t.Fatal("display-only assistant entered context")
		}
	}
	shown := entries[3]
	if shown.Model != nil {
		t.Fatal("display assistant has a model")
	}
	equal(t, shown.Data["reason"], "error", "display reason")
}

func TestRetryDisabledFailsProviderImmediately(t *testing.T) {
	models := newFake(fakeOptions{respond: func([]JsonObject, int) fakeResponse { return errorResponse("boom") }})
	env := openEnv(t, openOptions{models: models, root: retryRoot(false, 0)})
	env.wait(env.send(env.root, "A"))
	equal(t, failureOf(firstOfKind(t, env.tasks(), "pi.generation"))["reason"], "provider", "failure")
	equal(t, models.Calls(), 1, "calls")
}

func TestNoModelFailsWithoutProviderCall(t *testing.T) {
	models := newFake(fakeOptions{respond: echoScript})
	env := openEnv(t, openOptions{models: models, root: &RootSpec{Rewindable: JsonObject{}}})
	env.wait(env.send(env.root, "A"))
	equal(t, failureOf(firstOfKind(t, env.tasks(), "pi.generation"))["reason"], "no_model", "failure")
	equal(t, models.Calls(), 0, "calls")
}

func TestYieldContinuationKeepsTheInputGroup(t *testing.T) {
	yielded := 0
	env := openEnv(t, openOptions{hooks: &hooksByKind{generation: &GenerationHooks{OnYield: func(context.Context, ai.AssistantMessage, HookApi) (*YieldResult, error) {
		yielded++
		if yielded == 1 {
			return &YieldResult{Continue: "keep going"}, nil
		}
		return nil, nil
	}}}})
	first := env.send(env.root, "A")
	settled := env.wait(first)
	check(t, env.root.WaitForIdle(bg))
	entries := env.entries()
	equal(t, entryKinds(entries), "user system assistant user assistant", "entries")
	equal(t, entries[3].Data, JsonObject{"continuation": true, "from": entries[2].Id}, "continuation data")
	var groups [][]Id
	for _, generation := range tasksOfKind(env.tasks(), "pi.generation") {
		groups = append(groups, inputIds(generation))
	}
	equal(t, groups, [][]Id{{first.Id}, {first.Id}}, "groups")
	equal(t, *settled.Answer, entries[4].Id, "answer")
}

func TestConversationAbortMidStream(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: func(messages []JsonObject, call int) fakeResponse {
		if call == 0 {
			return textResponse(strings.Repeat("partial ", 100))
		}
		return echoScript(messages, call)
	}, gate: gate, tokenDelayMs: 5})})
	first := env.send(env.root, "A")
	gate.Arrivals(t, 1)
	steer := must(env.root.Send(bg, SendInput{Content: "S", WhenBusy: "steer"}))
	write := must(env.root.Write(bg, noticeEntry("n")))
	gate.Open()
	streamed := false
	for attempt := 0; attempt < 100 && !streamed; attempt++ {
		message := obj(obj(env.sticky(), "turn"), "message")
		for _, part := range arr(message, "content") {
			if text := str(asObject(part), "text"); text != "" {
				streamed = true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !streamed {
		t.Fatal("the abort must happen after streamed content")
	}
	check(t, env.root.Abort(bg))
	equal(t, env.input(first.Id).Reason, "aborted", "first")
	equal(t, env.input(steer.Id).Reason, "aborted", "steer")
	equal(t, env.input(write).Status, "queued", "write")
	equal(t, firstOfKind(t, env.tasks(), "pi.generation").Outcome.Status, "aborted", "generation")
	var partial *Entry
	for _, entry := range env.entries() {
		if entry.Kind == "pi.assistant" && entry.Data["reason"] == "aborted" {
			partial = &entry
		}
	}
	if partial == nil || partial.Model != nil || len(arr(obj(partial.Data, "display"), "content")) == 0 {
		t.Fatalf("partial = %#v", partial)
	}
	equal(t, inboxModes(env.sticky()), []string{"write"}, "inbox")
	env.wait(env.send(env.root, "B"))
	equal(t, entryKinds(env.entries()), "user system assistant notice user assistant", "entries")
}

func TestOverflowCreatesCollapseAndReplacementGeneration(t *testing.T) {
	models := newFake(fakeOptions{respond: func(messages []JsonObject, call int) fakeResponse {
		for _, message := range messages {
			if str(message, "role") == "user" && strings.HasPrefix(fmt.Sprint(message["content"]), "Summarize") {
				return textResponse("summary text")
			}
		}
		return echoScript(messages, call)
	}})
	env := openEnv(t, openOptions{models: models, root: &RootSpec{Rewindable: JsonObject{"model": testModel, "keepRecent": 100}}})
	for _, text := range []string{"one", "two", "three"} {
		env.wait(env.send(env.root, text))
	}
	models.model.Capabilities.ContextWindow = 200
	models.model.Capabilities.MaxOutputTokens = 10
	four := env.send(env.root, "four")
	settled := env.wait(four)
	check(t, env.root.WaitForIdle(bg))
	tasks := env.tasks()
	collapse := firstOfKind(t, tasks, "pi.collapse")
	equal(t, asObject(collapse.Input)["reason"], "overflow", "collapse reason")
	var overflowed, replacement *Task
	for _, generation := range tasksOfKind(tasks, "pi.generation") {
		if generation.Outcome.Status == OutcomeFailed {
			overflowed = &generation
		}
		if slices.Contains(generation.After, collapse.Id) {
			replacement = &generation
		}
	}
	equal(t, failureOf(overflowed)["reason"], "overflow", "overflow failure")
	equal(t, inputIds(*replacement), []Id{four.Id}, "replacement inputs")
	equal(t, settled.Status, "done", "status")
	found := false
	for _, entry := range env.entries() {
		found = found || entry.Kind == "pi.summary"
	}
	if !found {
		t.Fatal("no summary entry")
	}
}
