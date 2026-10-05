// Ports packages/durable/test/harness-tools.test.ts. See harness_tasks_test.go for the Go mappings that apply to every
// case.

package harness

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

var echoParameters = map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}

type tlExecute = func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error)

func tlTool(name string, execute tlExecute, edit ...func(*durable.ToolRegistration)) *durable.ToolRegistration {
	return ownTool(name, "The "+name+" tool", echoParameters, execute, edit...)
}

// tlNoContent is a tool body that returns an empty content list.
func tlNoContent(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
	return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
}

type tlCall struct {
	name string
	args map[string]any
	id   string
}

// tlCalls is a tool-calling faux answer with one call per entry.
func tlCalls(list ...tlCall) ai.FauxResponse {
	blocks := make([]ai.FauxContentBlock, 0, len(list))
	for _, call := range list {
		blocks = append(blocks, ai.FauxToolCall(call.name, call.args, call.id))
	}
	return ai.FauxResponse{Content: blocks, StopReason: "toolUse"}
}

func tlCallsStep(list ...tlCall) ai.FauxResponseStep { return ai.FauxStaticStep(tlCalls(list...)) }

func tlDone() ai.FauxResponseStep { return fauxAnswer("done") }

type tlRunResult struct {
	harness Harness
	root    Conversation
	entries []durable.EntryRecord
	status  durable.SubmissionStatus
}

func tlRun(t *testing.T, setup *chatState, responses []ai.FauxResponseStep, prepare func(Harness, Conversation), options ...openChatOptions) tlRunResult {
	t.Helper()
	setup.Faux.SetResponses(responses)
	harness, root := openChat(t, storage.NewMemoryStorage(), setup, options...)
	if prepare != nil {
		prepare(harness, root)
	}
	settled, err := ownSubmit(t, root, ownInput("go")).Wait(testContext)
	if err != nil {
		t.Fatal(err)
	}
	return tlRunResult{harness: harness, root: root, entries: allEntries(t, root), status: settled.Status}
}

func tlResults(entries []durable.EntryRecord) []ai.ToolResultMessage {
	var results []ai.ToolResultMessage
	for index := range entries {
		if durable.ToolResultEntry.Is(&entries[index]) {
			results = append(results, entries[index].Model[0].(ai.ToolResultMessage))
		}
	}
	return results
}

