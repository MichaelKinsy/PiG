package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Pi 1.2.0 harness/tool.ts executeTool and test/harness-nested-tools.test.ts: a tool calls another tool as a nested
// call. It is its own pi.tool task owned by the caller; its result goes to the caller, not the transcript.

// expectLiveEmpty checks that the conversation's pi.live document is the empty object, as Pi's toEqual({}).
func expectLiveEmpty(t *testing.T, harness Harness, conversation durable.ConversationId) {
	t.Helper()
	if live, err := harness.SnapshotErased(testContext, LiveDoc, conversation); err != nil || live.Len() != 0 {
		t.Fatalf("pi.live = %s, %v, want {}", mustJSON(t, live), err)
	}
}

func echoRegistration() *durable.ToolRegistration {
	return tlTool("echo", func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		text, _ := args.(map[string]any)["text"].(string)
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "echo " + text}}}, nil
	})
}

// harness-nested-tools.test.ts:172 it("runs a nested call as its own tool task and returns its result to the caller, not the
// transcript")
func TestANestedCallIsItsOwnToolTaskAndItsResultGoesToTheCaller(t *testing.T) {
	setup := chatSetup(t)
	addTool(t, setup.Registry, echoRegistration())
	var nested durable.NestedToolExecutionResult
	var nestedErr error
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		nested, nestedErr = api.ExecuteTool(ctx, "echo", map[string]any{"text": "a"})
		text, _ := nested.StructuredOutput.(string)
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "got " + text}}}, nil
	}))
	run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()}, nil)
	if nestedErr != nil || run.status != durable.SubmissionDone {
		t.Fatalf("status %s, nested error %v", run.status, nestedErr)
	}
	tasks := tlToolTasks(tlScanTasks(t, run.harness, run.root.Id()))
	if len(tasks) != 2 {
		t.Fatalf("%d tool tasks, want the caller and one nested call", len(tasks))
	}
	parent, child := tasks[0], tasks[1]
	if !nested.HasStructuredOutput || nested.StructuredOutput != "echo a" || nested.IsError || nested.TaskId != child.Id || len(nested.Diagnostics) != 0 || nested.DurationMs == nil {
		t.Fatalf("nested result = %+v, want task %d, structuredOutput \"echo a\", no error", nested, child.Id)
	}
	// One result entry: the model-issued call's, which records nothing of its nested calls.
	results := tlResults(run.entries)
	if len(results) != 1 || results[0].ToolCallID != "c1" || tlResultText(results[0]) != "got echo a" {
		t.Fatalf("result entries = %+v, want [c1 \"got echo a\"]", results)
	}
	if containsKey(mustJSON(t, results[0]), "nestedCalls") {
		t.Fatalf("the result %s has a nestedCalls member", mustJSON(t, results[0]))
	}
	parentInput, err := durable.FromJsonValue[ToolTaskInput](parent.Input)
	if err != nil || parentInput.Kind != "model" || parentInput.CallId != "c1" {
		t.Fatalf("parent input = %+v, %v", parentInput, err)
	}
	if child.Owner == nil || *child.Owner != parent.Id {
		t.Fatalf("child owner = %v, want task %d", child.Owner, parent.Id)
	}
	wantInput := fmt.Sprintf(`{"kind":"nested","parent":%d,"parentCallId":"c1","key":"1","call":{"type":"toolCall","id":"c1/1","name":"echo","arguments":{"text":"a"}}}`, parent.Id)
	if got := mustJSON(t, child.Input); !equalJSONText(t, got, wantInput) {
		t.Fatalf("child input = %s, want %s", got, wantInput)
	}
	// The receipt stays small: the result lived in the caller's documents, which retired with it.
	if got := mustJSON(t, child.State); !equalJSONText(t, got, `{"status":"terminal","outcome":{"status":"completed","result":{"kind":"nested"}}}`) {
		t.Fatalf("child state = %s", got)
	}
	if doc := must(durable.Snapshot[NestedCallState](testContext, run.harness, NestedCallDoc, parent.Id, "1")); doc != nil {
		t.Fatalf("the caller's nested-call document = %+v, want none", doc)
	}
	expectLiveEmpty(t, run.harness, run.root.Id())
	mustClose(t, run.harness)
}

// harness-nested-tools.test.ts:432 it("reports nested calls in pi.live.nestedTools and as tool events with their parent"): the
// pi.live.nestedTools half; the events half is TestNestedCallsAreToolEventsWithTheirParent.
func TestARunningNestedCallIsInTheLiveNestedToolSlots(t *testing.T) {
	setup := chatSetup(t)
	running, release := make(chan struct{}), make(chan struct{})
	addTool(t, setup.Registry, tlTool("slow", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		api.Output("working\n")
		if err := api.Details(ctx, map[string]any{"step": 1.0}); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		close(running)
		<-release
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "slow done"}}}, nil
	}))
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		nested, err := api.ExecuteTool(ctx, "slow", map[string]any{})
		text, _ := nested.StructuredOutput.(string)
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}, err
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	submission := ownSubmit(t, root, ownInput("go"))
	<-running
	live, err := durable.Snapshot[LiveState](testContext, harness, LiveDoc, root.Id())
	if err != nil || live == nil || len(live.NestedTools) != 1 {
		t.Fatalf("live = %+v, %v", live, err)
	}
	slot := live.NestedTools[0]
	if slot.CallId != "c1/1" || slot.ParentCallId != "c1" || slot.Name != "slow" || slot.Status != ToolSlotRunning || slot.ParentTaskId == 0 || slot.TaskId == 0 ||
		slot.Output == nil || *slot.Output != "working\n" || slot.Details == nil || mustJSON(t, *slot.Details) != `{"step":1}` || mustJSON(t, slot.Arguments) != `{}` {
		t.Fatalf("slot = %+v", slot)
	}
	close(release)
	if _, err := submission.Wait(testContext); err != nil {
		t.Fatal(err)
	}
	if err := root.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	expectLiveEmpty(t, harness, root.Id())
	mustClose(t, harness)
}

// harness-nested-tools.test.ts:242 it("runs the tool hooks on nested calls, which see their parent")
func TestToolHooksSeeTheParentOfANestedCall(t *testing.T) {
	setup := chatSetup(t)
	var runs int
	addTool(t, setup.Registry, tlTool("echo", func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		runs++
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "echo"}}}, nil
	}))
	var parents []*ToolParent
	addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{
		BeforeTool: func(_ context.Context, call ToolHookCall, _ HookApi) (*BeforeToolResult, error) {
			parents = append(parents, call.Parent)
			if call.Parent != nil && call.Arguments["text"] == "secret" {
				block := "no secrets"
				return &BeforeToolResult{Block: &block}, nil
			}
			return nil, nil
		},
		AfterTool: func(_ context.Context, call ToolHookCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
			if call.Parent == nil {
				return nil, nil
			}
			result.Output = []ai.ToolResultMessageContent{ai.TextContent{Text: "rewritten"}}
			return &result, nil
		},
	})
	var nested []durable.NestedToolExecutionResult
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		for _, text := range []string{"secret", "public"} {
			result, err := api.ExecuteTool(ctx, "echo", map[string]any{"text": text})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			nested = append(nested, result)
		}
		return durable.ToolExecutionResult{}, nil
	}))
	run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()}, nil)
	parent := tlToolTasks(tlScanTasks(t, run.harness, run.root.Id()))[0]
	if len(parents) != 3 || parents[0] != nil || *parents[1] != (ToolParent{TaskId: parent.Id, CallId: "c1"}) || *parents[2] != *parents[1] {
		t.Fatalf("hook parents = %+v, want none, then the batch call twice", parents)
	}
	if len(nested) != 2 || !nested[0].IsError || len(nested[0].Diagnostics) != 1 || nested[0].Diagnostics[0].Severity != durable.SeverityError || nested[0].Diagnostics[0].Code != "blocked" ||
		nested[0].Diagnostics[0].Message != "Tool call blocked: no secrets" || nested[1].StructuredOutput != "rewritten" || runs != 1 {
		t.Fatalf("nested = %+v, runs %d", nested, runs)
	}
}

