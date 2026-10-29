// Ports packages/durable/test/harness-tool-hooks.test.ts: `afterTool` runs on every result a tool task writes, also those
// the Harness decides without executing the tool (#11043), and a call to a tool the request did not offer runs as a
// flagged tool task. See harness_tasks_test.go for the Go mappings that apply to every case. The crash cases (`kill()` in
// the TS file) run over a Storage that hangs every call once killed, thKillable; the dead Harness is never closed against
// the file until the test ends.

package harness

// pi: packages/durable/src/harness/tool.ts

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

var errThKilled = errors.New("storage killed")

// thKillable is a Storage whose every call but MintId hangs once killed, as if its process died: it never answers, and it is
// released only when the test ends.
type thKillable struct {
	durable.Storage
	dead atomic.Bool
	hang chan struct{}
}

// hangs reports whether the storage was killed, after blocking until the test ended.
func (store *thKillable) hangs() bool {
	if !store.dead.Load() {
		return false
	}
	<-store.hang
	return true
}

func (store *thKillable) Commit(ctx context.Context, writes []durable.StorageWrite, adopt func(seq durable.Seq)) (durable.Seq, error) {
	if store.hangs() {
		return 0, errThKilled
	}
	return store.Storage.Commit(ctx, writes, adopt)
}

func (store *thKillable) Conversation(ctx context.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	if store.hangs() {
		return nil, errThKilled
	}
	return store.Storage.Conversation(ctx, id)
}

func (store *thKillable) ScanConversations(ctx context.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	if store.hangs() {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, errThKilled
	}
	return store.Storage.ScanConversations(ctx, query, limit, cursor)
}

func (store *thKillable) Entry(ctx context.Context, id durable.EntryId) (*durable.EntryAt, error) {
	if store.hangs() {
		return nil, errThKilled
	}
	return store.Storage.Entry(ctx, id)
}

func (store *thKillable) VisibleEntry(ctx context.Context, conversationId durable.ConversationId, id durable.EntryId) (*durable.EntryAt, error) {
	if store.hangs() {
		return nil, errThKilled
	}
	return store.Storage.VisibleEntry(ctx, conversationId, id)
}

func (store *thKillable) FindLatestHeadMarker(ctx context.Context, conversationId durable.ConversationId, atOrBeforeEntryId *durable.EntryId) (*durable.EntryRecord, error) {
	if store.hangs() {
		return nil, errThKilled
	}
	return store.Storage.FindLatestHeadMarker(ctx, conversationId, atOrBeforeEntryId)
}

func (store *thKillable) ScanEntries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	if store.hangs() {
		return durable.Page[durable.EntryRecord, durable.Cursor]{}, errThKilled
	}
	return store.Storage.ScanEntries(ctx, query, limit, cursor)
}

func (store *thKillable) Task(ctx context.Context, id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
	if store.hangs() {
		return nil, errThKilled
	}
	return store.Storage.Task(ctx, id)
}

func (store *thKillable) ScanTasks(ctx context.Context, query durable.TaskQuery, limit int, cursor durable.Cursor) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
	if store.hangs() {
		return durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor]{}, errThKilled
	}
	return store.Storage.ScanTasks(ctx, query, limit, cursor)
}

func (store *thKillable) Submission(ctx context.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	if store.hangs() {
		return nil, errThKilled
	}
	return store.Storage.Submission(ctx, id)
}

func (store *thKillable) ScanSubmissions(ctx context.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	if store.hangs() {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, errThKilled
	}
	return store.Storage.ScanSubmissions(ctx, query, limit, cursor)
}

func (store *thKillable) SubmissionByRequest(ctx context.Context, conversationId durable.ConversationId, requestId string) (*durable.SubmissionRecord, error) {
	if store.hangs() {
		return nil, errThKilled
	}
	return store.Storage.SubmissionByRequest(ctx, conversationId, requestId)
}

func (store *thKillable) FindDocument(ctx context.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	if store.hangs() {
		return nil, errThKilled
	}
	return store.Storage.FindDocument(ctx, address, at)
}

func (store *thKillable) Document(ctx context.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	if store.hangs() {
		return nil, errThKilled
	}
	return store.Storage.Document(ctx, id, at)
}

func (store *thKillable) ScanDocuments(ctx context.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	if store.hangs() {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, errThKilled
	}
	return store.Storage.ScanDocuments(ctx, query, limit, cursor)
}

func (store *thKillable) Close(ctx context.Context) error {
	if store.hangs() {
		return errThKilled
	}
	return store.Storage.Close(ctx)
}

// thOpened is a chat over a SQLite file; kill simulates a crash: every later storage call of this Harness hangs.
type thOpened struct {
	harness Harness
	root    Conversation
	kill    func()
}

// thOpen opens a chat over the SQLite file at path and resumes its Harness.
func thOpen(t *testing.T, path string, setup *chatState) thOpened {
	t.Helper()
	inner := tkOpenSqlite(t, path)
	store := &thKillable{Storage: inner, hang: make(chan struct{})}
	harness, root := openChat(t, store, setup)
	harness.Resume()
	kill := func() {
		store.dead.Store(true)
		// Storages of killed Harnesses are never closed by them: the test closes them when it ends.
		t.Cleanup(func() {
			close(store.hang)
			go func() { _ = harness.Close(context.Background()) }()
			_ = inner.Close(context.Background())
		})
	}
	return thOpened{harness: harness, root: root, kill: kill}
}

// thStored lets deferred progress commits (`persistWithinMs: 0`) reach Storage before a crash.
func thStored() { time.Sleep(50 * time.Millisecond) }

var thParameters = echoParameters

func thTool(name string, execute tlExecute, edit ...func(*durable.ToolRegistration)) *durable.ToolRegistration {
	return ownTool(name, name, thParameters, execute, edit...)
}

func thEmpty(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
	return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
}