func tlResultText(message ai.ToolResultMessage) string {
	parts := make([]string, 0, len(message.Content))
	for _, item := range message.Content {
		switch typed := item.(type) {
		case ai.TextContent:
			parts = append(parts, typed.Text)
		case ai.ImageContent:
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "|")
}

func tlResultById(entries []durable.EntryRecord) map[string]ai.ToolResultMessage {
	byId := map[string]ai.ToolResultMessage{}
	for _, result := range tlResults(entries) {
		byId[result.ToolCallID] = result
	}
	return byId
}

func tlSystems(entries []durable.EntryRecord) []ai.SystemMessage {
	var systems []ai.SystemMessage
	for _, entry := range entries {
		if entry.Kind == "pi.system" {
			systems = append(systems, entry.Model[0].(ai.SystemMessage))
		}
	}
	return systems
}

func tlDiagnosticCodes(entry durable.EntryRecord) []string {
	codes := []string{}
	for _, diagnostic := range entry.Data.(map[string]any)["diagnostics"].([]any) {
		code, _ := diagnostic.(map[string]any)["code"].(string)
		codes = append(codes, code)
	}
	return codes
}

func tlFirstToolResultEntry(t *testing.T, entries []durable.EntryRecord) durable.EntryRecord {
	t.Helper()
	for index := range entries {
		if durable.ToolResultEntry.Is(&entries[index]) {
			return entries[index]
		}
	}
	t.Fatal("no tool result entry")
	return durable.EntryRecord{}
}

func tlSetExecution(setup *chatState, mode durable.ToolExecutionMode) {
	setup.SetSettings(func(settings *HarnessSettings) { settings.ToolExecution = mode })
}

func tlSetExtensions(setup *chatState, extensions []*durable.Extension) {
	setup.SetSettings(func(settings *HarnessSettings) { settings.Extensions = extensions })
}

func tlScanTasks(t *testing.T, harness Harness, conversationId durable.ConversationId) []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
	t.Helper()
	page, err := durable.Commit(testContext, harness, func(tx durable.Tx) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
		return tx.ScanTasks(durable.TaskQuery{ConversationId: &conversationId}, 20, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}

func tlToolTasks(tasks []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
	var tools []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
	for _, task := range tasks {
		if task.Kind == "pi.tool" {
			tools = append(tools, task)
		}
	}
	return tools
}

func tlExpectText(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("text %q, want %q", got, want)
	}
}

func tlAddedNames(system ai.SystemMessage) []string {
	names := []string{}
	for _, tool := range system.ToolsAdded {
		names = append(names, tool.Name)
	}
	return names
}

func TestToolRound(t *testing.T) {
	t.Run("runs input, tool call, tool result, and answer, and settles the input", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, tlTool("echo", func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return ownText(fmt.Sprintf("echo %v", args.(map[string]any)["text"])), nil
		}))
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"echo", map[string]any{"text": "hi"}, "c1"}), tlDone()}, nil)
		if run.status != durable.SubmissionDone {
			t.Fatalf("status %s, want done", run.status)
		}
		kinds := []string{}
		for _, entry := range run.entries {
			kinds = append(kinds, entry.Kind)
		}
		if want := []string{"pi.user", "pi.system", "pi.assistant", "pi.tool-result", "pi.assistant"}; !reflect.DeepEqual(kinds, want) {
			t.Fatalf("entries %v, want %v", kinds, want)
		}
		system := run.entries[1].Model[0].(ai.SystemMessage)
		if len(system.ToolsAdded) != 1 || system.ToolsAdded[0].Name != "echo" || system.ToolsAdded[0].Description != "The echo tool" || system.ToolsAdded[0].Parameters == nil {
			t.Fatalf("toolsAdded %+v, want the echo declaration", system.ToolsAdded)
		}
		results := tlResults(run.entries)
		if results[0].ToolCallID != "c1" || results[0].ToolName != "echo" || results[0].IsError {
			t.Fatalf("result %+v", results[0])
		}
		tlExpectText(t, tlResultText(results[0]), "echo hi")
		if !reflect.DeepEqual(run.entries[3].Data, map[string]any{"diagnostics": []any{}}) {
			t.Fatalf("result data %v, want empty diagnostics", run.entries[3].Data)
		}
		if run.entries[3].ByTaskId == nil {
			t.Fatal("the result entry has no byTaskId")
		}
		// The second request sees the tool result right after its call.
		if setup.Faux.CallCount() != 2 {
			t.Fatalf("%d provider calls, want 2", setup.Faux.CallCount())
		}
		if live, err := run.harness.SnapshotErased(testContext, LiveDoc, run.root.Id()); err != nil || !reflect.DeepEqual(live, durable.JsonObject{}) {
			t.Fatalf("live document %v %v, want {}", live, err)
		}
		mustClose(t, run.harness)
	})

	t.Run("answers calls to tools the request did not offer without a task", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, tlTool("echo", tlNoContent))
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"ghost", map[string]any{}, "c1"}, tlCall{"echo", map[string]any{}, "c2"}), tlDone()}, nil)
		if run.status != durable.SubmissionDone {
			t.Fatalf("status %s, want done", run.status)
		}
		results := tlResults(run.entries)
		ghost, echo := results[0], results[1]
		if ghost.ToolCallID != "c1" || !ghost.IsError {
			t.Fatalf("ghost result %+v", ghost)
		}
		tlExpectText(t, tlResultText(ghost), "<harness>\n[error] Tool ghost is not available\n</harness>")
		if echo.ToolCallID != "c2" || echo.IsError {
			t.Fatalf("echo result %+v", echo)
		}
		wantData := map[string]any{"diagnostics": []any{map[string]any{"severity": "error", "code": "tool_unavailable", "message": "Tool ghost is not available"}}}
		if got := tlFirstToolResultEntry(t, run.entries).Data; !reflect.DeepEqual(got, wantData) {
			t.Fatalf("ghost data %v, want %v", got, wantData)
		}
		if count := len(tlToolTasks(tlScanTasks(t, run.harness, run.root.Id()))); count != 1 {
			t.Fatalf("%d tool tasks, want 1", count)
		}
		mustClose(t, run.harness)
	})

	t.Run("answers a call to a tool deactivated after preparation with tool_unavailable", func(t *testing.T) {
		setup := chatSetup(t)
		var seen atomic.Int32
		addTool(t, setup.Registry, tlTool("echo", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			seen.Add(1)
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
		}))
		root := &syncValue[Conversation]{}
		deactivate := ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.FauxResponse, error) {
			conversation, _ := root.get()
			if err := conversation.Configure(testContext, AgentChange{Tools: SetTo(ToolChange{Exact: true})}); err != nil {
				return ai.FauxResponse{}, err
			}
			return tlCalls(tlCall{"echo", map[string]any{}, "c1"}), nil
		})
		run := tlRun(t, setup, []ai.FauxResponseStep{deactivate, tlDone()}, func(_ Harness, conversation Conversation) { root.put(conversation) })
		if seen.Load() != 0 {
			t.Fatal("the deactivated tool ran")
		}
		tlExpectText(t, tlResultText(tlResults(run.entries)[0]), "<harness>\n[error] Tool echo is not available\n</harness>")
		// The next preparation removes it.
		systems := tlSystems(run.entries)
		if got := systems[len(systems)-1].ToolsRemoved; !reflect.DeepEqual(got, []ai.ToolReference{{Name: "echo"}}) {
			t.Fatalf("toolsRemoved %v, want [echo]", got)
		}
		mustClose(t, run.harness)
	})

	t.Run("removes unregistered active tools from the offer and adds them back after re-registration", func(t *testing.T) {
		setup := chatSetup(t)
		echo := tlTool("echo", tlNoContent)
		registration := addTool(t, setup.Registry, echo)
		first := tlRun(t, setup, []ai.FauxResponseStep{tlDone()}, nil)
		registration.dispose()
		setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"echo", map[string]any{}, "c1"}), tlDone()})
		second := ownSubmitAndWait(t, first.root, "again")
		if second.Status != durable.SubmissionDone {
			t.Fatalf("second input %s, want done", second.Status)
		}
		entries := allEntries(t, first.root)
		systems := tlSystems(entries)
		if got := systems[len(systems)-1].ToolsRemoved; !reflect.DeepEqual(got, []ai.ToolReference{{Name: "echo"}}) {
			t.Fatalf("toolsRemoved %v, want [echo]", got)
		}
		if !tlResults(entries)[0].IsError {
			t.Fatal("the unavailable call did not answer with an error")
		}
		// The stored agent is not rewritten; the tool is only not resolved.
		if agent := mustAgent(t, first.root); len(agent.Tools) != 0 {
			t.Fatalf("agent tools %v, want none", agent.Tools)
		}
		addTool(t, setup.Registry, echo)
		setup.Faux.SetResponses([]ai.FauxResponseStep{tlDone()})
		ownSubmitAndWait(t, first.root, "back")
		entries = allEntries(t, first.root)
		systems = tlSystems(entries)
		if got := tlAddedNames(systems[len(systems)-1]); !reflect.DeepEqual(got, []string{"echo"}) {
			t.Fatalf("toolsAdded %v, want [echo]", got)
		}
		mustClose(t, first.harness)
	})

	t.Run("produces tool_unavailable when the implementation is unregistered before its task runs", func(t *testing.T) {
		setup := chatSetup(t)
		second := &syncValue[installed]{}
		addTool(t, setup.Registry, tlTool("first", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			registration, _ := second.get()
			registration.dispose()
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
		}, func(tool *durable.ToolRegistration) { tool.ExecutionMode = durable.ToolExecutionSequential }))
		second.put(addTool(t, setup.Registry, tlTool("second", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return ownText("ran"), nil
		})))
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"first", map[string]any{}, "c1"}, tlCall{"second", map[string]any{}, "c2"}), tlDone()}, nil)
		if run.status != durable.SubmissionDone {
			t.Fatalf("status %s, want done", run.status)
		}
		tlExpectText(t, tlResultText(tlResults(run.entries)[1]), "<harness>\n[error] Tool second is not available\n</harness>")
		mustClose(t, run.harness)
	})

	t.Run("reads the execution mode when a round starts and keeps it for the round", func(t *testing.T) {
		setup := chatSetup(t)
		events := &syncList[string]{}
		slow := func(name string) tlExecute {
			return func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				events.add("start " + name)
				// Changed after the round started: not seen by this round.
				tlSetExecution(setup, durable.ToolExecutionParallel)
				time.Sleep(20 * time.Millisecond)
				events.add("end " + name)
				return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
			}
		}
		addTool(t, setup.Registry, tlTool("a", slow("a")))
		addTool(t, setup.Registry, tlTool("b", slow("b")))
		// Changed while the model request runs: the round that follows uses it.
		request := ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.FauxResponse, error) {
			tlSetExecution(setup, durable.ToolExecutionSequential)
			return tlCalls(tlCall{"a", map[string]any{}, "c1"}, tlCall{"b", map[string]any{}, "c2"}), nil
		})
		run := tlRun(t, setup, []ai.FauxResponseStep{request, tlDone()}, nil)
		if want := []string{"start a", "end a", "start b", "end b"}; !reflect.DeepEqual(events.all(), want) {
			t.Fatalf("events %v, want %v", events.all(), want)
		}
		mustClose(t, run.harness)
	})

	t.Run("runs a round in parallel by default and sequentially when configured or required by a tool", func(t *testing.T) {
		trace := func(setup *chatState) []string {
			events := &syncList[string]{}
			slow := func(name string) tlExecute {
				return func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					events.add("start " + name)
					time.Sleep(20 * time.Millisecond)
					events.add("end " + name)
					return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
				}
			}
			if setup.Registry.Snapshot().Extension("tool:a") == nil {
				addTool(t, setup.Registry, tlTool("a", slow("a")))
			}
			if setup.Registry.Snapshot().Extension("tool:b") == nil {
				addTool(t, setup.Registry, tlTool("b", slow("b")))
			}
			run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"a", map[string]any{}, "c1"}, tlCall{"b", map[string]any{}, "c2"}), tlDone()}, nil)
			mustClose(t, run.harness)
			return events.all()
		}
		// Upstream starts the tools of a round in call order; goroutines start in no fixed order, so only the pair is asserted.
		if got := sortedStrings(trace(chatSetup(t))[:2]); !reflect.DeepEqual(got, []string{"start a", "start b"}) {
			t.Fatalf("default round %v, want both tools started first", got)
		}
		configured := chatSetup(t)
		tlSetExecution(configured, durable.ToolExecutionSequential)
		if got, want := trace(configured), []string{"start a", "end a", "start b", "end b"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("sequential round %v, want %v", got, want)
		}
		perTool := chatSetup(t)
		events := &syncList[string]{}
		addTool(t, perTool.Registry, tlTool("a", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			time.Sleep(20 * time.Millisecond)
			events.add("a")
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
		}, func(tool *durable.ToolRegistration) { tool.ExecutionMode = durable.ToolExecutionSequential }))
		addTool(t, perTool.Registry, tlTool("b", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			events.add("b")
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
		}))
		run := tlRun(t, perTool, []ai.FauxResponseStep{tlCallsStep(tlCall{"a", map[string]any{}, "c1"}, tlCall{"b", map[string]any{}, "c2"}), tlDone()}, nil)
		tasks := tlScanTasks(t, run.harness, run.root.Id())
		toolTasks := tlToolTasks(tasks)
		slices.SortFunc(toolTasks, func(a, b durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) int {
			return int(a.Id - b.Id)
		})
		// The generation owns both tools and creates the second only after the first ended.
		var generation durable.TaskId
		for _, task := range tasks {
			if task.Kind == "pi.generation" {
				generation = task.Id
				break
			}
		}
		owners := []durable.TaskId{}
		for _, task := range toolTasks {
			owners = append(owners, *task.Owner)
		}
		if !reflect.DeepEqual(owners, []durable.TaskId{generation, generation}) {
			t.Fatalf("tool owners %v, want the generation %d twice", owners, generation)
		}
		if !reflect.DeepEqual(events.all(), []string{"a", "b"}) {
			t.Fatalf("events %v, want [a b]", events.all())
		}
		mustClose(t, run.harness)
	})
}