// PiG coverage beyond the Pi file: types.ts ToolHookCall = ToolCall & { parent? }: a hook reads the call's `type` ("toolCall")
// for a model-issued and for a nested call.
func TestToolHookCallsCarryTheToolCallType(t *testing.T) {
	setup := chatSetup(t)
	addTool(t, setup.Registry, tlTool("echo", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "echo"}}}, nil
	}))
	var seen []ToolHookCall
	addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{BeforeTool: func(_ context.Context, call ToolHookCall, _ HookApi) (*BeforeToolResult, error) {
		seen = append(seen, call)
		return nil, nil
	}})
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		_, err := api.ExecuteTool(ctx, "echo", map[string]any{})
		return durable.ToolExecutionResult{}, err
	}))
	tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()}, nil)
	if len(seen) != 2 || seen[0].Parent != nil || seen[1].Parent == nil {
		t.Fatalf("hooks saw %d calls: %+v", len(seen), seen)
	}
	for _, call := range seen {
		if call.Type != "toolCall" {
			t.Errorf("call %q: type = %q, want toolCall", call.Name, call.Type)
		}
	}
}

// harness-nested-tools.test.ts:920 it("commits only the status of a nested call made with progress: false, and keeps its output
// in the result")
func TestANestedCallWithoutProgressCommitsOnlyItsStatus(t *testing.T) {
	setup := chatSetup(t)
	running, release := make(chan struct{}), make(chan struct{})
	addTool(t, setup.Registry, tlTool("chatty", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		api.Output("line\n")
		api.Diagnostic(durable.ToolDiagnostic{Severity: durable.SeverityInfo, Message: "note"})
		if err := api.Details(ctx, map[string]any{"step": 1.0}); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		close(running)
		<-release
		return durable.ToolExecutionResult{}, nil
	}))
	var result durable.NestedToolExecutionResult
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		off := false
		var err error
		result, err = api.ExecuteTool(ctx, "chatty", map[string]any{}, durable.NestedCallOptions{Progress: &off})
		return durable.ToolExecutionResult{}, err
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	submission := ownSubmit(t, root, ownInput("go"))
	<-running
	live, err := durable.Snapshot[LiveState](testContext, harness, LiveDoc, root.Id())
	if err != nil || live == nil || len(live.NestedTools) != 1 {
		t.Fatalf("live = %+v, %v", live, err)
	}
	slot := live.NestedTools[0]
	if slot.Status != ToolSlotRunning || slot.Output != nil || slot.Details != nil || len(slot.Diagnostics) != 0 {
		t.Fatalf("slot = %+v, want only its status", slot)
	}
	close(release)
	if _, err := submission.Wait(testContext); err != nil {
		t.Fatal(err)
	}
	if result.StructuredOutput != "line\n" || !result.HasDetails || mustJSON(t, result.Details) != `{"step":1}` || len(result.Diagnostics) != 1 || result.Diagnostics[0].Message != "note" {
		t.Fatalf("result = %+v", result)
	}
	mustClose(t, harness)
}

// harness-nested-tools.test.ts:1009 it("shows a nested call's arguments in its slot: as made, then, unbounded, as it runs with
// them")
func TestANestedCallSlotShowsTheArgumentsItRunsWith(t *testing.T) {
	setup := chatSetup(t)
	hookEntered, hookRelease := make(chan struct{}), make(chan struct{})
	running, release := make(chan struct{}), make(chan struct{})
	large := strings.Repeat("x", 64*1024)
	addTool(t, setup.Registry, tlTool("slow", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		close(running)
		<-release
		return durable.ToolExecutionResult{}, nil
	}))
	addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{BeforeTool: func(_ context.Context, call ToolHookCall, _ HookApi) (*BeforeToolResult, error) {
		if call.Parent == nil {
			return nil, nil
		}
		close(hookEntered)
		<-hookRelease
		return &BeforeToolResult{Arguments: map[string]any{"text": large}}, nil
	}})
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		_, err := api.ExecuteTool(ctx, "slow", map[string]any{"text": "asked"})
		return durable.ToolExecutionResult{}, err
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	stream := must(WatchEvents(testContext, harness, root.Id()))
	var mu sync.Mutex
	var starts []ToolExecutionStartEvent
	var runEnded bool
	stream.Start(func(_ context.Context, batch []AgentEvent) error {
		mu.Lock()
		defer mu.Unlock()
		for _, event := range batch {
			switch typed := event.(type) {
			case ToolExecutionStartEvent:
				starts = append(starts, typed)
			case RunEndEvent:
				runEnded = true
			}
		}
		return nil
	})
	submission := ownSubmit(t, root, ownInput("go"))
	slot := func() NestedToolSlot {
		t.Helper()
		live := must(durable.Snapshot[LiveState](testContext, harness, LiveDoc, root.Id()))
		if live == nil || len(live.NestedTools) == 0 {
			t.Fatalf("live = %+v, want a nested slot", live)
		}
		return live.NestedTools[0]
	}
	<-hookEntered
	if got := slot(); got.Status != ToolSlotPending || mustJSON(t, got.Arguments) != `{"text":"asked"}` {
		t.Fatalf("slot while beforeTool runs = %+v, want pending with the arguments as made", got)
	}
	close(hookRelease)
	<-running
	if got := slot(); got.Status != ToolSlotRunning || mustJSON(t, got.Arguments) != mustJSON(t, map[string]any{"text": large}) {
		t.Fatalf("slot while running = %s %d-byte arguments, want running with the unbounded replaced arguments", got.Status, len(mustJSON(t, got.Arguments)))
	}
	close(release)
	if _, err := submission.Wait(testContext); err != nil {
		t.Fatal(err)
	}
	if err := root.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	untilDelivered(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return runEnded
	})
	_, _ = stream.Stop()
	// The start event carries the arguments the call runs with; the task input keeps the ones it was made with.
	mu.Lock()
	index := slices.IndexFunc(starts, func(start ToolExecutionStartEvent) bool { return start.ToolCallId == "c1/1" })
	mu.Unlock()
	if index < 0 || starts[index].Args == nil || starts[index].Args.Value("text") != large {
		t.Fatalf("the nested call's start event = %v, want the replaced arguments", index)
	}
	nested := 0
	for _, task := range tlToolTasks(tlScanTasks(t, harness, root.Id())) {
		input, err := durable.FromJsonValue[ToolTaskInput](task.Input)
		if err == nil && input.Kind == "nested" {
			nested++
			if mustJSON(t, input.Call.Arguments) != `{"text":"asked"}` {
				t.Fatalf("nested input arguments = %s, want the ones it was made with", mustJSON(t, input.Call.Arguments))
			}
		}
	}
	if nested != 1 {
		t.Fatalf("%d nested tasks, want 1", nested)
	}
	mustClose(t, harness)
}