// thCalls is a tool-calling faux answer with one call per entry.
func thCalls(list ...tlCall) ai.FauxResponseStep { return tlCallsStep(list...) }

func thResults(entries []durable.EntryRecord) map[string]ai.ToolResultMessage {
	return tlResultById(entries)
}

func thText(message ai.ToolResultMessage) string { return tlResultText(message) }

func thResultCodes(diagnostics []durable.ToolDiagnostic) []string {
	codes := []string{}
	for _, diagnostic := range diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return codes
}

// thDiagnosticsOf is the diagnostic codes of the data of the result entry of call callId.
func thDiagnosticsOf(entries []durable.EntryRecord, callId string) []string {
	for index := range entries {
		if !durable.ToolResultEntry.Is(&entries[index]) || entries[index].Model[0].(ai.ToolResultMessage).ToolCallID != callId {
			continue
		}
		codes := []string{}
		diagnostics, _ := plainObject(entries[index].Data)["diagnostics"].([]any)
		for _, diagnostic := range diagnostics {
			code, _ := diagnostic.(map[string]any)["code"].(string)
			codes = append(codes, code)
		}
		return codes
	}
	return []string{}
}

type thSeen struct {
	call   ToolHookCall
	result durable.ToolExecutionResult
}

// thSeenCodes is "id: codes" for each seen result, sorted.
func thSeenCodes(seen []thSeen) []string {
	lines := []string{}
	for _, each := range seen {
		lines = append(lines, each.call.ID+": "+strings.Join(thResultCodes(each.result.Diagnostics), ","))
	}
	slices.Sort(lines)
	return lines
}

func thExpectEqual[T comparable](t *testing.T, name string, got, want []T) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s %v, want %v", name, got, want)
	}
}

func thToolTaskId(t *testing.T, harness Harness, root Conversation, index int) durable.TaskId {
	t.Helper()
	var id durable.TaskId
	waitFor(t, func() bool {
		live := liveOf(t, harness, root.Id())
		if live == nil || len(live.Tools) <= index || live.Tools[index].TaskId == nil {
			return false
		}
		id = *live.Tools[index].TaskId
		return true
	})
	return id
}

func thWait(t *testing.T, submission durable.Submission) durable.SubmissionStatus {
	t.Helper()
	settled, err := submission.Wait(testContext)
	if err != nil {
		t.Fatal(err)
	}
	return settled.Status
}

func thSubmitted(t *testing.T, harness Harness, id durable.SubmissionId) durable.Submission {
	t.Helper()
	submission, err := harness.Submission(testContext, id)
	if err != nil || submission == nil {
		t.Fatalf("Submission(%d) %v %v", id, submission, err)
	}
	return submission
}

func thExpectStatus(t *testing.T, got, want durable.SubmissionStatus) {
	t.Helper()
	if got != want {
		t.Fatalf("status %s, want %s", got, want)
	}
}

func thReplaceOutput(result durable.ToolExecutionResult, text string) durable.ToolExecutionResult {
	result.Output = []ai.ToolResultMessageContent{ai.TextContent{Text: text}}
	return result
}

// thBlocking is a tool that writes output, then blocks until its invocation is cancelled.
type thBlocking struct {
	registration *durable.ToolRegistration
	started      *deferredGate
	runs         atomic.Int32
}

func thBlockingTool(name string, edit ...func(*durable.ToolRegistration)) *thBlocking {
	blocking := &thBlocking{started: deferred()}
	blocking.registration = thTool(name, func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		run := blocking.runs.Add(1)
		api.Output(name + " " + strings.Repeat("o", 40) + "\n")
		if err := api.Details(ctx, map[string]any{"run": float64(run)}); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		blocking.started.resolve()
		return durable.ToolExecutionResult{}, abortedBy(ctx)
	}, edit...)
	return blocking
}

func thHasDetails(t *testing.T, message ai.ToolResultMessage, want map[string]any) {
	t.Helper()
	if !message.IsError || !reflect.DeepEqual(plainObject(message.Details), want) {
		t.Fatalf("result %+v, want an error with details %v", message, want)
	}
}

func thProgress(setup *chatState, persistWithinMs float64) {
	setup.SetSettings(func(settings *HarnessSettings) {
		settings.Progress = &ProgressPolicyPatch{OutputIntervalMs: new(0.0), PersistWithinMs: new(persistWithinMs)}
	})
}

