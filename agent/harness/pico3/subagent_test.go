package pico3

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Upstream: packages/agent/test/harness/pico3/subagent.test.ts, all six cases.
func subagentDelegate() *ToolDeclaration {
	return &ToolDeclaration{Name: "delegate", Parameters: toolSchema, Execute: func(ctx context.Context, args JsonValue, api *ToolApi) (ToolResult, error) {
		child, err := api.Conversation(ctx, OwnedConversationSpec{Rewindable: JsonObject{"model": testModel, "selectedTools": []any{}, "plugins": JsonObject{"forged": JsonObject{"child": true}}}, Sticky: JsonObject{"plugins": JsonObject{"forged": JsonObject{"child": true}}}})
		if err != nil {
			return ToolResult{}, err
		}
		sent, err := child.Send(ctx, SendInput{Content: "child task " + str(asObject(args), "v")})
		if err != nil {
			return ToolResult{}, err
		}
		settled, err := sent.Wait(ctx)
		if err != nil {
			return ToolResult{}, err
		}
		details := JsonObject{"child": child.Id}
		if settled.Answer != nil {
			details["answer"] = *settled.Answer
		}
		return ToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("child %d %s", child.Id, settled.Status)}}, Details: details}, nil
	}}
}

func subagentRequest(messages []JsonObject) bool {
	last := lastMessage(messages)
	return str(last, "role") == "user" && strings.HasPrefix(str(last, "content"), "child task")
}
func subagentResponse(text string) func([]JsonObject, int) fakeResponse {
	return func(messages []JsonObject, call int) fakeResponse {
		if subagentRequest(messages) {
			return textResponse(text)
		}
		return echoScript(messages, call)
	}
}

func subagentResult(t *testing.T, env *testEnv) (*Entry, Id) {
	t.Helper()
	for _, entry := range env.entries() {
		if entry.Kind == "pi.tool_result" {
			return &entry, Id(numberOr(asObject(entry.Data["details"])["child"], 0))
		}
	}
	t.Fatal("no delegate tool result")
	return nil, 0
}

func TestSubagentOwnedConversationAndIsolatedConfiguration(t *testing.T) {
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{subagentDelegate()}, models: newFake(fakeOptions{respond: subagentResponse("child says hi")})})
	input := env.wait(env.send(env.root, "tool:delegate"))
	env.idle()
	equal(t, input.Status, InputDone, "parent continues")
	result, child := subagentResult(t, env)
	answer := Id(numberOr(asObject(result.Data["details"])["answer"], 0))
	if !strings.Contains(contentOf(result), "done") {
		t.Fatal(contentOf(result))
	}
	conversation := must(HostCommit(bg, env.root, func(_ context.Context, tx *Tx) (*Conversation, error) { return tx.Conversation(child) }))
	var owner *Task
	for _, task := range env.tasks(1) {
		if task.Kind == "pi.tool" {
			owner = &task
		}
	}
	if owner == nil || conversation.Owner == nil {
		t.Fatal("missing child owner")
	}
	equal(t, *conversation.Owner, owner.Id, "owner task")
	equal(t, owner.Owns, []Id{child}, "owned conversations")
	equal(t, conversation.Parent, (*ConversationParent)(nil), "not a fork")
	entries := env.entries(child)
	equal(t, entryKinds(entries), "user system assistant", "child transcript")
	var answered *Entry
	for _, entry := range entries {
		if entry.Id == answer {
			answered = &entry
		}
	}
	if answered == nil {
		t.Fatal("answer entry missing")
	}
	equal(t, answered.Model[0]["content"], []any{JsonObject{"type": "text", "text": "child says hi "}}, "child answer")
	handle := must(env.h.Conversation(bg, child))
	rewindable := must(handle.Rewindable(bg))
	equal(t, rewindable["plugins"], JsonObject{}, "forged rewindable plugins stripped")
	equal(t, must(handle.Sticky(bg))["plugins"], JsonObject{}, "forged sticky plugins stripped")
	equal(t, rewindable["selectedTools"], []any{}, "isolated tools")
}