// A nested call's stored call (tool.ts:686 copyJson(args)), its slot (tool.ts:724 call.arguments), its execute intent
// (tool.ts:155-163 final) and the start event carry the arguments in the member order of the object the caller passed, as
// a JavaScript object keeps it. A Go map has none, so ExecuteTool takes the order as NestedCallOptions.ArgumentsJSON.
func TestANestedCallKeepsTheCallersArgumentOrder(t *testing.T) {
	const ordered = `{"zeta":1,"alpha":{"y":"a","b":"c"},"text":"x"}`
	for name, open := range map[string]func(t *testing.T) durable.Storage{
		"memory": func(*testing.T) durable.Storage { return storage.NewMemoryStorage() },
		"sqlite": func(t *testing.T) durable.Storage { return openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")) },
	} {
		t.Run(name, func(t *testing.T) {
			setup := chatSetup(t)
			running, release := make(chan struct{}), make(chan struct{})
			var slots, input, checkpoint string
			addTool(t, setup.Registry, tlTool("slow", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				// Its own record while it runs: the input as made, and the execute intent. A failed read is a test failure
				// below (empty input), and the caller still gets its slot read and release.
				defer func() { <-release }()
				defer close(running)
				record, err := api.GetTask(ctx, api.TaskId())
				if err != nil || record == nil || record.State.Checkpoint == nil {
					return durable.ToolExecutionResult{}, fmt.Errorf("own record %+v: %w", record, err)
				}
				encodedInput, inputErr := json.Marshal(record.Input)
				encodedCheckpoint, checkpointErr := json.Marshal(*record.State.Checkpoint)
				input, checkpoint = string(encodedInput), string(encodedCheckpoint)
				return durable.ToolExecutionResult{}, errors.Join(inputErr, checkpointErr)
			}))
			addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				done := make(chan error, 1)
				go func() {
					args := map[string]any{"text": "x", "alpha": map[string]any{"b": "c", "y": "a"}, "zeta": 1.0}
					_, err := api.ExecuteTool(ctx, "slow", args, durable.NestedCallOptions{ArgumentsJSON: []byte(ordered)})
					done <- err
				}()
				<-running
				_, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
					live, err := docDraft(tx, LiveDoc, api.ConversationId())
					if err != nil {
						return nil, err
					}
					encoded, err := json.Marshal(live.Snapshot().Value("nestedTools"))
					slots = string(encoded)
					return nil, err
				})
				close(release)
				if doneErr := <-done; err == nil {
					err = doneErr
				}
				return durable.ToolExecutionResult{}, err
			}))
			setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
			harness, root := openChat(t, open(t), setup)
			stream := must(WatchEvents(testContext, harness, root.Id()))
			var mu sync.Mutex
			var startArgs string
			var runEnded bool
			stream.Start(func(_ context.Context, batch []AgentEvent) error {
				mu.Lock()
				defer mu.Unlock()
				for _, event := range batch {
					switch typed := event.(type) {
					case ToolExecutionStartEvent:
						if typed.ToolCallId == "c1/1" {
							startArgs = mustJSON(t, typed.Args)
						}
					case RunEndEvent:
						runEnded = true
					}
				}
				return nil
			})
			if settled := must(ownSubmit(t, root, ownInput("go")).Wait(testContext)); settled.Status != durable.SubmissionDone {
				t.Fatalf("submission %s, want done", settled.Status)
			}
			untilDelivered(t, func() bool {
				mu.Lock()
				defer mu.Unlock()
				return runEnded
			})
			_, _ = stream.Stop()
			if !strings.Contains(slots, `"arguments":`+ordered+`,"status":"running"`) {
				t.Errorf("stored nestedTools = %s, want the arguments %s", slots, ordered)
			}
			if !strings.HasSuffix(input, `"name":"slow","arguments":`+ordered+`}}`) {
				t.Errorf("stored input = %s, want the call's arguments %s", input, ordered)
			}
			if want := `{"phase":"execute","arguments":` + ordered + `,"replay":"unsafe"}`; checkpoint != want {
				t.Errorf("stored checkpoint = %s, want %s", checkpoint, want)
			}
			mu.Lock()
			defer mu.Unlock()
			if startArgs != ordered {
				t.Errorf("start event arguments = %s, want %s", startArgs, ordered)
			}
			mustClose(t, harness)
		})
	}
}

// harness-nested-tools.test.ts:432 it("reports nested calls in pi.live.nestedTools and as tool events with their parent"): the
// events half, with the empty snapshot nestedTools.
func TestNestedCallsAreToolEventsWithTheirParent(t *testing.T) {
	setup := chatSetup(t)
	running, release := make(chan struct{}), make(chan struct{})
	addTool(t, setup.Registry, tlTool("slow", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		api.Output("working\n")
		if err := api.Details(ctx, map[string]any{"step": 1.0}); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		close(running)
		<-release
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "slow done"}}}, nil
	}))
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		result, err := api.ExecuteTool(ctx, "slow", map[string]any{})
		text, _ := result.StructuredOutput.(string)
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}, err
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	stream, err := WatchEvents(testContext, harness, root.Id())
	if err != nil || stream.Snapshot.NestedTools == nil || len(stream.Snapshot.NestedTools) != 0 {
		t.Fatalf("snapshot nestedTools = %#v, %v, want an empty list", stream.Snapshot.NestedTools, err)
	}
	var mu sync.Mutex
	var events []AgentEvent
	var runEnded bool
	stream.Start(func(_ context.Context, batch []AgentEvent) error {
		mu.Lock()
		events = append(events, batch...)
		for _, event := range batch {
			if _, ok := event.(RunEndEvent); ok {
				runEnded = true
			}
		}
		mu.Unlock()
		return nil
	})
	submission := ownSubmit(t, root, ownInput("go"))
	<-running
	close(release)
	if _, err := submission.Wait(testContext); err != nil {
		t.Fatal(err)
	}
	if err := root.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	untilDelivered(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return runEnded
	})
	_, _ = stream.Stop()
	tasks := tlToolTasks(tlScanTasks(t, harness, root.Id()))
	parent, child := tasks[0], tasks[1]
	var summary []string
	var nestedEnd, parentEnd *ToolExecutionEndEvent
	var nestedUpdate bool
	for _, event := range events {
		switch typed := event.(type) {
		case ToolExecutionStartEvent:
			summary = append(summary, "start "+typed.ToolCallId+" parent="+typed.ParentToolCallId)
		case ToolExecutionEndEvent:
			summary = append(summary, "end "+typed.ToolCallId+" parent="+typed.ParentToolCallId)
			if typed.ToolCallId == "c1/1" {
				nestedEnd = &typed
			} else {
				parentEnd = &typed
			}
		case ToolExecutionUpdateEvent:
			nestedUpdate = nestedUpdate || typed.ToolCallId == "c1/1" && typed.ParentToolCallId == "c1"
		}
	}
	if got, want := strings.Join(summary, ";"), "start c1 parent=;start c1/1 parent=c1;end c1/1 parent=c1;end c1 parent="; got != want {
		t.Fatalf("tool events = %s, want %s", got, want)
	}
	if nestedEnd.Result == nil || nestedEnd.Result.StructuredOutput != "slow done" || nestedEnd.Result.IsError {
		t.Fatalf("nested end = %+v", nestedEnd)
	}
	if nestedEnd.TaskId == nil || *nestedEnd.TaskId != child.Id || nestedEnd.ParentTaskId == nil || *nestedEnd.ParentTaskId != parent.Id ||
		parentEnd.TaskId == nil || *parentEnd.TaskId != parent.Id || parentEnd.ParentTaskId != nil || parentEnd.Result != nil {
		t.Fatalf("task ids: nested %+v, parent %+v", nestedEnd, parentEnd)
	}
	if !nestedUpdate {
		t.Fatal("no tool_execution_update for the nested call")
	}
	mustClose(t, harness)
}