func TestAfterToolOnResultsTheCallPhaseDecides(t *testing.T) {
	// harness-tool-hooks.test.ts:135 it("sees and replaces unavailable, invalid, and blocked results, with the raw arguments") (#11043)
	t.Run("sees and replaces unavailable, invalid, and blocked results, with the raw arguments", func(t *testing.T) {
		setup := chatSetup(t)
		var ran atomic.Int32
		echo := thTool("echo", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			ran.Add(1)
			return ownText("ran"), nil
		})
		addTool(t, setup.Registry, echo)
		addTool(t, setup.Registry, thTool("gone", thEmpty))
		seen := &syncList[thSeen]{}
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{
			BeforeTool: func(_ context.Context, call ToolHookCall, _ HookApi) (*BeforeToolResult, error) {
				if call.ID == "block" {
					return &BeforeToolResult{Block: new("not today")}, nil
				}
				if call.ID == "rewrite" {
					return &BeforeToolResult{Arguments: map[string]any{"text": map[string]any{"not": "a string"}}}, nil
				}
				return nil, nil
			},
			AfterTool: func(_ context.Context, call ToolHookCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
				seen.add(thSeen{call, result})
				replaced := thReplaceOutput(result, "hooked "+call.ID)
				replaced.Details, replaced.HasDetails = map[string]any{"by": "hook"}, true
				return &replaced, nil
			},
		})
		root := &syncValue[Conversation]{}
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.AssistantMessage, error) {
				// The tool is offered but no longer resolves when its task runs.
				conversation, _ := root.get()
				if err := conversation.Configure(testContext, AgentChange{Tools: SetTo(ToolChange{List: []*durable.ToolRegistration{echo}})}); err != nil {
					return ai.FauxResponse{}.AssistantMessage(), err
				}
				return tlCalls(
					tlCall{"gone", map[string]any{}, "unavailable"},
					tlCall{"echo", map[string]any{"text": map[string]any{"not": "a string"}}, "invalid"},
					tlCall{"echo", map[string]any{"text": "x"}, "rewrite"},
					tlCall{"echo", map[string]any{}, "block"},
				).AssistantMessage(), nil
			}),
			tlDone(),
		})
		harness, conversation := openChat(t, storage.NewMemoryStorage(), setup)
		root.put(conversation)
		thExpectStatus(t, thWait(t, ownSubmit(t, conversation, ownInput("go"))), durable.SubmissionDone)
		if ran.Load() != 0 {
			t.Fatalf("the tool ran %d times, want 0", ran.Load())
		}
		byId := map[string]thSeen{}
		for _, each := range seen.all() {
			byId[each.call.ID] = each
		}
		ids := []string{}
		for id := range byId {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		thExpectEqual(t, "hooked call ids", ids, []string{"block", "invalid", "rewrite", "unavailable"})
		for id, code := range map[string]string{"unavailable": "tool_unavailable", "invalid": "invalid_arguments", "rewrite": "invalid_arguments", "block": "blocked"} {
			thExpectEqual(t, id+" codes", thResultCodes(byId[id].result.Diagnostics), []string{code})
		}
		// Hooks see the call as the model made it, also when it is invalid or `beforeTool` rewrote it.
		if got := plainObject(byId["invalid"].call.Arguments); !reflect.DeepEqual(got, map[string]any{"text": map[string]any{"not": "a string"}}) {
			t.Fatalf("invalid call arguments %v", got)
		}
		if got := plainObject(byId["rewrite"].call.Arguments); !reflect.DeepEqual(got, map[string]any{"text": "x"}) {
			t.Fatalf("rewritten call arguments %v", got)
		}
		messages := thResults(allEntries(t, conversation))
		for _, id := range []string{"unavailable", "invalid", "rewrite", "block"} {
			message := messages[id]
			thHasDetails(t, message, map[string]any{"by": "hook"})
			if !strings.HasPrefix(thText(message), "hooked "+id+"|<harness>") {
				t.Fatalf("%s text %q, want it to start with the hook's output and the harness block", id, thText(message))
			}
		}
		mustClose(t, harness)
	})

	// harness-tool-hooks.test.ts:198 it("bounds a replacement output with the tool's limits, or the defaults for an unresolved tool") (#11043)
	t.Run("bounds a replacement output with the tool's limits, or the defaults for an unresolved tool", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, thTool("small", thEmpty, func(tool *durable.ToolRegistration) {
			tool.OutputLimits = &durable.ToolOutputLimits{MaxBytes: new(10)}
		}))
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{
			BeforeTool: func(context.Context, ToolHookCall, HookApi) (*BeforeToolResult, error) {
				return &BeforeToolResult{Block: new("no")}, nil
			},
			AfterTool: func(_ context.Context, call ToolHookCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
				text := strings.Repeat("y", 60*1024)
				if call.ID == "c2" {
					text = strings.Repeat("x", 100)
				}
				replaced := thReplaceOutput(result, text)
				return &replaced, nil
			},
		})
		root := &syncValue[Conversation]{}
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.AssistantMessage, error) {
				conversation, _ := root.get()
				if err := conversation.Configure(testContext, AgentChange{Tools: SetTo(ToolChange{List: []*durable.ToolRegistration{}})}); err != nil {
					return ai.FauxResponse{}.AssistantMessage(), err
				}
				return tlCalls(tlCall{"small", map[string]any{}, "c1"}).AssistantMessage(), nil
			}),
			tlDone(),
		})
		harness, conversation := openChat(t, storage.NewMemoryStorage(), setup)
		root.put(conversation)
		thWait(t, ownSubmit(t, conversation, ownInput("go")))
		if err := conversation.Configure(testContext, AgentChange{Tools: Cleared[ToolChange]()}); err != nil {
			t.Fatal(err)
		}
		setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"small", map[string]any{}, "c2"}), tlDone()})
		thWait(t, ownSubmit(t, conversation, ownInput("again")))
		entries := allEntries(t, conversation)
		messages := thResults(entries)
		// Unresolved: the default 50 KB.
		if text, ok := messages["c1"].Content[0].(ai.TextContent); !ok || len(text.Text) != 50*1024 {
			t.Fatalf("unresolved result content %v, want text of 50 KB", messages["c1"].Content[0])
		}
		thExpectEqual(t, "c1 diagnostics", thDiagnosticsOf(entries, "c1"), []string{"tool_unavailable", "truncated"})
		if text, ok := messages["c2"].Content[0].(ai.TextContent); !ok || text.Text != strings.Repeat("x", 10) {
			t.Fatalf("bounded result content %v, want ten x", messages["c2"].Content[0])
		}
		thExpectEqual(t, "c2 diagnostics", thDiagnosticsOf(entries, "c2"), []string{"blocked", "truncated"})
		mustClose(t, harness)
	})

	// harness-tool-hooks.test.ts:238 it("lets a hook's terminate on a blocked call count toward the round") (#11043)
	t.Run("lets a hook's terminate on a blocked call count toward the round", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, thTool("echo", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}, Control: &durable.ToolControl{Terminate: true}}, nil
		}))
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{
			BeforeTool: func(_ context.Context, call ToolHookCall, _ HookApi) (*BeforeToolResult, error) {
				if call.ID == "c2" {
					return &BeforeToolResult{Block: new("no")}, nil
				}
				return nil, nil
			},
			AfterTool: func(_ context.Context, _ ToolHookCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
				result.Control = &durable.ToolControl{Terminate: true}
				return &result, nil
			},
		})
		setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"echo", map[string]any{}, "c1"}, tlCall{"echo", map[string]any{}, "c2"}), tlDone()})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		thExpectStatus(t, thWait(t, ownSubmit(t, root, ownInput("go"))), durable.SubmissionDone)
		// The round terminated: no second request.
		if calls := setup.Faux.CallCount(); calls != 1 {
			t.Fatalf("%d requests, want 1", calls)
		}
		mustClose(t, harness)
	})

	// harness-tool-hooks.test.ts:258 it("runs on nested invalid and blocked calls; the caller gets diagnostics and details, not output") (#11043)
	t.Run("runs on nested invalid and blocked calls; the caller gets diagnostics and details, not output", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, thTool("echo", thEmpty))
		nested := &syncList[durable.NestedToolExecutionResult]{}
		addTool(t, setup.Registry, thTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			for _, call := range []struct {
				name string
				args map[string]any
			}{
				{"echo", map[string]any{"text": map[string]any{"not": "a string"}}},
				{"echo", map[string]any{"text": "secret"}},
				{"missing", map[string]any{}},
			} {
				result, err := api.ExecuteTool(ctx, call.name, call.args)
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				nested.add(result)
			}
			return durable.ToolExecutionResult{}, nil
		}))
		seen := &syncList[ToolHookCall]{}
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{
			BeforeTool: func(_ context.Context, call ToolHookCall, _ HookApi) (*BeforeToolResult, error) {
				if call.Arguments["text"] == "secret" {
					return &BeforeToolResult{Block: new("no secrets")}, nil
				}
				return nil, nil
			},
			AfterTool: func(_ context.Context, call ToolHookCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
				if call.Parent == nil {
					return nil, nil
				}
				seen.add(call)
				replaced := thReplaceOutput(result, "dropped")
				replaced.StructuredOutput, replaced.HasStructuredOutput = map[string]any{"dropped": true}, true
				replaced.Details, replaced.HasDetails = map[string]any{"hooked": call.ID}, true
				replaced.Diagnostics = append(slices.Clone(result.Diagnostics), durable.ToolDiagnostic{Severity: "info", Code: "hooked", Message: "seen"})
				return &replaced, nil
			},
		})
		setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		thWait(t, ownSubmit(t, root, ownInput("go")))
		ids := []string{}
		for _, call := range seen.all() {
			ids = append(ids, call.ID)
			if call.Parent == nil || call.Parent.CallId != "c1" {
				t.Fatalf("hooked call %s has parent %+v, want call c1", call.ID, call.Parent)
			}
		}
		thExpectEqual(t, "hooked nested call ids", ids, []string{"c1/1", "c1/2", "c1/3"})
		results := nested.all()
		if len(results) != 3 {
			t.Fatalf("%d nested results, want 3", len(results))
		}
		for index, want := range [][]string{{"invalid_arguments", "hooked"}, {"blocked", "hooked"}, {"tool_unavailable", "hooked"}} {
			if !results[index].IsError {
				t.Fatalf("nested result %d is not an error", index)
			}
			thExpectEqual(t, "nested codes", thResultCodes(results[index].Diagnostics), want)
			thHasDetails(t, ai.ToolResultMessage{IsError: true, Details: results[index].Details}, map[string]any{"hooked": ids[index]})
		}
		// A Harness error result has no structured output, even when a hook supplies one.
		if results[1].HasStructuredOutput || results[2].HasStructuredOutput {
			t.Fatalf("blocked %+v and unavailable %+v nested results keep a structured output", results[1], results[2])
		}
		mustClose(t, harness)
	})

	// harness-tool-hooks.test.ts:311 it("gives a nested call a hook turns into a success its output as value, unless the tool has a schema") (#11043)
	t.Run("gives a nested call a hook turns into a success its output as value, unless the tool has a schema", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, thTool("plain", thEmpty))
		addTool(t, setup.Registry, thTool("typed", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}, StructuredOutput: map[string]any{"ok": true}, HasStructuredOutput: true}, nil
		}, func(tool *durable.ToolRegistration) {
			tool.StructuredOutputSchema = map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []any{"ok"}}
		}))
		nested := &syncList[durable.NestedToolExecutionResult]{}
		addTool(t, setup.Registry, thTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			for _, name := range []string{"plain", "typed"} {
				result, err := api.ExecuteTool(ctx, name, map[string]any{})
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				nested.add(result)
			}
			return durable.ToolExecutionResult{}, nil
		}))
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{
			BeforeTool: func(_ context.Context, call ToolHookCall, _ HookApi) (*BeforeToolResult, error) {
				if call.Parent == nil {
					return nil, nil
				}
				return &BeforeToolResult{Block: new("no")}, nil
			},
			AfterTool: func(_ context.Context, call ToolHookCall, _ durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
				if call.Parent == nil {
					return nil, nil
				}
				return &durable.ToolExecutionResult{
					Output:           []ai.ToolResultMessageContent{ai.TextContent{Text: "fine " + call.Name}},
					StructuredOutput: map[string]any{"ok": false}, HasStructuredOutput: true,
				}, nil
			},
		})
		setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		thWait(t, ownSubmit(t, root, ownInput("go")))
		results := nested.all()
		if len(results) != 2 {
			t.Fatalf("%d nested results, want 2", len(results))
		}
		if results[0].IsError || !results[0].HasStructuredOutput || results[0].StructuredOutput != "fine plain" {
			t.Fatalf("plain nested result %+v, want a success with value \"fine plain\"", results[0])
		}
		if results[1].IsError || results[1].HasStructuredOutput {
			t.Fatalf("typed nested result %+v, want a success without a value", results[1])
		}
		mustClose(t, harness)
	})
}