func TestToolResults(t *testing.T) {
	t.Run("uses retained output and the last details when the result omits them, with diagnostics in order", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, tlTool("log", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			api.Output("line 1\n")
			api.Output([]byte("line 2\nline 3\n"))
			api.Diagnostic(durable.ToolDiagnostic{Severity: durable.SeverityInfo, Message: "from api"})
			if err := api.Details(testContext, map[string]any{"step": float64(1)}); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if err := api.Details(testContext, map[string]any{"step": float64(2)}); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return durable.ToolExecutionResult{Diagnostics: []durable.ToolDiagnostic{{Severity: durable.SeverityWarn, Message: "from result"}}}, nil
		}, func(tool *durable.ToolRegistration) {
			lines := 2
			tool.OutputLimits = &durable.ToolOutputLimits{MaxLines: &lines}
		}))
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"log", map[string]any{}, "c1"}), tlDone()}, nil)
		result := tlResults(run.entries)[0]
		if !reflect.DeepEqual(result.Details, map[string]any{"step": float64(2)}) {
			t.Fatalf("details %v, want step 2", result.Details)
		}
		tlExpectText(t, tlResultText(result), "line 1\nline 2\n|<harness>\n[info] from api\n[warn] from result\n[warn] Output truncated to its beginning: 1 lines, 7 bytes dropped\n</harness>")
		if got := tlDiagnosticCodes(tlFirstToolResultEntry(t, run.entries)); !reflect.DeepEqual(got, []string{"", "", "truncated"}) {
			t.Fatalf("diagnostic codes %v, want [  truncated]", got)
		}
		mustClose(t, run.harness)
	})

	t.Run("offers the tail window with the configured pace, not a head window, and accepts skipped output", func(t *testing.T) {
		// upstream: packages/durable/test/harness-tools.test.ts:328
		setup := chatSetup(t)
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Progress = &ProgressPolicyPatch{OutputIntervalMs: new(250.0)}
		})
		var mu sync.Mutex
		var windows []*env.ShellOutputWindow
		record := func(window *env.ShellOutputWindow) {
			mu.Lock()
			defer mu.Unlock()
			windows = append(windows, window)
		}
		addTool(t, setup.Registry, tlTool("tailed", func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			record(api.OutputWindow())
			api.Output("dropped\n")
			api.OutputSkipping("x\ny\n", env.ShellOutputSkip{Bytes: 8, Newlines: 1, EndsWithNewline: true})
			return durable.ToolExecutionResult{}, nil
		}, func(tool *durable.ToolRegistration) {
			lines := 1
			tool.OutputLimits = &durable.ToolOutputLimits{MaxLines: &lines, Retain: durable.RetainTail}
		}))
		addTool(t, setup.Registry, tlTool("headed", func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			record(api.OutputWindow())
			return durable.ToolExecutionResult{}, nil
		}, func(tool *durable.ToolRegistration) {
			lines := 1
			tool.OutputLimits = &durable.ToolOutputLimits{MaxLines: &lines}
		}))
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"tailed", map[string]any{}, "c1"}, tlCall{"headed", map[string]any{}, "c2"}), tlDone()}, nil)
		want := env.ShellOutputWindow{MaxBytes: 50 * 1024, MaxLines: 1, MinIntervalMs: 250, BytesPerSecond: 100 * 1024}
		mu.Lock()
		offered := slices.Clone(windows)
		mu.Unlock()
		if !slices.ContainsFunc(offered, func(window *env.ShellOutputWindow) bool { return window != nil && *window == want }) {
			t.Fatalf("windows %v, want one equal to %+v", offered, want)
		}
		if !slices.Contains(offered, nil) {
			t.Fatalf("windows %v, want a nil window for the head-retaining tool", offered)
		}
		// 8 bytes written, 8 skipped, then "x\n" dropped by the window: 3 lines, 18 bytes in all.
		var tailed ai.ToolResultMessage
		for _, result := range tlResults(run.entries) {
			if result.ToolCallID == "c1" {
				tailed = result
			}
		}
		tlExpectText(t, tlResultText(tailed), "y\n|<harness>\n[warn] Output truncated to its end: 3 lines, 18 bytes dropped\n</harness>")
		mustClose(t, run.harness)
	})

	t.Run("bounds explicit text content and keeps other content", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, tlTool("big", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{
				ai.TextContent{Text: "a\nb\n"}, ai.ImageContent{Data: "AAAA", MimeType: "image/png"}, ai.TextContent{Text: "c\nd\n"},
			}}, nil
		}, func(tool *durable.ToolRegistration) {
			lines := 2
			tool.OutputLimits = &durable.ToolOutputLimits{MaxLines: &lines, Retain: durable.RetainTail}
		}))
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"big", map[string]any{}, "c1"}), tlDone()}, nil)
		tlExpectText(t, tlResultText(tlResults(run.entries)[0]), "[image]|c\nd\n|<harness>\n[warn] Output truncated to its end: 2 lines, 4 bytes dropped\n</harness>")
		mustClose(t, run.harness)
	})

	t.Run("turns a throw into a tool_error result with the partial output", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, tlTool("fail", func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			api.Output("partial\n")
			return durable.ToolExecutionResult{}, errors.New("boom")
		}))
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"fail", map[string]any{}, "c1"}), tlDone()}, nil)
		if run.status != durable.SubmissionDone {
			t.Fatalf("status %s, want done", run.status)
		}
		result := tlResults(run.entries)[0]
		if !result.IsError {
			t.Fatal("the result is not an error")
		}
		tlExpectText(t, tlResultText(result), "partial\n|<harness>\n[error] boom\n</harness>")
		mustClose(t, run.harness)
	})

	t.Run("validates arguments before and after beforeTool and applies blocks and replacements", func(t *testing.T) {
		setup := chatSetup(t)
		seen := &syncList[string]{}
		addTool(t, setup.Registry, tlTool("echo", func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			text, _ := args.(map[string]any)["text"].(string)
			seen.add(text)
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
		}))
		addHooks(t, setup.Registry, ToolTask, &ToolHooks{BeforeTool: func(_ context.Context, call ai.ToolCall, _ HookApi) (*BeforeToolResult, error) {
			switch call.ID {
			case "block":
				reason := "not today"
				return &BeforeToolResult{Block: &reason}, nil
			case "throw":
				return nil, errors.New("hook failed")
			case "bad":
				return &BeforeToolResult{Arguments: durable.JsonObject{"text": map[string]any{"not": "a string"}}}, nil
			}
			return &BeforeToolResult{Arguments: durable.JsonObject{"text": fmt.Sprintf("%v!", call.Arguments["text"])}}, nil
		}})
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(
			tlCall{"echo", map[string]any{"text": float64(1)}, "coerced"},
			tlCall{"echo", map[string]any{"text": map[string]any{"not": "a string"}}, "invalid"},
			tlCall{"echo", map[string]any{}, "block"},
			tlCall{"echo", map[string]any{}, "throw"},
			tlCall{"echo", map[string]any{"text": "x"}, "bad"},
			tlCall{"echo", map[string]any{"text": "x"}, "ok"},
		), tlDone()}, nil)
		// Parallel tools append their results in completion order.
		byId := tlResultById(run.entries)
		type outcome struct {
			isError bool
			text    string
		}
		got := func(id string) outcome { return outcome{byId[id].IsError, tlResultText(byId[id])} }
		if want := (outcome{true, "<harness>\n[error] Tool call blocked: not today\n</harness>"}); got("block") != want {
			t.Fatalf("block %+v, want %+v", got("block"), want)
		}
		if want := (outcome{true, "<harness>\n[error] Tool call blocked: hook failed\n</harness>"}); got("throw") != want {
			t.Fatalf("throw %+v, want %+v", got("throw"), want)
		}
		for _, id := range []string{"bad", "invalid"} {
			if !byId[id].IsError || !strings.Contains(tlResultText(byId[id]), "Validation failed") {
				t.Fatalf("%s result %+v, want a validation failure", id, got(id))
			}
		}
		if want := (outcome{false, ""}); got("ok") != want {
			t.Fatalf("ok %+v, want %+v", got("ok"), want)
		}
		// pi-ai coerces a number to a string before the first validation.
		if want := (outcome{false, ""}); got("coerced") != want {
			t.Fatalf("coerced %+v, want %+v", got("coerced"), want)
		}
		ran := sortedStrings(seen.all())
		if !reflect.DeepEqual(ran, []string{"1!", "x!"}) {
			t.Fatalf("tool ran with %v, want [1! x!]", ran)
		}
		mustClose(t, run.harness)
	})

	t.Run("repairs arguments with prepareArguments before validation, and a throwing repair is invalid", func(t *testing.T) {
		setup := chatSetup(t)
		seen := &syncList[string]{}
		addTool(t, setup.Registry, tlTool("echo", func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			text, _ := args.(map[string]any)["text"].(string)
			seen.add(text)
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
		}, func(tool *durable.ToolRegistration) {
			tool.PrepareArguments = func(args any) (any, error) {
				text := args.(durable.JsonObject)["text"]
				if text == "throw" {
					return nil, errors.New("cannot repair")
				}
				if number, ok := text.(float64); ok {
					return map[string]any{"text": fmt.Sprintf("#%v", number)}, nil
				}
				return args, nil
			}
		}))
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"echo", map[string]any{"text": float64(7)}, "fixed"}, tlCall{"echo", map[string]any{"text": "throw"}, "broken"}), tlDone()}, nil)
		byId := tlResultById(run.entries)
		tlExpectText(t, tlResultText(byId["fixed"]), "")
		tlExpectText(t, tlResultText(byId["broken"]), "<harness>\n[error] cannot repair\n</harness>")
		if !reflect.DeepEqual(seen.all(), []string{"#7"}) {
			t.Fatalf("tool ran with %v, want [#7]", seen.all())
		}
		// The stored call keeps what the model sent.
		var stored ai.ToolCall
		for _, block := range run.entries[2].Model[0].(ai.AssistantMessage).Content {
			if call, ok := block.(ai.ToolCall); ok {
				stored = call
				break
			}
		}
		if !reflect.DeepEqual(map[string]any(stored.Arguments), map[string]any{"text": float64(7)}) {
			t.Fatalf("stored arguments %v, want text 7", stored.Arguments)
		}
		mustClose(t, run.harness)
	})

	t.Run("lets the first beforeTool block win and skips later handlers", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, tlTool("echo", tlNoContent))
		asked := &syncList[string]{}
		block := func(label string) *ToolHooks {
			return &ToolHooks{BeforeTool: func(context.Context, ai.ToolCall, HookApi) (*BeforeToolResult, error) {
				asked.add(label)
				reason := label + " says no"
				return &BeforeToolResult{Block: &reason}, nil
			}}
		}
		addHooks(t, setup.Registry, ToolTask, block("first"))
		addHooks(t, setup.Registry, ToolTask, block("second"))
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"echo", map[string]any{}, "c1"}), tlDone()}, nil)
		if !reflect.DeepEqual(asked.all(), []string{"first"}) {
			t.Fatalf("asked %v, want [first]", asked.all())
		}
		tlExpectText(t, tlResultText(tlResults(run.entries)[0]), "<harness>\n[error] Tool call blocked: first says no\n</harness>")
		mustClose(t, run.harness)
	})

	t.Run("chains afterTool replacements and observes the round with afterTools", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, tlTool("echo", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return ownText("raw"), nil
		}))
		addHooks(t, setup.Registry, ToolTask, &ToolHooks{AfterTool: func(_ context.Context, _ ai.ToolCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
			result.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: "first"}}
			return &result, nil
		}})
		addHooks(t, setup.Registry, ToolTask, &ToolHooks{AfterTool: func(_ context.Context, _ ai.ToolCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
			text := ""
			for _, item := range result.Content {
				if content, ok := item.(ai.TextContent); ok {
					text += content.Text
				}
			}
			result.Details, result.HasDetails = map[string]any{"replaced": text}, true
			return &result, nil
		}})
		type observation struct {
			assistant durable.EntryId
			results   []durable.EntryId
		}
		observed := &syncList[observation]{}
		addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{AfterTools: func(_ context.Context, assistant durable.EntryId, results []durable.EntryId, _ HookApi) error {
			observed.add(observation{assistant, results})
			return nil
		}})
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"echo", map[string]any{}, "c1"}), tlDone()}, nil)
		result := tlResults(run.entries)[0]
		tlExpectText(t, tlResultText(result), "first")
		if !reflect.DeepEqual(result.Details, map[string]any{"replaced": "first"}) {
			t.Fatalf("details %v, want replaced: first", result.Details)
		}
		resultEntry := tlFirstToolResultEntry(t, run.entries)
		if want := []observation{{run.entries[2].Id, []durable.EntryId{resultEntry.Id}}}; !reflect.DeepEqual(observed.all(), want) {
			t.Fatalf("afterTools observed %+v, want %+v", observed.all(), want)
		}
		mustClose(t, run.harness)
	})

	t.Run("runs the hooks of the selected extensions; a task-owned child copies its owner's selection", func(t *testing.T) {
		setup := chatSetup(t)
		calledIn := &syncList[durable.ConversationId]{}
		echo := new(durable.Extension{Name: "echo", Tools: []*durable.ToolRegistration{tlTool("echo", tlNoContent)}})
		audit := new(durable.Extension{Name: "audit", Hooks: []durable.HookRegistration{Hook(ToolTask, &ToolHooks{BeforeTool: func(_ context.Context, _ ai.ToolCall, api HookApi) (*BeforeToolResult, error) {
			calledIn.add(api.ConversationId())
			return nil, nil
		}})}})
		mustInstall(t, setup.Registry, echo, audit)
		// Audit is installed but not in the default selection.
		tlSetExtensions(setup, []*durable.Extension{echo})
		first := tlRun(t, setup, []ai.FauxResponseStep{tlDone()}, nil)
		mustConfigure(t, first.root, AgentChange{Extensions: SetTo(ExtensionChange{Add: []*durable.Extension{audit}})})
		// An owner task no registered definition takes stays live and pending.
		type ownerInput struct{}
		owner := durable.DefineTask(durable.TaskDefinition[ownerInput, stepState, durable.JsonValue, any]{
			Name:    "test.owner",
			Version: 1,
			Initial: func(ownerInput) stepState { return stepState{Phase: "never"} },
			Phases: map[string]durable.PhaseHandler[ownerInput, stepState, durable.JsonValue, any]{
				"never": func(context.Context, durable.RunningTask[ownerInput, stepState, durable.JsonValue], durable.TaskRuntime[ownerInput, stepState, durable.JsonValue, any]) error {
					return nil
				},
			},
			Abort: tkNoopAbort[ownerInput, stepState, durable.JsonValue, any],
		})
		rootId := first.root.Id()
		childId, err := durable.Commit(testContext, first.harness, func(tx durable.Tx) (durable.ConversationId, error) {
			taskId, err := durable.CreateTask(tx, owner, ownerInput{}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &rootId})
			if err != nil {
				return 0, err
			}
			created, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: taskId}})
			return created.Id, err
		})
		if err != nil {
			t.Fatal(err)
		}
		child := ownConversation(t, first.harness, childId)
		other, err := first.harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless, Agent: &AgentChange{Model: SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
		if err != nil {
			t.Fatal(err)
		}
		for _, conversation := range []Conversation{first.root, child, other} {
			setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"echo", map[string]any{}, "c1"}), tlDone()})
			ownSubmitAndWait(t, conversation, "go")
		}
		if want := []durable.ConversationId{first.root.Id(), child.Id()}; !reflect.DeepEqual(calledIn.all(), want) {
			t.Fatalf("hook ran in %v, want %v", calledIn.all(), want)
		}
		mustClose(t, first.harness)
	})

	t.Run("applies addTools and terminates only when every result of the round asks to", func(t *testing.T) {
		setup := chatSetup(t)
		stop := tlTool("stop", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}, Control: &durable.ToolControl{Terminate: true}}, nil
		})
		grow := tlTool("grow", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}, Control: &durable.ToolControl{AddTools: []string{"extra", "stop"}}}, nil
		})
		addTool(t, setup.Registry, stop)
		addTool(t, setup.Registry, grow)
		addTool(t, setup.Registry, tlTool("extra", tlNoContent))
		first := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"stop", map[string]any{}, "c1"})}, func(_ Harness, root Conversation) {
			mustConfigure(t, root, AgentChange{Tools: SetTo(ToolChange{Exact: true, List: []*durable.ToolRegistration{stop, grow}})})
		})
		if first.status != durable.SubmissionDone {
			t.Fatalf("status %s, want done", first.status)
		}
		if last := first.entries[len(first.entries)-1].Kind; last != "pi.tool-result" {
			t.Fatalf("last entry %s, want pi.tool-result", last)
		}
		for _, task := range tlScanTasks(t, first.harness, first.root.Id()) {
			if task.State.Status != durable.TaskTerminal {
				t.Fatalf("task %d is %s, want terminal", task.Id, task.State.Status)
			}
		}

		setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"stop", map[string]any{}, "c1"}, tlCall{"grow", map[string]any{}, "c2"}), tlDone()})
		if second := ownSubmitAndWait(t, first.root, "again"); second.Status != durable.SubmissionDone {
			t.Fatalf("second input %s, want done", second.Status)
		}
		entries := allEntries(t, first.root)
		if last := entries[len(entries)-1].Kind; last != "pi.assistant" {
			t.Fatalf("last entry %s, want pi.assistant", last)
		}
		// addTools appends to the stored tool array, skipping names it already holds.
		state, err := durable.Snapshot[AgentState](testContext, first.harness, AgentDoc, first.root.Id())
		if err != nil || state == nil || state.Tools == nil || !reflect.DeepEqual(state.Tools.Names, []string{"stop", "grow", "extra"}) {
			t.Fatalf("stored tools %+v %v, want [stop grow extra]", state, err)
		}
		mustClose(t, first.harness)
	})
}

