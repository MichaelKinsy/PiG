package pico3

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestToolMemoConcurrentWritersAndTaskIsolation(t *testing.T) {
	var winners [][]JsonValue
	tool := &ToolDeclaration{Name: "memo", Parameters: toolSchema, Replay: "safe", Execute: func(ctx context.Context, _ JsonValue, api *ToolApi) (ToolResult, error) {
		pair := make([]JsonValue, 2)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i, value := range []string{"first", "second"} {
			wg.Go(func() { pair[i], errs[i] = api.Memo(ctx, "winner", value) })
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				return ToolResult{}, err
			}
		}
		stored, present, err := api.MemoGet(ctx, "winner")
		if err != nil {
			return ToolResult{}, err
		}
		if !present || pair[0] != pair[1] || stored != pair[0] {
			return ToolResult{}, errors.New("memo did not select one durable winner")
		}
		winners = append(winners, pair)
		return ToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "memo won"}}}, nil
	}}
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{tool}})
	for range 2 {
		env.wait(env.send(env.root, "tool:memo"))
	}
	env.idle()
	equal(t, len(winners), 2, "independent turns")
	tasks := obj(env.sticky(), "tasks")
	count := 0
	for _, task := range env.tasks() {
		if task.Kind == "pi.tool" {
			count++
			if _, ok := tasks[fmt.Sprint(task.Id)]; ok {
				t.Fatal("terminal memo retained")
			}
		}
	}
	equal(t, count, 2, "tool tasks")
}

func TestSafeToolMemoSurvivesRecovery(t *testing.T) {
	gate := &testGate{}
	var asks atomic.Int32
	makeTool := func(block bool) *ToolDeclaration {
		return &ToolDeclaration{Name: "recover-memo", Parameters: toolSchema, Replay: "safe", Execute: func(ctx context.Context, _ JsonValue, api *ToolApi) (ToolResult, error) {
			decision, exists, err := api.MemoGet(ctx, "decision")
			if err != nil {
				return ToolResult{}, err
			}
			if !exists {
				asks.Add(1)
				decision, err = api.Memo(ctx, "decision", "approved")
				if err != nil {
					return ToolResult{}, err
				}
				if block {
					if err := gate.Wait(ctx); err != nil {
						return ToolResult{}, err
					}
				}
			}
			return ToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: decision.(string)}}}, nil
		}}
	}
	env := openEnv(t, openOptions{backend: "jsonl", tools: []*ToolDeclaration{makeTool(true)}})
	env.send(env.root, "tool:recover-memo")
	gate.Arrivals(t, 1)
	env.close()
	next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir, tools: []*ToolDeclaration{makeTool(false)}})
	next.idle()
	equal(t, asks.Load(), int32(1), "external decision once")
	if !strings.Contains(contentOf(new(toolResultEntry(t, next))), "approved") {
		t.Fatal("missing durable decision")
	}
}

func TestWaitingHookClearsOnStartAndEmits(t *testing.T) {
	approval, toolGate := &testGate{}, &testGate{}
	tool := newTool("approved", toolOptions{gate: toolGate})
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{tool.ToolDeclaration}})
	ns := must(env.h.Namespace("spec.approval", NamespaceDefaults{}, nil))
	watch := collectWatch(t, env.root)
	off := must(env.h.Hooks(ns, Kinds.Tool, &ToolHooks{BeforeTool: func(ctx context.Context, _ JsonObject, api *BeforeToolApi) (*BeforeToolResult, error) {
		if err := api.Waiting(ctx); err != nil {
			return nil, err
		}
		if err := approval.Wait(ctx); err != nil {
			return nil, err
		}
		if _, err := api.Memo(ctx, "decision", "allow"); err != nil {
			return nil, err
		}
		return nil, api.Emit(ctx, "approved", JsonObject{"by": "spec"})
	}}))
	defer off()
	input := env.send(env.root, "tool:approved")
	approval.Arrivals(t, 1)
	equal(t, asObject(arr(obj(env.sticky(), "turn"), "tools")[0])["waitingOn"], ns.Id, "waiting namespace")
	approval.Open()
	toolGate.Arrivals(t, 1)
	slot := asObject(arr(obj(env.sticky(), "turn"), "tools")[0])
	equal(t, slot["status"], "running", "started")
	if _, ok := slot["waitingOn"]; ok {
		t.Fatal("waitingOn persisted after start")
	}
	toolGate.Open()
	env.wait(input)
	found := false
	for _, envelope := range watch.Envelopes() {
		for _, event := range envelope.Events {
			if event["type"] == "plugin.spec.approval.approved" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("missing namespaced event")
	}
}