func nestedBatch(t *testing.T, setup *chatState, body func(ctx context.Context, api durable.ToolExecutionApi) error) tlRunResult {
	t.Helper()
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{}, body(ctx, api)
	}))
	return tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()}, nil)
}

func nestedText(result durable.NestedToolExecutionResult) string {
	text, _ := result.StructuredOutput.(string)
	return text
}

// harness-nested-tools.test.ts:216 it("gives each nested call its own task and call ID")
func TestEachNestedCallHasItsOwnTaskAndCallId(t *testing.T) {
	setup := chatSetup(t)
	var mu sync.Mutex
	type seen struct {
		task durable.TaskId
		call string
	}
	var got []seen
	addTool(t, setup.Registry, tlTool("who", func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		mu.Lock()
		got = append(got, seen{api.TaskId(), api.CallId()})
		mu.Unlock()
		return durable.ToolExecutionResult{}, nil
	}))
	nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		var wg sync.WaitGroup
		for _, key := range []string{"a", "b"} {
			wg.Go(func() { _, _ = api.ExecuteTool(ctx, "who", map[string]any{}, durable.NestedCallOptions{Key: &key}) })
		}
		wg.Wait()
		return nil
	})
	ids := []string{}
	tasks := map[durable.TaskId]bool{}
	for _, each := range got {
		ids = append(ids, each.call)
		tasks[each.task] = true
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"c1/a", "c1/b"}) || len(tasks) != 2 {
		t.Fatalf("calls %v, %d tasks", ids, len(tasks))
	}
}

// harness-nested-tools.test.ts:278 it("returns an error result for an unavailable tool and invalid arguments")
func TestANestedCallToAnUnavailableToolOrWithInvalidArgumentsIsAnErrorResult(t *testing.T) {
	setup := chatSetup(t)
	addTool(t, setup.Registry, echoRegistration())
	var codes []string
	nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		for _, call := range []struct {
			name string
			args map[string]any
		}{{"missing", map[string]any{}}, {"echo", map[string]any{"text": map[string]any{"not": "a string"}}}} {
			result, err := api.ExecuteTool(ctx, call.name, call.args)
			if err != nil || !result.IsError || len(result.Diagnostics) == 0 {
				return fmt.Errorf("result %+v: %w", result, errors.Join(err))
			}
			codes = append(codes, result.Diagnostics[0].Code)
		}
		return nil
	})
	if !slices.Equal(codes, []string{"tool_unavailable", "invalid_arguments"}) {
		t.Fatalf("codes = %v", codes)
	}
}

// harness-nested-tools.test.ts:298 it("returns the call a key already names, and rejects reusing the key for another call")
func TestANestedKeyReturnsItsCallAndRejectsAnotherCall(t *testing.T) {
	setup := chatSetup(t)
	var runs atomic.Int32
	addTool(t, setup.Registry, tlTool("echo", func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		runs.Add(1)
		text, _ := args.(map[string]any)["text"].(string)
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "echo " + text}}}, nil
	}))
	var reused durable.NestedToolExecutionResult
	run := nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		key := "k"
		opts := durable.NestedCallOptions{Key: &key}
		if _, err := api.ExecuteTool(ctx, "echo", map[string]any{"text": "a"}, opts); err != nil {
			return err
		}
		var err error
		if reused, err = api.ExecuteTool(ctx, "echo", map[string]any{"text": "a"}, opts); err != nil {
			return err
		}
		_, err = api.ExecuteTool(ctx, "echo", map[string]any{"text": "b"}, opts)
		return err
	})
	var failure string
	for _, entry := range run.entries {
		if entry.Kind == "pi.tool-result" {
			failure, _ = textOf(entry.Model[0])
		}
	}
	if runs.Load() != 1 || nestedText(reused) != "echo a" || !strings.Contains(failure, "Nested call c1/k was already made with another tool or other arguments") {
		t.Fatalf("runs %d, reused %q, result %q", runs.Load(), nestedText(reused), failure)
	}
}

// harness-nested-tools.test.ts:321 it("matches a reused key's arguments regardless of key order")
func TestAReusedNestedKeyMatchesArgumentsRegardlessOfKeyOrder(t *testing.T) {
	setup := chatSetup(t)
	var runs atomic.Int32
	addTool(t, setup.Registry, ownTool("pair", "pair", map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}, "b": map[string]any{"type": "string"}}, "required": []any{"a", "b"}},
		func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			runs.Add(1)
			m := args.(map[string]any)
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: m["a"].(string) + m["b"].(string)}}}, nil
		}))
	var texts []string
	nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		key := "k"
		for _, args := range []map[string]any{{"a": "x", "b": "y"}, {"b": "y", "a": "x"}} {
			result, err := api.ExecuteTool(ctx, "pair", args, durable.NestedCallOptions{Key: &key})
			if err != nil {
				return err
			}
			texts = append(texts, nestedText(result))
		}
		return nil
	})
	if runs.Load() != 1 || !slices.Equal(texts, []string{"xy", "xy"}) {
		t.Fatalf("runs %d, texts %v", runs.Load(), texts)
	}
}

// harness-nested-tools.test.ts:788 it("rejects explicit keys that are empty, have a slash, are __proto__, or are positive integers")
func TestExplicitNestedKeysAreChecked(t *testing.T) {
	setup := chatSetup(t)
	addTool(t, setup.Registry, echoRegistration())
	var errs []string
	nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		for _, key := range []string{"", "a/b", "__proto__", "7"} {
			if _, err := api.ExecuteTool(ctx, "echo", map[string]any{}, durable.NestedCallOptions{Key: &key}); err != nil {
				errs = append(errs, err.Error())
			}
		}
		return nil
	})
	rule := `must be non-empty, without "/", not "__proto__", and not a positive integer`
	want := []string{`Nested call key "" ` + rule, `Nested call key "a/b" ` + rule, `Nested call key "__proto__" ` + rule, `Nested call key "7" ` + rule}
	if !slices.Equal(errs, want) {
		t.Fatalf("errors = %q", errs)
	}
}

// harness-nested-tools.test.ts:406 it("records a nested call's usage under its tool")
func TestANestedCallsUsageIsRecordedUnderItsTool(t *testing.T) {
	setup := chatSetup(t)
	usage := ai.Usage{Input: 1, Output: 2, TotalTokens: 3, Cost: ai.UsageCost{Input: 0.1, Output: 0.2, Total: 0.3}}
	addTool(t, setup.Registry, tlTool("paid", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{Usage: &usage}, nil
	}))
	run := nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		_, err := api.ExecuteTool(ctx, "paid", map[string]any{})
		return err
	})
	ledger, err := durable.Snapshot[UsageState](testContext, run.harness, UsageDoc, run.root.Id())
	if err != nil || ledger == nil || ledger.Tools["paid"] != usage {
		t.Fatalf("pi.usage.tools = %+v, %v", ledger, err)
	}
}