func TestAfterToolOnResultsRebuiltFromTheSlot(t *testing.T) {
	// harness-tool-hooks.test.ts:370 it("runs on an interrupted call; a replaced output loses the slot's truncation diagnostic") (#11043)
	t.Run("runs on an interrupted call; a replaced output loses the slot's truncation diagnostic", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		thProgress(setup, 0)
		limits := func(tool *durable.ToolRegistration) {
			tool.OutputLimits = &durable.ToolOutputLimits{MaxBytes: new(10), Retain: durable.RetainTail}
		}
		work := thBlockingTool("work", limits)
		keep := thBlockingTool("keep", limits)
		addTool(t, setup.Registry, work.registration)
		addTool(t, setup.Registry, keep.registration)
		seen := &syncList[thSeen]{}
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{AfterTool: func(_ context.Context, call ToolHookCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
			seen.add(thSeen{call, result})
			if call.Name == "work" {
				replaced := thReplaceOutput(result, "redacted")
				return &replaced, nil
			}
			return nil, nil
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"work", map[string]any{}, "c1"}, tlCall{"keep", map[string]any{}, "c2"}), tlDone()})
		opened := thOpen(t, path, setup)
		id := ownSubmit(t, opened.root, ownInput("go")).Id()
		for _, gate := range []*deferredGate{work.started, keep.started} {
			if err := gate.wait(testContext); err != nil {
				t.Fatal(err)
			}
		}
		thStored()
		opened.kill()

		opened = thOpen(t, path, setup)
		thExpectStatus(t, thWait(t, thSubmitted(t, opened.harness, id)), durable.SubmissionDone)
		thExpectEqual(t, "hooked results", thSeenCodes(seen.all()), []string{"c1: interrupted", "c2: interrupted"})
		entries := allEntries(t, opened.root)
		messages := thResults(entries)
		if !strings.HasPrefix(thText(messages["c1"]), "redacted|") {
			t.Fatalf("c1 text %q, want it to start with the redacted output", thText(messages["c1"]))
		}
		thHasDetails(t, messages["c1"], map[string]any{"run": 1.0})
		thExpectEqual(t, "c1 diagnostics", thDiagnosticsOf(entries, "c1"), []string{"interrupted"})
		// The kept output keeps its truncation diagnostic, after the hooks' result.
		thExpectEqual(t, "c2 diagnostics", thDiagnosticsOf(entries, "c2"), []string{"interrupted", "truncated"})
		mustClose(t, opened.harness)
	})

	// harness-tool-hooks.test.ts:411 it("runs on an abort result, but not on the aborted results of calls that never started") (#11043)
	t.Run("runs on an abort result, but not on the aborted results of calls that never started", func(t *testing.T) {
		setup := chatSetup(t)
		tlSetExecution(setup, durable.ToolExecutionSequential)
		first := thBlockingTool("first")
		addTool(t, setup.Registry, first.registration)
		addTool(t, setup.Registry, thTool("second", thEmpty))
		seen := &syncList[thSeen]{}
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{AfterTool: func(_ context.Context, call ToolHookCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
			seen.add(thSeen{call, result})
			result.Details, result.HasDetails = map[string]any{"hooked": true}, true
			return &result, nil
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"first", map[string]any{}, "c1"}, tlCall{"second", map[string]any{}, "c2"}), tlDone()})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := ownSubmit(t, root, ownInput("go"))
		if err := first.started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		if err := root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		thExpectStatus(t, thWait(t, submission), durable.SubmissionUnanswered)
		thExpectEqual(t, "hooked results", thSeenCodes(seen.all()), []string{"c1: aborted"})
		messages := thResults(allEntries(t, root))
		thHasDetails(t, messages["c1"], map[string]any{"hooked": true})
		if messages["c2"].Details != nil {
			t.Fatalf("c2 details %v, want none", messages["c2"].Details)
		}
		mustClose(t, harness)
	})

	// harness-tool-hooks.test.ts:441 it("reports a throwing hook on an abort result and settles the result as built") (#11043)
	t.Run("reports a throwing hook on an abort result and settles the result as built", func(t *testing.T) {
		setup := chatSetup(t)
		work := thBlockingTool("work")
		addTool(t, setup.Registry, work.registration)
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{AfterTool: func(context.Context, ToolHookCall, durable.ToolExecutionResult, HookApi) (*durable.ToolExecutionResult, error) {
			return nil, errors.New("hook failed")
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"work", map[string]any{}, "c1"}), tlDone()})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := ownSubmit(t, root, ownInput("go"))
		if err := work.started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		if err := root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		thExpectStatus(t, thWait(t, submission), durable.SubmissionUnanswered)
		entries := allEntries(t, root)
		if text := thText(thResults(entries)["c1"]); !strings.HasPrefix(text, "work "+strings.Repeat("o", 40)) {
			t.Fatalf("c1 text %q, want it to start with the call's output", text)
		}
		thExpectEqual(t, "c1 diagnostics", thDiagnosticsOf(entries, "c1"), []string{"aborted"})
		reported := false
		for _, report := range setup.Reports.all() {
			reported = reported || report.Error() == "hook failed"
		}
		if !reported {
			t.Fatalf("reports %v, want the hook's failure", setup.Reports.all())
		}
		mustClose(t, harness)
	})

	// harness-tool-hooks.test.ts:464 it("settles an abort without the hooks in the abort handler's last attempt") (#11043)
	t.Run("settles an abort without the hooks in the abort handler's last attempt", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		setup.SetSettings(func(settings *HarnessSettings) { settings.RestartLimit = new(2.0) })
		thProgress(setup, 0)
		work := thBlockingTool("work")
		addTool(t, setup.Registry, work.registration)
		seen := &syncList[thSeen]{}
		hanging := deferred()
		never := make(chan struct{})
		t.Cleanup(func() { close(never) })
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{AfterTool: func(_ context.Context, call ToolHookCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
			seen.add(thSeen{call, result})
			hanging.resolve()
			// Never returns: the host dies inside the hook.
			<-never
			return nil, nil
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"work", map[string]any{}, "c1"}), tlDone()})
		opened := thOpen(t, path, setup)
		id := ownSubmit(t, opened.root, ownInput("go")).Id()
		if err := work.started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		taskId := thToolTaskId(t, opened.harness, opened.root, 0)
		thStored()
		go func() { _ = opened.root.Abort(context.Background(), nil) }()
		if err := hanging.wait(testContext); err != nil {
			t.Fatal(err)
		}
		opened.kill()

		// One restart, at the limit minus one: the attempt the scheduler would fault when cut off runs no hooks.
		opened = thOpen(t, path, setup)
		thExpectStatus(t, thWait(t, thSubmitted(t, opened.harness, id)), durable.SubmissionUnanswered)
		thExpectEqual(t, "hooked results", thSeenCodes(seen.all()), []string{"c1: aborted"})
		entries := allEntries(t, opened.root)
		if text := thText(thResults(entries)["c1"]); !strings.HasPrefix(text, "work "+strings.Repeat("o", 40)) {
			t.Fatalf("c1 text %q, want it to start with the call's output", text)
		}
		thExpectEqual(t, "c1 diagnostics", thDiagnosticsOf(entries, "c1"), []string{"aborted"})
		record := tkTaskRecord(t, opened.harness, taskId)
		if record.State.Status != durable.TaskTerminal || record.State.Outcome == nil || record.State.Outcome.Status != durable.OutcomeAborted {
			t.Fatalf("tool task state %+v, want terminal aborted", record.State)
		}
		mustClose(t, opened.harness)
	})

	// harness-tool-hooks.test.ts:506 it("sees the held progress of an aborted call, and the result stores it") (#11043)
	t.Run("sees the held progress of an aborted call, and the result stores it", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		// Progress commits are held in memory until the result commit stores them.
		thProgress(setup, 60_000)
		work := thBlockingTool("work")
		addTool(t, setup.Registry, work.registration)
		seen := &syncList[thSeen]{}
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{AfterTool: func(_ context.Context, call ToolHookCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
			seen.add(thSeen{call, result})
			return nil, nil
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"work", map[string]any{}, "c1"}), tlDone()})
		opened := thOpen(t, path, setup)
		submission := ownSubmit(t, opened.root, ownInput("go"))
		if err := work.started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		if err := opened.root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		thExpectStatus(t, thWait(t, submission), durable.SubmissionUnanswered)
		hooked := seen.all()
		if len(hooked) != 1 {
			t.Fatalf("%d hooked results, want 1", len(hooked))
		}
		if !reflect.DeepEqual(plainObject(hooked[0].result.Details), map[string]any{"run": 1.0}) {
			t.Fatalf("hooked details %v, want run 1", hooked[0].result.Details)
		}
		thExpectText := thText(ai.ToolResultMessage{Content: hooked[0].result.Output})
		if want := "work " + strings.Repeat("o", 40) + "\n"; thExpectText != want {
			t.Fatalf("hooked output %q, want %q", thExpectText, want)
		}
		mustClose(t, opened.harness)

		opened = thOpen(t, path, setup)
		message := thResults(allEntries(t, opened.root))["c1"]
		thHasDetails(t, message, map[string]any{"run": 1.0})
		if !strings.HasPrefix(thText(message), "work "+strings.Repeat("o", 40)) {
			t.Fatalf("c1 text %q, want it to start with the held output", thText(message))
		}
		mustClose(t, opened.harness)
	})

	// harness-tool-hooks.test.ts:539 it("runs on an abandoned nested call's result") (#11043)
	t.Run("runs on an abandoned nested call's result", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		gated := deferred()
		var gate atomic.Bool
		gate.Store(true)
		addTool(t, setup.Registry, thTool("gated", thEmpty))
		addTool(t, setup.Registry, thTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			if _, err := api.ExecuteTool(ctx, "gated", map[string]any{}); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return durable.ToolExecutionResult{}, nil
		}))
		seen := &syncList[thSeen]{}
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{
			// Holds the nested call in its `call` phase, before intent, until the Harness closes.
			BeforeTool: func(ctx context.Context, call ToolHookCall, _ HookApi) (*BeforeToolResult, error) {
				if call.Name != "gated" || !gate.Load() {
					return nil, nil
				}
				gated.resolve()
				return nil, abortedBy(ctx)
			},
			AfterTool: func(_ context.Context, call ToolHookCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
				if call.Parent == nil {
					return nil, nil
				}
				seen.add(thSeen{call, result})
				result.Details, result.HasDetails = map[string]any{"hooked": true}, true
				return &result, nil
			},
		})
		setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
		opened := thOpen(t, path, setup)
		id := ownSubmit(t, opened.root, ownInput("go")).Id()
		if err := gated.wait(testContext); err != nil {
			t.Fatal(err)
		}
		mustClose(t, opened.harness)
		gate.Store(false)

		opened = thOpen(t, path, setup)
		thExpectStatus(t, thWait(t, thSubmitted(t, opened.harness, id)), durable.SubmissionDone)
		thExpectEqual(t, "hooked results", thSeenCodes(seen.all()), []string{"c1/1: abandoned"})
		mustClose(t, opened.harness)
	})

	// harness-tool-hooks.test.ts:583 it("runs on a restart_limit result at the limit, and not once the hooks themselves were cut off") (#11043)
	t.Run("runs on a restart_limit result at the limit, and not once the hooks themselves were cut off", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		setup.SetSettings(func(settings *HarnessSettings) { settings.RestartLimit = new(1.0) })
		thProgress(setup, 0)
		work := thBlockingTool("work")
		addTool(t, setup.Registry, work.registration)
		seen := &syncList[thSeen]{}
		hanging := deferred()
		never := make(chan struct{})
		t.Cleanup(func() { close(never) })
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{AfterTool: func(_ context.Context, call ToolHookCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
			seen.add(thSeen{call, result})
			hanging.resolve()
			// Never returns: the host dies inside the hook.
			<-never
			return nil, nil
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"work", map[string]any{}, "c1"}), tlDone()})
		opened := thOpen(t, path, setup)
		id := ownSubmit(t, opened.root, ownInput("go")).Id()
		if err := work.started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		taskId := thToolTaskId(t, opened.harness, opened.root, 0)
		thStored()
		opened.kill()

		// Cut off once, at the limit: the task ends itself, and the hooks run on its result.
		opened = thOpen(t, path, setup)
		if err := hanging.wait(testContext); err != nil {
			t.Fatal(err)
		}
		thExpectEqual(t, "hooked results", thSeenCodes(seen.all()), []string{"c1: restart_limit"})
		opened.kill()

		// Past the limit: ends without the hooks.
		opened = thOpen(t, path, setup)
		thExpectStatus(t, thWait(t, thSubmitted(t, opened.harness, id)), durable.SubmissionDone)
		if len(seen.all()) != 1 {
			t.Fatalf("%d hooked results, want 1", len(seen.all()))
		}
		if runs := work.runs.Load(); runs != 1 {
			t.Fatalf("work ran %d times, want 1", runs)
		}
		entries := allEntries(t, opened.root)
		thExpectEqual(t, "c1 diagnostics", thDiagnosticsOf(entries, "c1"), []string{"restart_limit"})
		record := tkTaskRecord(t, opened.harness, taskId)
		outcome := record.State.Outcome
		if record.State.Status != durable.TaskTerminal || outcome == nil || outcome.Status != durable.OutcomeFailed || outcome.Error == nil || plainObject(outcome.Error.Detail)["reason"] != "restart_limit" {
			t.Fatalf("tool task state %+v, want terminal failed with reason restart_limit", record.State)
		}
		mustClose(t, opened.harness)
	})
}