func tlRoleText(message ai.Message) string {
	text, _ := textOf(message)
	switch message.(type) {
	case ai.UserMessage:
		return "user:" + text
	case ai.AssistantMessage:
		return "assistant:" + text
	case ai.ToolResultMessage:
		return "toolResult:" + text
	case ai.SystemMessage:
		return "system:" + text
	}
	return text
}

func TestGenerationHooks(t *testing.T) {
	t.Run("replaces request messages, observes responses, and continues on yield", func(t *testing.T) {
		setup := chatSetup(t)
		requests := &syncList[[]string]{}
		record := ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.FauxResponse, error) {
			rendered := []string{}
			for _, message := range request.Messages() {
				rendered = append(rendered, tlRoleText(message))
			}
			requests.add(rendered)
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(fmt.Sprintf("answer %d", requests.len()))}}, nil
		})
		addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{BeforeRequest: func(_ context.Context, request GenerationRequest, _ HookApi) (*GenerationRequest, error) {
			messages := append(slices.Clone(request.Messages), ai.UserMessage{Content: ai.UserText("injected"), Timestamp: 0})
			return &GenerationRequest{Messages: messages}, nil
		}})
		responses := &syncList[string]{}
		addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{AfterResponse: func(_ context.Context, message ai.AssistantMessage, _ HookApi) error {
			text, _ := textOf(message)
			responses.add(text)
			return nil
		}})
		var yields atomic.Int32
		addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{OnYield: func(context.Context, ai.AssistantMessage, HookApi) (*YieldContinue, error) {
			if yields.Add(1) == 1 {
				return &YieldContinue{Continue: ai.UserContentBlocks{ai.TextContent{Text: "keep going"}}}, nil
			}
			return nil, nil
		}})
		run := tlRun(t, setup, []ai.FauxResponseStep{record, record}, nil)
		if run.status != durable.SubmissionDone {
			t.Fatalf("status %s, want done", run.status)
		}
		first := requests.all()[0]
		if got := first[len(first)-2:]; !reflect.DeepEqual(got, []string{"user:go", "user:injected"}) {
			t.Fatalf("request tail %v, want [user:go user:injected]", got)
		}
		if !reflect.DeepEqual(responses.all(), []string{"answer 1", "answer 2"}) {
			t.Fatalf("responses %v", responses.all())
		}
		kinds := []string{}
		for _, entry := range run.entries {
			kinds = append(kinds, entry.Kind)
		}
		if want := []string{"pi.user", "pi.assistant", "pi.user", "pi.assistant"}; !reflect.DeepEqual(kinds, want) {
			t.Fatalf("entries %v, want %v", kinds, want)
		}
		// The injected message was used for the request only.
		for _, entry := range run.entries {
			if len(entry.Model) > 0 {
				if text, _ := textOf(entry.Model[0]); text == "injected" {
					t.Fatal("the injected message was stored")
				}
			}
		}
		mustClose(t, run.harness)
	})

	t.Run("keeps the run's input open across an onYield continuation and answers it with the final answer", func(t *testing.T) {
		setup := chatSetup(t)
		var yields atomic.Int32
		harnessRef := &syncValue[Harness]{}
		input := &syncValue[durable.SubmissionId]{}
		statusAtSecondRequest := &syncValue[durable.SubmissionStatus]{}
		addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{OnYield: func(context.Context, ai.AssistantMessage, HookApi) (*YieldContinue, error) {
			if yields.Add(1) == 1 {
				return &YieldContinue{Continue: ai.UserText("again")}, nil
			}
			return nil, nil
		}})
		second := ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.FauxResponse, error) {
			harness, _ := harnessRef.get()
			live, err := durable.Snapshot[LiveState](testContext, harness, LiveDoc, durable.ConversationId(1))
			if err != nil || live == nil || live.Run == nil || len(live.Run.Inputs) == 0 {
				return ai.FauxResponse{}, fmt.Errorf("live run %+v: %w", live, err)
			}
			input.put(live.Run.Inputs[0])
			statusAtSecondRequest.put(ownSubmissionStatus(t, harness, live.Run.Inputs[0]).Status)
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("second")}}, nil
		})
		run := tlRun(t, setup, []ai.FauxResponseStep{fauxAnswer("first"), second}, func(harness Harness, _ Conversation) { harnessRef.put(harness) })
		if status, _ := statusAtSecondRequest.get(); status != durable.SubmissionPlaced {
			t.Fatalf("input at the second request is %s, want placed", status)
		}
		var answers []durable.EntryRecord
		for _, entry := range run.entries {
			if entry.Kind == "pi.assistant" {
				answers = append(answers, entry)
			}
		}
		id, _ := input.get()
		record := ownSubmissionStatus(t, run.harness, id)
		if record.Status != durable.SubmissionDone || record.Answer == nil || *record.Answer != answers[1].Id {
			t.Fatalf("input %+v, want done with answer %d", record, answers[1].Id)
		}
		mustClose(t, run.harness)
	})

	t.Run("observes responses that arrive by polling a deferred request", func(t *testing.T) {
		setup := chatSetup(t, ai.FauxConfig{Deferred: &ai.FauxDeferredConfig{PendingFetches: 1, PollAfterMS: new(int64(1))}})
		observed := &syncList[string]{}
		addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{AfterResponse: func(_ context.Context, message ai.AssistantMessage, _ HookApi) error {
			text, _ := textOf(message)
			observed.add(fmt.Sprintf("%s:%s", message.StopReason, text))
			return nil
		}})
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Stream = &durable.ConversationStreamOptions{Deferred: &ai.DeferredOption{Enabled: true}}
		})
		run := tlRun(t, setup, []ai.FauxResponseStep{fauxAnswer("late")}, nil)
		if run.status != durable.SubmissionDone {
			t.Fatalf("status %s, want done", run.status)
		}
		// The still-deferred results are not terminal.
		if !reflect.DeepEqual(observed.all(), []string{"stop:late"}) {
			t.Fatalf("observed %v, want [stop:late]", observed.all())
		}
		mustClose(t, run.harness)
	})

	t.Run("lets the first onYield continuation win and reports throws without stopping later handlers", func(t *testing.T) {
		setup := chatSetup(t)
		called := &syncList[string]{}
		addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{AfterResponse: func(context.Context, ai.AssistantMessage, HookApi) error {
			called.add("throwing observer")
			return errors.New("observer failed")
		}})
		addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{AfterResponse: func(context.Context, ai.AssistantMessage, HookApi) error {
			called.add("next observer")
			return nil
		}})
		var yields atomic.Int32
		addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{OnYield: func(context.Context, ai.AssistantMessage, HookApi) (*YieldContinue, error) {
			if yields.Add(1) == 1 {
				return &YieldContinue{Continue: ai.UserText("first")}, nil
			}
			return nil, nil
		}})
		addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{OnYield: func(context.Context, ai.AssistantMessage, HookApi) (*YieldContinue, error) {
			called.add("second onYield")
			count := 0
			for _, name := range called.all() {
				if name == "second onYield" {
					count++
				}
			}
			if count == 1 {
				return &YieldContinue{Continue: ai.UserText("second")}, nil
			}
			return nil, nil
		}})
		run := tlRun(t, setup, []ai.FauxResponseStep{fauxAnswer("a"), fauxAnswer("b"), fauxAnswer("c")}, nil)
		// The first continuation skips the second handler; on the next answer the second handler's continuation wins.
		users := []string{}
		for _, entry := range run.entries {
			if entry.Kind == "pi.user" {
				text, _ := textOf(entry.Model[0])
				users = append(users, text)
			}
		}
		if !reflect.DeepEqual(users, []string{"go", "first", "second"}) {
			t.Fatalf("users %v, want [go first second]", users)
		}
		count := func(name string) int {
			total := 0
			for _, candidate := range called.all() {
				if candidate == name {
					total++
				}
			}
			return total
		}
		if count("second onYield") != 2 || count("next observer") != 3 {
			t.Fatalf("calls %v, want two second onYield and three next observer", called.all())
		}
		messages := []string{}
		for _, report := range setup.Reports.all() {
			messages = append(messages, report.Error())
		}
		if !slices.Contains(messages, "observer failed") {
			t.Fatalf("reports %v do not include the observer failure", messages)
		}
		mustClose(t, run.harness)
	})

	t.Run("keeps durable hook decisions in task memos", func(t *testing.T) {
		setup := chatSetup(t)
		var asked atomic.Int32
		addTool(t, setup.Registry, tlTool("echo", tlNoContent))
		addHooks(t, setup.Registry, ToolTask, &ToolHooks{BeforeTool: func(ctx context.Context, _ ai.ToolCall, api HookApi) (*BeforeToolResult, error) {
			asked.Add(1)
			decision, err := api.MemoCandidate(ctx, "approval:decision", "approved")
			if err != nil {
				return nil, err
			}
			again, err := api.MemoCandidate(ctx, "approval:decision", "denied")
			if err != nil {
				return nil, err
			}
			if again != decision {
				return nil, fmt.Errorf("memo %v then %v", decision, again)
			}
			return nil, nil
		}})
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"echo", map[string]any{}, "c1"}), tlDone()}, nil)
		if asked.Load() != 1 {
			t.Fatalf("beforeTool ran %d times, want 1", asked.Load())
		}
		// A memo failure would block the call.
		if result := tlResults(run.entries)[0]; result.IsError {
			t.Fatalf("result %+v is an error", result)
		}
		mustClose(t, run.harness)
	})
}