// hang is a tool that reports, then runs until it is aborted.
func hangTool(running chan struct{}, report bool) *durable.ToolRegistration {
	return tlTool("hang", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		if report {
			api.Output("partial\n")
			if err := api.Details(ctx, map[string]any{"step": 1.0}); err != nil {
				return durable.ToolExecutionResult{}, err
			}
		}
		close(running)
		<-ctx.Done()
		return durable.ToolExecutionResult{}, ctx.Err()
	})
}

func nestedOutcome(t *testing.T, task durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) string {
	t.Helper()
	state := mustJSON(t, task.State)
	return state
}

// harness-nested-tools.test.ts:351 it("aborts the nested calls a caller left running when it returns, keeping their details")
func TestACallerThatReturnsAbortsTheNestedCallsItLeftRunning(t *testing.T) {
	setup := chatSetup(t)
	running := make(chan struct{})
	addTool(t, setup.Registry, hangTool(running, true))
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		go func() { _, _ = api.ExecuteTool(ctx, "hang", map[string]any{}) }()
		<-running
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "returned early"}}}, nil
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	stream, err := WatchEvents(testContext, harness, root.Id())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var ends []ToolExecutionEndEvent
	var runEnded bool
	stream.Start(func(_ context.Context, batch []AgentEvent) error {
		mu.Lock()
		defer mu.Unlock()
		for _, event := range batch {
			if end, ok := event.(ToolExecutionEndEvent); ok {
				ends = append(ends, end)
			}
			if _, ok := event.(RunEndEvent); ok {
				runEnded = true
			}
		}
		return nil
	})
	settled, err := ownSubmit(t, root, ownInput("go")).Wait(testContext)
	if err != nil || settled.Status != durable.SubmissionDone {
		t.Fatalf("settled %+v, %v", settled, err)
	}
	if err := root.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	untilDelivered(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return runEnded
	})
	_, _ = stream.Stop()
	if results := tlResults(allEntries(t, root)); len(results) == 0 || tlResultText(results[0]) != "returned early" {
		t.Fatalf("results %+v, want the caller's \"returned early\"", results)
	}
	tasks := tlToolTasks(tlScanTasks(t, harness, root.Id()))
	if len(tasks) != 2 {
		t.Fatalf("%d tool tasks", len(tasks))
	}
	if state := nestedOutcome(t, tasks[1]); !strings.Contains(state, `"outcome":{"status":"aborted","result":{"kind":"nested"}}`) {
		t.Fatalf("child state = %s", state)
	}
	mu.Lock()
	defer mu.Unlock()
	// The aborted call's result keeps its details; programs get no partial output, which only the model read. The nested
	// call ends, with its result, before its caller.
	if len(ends) != 2 || ends[0].ToolCallId != "c1/1" || ends[1].ToolCallId != "c1" || ends[1].Result != nil {
		t.Fatalf("ends = %+v", ends)
	}
	result := ends[0].Result
	if result == nil || !result.IsError || !result.HasDetails || mustJSON(t, result.Details) != `{"step":1}` || result.HasStructuredOutput {
		t.Fatalf("nested end result = %+v", result)
	}
	expectLiveEmpty(t, harness, root.Id())
	mustClose(t, harness)
}

// harness-nested-tools.test.ts:510 it("aborts nested calls with their caller")
func TestNestedCallsAreAbortedWithTheirCaller(t *testing.T) {
	setup := chatSetup(t)
	running := make(chan struct{})
	addTool(t, setup.Registry, hangTool(running, false))
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		_, err := api.ExecuteTool(ctx, "hang", map[string]any{})
		return durable.ToolExecutionResult{}, err
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	submission := ownSubmit(t, root, ownInput("go"))
	<-running
	if err := root.Abort(testContext, nil); err != nil {
		t.Fatal(err)
	}
	settled, err := submission.Wait(testContext)
	if err != nil || settled.Status != durable.SubmissionUnanswered {
		t.Fatalf("settled %+v, %v", settled, err)
	}
	tasks := tlToolTasks(tlScanTasks(t, harness, root.Id()))
	if len(tasks) != 2 || !strings.Contains(nestedOutcome(t, tasks[1]), `"outcome":{"status":"aborted","result":{"kind":"nested"}}`) ||
		!strings.Contains(nestedOutcome(t, tasks[0]), `"outcome":{"status":"aborted","result":{"kind":"model"`) {
		t.Fatalf("tasks = %v", tasks)
	}
	expectLiveEmpty(t, harness, root.Id())
	mustClose(t, harness)
}

// harness-nested-tools.test.ts:542 it("aborts a running nested call when its caller throws")
func TestARunningNestedCallIsAbortedWhenItsCallerThrows(t *testing.T) {
	setup := chatSetup(t)
	running := make(chan struct{})
	addTool(t, setup.Registry, hangTool(running, false))
	run := nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		go func() { _, _ = api.ExecuteTool(ctx, "hang", map[string]any{}) }()
		<-running
		return errors.New("caller failed")
	})
	var failure string
	for _, entry := range run.entries {
		if entry.Kind == "pi.tool-result" {
			failure, _ = textOf(entry.Model[0])
		}
	}
	tasks := tlToolTasks(tlScanTasks(t, run.harness, run.root.Id()))
	if run.status != durable.SubmissionDone || !strings.Contains(failure, "caller failed") || len(tasks) != 2 || !strings.Contains(nestedOutcome(t, tasks[1]), `"status":"aborted"`) {
		t.Fatalf("status %s, result %q, tasks %d", run.status, failure, len(tasks))
	}
}

// harness-nested-tools.test.ts:614 it("stops only the wait when a caller cancels it; the call runs on and its key returns its result")
func TestCancellingTheWaitForANestedCallStopsOnlyTheWait(t *testing.T) {
	setup := chatSetup(t)
	cancelled, release := make(chan struct{}), make(chan struct{})
	addTool(t, setup.Registry, tlTool("slow", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		close(cancelled)
		<-release
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "slow done"}}}, nil
	}))
	var first error
	var retry durable.NestedToolExecutionResult
	run := nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		key := "same"
		options := durable.NestedCallOptions{Key: &key}
		waitCtx, cancel := context.WithCancelCause(ctx)
		go func() { <-cancelled; cancel(errors.New("cancelled")) }()
		_, first = api.ExecuteTool(waitCtx, "slow", map[string]any{}, options)
		close(release)
		var err error
		retry, err = api.ExecuteTool(ctx, "slow", map[string]any{}, options)
		return err
	})
	tasks := tlToolTasks(tlScanTasks(t, run.harness, run.root.Id()))
	if first == nil || retry.IsError || nestedText(retry) != "slow done" || len(tasks) != 2 {
		t.Fatalf("first %v, retry %+v, %d tasks", first, retry, len(tasks))
	}
}

// harness-nested-tools.test.ts:648 it("admits one nested call for a key used by concurrent calls")
func TestConcurrentCallsWithOneKeyAdmitOneNestedCall(t *testing.T) {
	setup := chatSetup(t)
	var runs atomic.Int32
	addTool(t, setup.Registry, tlTool("echo", func(_ context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		runs.Add(1)
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "echo a"}}}, nil
	}))
	both := make([]durable.NestedToolExecutionResult, 2)
	run := nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		key := "k"
		var wg sync.WaitGroup
		for index := range both {
			wg.Go(func() {
				both[index], _ = api.ExecuteTool(ctx, "echo", map[string]any{"text": "a"}, durable.NestedCallOptions{Key: &key})
			})
		}
		wg.Wait()
		return nil
	})
	tasks := tlToolTasks(tlScanTasks(t, run.harness, run.root.Id()))
	if runs.Load() != 1 || nestedText(both[0]) != "echo a" || nestedText(both[1]) != "echo a" || len(tasks) != 2 {
		t.Fatalf("runs %d, results %q %q, %d tasks", runs.Load(), nestedText(both[0]), nestedText(both[1]), len(tasks))
	}
}