func TestSubagentConversationAbortReachesOwnedChild(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{subagentDelegate()}, models: newFake(fakeOptions{respond: subagentResponse("slow child"), gate: gate, gateWhen: subagentRequest})})
	input := env.send(env.root, "tool:delegate")
	gate.Arrivals(t, 1)
	var child Id
	for _, task := range env.liveTasks() {
		if task.ConversationId != 1 {
			child = task.ConversationId
		}
	}
	if child == 0 {
		t.Fatal("no owned child")
	}
	background := must(HostCommit(bg, env.root, func(_ context.Context, tx *Tx) (TaskRef, error) {
		return tx.CreateTask(Kinds.Plugin, JsonObject{"handler": "x", "input": nil}, TaskOptions{ConversationId: &child, Background: true})
	}))
	check(t, env.root.Abort(bg))
	equal(t, env.input(input.Id).Reason, "aborted", "parent input aborted")
	var tool, childGeneration *Task
	for _, task := range env.tasks() {
		if task.Kind == "pi.tool" {
			tool = &task
		}
		if task.Kind == "pi.generation" && task.ConversationId == child {
			childGeneration = &task
		}
	}
	if tool == nil || childGeneration == nil {
		t.Fatal("missing aborted tasks")
	}
	equal(t, tool.Outcome.Status, OutcomeAborted, "owner tool aborts")
	equal(t, childGeneration.Outcome.Status, OutcomeAborted, "owned generation aborts")
	if slices.ContainsFunc(env.entries(child), func(entry Entry) bool { return entry.Kind == "pi.assistant" }) {
		t.Fatal("no token means no aborted assistant entry")
	}
	terminal := env.untilTerminal(background.Id)
	equal(t, str(failureOf(&terminal), "reason"), "missing_handler", "background child not aborted")
	equal(t, len(env.liveTasks()), 0, "all tasks settled")
}

func TestSubagentSubtreeHooksReachChild(t *testing.T) {
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{subagentDelegate()}, models: newFake(fakeOptions{respond: subagentResponse("ok")})})
	namespace := must(env.h.Namespace("test.subtree-hooks", NamespaceDefaults{}, nil))
	var mu sync.Mutex
	var seen, local []Id
	must(env.root.Hooks(namespace, Kinds.Generation, &GenerationHooks{AfterResponse: func(_ context.Context, _ ai.AssistantMessage, info AfterResponseInfo) error {
		mu.Lock()
		seen = append(seen, info.ConversationId)
		mu.Unlock()
		return nil
	}}, true))
	env.wait(env.send(env.root, "tool:delegate"))
	env.idle()
	mu.Lock()
	first := slices.Clone(seen)
	mu.Unlock()
	if !slices.Contains(first, Id(1)) || !slices.ContainsFunc(first, func(id Id) bool { return id != 1 }) {
		t.Fatalf("missing parent/child response: %v", first)
	}
	must(env.root.Hooks(namespace, Kinds.Generation, &GenerationHooks{AfterResponse: func(_ context.Context, _ ai.AssistantMessage, info AfterResponseInfo) error {
		mu.Lock()
		local = append(local, info.ConversationId)
		mu.Unlock()
		return nil
	}}, false))
	env.wait(env.send(env.root, "tool:delegate"))
	env.idle()
	mu.Lock()
	defer mu.Unlock()
	if len(local) == 0 {
		t.Fatal("local hook never ran")
	}
	for _, id := range local {
		equal(t, id, Id(1), "no child without subtree")
	}
}

func TestSubagentReopenedSubtreeUsesTerminalOwnerAncestry(t *testing.T) {
	env := openEnv(t, openOptions{backend: "jsonl", tools: []*ToolDeclaration{subagentDelegate()}, models: newFake(fakeOptions{respond: subagentResponse("ok")})})
	env.wait(env.send(env.root, "tool:delegate"))
	env.idle()
	_, child := subagentResult(t, env)
	if child == 0 {
		t.Fatal("missing child")
	}
	env.crash()
	next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir, tools: []*ToolDeclaration{subagentDelegate()}, models: newFake(fakeOptions{respond: subagentResponse("ok")})})
	namespace := must(next.h.Namespace("test.reopened-subtree-hooks", NamespaceDefaults{}, nil))
	var mu sync.Mutex
	var seen, local []Id
	must(next.root.Hooks(namespace, Kinds.Generation, &GenerationHooks{AfterResponse: func(_ context.Context, _ ai.AssistantMessage, info AfterResponseInfo) error {
		mu.Lock()
		seen = append(seen, info.ConversationId)
		mu.Unlock()
		return nil
	}}, true))
	handle := must(next.h.Conversation(bg, child))
	next.wait(next.send(handle, "child task again"))
	mu.Lock()
	equal(t, slices.Clone(seen), []Id{child}, "terminal owner ancestry restored")
	mu.Unlock()
	must(next.root.Hooks(namespace, Kinds.Generation, &GenerationHooks{AfterResponse: func(_ context.Context, _ ai.AssistantMessage, info AfterResponseInfo) error {
		mu.Lock()
		local = append(local, info.ConversationId)
		mu.Unlock()
		return nil
	}}, false))
	next.wait(next.send(handle, "child task once more"))
	mu.Lock()
	defer mu.Unlock()
	equal(t, len(local), 0, "no subtree flag excludes child")
}