func TestToolExecutionApi(t *testing.T) {
	t.Run("builds the environment per call from the conversation's cwd and runs commits, memos, and child tasks", func(t *testing.T) {
		setup := chatSetup(t)
		type childInput struct {
			N int `json:"n"`
		}
		type childRuntime = durable.TaskRuntime[childInput, stepState, durable.JsonValue, any]
		type childRecord = durable.RunningTask[childInput, stepState, durable.JsonValue]
		child := durable.DefineTask(durable.TaskDefinition[childInput, stepState, durable.JsonValue, any]{
			Name:    "test.child",
			Version: 1,
			Initial: func(childInput) stepState { return stepState{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[childInput, stepState, durable.JsonValue, any]{
				"run": func(ctx context.Context, task childRecord, runtime childRuntime) error {
					return runtime.Commit(ctx, func(durable.Tx, childRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						return completed[stepState, durable.JsonValue](float64(task.Input.N * 2)), nil
					})
				},
			},
			Abort: tkNoopAbort[childInput, stepState, durable.JsonValue, any],
		})
		addTask(t, setup.Registry, child)
		seen := &syncList[any]{}
		probe := tlTool("probe", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			seen.add(api.Env().Cwd())
			agent, err := api.Agent(testContext)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			names := []string{}
			for _, each := range agent.Tools {
				names = append(names, each.Name)
			}
			seen.add(names)
			seen.add(api.Registry().Extension("tool:probe") != nil)
			value, err := api.Commit(testContext, func(tx durable.Tx) (any, error) {
				// The next call runs in the new directory: its environment is built when it executes.
				if err := Configure(tx, api.ConversationId(), AgentChange{Cwd: SetTo("/")}); err != nil {
					return nil, err
				}
				return tx.AppendEntry(api.ConversationId(), durable.EntryDraft{Kind: "test.note", Data: api.CallId()})
			})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			entry := value.(durable.EntryRecord)
			seen.add(entry.ByTaskId != nil && *entry.ByTaskId == api.TaskId())
			first, err := api.MemoCandidate(testContext, "m", float64(1))
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			second, err := api.MemoCandidate(testContext, "m", float64(2))
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			seen.add(first)
			seen.add(second)
			input, _ := durable.ToJsonValue(childInput{N: 21})
			id, err := api.CreateTaskErased(testContext, child, input, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			done, err := api.WaitForTask(testContext, id)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			seen.add(describeOutcome(*done.State.Outcome))
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
		})
		addTool(t, setup.Registry, probe)
		setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"probe", map[string]any{}, "c1"}, tlCall{"probe", map[string]any{}, "c2"}), tlDone()})
		tlSetExecution(setup, durable.ToolExecutionSequential)
		type target struct {
			conversation durable.ConversationId
			cwd          string
		}
		targets := &syncList[target]{}
		harness, root := openChat(t, storage.NewMemoryStorage(), setup, openChatOptions{envFunc: func(_ context.Context, request EnvTarget) (env.ExecutionEnv, error) {
			cwd := "/tmp"
			if request.Cwd != nil {
				cwd = *request.Cwd
			}
			targets.add(target{request.ConversationId, func() string {
				if request.Cwd == nil {
					return ""
				}
				return *request.Cwd
			}()})
			return envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: cwd}), nil
		}})
		mustConfigure(t, root, AgentChange{Cwd: SetTo("/tmp")})
		ownSubmitAndWait(t, root, "go")
		call := func(cwd string) []any {
			return []any{cwd, []string{"probe"}, true, true, float64(1), float64(1), "completed result=42"}
		}
		want := append(call("/tmp"), call("/")...)
		if !reflect.DeepEqual(seen.all(), want) {
			t.Fatalf("seen %v, want %v", seen.all(), want)
		}
		if !slices.Contains(targets.all(), target{root.Id(), "/tmp"}) || !slices.Contains(targets.all(), target{root.Id(), "/"}) {
			t.Fatalf("env targets %v, want both /tmp and /", targets.all())
		}
		mustClose(t, harness)
	})

	t.Run("answers a throwing environment with a tool_error result and reports it once while preparing", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, tlTool("probe", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return ownText("ran"), nil
		}))
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"probe", map[string]any{}, "c1"}), tlDone()}, nil, openChatOptions{envFunc: func(context.Context, EnvTarget) (env.ExecutionEnv, error) {
			return nil, errors.New("no sandbox")
		}})
		result := tlResults(run.entries)[0]
		if !result.IsError {
			t.Fatal("the result is not an error")
		}
		tlExpectText(t, tlResultText(result), "<harness>\n[error] no sandbox\n</harness>")
		// Each preparation reports the failure and renders without an environment.
		count := 0
		for _, report := range setup.Reports.all() {
			if report.Error() == "no sandbox" {
				count++
			}
		}
		if count != 2 {
			t.Fatalf("%d reports of the environment failure, want 2", count)
		}
		mustClose(t, run.harness)
	})
}