// harness-nested-tools.test.ts:568 it("reattaches a replay-safe caller to the nested calls it made before a restart")
func TestAReplaySafeCallerReattachesToTheNestedCallsItMadeBeforeARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite")
	setup := chatSetup(t)
	var echoRuns, runs atomic.Int32
	addTool(t, setup.Registry, tlTool("echo", func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		echoRuns.Add(1)
		text, _ := args.(map[string]any)["text"].(string)
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "echo " + text}}}, nil
	}))
	blocked := deferred()
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		run := runs.Add(1)
		first, err := api.ExecuteTool(ctx, "echo", map[string]any{"text": "a"})
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if run == 1 {
			blocked.resolve()
			return durable.ToolExecutionResult{}, abortedBy(ctx)
		}
		// A call new to the rerun: its slot, after the reattached one's, is found and finishes.
		second, err := api.ExecuteTool(ctx, "echo", map[string]any{"text": "b"})
		text := fmt.Sprintf("run %d: %s, %s", run, nestedText(first), nestedText(second))
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}, err
	}, func(registration *durable.ToolRegistration) { registration.Replay = durable.ReplaySafe }))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	id := ownSubmit(t, root, ownInput("go")).Id()
	if err := blocked.wait(testContext); err != nil {
		t.Fatal(err)
	}
	mustClose(t, harness)

	harness, root = openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	if settled := must(must(harness.Submission(testContext, id)).Wait(testContext)); settled.Status != durable.SubmissionDone {
		t.Fatalf("submission %s, want done", settled.Status)
	}
	if runs.Load() != 2 || echoRuns.Load() != 2 {
		t.Fatalf("batch ran %d times, echo %d, want 2 and 2", runs.Load(), echoRuns.Load())
	}
	if results := tlResults(allEntries(t, root)); len(results) == 0 || tlResultText(results[0]) != "run 2: echo a, echo b" {
		t.Fatalf("results %+v, want \"run 2: echo a, echo b\"", results)
	}
	if nested := nestedTasksOf(t, harness); len(nested) != 2 {
		t.Fatalf("%d nested tasks, want 2", len(nested))
	}
	tasks := tlToolTasks(tlScanTasks(t, harness, root.Id()))
	if len(tasks) < 2 || tasks[1].State.Outcome == nil || !strings.Contains(mustJSON(t, tasks[1].State.Outcome.Result), `"kind":"nested"`) {
		t.Fatalf("the second tool task %+v, want a nested result", tasks)
	}
	mustClose(t, harness)
}

// harness-nested-tools.test.ts:670 it("aborts left-running nested calls of a caller interrupted while it cleaned them up")
func TestTheLeftRunningNestedCallsOfACallerInterruptedWhileItCleanedThemUpAreAborted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite")
	setup := chatSetup(t)
	running, cleaning, release := deferred(), deferred(), deferred()
	var hangRuns atomic.Int32
	addTool(t, setup.Registry, tlTool("hang", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		hangRuns.Add(1)
		api.Output("partial\n")
		if err := api.Details(ctx, map[string]any{"step": 1.0}); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		running.resolve()
		<-ctx.Done()
		// The caller's cleanup waits for this abort; hold it there until the Harness closes.
		cleaning.resolve()
		_ = release.wait(testContext)
		return durable.ToolExecutionResult{}, errors.New("aborted")
	}))
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		go func() { _, _ = api.ExecuteTool(ctx, "hang", map[string]any{}) }()
		if err := running.wait(ctx); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "returned early"}}}, nil
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	id := ownSubmit(t, root, ownInput("go")).Id()
	if err := cleaning.wait(testContext); err != nil {
		t.Fatal(err)
	}
	closed := goCall(func() error { return harness.Close(testContext) })
	release.resolve()
	if err := closed.wait(t); err != nil {
		t.Fatal(err)
	}

	harness, root = openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	if settled := must(must(harness.Submission(testContext, id)).Wait(testContext)); settled.Status != durable.SubmissionDone {
		t.Fatalf("submission %s, want done", settled.Status)
	}
	if err := harness.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	// The caller is replay-unsafe: it reports the interruption, and its cleanup aborts the nested call.
	results := tlResults(allEntries(t, root))
	if len(results) == 0 || results[0].ToolCallID != "c1" || !results[0].IsError || !strings.Contains(tlResultText(results[0]), "Tool batch was interrupted") {
		t.Fatalf("results %+v, want c1 as an interrupted error", results)
	}
	tasks := tlToolTasks(tlScanTasks(t, harness, root.Id()))
	if len(tasks) < 2 || !strings.Contains(nestedOutcome(t, tasks[1]), `"status":"terminal","outcome":{"status":"aborted"`) {
		t.Fatalf("tool tasks %+v, want the nested call terminal aborted", tasks)
	}
	if hangRuns.Load() != 1 {
		t.Fatalf("hang ran %d times, want 1", hangRuns.Load())
	}
	expectLiveEmpty(t, harness, root.Id())
	mustClose(t, harness)
}

// harness-nested-tools.test.ts:1267 it("migrates version 1 tool task input to a model-issued call")
func TestToolTaskMigratesVersion1InputToAModelIssuedCall(t *testing.T) {
	input, checkpoint, err := ToolTask.Definition.Migrate(map[string]any{"assistant": 3.0, "callId": "c1"}, map[string]any{"phase": "call"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, input); !equalJSONText(t, got, `{"kind":"model","assistant":3,"callId":"c1"}`) {
		t.Fatalf("input = %s, want {kind: model, assistant: 3, callId: c1}", got)
	}
	if got := mustJSON(t, checkpoint); !equalJSONText(t, got, `{"phase":"call"}`) {
		t.Fatalf("checkpoint = %s, want {phase: call}", got)
	}
}

// PiG coverage beyond the Pi file: executeTool after the caller settled rejects.
func TestANestedCallAfterItsCallerSettledIsRejected(t *testing.T) {
	setup := chatSetup(t)
	addTool(t, setup.Registry, echoRegistration())
	var api durable.ToolExecutionApi
	nestedBatch(t, setup, func(_ context.Context, caller durable.ToolExecutionApi) error {
		api = caller
		return nil
	})
	// executeTool is async upstream: a settled caller is a rejection of the returned promise, not a synchronous throw.
	if _, err := api.ExecuteTool(testContext, "echo", map[string]any{"text": "late"}); err == nil || !strings.Contains(err.Error(), "has settled") {
		t.Fatalf("error = %v, want the call has settled", err)
	}
}

// harness-nested-tools.test.ts:750 it("aborts every left-running nested call before waiting for any")
func TestEveryNestedCallLeftRunningIsAbortedBeforeAnyIsWaitedFor(t *testing.T) {
	setup := chatSetup(t)
	bAborted := make(chan struct{})
	started := map[string]chan struct{}{"a": make(chan struct{}), "b": make(chan struct{})}
	// A only stops once B was told to stop.
	addTool(t, setup.Registry, tlTool("a", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		close(started["a"])
		<-bAborted
		return durable.ToolExecutionResult{}, nil
	}))
	addTool(t, setup.Registry, tlTool("b", func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		close(started["b"])
		<-ctx.Done()
		close(bAborted)
		return durable.ToolExecutionResult{}, errors.New("aborted")
	}))
	run := nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		for _, name := range []string{"a", "b"} {
			go func() { _, _ = api.ExecuteTool(ctx, name, map[string]any{}, durable.NestedCallOptions{Key: &name}) }()
		}
		<-started["a"]
		<-started["b"]
		return nil
	})
	var states []string
	for _, task := range tlToolTasks(tlScanTasks(t, run.harness, run.root.Id()))[1:] {
		states = append(states, mustJSON(t, task.State)[:20])
	}
	if run.status != durable.SubmissionDone || len(states) != 2 || !strings.HasPrefix(states[0], `{"status":"terminal"`) || !strings.HasPrefix(states[1], `{"status":"terminal"`) {
		t.Fatalf("status %s, states %v", run.status, states)
	}
}