func TestWaitingHookThrowClearsWithinFinishedEnvelope(t *testing.T) {
	tool := newTool("blocked", toolOptions{})
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{tool.ToolDeclaration}})
	ns := must(env.h.Namespace("spec.throwing", NamespaceDefaults{}, nil))
	must(env.h.Hooks(ns, Kinds.Tool, &ToolHooks{BeforeTool: func(ctx context.Context, _ JsonObject, api *BeforeToolApi) (*BeforeToolResult, error) {
		if err := api.Waiting(ctx); err != nil {
			return nil, err
		}
		return nil, errors.New("approval service failed")
	}}))
	watch := collectWatch(t, env.root)
	env.wait(env.send(env.root, "tool:blocked"))
	equal(t, tool.calls.Load(), int64(0), "never invoked")
	if !strings.Contains(contentOf(new(toolResultEntry(t, env))), "approval service failed") {
		t.Fatal("missing hook failure")
	}
	found := false
	for _, envelope := range watch.Envelopes() {
		for _, event := range envelope.Events {
			if event["type"] == "tool.finished" {
				found = true
				if !strings.Contains(string(mustJSON(envelope.Ops)), "waitingOn") {
					t.Fatal("waiting clear not atomic with result")
				}
			}
		}
	}
	if !found {
		t.Fatal("missing tool.finished")
	}
	equal(t, len(arr(obj(env.sticky(), "turn"), "tools")), 0, "cleared turn")
}

func TestWaitingHookAbortAndSuspend(t *testing.T) {
	for _, suspend := range []bool{false, true} {
		t.Run(map[bool]string{false: "abort", true: "suspend"}[suspend], func(t *testing.T) {
			gate := &testGate{}
			tool := newTool("waiting", toolOptions{})
			env := openEnv(t, openOptions{backend: "jsonl", tools: []*ToolDeclaration{tool.ToolDeclaration}})
			ns := must(env.h.Namespace("spec.waiting", NamespaceDefaults{}, nil))
			must(env.h.Hooks(ns, Kinds.Tool, &ToolHooks{BeforeTool: func(ctx context.Context, _ JsonObject, api *BeforeToolApi) (*BeforeToolResult, error) {
				if err := api.Waiting(ctx); err != nil {
					return nil, err
				}
				return nil, gate.Wait(ctx)
			}}))
			input := env.send(env.root, "tool:waiting")
			gate.Arrivals(t, 1)
			equal(t, asObject(arr(obj(env.sticky(), "turn"), "tools")[0])["waitingOn"], ns.Id, "waiting")
			task := firstOfKind(t, env.tasks(), "pi.tool")
			if suspend {
				check(t, env.h.Suspend(bg))
				env.closed = true
				storage := must(OpenJsonlStorage(bg, env.dir, JsonlOptions{Fsync: new(false)}))
				defer func() { check(t, storage.Close(bg)) }()
				slots := arr(obj(must(storage.Doc(bg, StickyDoc(1))), "turn"), "tools")
				if len(slots) > 0 {
					if _, ok := asObject(slots[0])["waitingOn"]; ok {
						t.Fatal("suspended hook still waiting")
					}
				}
				equal(t, must(storage.Task(bg, task.Id)).Status, TaskRunning, "durable running")
			} else {
				check(t, env.root.Abort(bg))
				equal(t, env.input(input.Id).Reason, "aborted", "settled abort")
				equal(t, obj(env.sticky(), "turn"), JsonObject{"tools": []any{}}, "empty turn")
			}
		})
	}
}

func TestHookMemoNamesAreNamespaceIsolated(t *testing.T) {
	tool := newTool("approvals", toolOptions{})
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{tool.ToolDeclaration}})
	observed := JsonObject{}
	for _, id := range []string{"spec.a", "spec.b"} {
		ns := must(env.h.Namespace(id, NamespaceDefaults{}, nil))
		must(env.h.Hooks(ns, Kinds.Tool, &ToolHooks{BeforeTool: func(ctx context.Context, _ JsonObject, api *BeforeToolApi) (*BeforeToolResult, error) {
			if _, err := api.Memo(ctx, "decision", id); err != nil {
				return nil, err
			}
			value, exists, err := api.MemoGet(ctx, "decision")
			if err != nil {
				return nil, err
			}
			if !exists {
				return nil, errors.New("memo missing")
			}
			observed[id] = value
			return nil, nil
		}}))
	}
	env.wait(env.send(env.root, "tool:approvals"))
	equal(t, observed, JsonObject{"spec.a": "spec.a", "spec.b": "spec.b"}, "namespace memos")
}