func TestSubagentTaskAbortPreservesQueuedInput(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: echoScript, gate: gate})})
	first := env.send(env.root, "A")
	gate.Arrivals(t, 1)
	follow := env.send(env.root, "F")
	var generation Task
	for _, task := range env.tasks() {
		if task.Kind == "pi.generation" {
			generation = task
		}
	}
	must(env.h.MarkTask(bg, generation.Id))
	env.untilTerminal(generation.Id)
	equal(t, env.input(first.Id).Reason, "aborted", "first aborted")
	equal(t, env.input(follow.Id).Status, InputQueued, "queued input survives task abort")
	gate.Open()
	env.wait(env.send(env.root, "B"))
	env.idle()
	equal(t, env.input(follow.Id).Status, InputDone, "idle send consumes queue")
	var users []string
	for _, entry := range env.entries() {
		if entry.Kind == "pi.user" {
			users = append(users, contentOf(&entry))
		}
	}
	equal(t, users, []string{"A", "F", "B"}, "queued input precedes idle send")
}

func TestSubagentInheritedConversationForksOwnTip(t *testing.T) {
	inheriting := &ToolDeclaration{Name: "inherit", Parameters: toolSchema, Execute: func(ctx context.Context, _ JsonValue, api *ToolApi) (ToolResult, error) {
		child, err := api.Conversation(ctx, OwnedConversationSpec{Inherit: true, Rewindable: JsonObject{"selectedTools": []any{}}})
		if err != nil {
			return ToolResult{}, err
		}
		input, err := child.Send(ctx, SendInput{Content: "child task: what did we say?"})
		if err != nil {
			return ToolResult{}, err
		}
		settled, err := input.Wait(ctx)
		if err != nil {
			return ToolResult{}, err
		}
		return ToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("%d:%s", child.Id, settled.Status)}}, Details: JsonObject{"child": child.Id}}, nil
	}}
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{inheriting}, models: newFake(fakeOptions{respond: subagentResponse("we said hi")})})
	state := must(env.h.Namespace("test.subagent-state", NamespaceDefaults{Rewindable: JsonObject{"marker": ""}}, nil))
	setMarker := func(marker string) {
		_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
			node, err := tx.Plugins(state)
			if err != nil {
				return nil, err
			}
			node.Set("marker", marker)
			return nil, nil
		})
		check(t, err)
	}
	setMarker("before")
	env.wait(env.send(env.root, "hi"))
	setMarker("at-fork")
	env.wait(env.send(env.root, "tool:inherit"))
	env.idle()
	_, child := subagentResult(t, env)
	conversation := must(HostCommit(bg, env.root, func(_ context.Context, tx *Tx) (*Conversation, error) { return tx.Conversation(child) }))
	if conversation.Parent == nil {
		t.Fatal("inherited child lacks parent")
	}
	equal(t, conversation.Parent.ConversationId, Id(1), "fork parent")
	handle := must(env.h.Conversation(bg, child))
	rewindable := must(handle.Rewindable(bg))
	equal(t, asObject(asObject(rewindable["plugins"])["test.subagent-state"])["marker"], "at-fork", "tip configuration inherited")
	equal(t, rewindable["selectedTools"], []any{}, "child tools override inherited")
	view := must(HostCommit(bg, handle, func(_ context.Context, tx *Tx) (ContextView, error) { return tx.Context(child, nil) }))
	kinds := entryKinds(view.Entries)
	if !strings.HasPrefix(kinds, "user system assistant user") || (!strings.HasSuffix(kinds, "user system assistant") && !strings.HasSuffix(kinds, "user assistant")) {
		t.Fatalf("fork context: %s", kinds)
	}
}