// harness-nested-tools.test.ts:814 it("runs nested calls of nested calls, and ends them children first")
func TestNestedCallsOfNestedCallsEndChildrenFirst(t *testing.T) {
	setup := chatSetup(t)
	addTool(t, setup.Registry, echoRegistration())
	relay := func(name, callee string, args map[string]any, options durable.NestedCallOptions) *durable.ToolRegistration {
		return tlTool(name, func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			result, err := api.ExecuteTool(ctx, callee, args, options)
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: nestedText(result)}}}, err
		})
	}
	addTool(t, setup.Registry, relay("inner", "echo", map[string]any{"text": "deep"}, durable.NestedCallOptions{Key: new("x")}))
	addTool(t, setup.Registry, relay("outer", "inner", map[string]any{}, durable.NestedCallOptions{}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"outer", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	stream, err := WatchEvents(testContext, harness, root.Id())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var tools []string
	var runEnded bool
	stream.Start(func(_ context.Context, batch []AgentEvent) error {
		mu.Lock()
		defer mu.Unlock()
		for _, event := range batch {
			switch typed := event.(type) {
			case RunEndEvent:
				runEnded = true
			case ToolExecutionStartEvent:
				tools = append(tools, "start "+typed.ToolCallId+" <"+typed.ParentToolCallId+">")
			case ToolExecutionEndEvent:
				tools = append(tools, "end "+typed.ToolCallId+" <"+typed.ParentToolCallId+">")
			}
		}
		return nil
	})
	if _, err := ownSubmit(t, root, ownInput("go")).Wait(testContext); err != nil {
		t.Fatal(err)
	}
	if err := root.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	untilDelivered(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return runEnded
	})
	_, _ = stream.Stop()
	if results := tlResults(allEntries(t, root)); len(results) == 0 || tlResultText(results[0]) != "echo deep" {
		t.Fatalf("results %+v, want \"echo deep\"", results)
	}
	all := tlToolTasks(tlScanTasks(t, harness, root.Id()))
	if len(all) != 3 || all[1].Owner == nil || *all[1].Owner != all[0].Id || all[2].Owner == nil || *all[2].Owner != all[1].Id {
		t.Fatalf("tasks = %v", all)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"start c1 <>", "start c1/1 <c1>", "start c1/1/x <c1/1>", "end c1/1/x <c1/1>", "end c1/1 <c1>", "end c1 <>"}
	if !slices.Equal(tools, want) {
		t.Fatalf("tool events = %q", tools)
	}
	expectLiveEmpty(t, harness, root.Id())
	mustClose(t, harness)
}

// harness-nested-tools.test.ts:1093 it("finds each nested call's slot after slots in the middle were removed")
func TestEachNestedCallsSlotIsFoundAfterSlotsInTheMiddleWereRemoved(t *testing.T) {
	setup := chatSetup(t)
	addTool(t, setup.Registry, echoRegistration())
	addTool(t, setup.Registry, tlTool("inner", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		for _, text := range []string{"m1", "m2"} {
			if _, err := api.ExecuteTool(ctx, "echo", map[string]any{"text": text}); err != nil {
				return durable.ToolExecutionResult{}, err
			}
		}
		return durable.ToolExecutionResult{}, nil
	}))
	var slots []NestedToolSlot
	nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		// n2 makes m1 and m2 and settles, which removes them from between n2 and n3.
		for _, call := range []struct{ name, text string }{{"echo", "n1"}, {"inner", ""}, {"echo", "n3"}, {"echo", "n4"}} {
			if _, err := api.ExecuteTool(ctx, call.name, map[string]any{"text": call.text}); err != nil {
				return err
			}
		}
		live, err := durable.Snapshot[LiveState](ctx, api, LiveDoc, api.ConversationId())
		if live != nil {
			slots = live.NestedTools
		}
		return err
	})
	var ids []string
	for _, slot := range slots {
		if slot.Status != ToolSlotDone || slot.Summary == nil || slot.Summary.IsError || slot.Summary.DurationMs == nil {
			t.Fatalf("slot %+v, want done with a summary", slot)
		}
		ids = append(ids, slot.CallId)
	}
	if !slices.Equal(ids, []string{"c1/1", "c1/2", "c1/3", "c1/4"}) {
		t.Fatalf("slots = %v", ids)
	}
}

// harness-nested-tools.test.ts:1161 it("gives callers what a schema-less tool streamed before it threw")
func TestACallerGetsWhatASchemaLessToolStreamedBeforeItFailed(t *testing.T) {
	setup := chatSetup(t)
	addTool(t, setup.Registry, tlTool("fails", func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		api.Output("half way\n")
		return durable.ToolExecutionResult{}, errors.New("broke")
	}))
	var result durable.NestedToolExecutionResult
	nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		var err error
		result, err = api.ExecuteTool(ctx, "fails", map[string]any{})
		return err
	})
	if !result.IsError || result.StructuredOutput != "half way\n" || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "tool_error" || result.Diagnostics[0].Message != "broke" {
		t.Fatalf("result = %+v", result)
	}
}

// harness-nested-tools.test.ts:1065 it("runs a nested call whose repaired arguments set an optional property to undefined"). Go
// has no undefined: the repaired arguments leave the optional property out, which is what Pi's tool receives ({}).
func TestANestedCallsRepairedArgumentsMayLeaveAnOptionalPropertyOut(t *testing.T) {
	setup := chatSetup(t)
	var seen []any
	addTool(t, setup.Registry, tlTool("repaired", func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		seen = append(seen, args)
		return durable.ToolExecutionResult{}, nil
	}, func(tool *durable.ToolRegistration) {
		tool.PrepareArguments = func(any) (any, error) { return map[string]any{}, nil }
	}))
	var result durable.NestedToolExecutionResult
	nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		var err error
		result, err = api.ExecuteTool(ctx, "repaired", map[string]any{"text": "x"})
		return err
	})
	if result.IsError || len(seen) != 1 || mustJSON(t, seen[0]) != `{}` {
		t.Fatalf("result %+v, seen %v", result, seen)
	}
}