func TestCallsToToolsTheRequestDidNotOffer(t *testing.T) {
	// harness-tool-hooks.test.ts:631 it("run afterTool inside their own tool task") (#11043)
	t.Run("run afterTool inside their own tool task", func(t *testing.T) {
		setup := chatSetup(t)
		type seenIn struct {
			id     string
			taskId durable.TaskId
		}
		seen := &syncList[seenIn]{}
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{AfterTool: func(_ context.Context, call ToolHookCall, result durable.ToolExecutionResult, api HookApi) (*durable.ToolExecutionResult, error) {
			seen.add(seenIn{call.ID, api.TaskId()})
			replaced := thReplaceOutput(result, "try another tool")
			return &replaced, nil
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"ghost", map[string]any{}, "c1"}), tlDone()})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		thWait(t, ownSubmit(t, root, ownInput("go")))
		hooked := seen.all()
		if len(hooked) != 1 || hooked[0].id != "c1" {
			t.Fatalf("hooked calls %v, want c1", hooked)
		}
		record := tkTaskRecord(t, harness, hooked[0].taskId)
		input := plainObject(record.Input)
		if record.Kind != "pi.tool" || input["kind"] != "model" || input["callId"] != "c1" || input["unoffered"] != true {
			t.Fatalf("hook ran in task %+v, want a pi.tool task with a flagged model input for c1", record)
		}
		entries := allEntries(t, root)
		if text := thText(thResults(entries)["c1"]); !strings.HasPrefix(text, "try another tool|") {
			t.Fatalf("c1 text %q, want it to start with the hook's output", text)
		}
		thExpectEqual(t, "c1 diagnostics", thDiagnosticsOf(entries, "c1"), []string{"tool_unavailable"})
		mustClose(t, harness)
	})

	// harness-tool-hooks.test.ts:655 it("count toward a round's terminate") (#11043)
	t.Run("count toward a round's terminate", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, thTool("echo", thEmpty))
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{AfterTool: func(_ context.Context, _ ToolHookCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
			result.Control = &durable.ToolControl{Terminate: true}
			return &result, nil
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"echo", map[string]any{}, "c1"}, tlCall{"ghost", map[string]any{}, "c2"}), tlDone()})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		thExpectStatus(t, thWait(t, ownSubmit(t, root, ownInput("go"))), durable.SubmissionDone)
		if calls := setup.Faux.CallCount(); calls != 1 {
			t.Fatalf("%d requests, want 1", calls)
		}
		mustClose(t, harness)
	})

	// harness-tool-hooks.test.ts:673 it("take their turn in a sequential round, and the last handoff in call order wins") (#11043)
	t.Run("take their turn in a sequential round, and the last handoff in call order wins", func(t *testing.T) {
		setup := chatSetup(t)
		tlSetExecution(setup, durable.ToolExecutionSequential)
		order := &syncList[string]{}
		for _, name := range []string{"a", "b"} {
			addTool(t, setup.Registry, thTool(name, func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				order.add(name)
				return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
			}))
		}
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{AfterTool: func(_ context.Context, call ToolHookCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
			order.add("hook " + call.ID)
			if call.ID == "c1" {
				return nil, nil
			}
			result.Control = &durable.ToolControl{Handoff: new("from " + call.ID)}
			return &result, nil
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"a", map[string]any{}, "c1"}, tlCall{"ghost", map[string]any{}, "c2"}, tlCall{"b", map[string]any{}, "c3"}), tlDone()})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		thWait(t, ownSubmit(t, root, ownInput("go")))
		thExpectEqual(t, "order", order.all(), []string{"a", "hook c1", "hook c2", "b", "hook c3"})
		resets := []string{}
		for _, entry := range allEntries(t, root) {
			if durable.ResetEntry.Is(&entry) {
				text, _ := textOf(entry.Model[0])
				resets = append(resets, text)
			}
		}
		thExpectEqual(t, "reset contents", resets, []string{"from c3"})
		mustClose(t, harness)
	})

	// harness-tool-hooks.test.ts:702 it("each get a tool task in a parallel round of only such calls") (#11043)
	t.Run("each get a tool task in a parallel round of only such calls", func(t *testing.T) {
		setup := chatSetup(t)
		seen := &syncList[string]{}
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{AfterTool: func(_ context.Context, call ToolHookCall, _ durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
			seen.add(call.ID)
			return nil, nil
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"ghost", map[string]any{}, "c1"}, tlCall{"spirit", map[string]any{}, "c2"}, tlCall{"ghost", map[string]any{}, "c3"}), tlDone()})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		thExpectStatus(t, thWait(t, ownSubmit(t, root, ownInput("go"))), durable.SubmissionDone)
		ids := seen.all()
		slices.Sort(ids)
		thExpectEqual(t, "hooked call ids", ids, []string{"c1", "c2", "c3"})
		entries := allEntries(t, root)
		for _, id := range []string{"c1", "c2", "c3"} {
			thExpectEqual(t, id+" diagnostics", thDiagnosticsOf(entries, id), []string{"tool_unavailable"})
		}
		// The round's results go to the next request.
		if calls := setup.Faux.CallCount(); calls != 2 {
			t.Fatalf("%d requests, want 2", calls)
		}
		mustClose(t, harness)
	})

	// harness-tool-hooks.test.ts:724 it("keep their flag in a version 2 generation's sequential round, also one migrated from version 1") (#11043)
	t.Run("keep their flag in a version 2 generation's sequential round, also one migrated from version 1", func(t *testing.T) {
		for _, version := range []int{2, 1} {
			path := sqlitePath(t)
			setup := chatSetup(t)
			tlSetExecution(setup, durable.ToolExecutionSequential)
			started := deferred()
			var runs atomic.Int32
			a := thTool("a", func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				if runs.Add(1) > 1 {
					return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
				}
				started.resolve()
				return durable.ToolExecutionResult{}, abortedBy(ctx)
			}, func(tool *durable.ToolRegistration) { tool.Replay = durable.ReplaySafe })
			addTool(t, setup.Registry, a)
			addTool(t, setup.Registry, thTool("ghost", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				return ownText("ran"), nil
			}))
			setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"a", map[string]any{}, "c1"}, tlCall{"ghost", map[string]any{}, "c2"}), tlDone()})
			opened := thOpen(t, path, setup)
			root := opened.root
			// `ghost` is registered, but not offered to this request.
			if err := root.Configure(testContext, AgentChange{Tools: SetTo(ToolChange{List: []*durable.ToolRegistration{a}})}); err != nil {
				t.Fatal(err)
			}
			id := ownSubmit(t, root, ownInput("go")).Id()
			if err := started.wait(testContext); err != nil {
				t.Fatal(err)
			}
			live := liveOf(t, opened.harness, root.Id())
			generation := live.Run.TaskId
			if len(live.Tools) != 2 || live.Tools[0].CallId != "c1" || live.Tools[0].Unoffered || live.Tools[1].CallId != "c2" || !live.Tools[1].Unoffered {
				t.Fatalf("version %d: slots %+v, want c1 offered and c2 unoffered", version, live.Tools)
			}
			if got := tkTaskRecord(t, opened.harness, generation).Version; got != 2 {
				t.Fatalf("version %d: generation version %d, want 2", version, got)
			}
			// The tools are offered when the round goes on: only the flag keeps `ghost` from running.
			if err := root.Configure(testContext, AgentChange{Tools: Cleared[ToolChange]()}); err != nil {
				t.Fatal(err)
			}
			mustClose(t, opened.harness)
			if version == 1 {
				store := tkOpenSqlite(t, path)
				record, err := store.Task(testContext, generation)
				if err != nil || record == nil {
					t.Fatalf("task %d: %v %v", generation, record, err)
				}
				record.Version = 1
				if _, err := store.Commit(testContext, []durable.StorageWrite{durable.TaskWrite{Value: *record}}, func(durable.Seq) {}); err != nil {
					t.Fatal(err)
				}
				if err := store.Close(testContext); err != nil {
					t.Fatal(err)
				}
			}

			opened = thOpen(t, path, setup)
			thExpectStatus(t, thWait(t, thSubmitted(t, opened.harness, id)), durable.SubmissionDone)
			if got := tkTaskRecord(t, opened.harness, generation).Version; got != 2 {
				t.Fatalf("version %d: generation version %d after reopening, want 2", version, got)
			}
			entries := allEntries(t, opened.root)
			thExpectEqual(t, "c2 diagnostics", thDiagnosticsOf(entries, "c2"), []string{"tool_unavailable"})
			if text := thText(thResults(entries)["c2"]); strings.Contains(text, "ran") {
				t.Fatalf("version %d: c2 text %q, want the unoffered tool not to run", version, text)
			}
			mustClose(t, opened.harness)
		}
	})

	// harness-tool-hooks.test.ts:782 it("settle unavailable, not aborted, when their sequential round is aborted before they start") (#11043)
	t.Run("settle unavailable, not aborted, when their sequential round is aborted before they start", func(t *testing.T) {
		setup := chatSetup(t)
		tlSetExecution(setup, durable.ToolExecutionSequential)
		started := deferred()
		addTool(t, setup.Registry, thTool("wait", func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			started.resolve()
			return durable.ToolExecutionResult{}, abortedBy(ctx)
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{thCalls(tlCall{"wait", map[string]any{}, "c1"}, tlCall{"ghost", map[string]any{}, "c2"}), tlDone()})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := ownSubmit(t, root, ownInput("go"))
		if err := started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		if err := root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		thExpectStatus(t, thWait(t, submission), durable.SubmissionUnanswered)
		entries := allEntries(t, root)
		thExpectEqual(t, "c1 diagnostics", thDiagnosticsOf(entries, "c1"), []string{"aborted"})
		thExpectEqual(t, "c2 diagnostics", thDiagnosticsOf(entries, "c2"), []string{"tool_unavailable"})
		mustClose(t, harness)
	})
}