// harness-nested-tools.test.ts:1187 it("shows a finished nested call as a done slot with its summary until its caller settles")
func TestAFinishedNestedCallIsADoneSlotWithItsSummaryUntilItsCallerSettles(t *testing.T) {
	setup := chatSetup(t)
	// Diagnostics win over output for the error text, which is cut to 500 characters.
	addTool(t, setup.Registry, tlTool("verbose", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{
			Output:      []ai.ToolResultMessageContent{ai.TextContent{Text: "ignored"}},
			IsError:     new(true),
			Diagnostics: []durable.ToolDiagnostic{{Severity: durable.SeverityError, Message: strings.Repeat("e", 600)}},
		}, nil
	}))
	usage := ai.Usage{Input: 1, Output: 2, TotalTokens: 3, Cost: ai.UsageCost{Input: 0.1, Output: 0.2, Total: 0.3}}
	addTool(t, setup.Registry, echoRegistration())
	addTool(t, setup.Registry, tlTool("paid", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{Usage: &usage}, nil
	}))
	// An error with mixed content: programs get the list, the summary its text.
	addTool(t, setup.Registry, tlTool("broken", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{
			Output:  []ai.ToolResultMessageContent{ai.TextContent{Text: "bad image"}, ai.ImageContent{Data: "AAAA", MimeType: "image/png"}},
			IsError: new(true),
		}, nil
	}))
	var slots []NestedToolSlot
	run := nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		for _, call := range []struct {
			name string
			args map[string]any
		}{{"echo", map[string]any{"text": "a"}}, {"missing", map[string]any{}}, {"paid", map[string]any{}}, {"broken", map[string]any{}}, {"verbose", map[string]any{}}} {
			if _, err := api.ExecuteTool(ctx, call.name, call.args); err != nil {
				return err
			}
		}
		live, err := durable.Snapshot[LiveState](ctx, api, LiveDoc, api.ConversationId())
		if live != nil {
			slots = live.NestedTools
		}
		return err
	})
	summaries := make([]string, len(slots))
	for index, slot := range slots {
		if slot.Status != ToolSlotDone || slot.Summary == nil {
			t.Fatalf("slot %d = %+v", index, slot)
		}
		summary := *slot.Summary
		summaries[index] = fmt.Sprintf("%t timed=%t usage=%t %.12s %d", summary.IsError, summary.DurationMs != nil, summary.Usage != nil, summary.Error, len(summary.Error))
	}
	want := []string{
		"false timed=true usage=false  0",
		"true timed=false usage=false Tool missing 29",
		"false timed=true usage=true  0",
		"true timed=true usage=false bad image 9",
		"true timed=true usage=false eeeeeeeeeeee 500",
	}
	if !slices.Equal(summaries, want) || *slots[2].Summary.Usage != usage {
		t.Fatalf("summaries = %q", summaries)
	}
	expectLiveEmpty(t, run.harness, run.root.Id())
}

// harness-nested-tools.test.ts:968 it("loses the running output of a nested call made with progress: false when it is aborted")
func TestAnAbortedNestedCallWithoutProgressLosesItsRunningOutput(t *testing.T) {
	setup := chatSetup(t)
	running := make(chan struct{})
	addTool(t, setup.Registry, tlTool("chatty", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		api.Output("line\n")
		api.Diagnostic(durable.ToolDiagnostic{Severity: durable.SeverityInfo, Message: "note"})
		if err := api.Details(ctx, map[string]any{"step": 1.0}); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		close(running)
		<-ctx.Done()
		return durable.ToolExecutionResult{}, ctx.Err()
	}))
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		off := false
		go func() {
			_, _ = api.ExecuteTool(ctx, "chatty", map[string]any{}, durable.NestedCallOptions{Progress: &off})
		}()
		<-running
		return durable.ToolExecutionResult{}, nil
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	stream, err := WatchEvents(testContext, harness, root.Id())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var nestedEnd *ToolExecutionEndEvent
	var runEnded bool
	stream.Start(func(_ context.Context, batch []AgentEvent) error {
		mu.Lock()
		defer mu.Unlock()
		for _, event := range batch {
			if end, ok := event.(ToolExecutionEndEvent); ok && end.ToolCallId == "c1/1" {
				nestedEnd = &end
			}
			if _, ok := event.(RunEndEvent); ok {
				runEnded = true
			}
		}
		return nil
	})
	if _, err := ownSubmit(t, root, ownInput("go")).Wait(testContext); err != nil {
		t.Fatal(err)
	}
	if err := root.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	untilDelivered(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return runEnded
	})
	_, _ = stream.Stop()
	mu.Lock()
	defer mu.Unlock()
	if nestedEnd == nil || nestedEnd.Result == nil {
		t.Fatalf("nested end = %+v", nestedEnd)
	}
	result := nestedEnd.Result
	if !result.IsError || result.HasStructuredOutput || result.HasDetails || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "aborted" {
		t.Fatalf("result = %+v", result)
	}
	mustClose(t, harness)
}

// harness-nested-tools.test.ts:861 it("reports the output of several running nested calls, and shows them to a late subscriber")
func TestSeveralRunningNestedCallsReportTheirOutputAndShowToALateSubscriber(t *testing.T) {
	setup := chatSetup(t)
	release := make(chan struct{})
	running := map[string]chan struct{}{"one": make(chan struct{}), "two": make(chan struct{})}
	wrote := map[string]chan struct{}{"one": make(chan struct{}), "two": make(chan struct{})}
	addTool(t, setup.Registry, tlTool("slow", func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		name, _ := args.(map[string]any)["text"].(string)
		close(running[name])
		<-wrote[name]
		api.Output(name + "\n")
		if err := api.Details(ctx, map[string]any{"name": name}); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		<-release
		return durable.ToolExecutionResult{}, nil
	}))
	nestedDone := make(chan struct{})
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		defer close(nestedDone)
		// Promise.all of both calls.
		promiseAll(
			func() { _, _ = api.ExecuteTool(ctx, "slow", map[string]any{"text": "one"}) },
			func() { _, _ = api.ExecuteTool(ctx, "slow", map[string]any{"text": "two"}) },
		)
		return durable.ToolExecutionResult{}, nil
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	submission := ownSubmit(t, root, ownInput("go"))
	<-running["one"]
	<-running["two"]
	stream, err := WatchEvents(testContext, harness, root.Id())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, slot := range stream.Snapshot.NestedTools {
		ids = append(ids, slot.CallId+" "+string(slot.Status))
	}
	if !slices.Equal(ids, []string{"c1/1 running", "c1/2 running"}) {
		t.Fatalf("snapshot nestedTools = %v", ids)
	}
	var mu sync.Mutex
	var updates []string
	stream.Start(func(_ context.Context, batch []AgentEvent) error {
		mu.Lock()
		defer mu.Unlock()
		for _, event := range batch {
			if update, ok := event.(ToolExecutionUpdateEvent); ok && update.Output != nil {
				updates = append(updates, update.ToolCallId+" "+*update.Output.Set)
			}
		}
		return nil
	})
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(updates) }
	waitFor := func(n int) {
		t.Helper()
		for range 400 {
			if count() >= n {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("updates = %v, want %d", updates, n)
	}
	// The second slot writes first, so a wrong slot index in the update path would show.
	close(wrote["two"])
	waitFor(1)
	close(wrote["one"])
	waitFor(2)
	close(release)
	if _, err := submission.Wait(testContext); err != nil {
		t.Fatal(err)
	}
	<-nestedDone
	_, _ = stream.Stop()
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(updates, []string{"c1/2 two\n", "c1/1 one\n"}) {
		t.Fatalf("updates = %q", updates)
	}
	mustClose(t, harness)
}

// untilDelivered waits until the stream delivered the run's end. The stream delivers on its own goroutine, so idle does not
// mean delivered (Pi drains in a microtask before waitForIdle resolves), and Stop drops undelivered batches.
func untilDelivered(t *testing.T, delivered func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if delivered() {
			return
		}
	}
	t.Fatal("the run's end event was not delivered")
}
