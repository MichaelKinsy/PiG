// The Go twin of app.mjs, scenario.mjs, resume.mjs, apidump.mjs and rawdump.mjs.

package interop

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
	jsonlnode "github.com/MichaelKinsy/PiG/durable/storage/jsonl/node"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

const clock = 1_700_000_000_000

var background = context.Background()

type notes struct {
	Lines []string `json:"lines"`
}

type sessions struct {
	Items map[string]any `json:"items"`
}

var notesDoc = durable.DefineDoc(durable.DocDefinition[notes]{
	CommonDocDefinition: durable.CommonDocDefinition[notes]{Kind: "app.notes", Version: 1, Initial: func() notes { return notes{Lines: []string{}} }},
	DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkCurrent},
})

var sessionsDoc = durable.DefineDoc(durable.DocDefinition[sessions]{
	CommonDocDefinition: durable.CommonDocDefinition[sessions]{Kind: "app.sessions", Version: 1, Initial: func() sessions { return sessions{Items: map[string]any{}} }},
	DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeSession},
})

type plan struct {
	Steps []string `json:"steps"`
}

type blob struct {
	Content string `json:"content"`
}

var planDoc = durable.DefineDoc(durable.DocDefinition[plan]{
	CommonDocDefinition: durable.CommonDocDefinition[plan]{Kind: "app.plan", Version: 1, Initial: func() plan { return plan{Steps: []string{}} }},
	DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf},
})

var blobDoc = durable.DefineDocFamily(durable.DocFamilyDefinition[blob, blob]{
	Family: true,
	Kind:   "app.blob", Version: 1,
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkCurrent},
	Initial:           func(seed blob) blob { return blob{Content: seed.Content} },
})

type jobState struct {
	Phase string `json:"phase"`
	N     int    `json:"n,omitempty"`
}

type jobResult struct {
	N int `json:"n"`
}

type (
	jobRuntime = durable.TaskRuntime[durable.JsonObject, jobState, jobResult, any]
	jobRunning = durable.RunningTask[durable.JsonObject, jobState, jobResult]
	jobNext    = durable.NextTaskState[jobState, jobResult]
)

var job = durable.DefineTask(durable.TaskDefinition[durable.JsonObject, jobState, jobResult, any]{
	Name:    "app.job",
	Version: 1,
	Initial: func(durable.JsonObject) jobState { return jobState{Phase: "first"} },
	Phases: map[string]durable.PhaseHandler[durable.JsonObject, jobState, jobResult, any]{
		"first": func(ctx context.Context, _ jobRunning, runtime jobRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, jobRunning) (*jobNext, error) {
				return &jobNext{Status: durable.TaskRunning, Checkpoint: &jobState{Phase: "second", N: 1}}, nil
			})
		},
		"second": func(ctx context.Context, task jobRunning, runtime jobRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, jobRunning) (*jobNext, error) {
				return &jobNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[jobResult]{Status: durable.OutcomeCompleted, Result: &jobResult{N: task.State.Checkpoint.N}}}, nil
			})
		},
	},
	Abort: func(ctx context.Context, _ jobRunning, runtime jobRuntime) error {
		return runtime.Commit(ctx, func(durable.Tx, jobRunning) (*jobNext, error) {
			return &jobNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[jobResult]{Status: durable.OutcomeAborted}}, nil
		})
	},
})

var noteTool = &durable.ToolRegistration{
	ToolSchema: ai.ToolSchema{
		Name:        "note",
		Description: "Appends a line to the conversation's notes and starts a job",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"text": map[string]any{"type": "string"}},
			"required":   []any{"text"},
		},
	},
	Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		text, _ := args.(map[string]any)["text"].(string)
		if _, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
			draft, err := durable.TxDoc[notes](tx, notesDoc, api.ConversationId())
			if err != nil {
				return nil, err
			}
			if _, err := draft.Array("lines").Push(text); err != nil {
				return nil, err
			}
			_, err = durable.CreateTask(tx, job, delta.NewJsonObject(0), durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
			return nil, err
		}); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "noted " + text}}}, nil
	},
}

// spawnTool runs a child conversation owned by its own task and returns the child's answer.
var spawnTool = &durable.ToolRegistration{
	ToolSchema: ai.ToolSchema{
		Name:        "spawn",
		Description: "Runs a child conversation",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"task": map[string]any{"type": "string"}},
			"required":   []any{"task"},
		},
	},
	Replay: durable.ReplaySafe,
	Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		task, _ := args.(map[string]any)["task"].(string)
		created, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
			child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: api.TaskId()}})
			if err != nil {
				return nil, err
			}
			return child.Id, nil
		})
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		child := created.(durable.ConversationId)
		handle, err := api.Conversation(ctx, child)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		submission, err := handle.Submit(ctx, durable.InputSubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(task), RequestId: new("spawn:" + strconv.FormatInt(int64(api.TaskId()), 10))})
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		settled, err := submission.Wait(ctx)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		entry, err := api.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.Entry(*settled.Answer) })
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		var text strings.Builder
		for _, block := range entry.(*durable.EntryRecord).Model[0].(ai.AssistantMessage).Content {
			if block, ok := block.(ai.TextContent); ok {
				text.WriteString(block.Text)
			}
		}
		return durable.ToolExecutionResult{
			Content:    []ai.ToolResultMessageContent{ai.TextContent{Text: text.String()}},
			Details:    delta.JsonObjectOf("conversationId", float64(child)),
			HasDetails: true,
		}, nil
	},
}

var appExtension = &durable.Extension{Name: "app", Tools: []*durable.ToolRegistration{noteTool, spawnTool}, Tasks: []durable.AnyTask{job}}

var fauxModel = durable.ModelRef{Provider: "faux", ModelId: "faux-1"}

func lastText(messages []ai.Message) (role, text string) {
	for _, message := range slices.Backward(messages) {
		switch message := message.(type) {
		case ai.SystemMessage:
			continue
		case ai.ToolResultMessage:
			return "toolResult", ""
		case ai.UserMessage:
			switch content := message.Content.(type) {
			case ai.UserText:
				return "user", string(content)
			case ai.UserContentBlocks:
				for _, block := range content {
					if block, ok := block.(ai.TextContent); ok {
						return "user", block.Text
					}
				}
			}
			return "user", ""
		default:
			return "other", ""
		}
	}
	return "", ""
}

// route is app.mjs's scripted model: it answers by the last non-system message.
func route(transcript ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
	messages := transcript.Messages()
	if system, ok := messages[0].(ai.SystemMessage); ok {
		if content, ok := system.Content.(ai.SystemText); ok && strings.Contains(string(content), "summarization") {
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("## Goal\nScripted summary.")}}.AssistantMessage(), nil
		}
	}
	role, text := lastText(messages)
	if role == "toolResult" {
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Noted.")}}.AssistantMessage(), nil
	}
	if strings.Contains(text, "boom") {
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("")}, StopReason: "error", ErrorMessage: "boom"}.AssistantMessage(), nil
	}
	if strings.Contains(text, "delegate") {
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("spawn", map[string]any{"task": "child task"}, &ai.FauxToolCallOptions{ID: "call-2"})}, StopReason: "toolUse"}.AssistantMessage(), nil
	}
	if strings.Contains(text, "hello") {
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("note", map[string]any{"text": "first"}, &ai.FauxToolCallOptions{ID: "call-1"})}, StopReason: "toolUse"}.AssistantMessage(), nil
	}
	return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Answer to " + text)}}.AssistantMessage(), nil
}

func openApp(t *testing.T, path string) harness.Harness {
	t.Helper()
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	steps := make([]ai.FauxResponseStep, 200)
	for i := range steps {
		steps[i] = ai.FauxFactoryStep(route)
	}
	faux.SetResponses(steps)
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	registry := harness.CreateRegistry()
	if err := registry.Install(appExtension); err != nil {
		t.Fatal(err)
	}
	// A path ending in .sqlite is a SQLite file; any other path is a JSONL directory.
	var store durable.Storage
	var err error
	if strings.HasSuffix(path, ".sqlite") {
		store, err = sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{})
	} else {
		store, err = jsonlnode.OpenNodeJsonlStorage(background, path, jsonl.JsonlStorageOptions{})
	}
	if err != nil {
		t.Fatal(err)
	}
	opened, err := harness.OpenHarness(background, store, harness.HarnessOptions{Models: models, Registry: registry, Now: func() float64 { return clock }, Settings: func() *harness.HarnessSettings {
		return &harness.HarnessSettings{Retry: &harness.RetryPolicyPatch{Enabled: new(false)}}
	}})
	if err != nil {
		t.Fatal(err)
	}
	return opened
}

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func say(t *testing.T, conversation harness.Conversation, text string) {
	t.Helper()
	submission := must(conversation.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(text)}))
	must(submission.Wait(background))
}

func waitTasks(t *testing.T, opened harness.Harness) {
	t.Helper()
	for _, task := range must(opened.Inspect(background)).Tasks {
		must(opened.WaitForTask(background, task.Record.Id))
	}
}

func rootOf(t *testing.T, opened harness.Harness) harness.Conversation {
	return must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
}

// runScenario is scenario.mjs.
func runScenario(t *testing.T, path string) {
	t.Helper()
	opened := openApp(t, path)
	root := rootOf(t, opened)
	must(opened.Commit(background, func(tx durable.Tx) (any, error) {
		draft, err := durable.TxDoc[sessions](tx, sessionsDoc)
		if err != nil {
			return nil, err
		}
		return nil, draft.Object("items").Set("1", map[string]any{"title": "first", "cwd": "/work"})
	}))
	say(t, root, "hello")
	waitTasks(t, opened)

	answer := must(root.Entries(background, durable.EntryQuery{}, 50, nil)).Items[0]
	fork := must(root.Fork(background, answer.Id, harness.ConversationCreateOptions{
		Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless},
		Init: func(tx durable.Tx, id durable.ConversationId) error {
			draft, err := durable.TxDoc[notes](tx, notesDoc, id)
			if err != nil {
				return err
			}
			_, err = draft.Array("lines").Push("forked")
			return err
		},
	}))
	say(t, fork, "second")
	say(t, root, "third")
	compaction := must(root.Compact(background, new("keep notes")))
	must(opened.WaitForTask(background, compaction))
	if err := root.Reset(background, new("handoff")); err != nil {
		t.Fatal(err)
	}
	say(t, root, "after reset")
	if err := opened.Close(background); err != nil {
		t.Fatal(err)
	}
}

// runResume is resume.mjs.
func runResume(t *testing.T, path string) {
	t.Helper()
	opened := openApp(t, path)
	root := rootOf(t, opened)
	opened.Resume()
	say(t, root, "after reopen")
	conversations := must(durable.Commit(background, opened, func(tx durable.Tx) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
		return tx.ScanConversations(durable.ConversationQuery{}, 100, nil)
	}))
	var forked *durable.ConversationRecord
	for i := range conversations.Items {
		if conversations.Items[i].Id != root.Id() {
			forked = &conversations.Items[i]
			break
		}
	}
	if forked == nil {
		t.Fatal("the store has no fork")
	}
	say(t, must(opened.Conversation(background, forked.Id)), "fork again")
	compaction := must(root.Compact(background, new("keep notes again")))
	must(opened.WaitForTask(background, compaction))
	say(t, root, "last")
	if err := opened.Close(background); err != nil {
		t.Fatal(err)
	}
}

// normalize is rawdump.mjs's canonical: wall-clock and random fields are fixed.
func normalize(value any, key string) any {
	switch v := value.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalize(item, "")
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for name, item := range v {
			out[name] = normalize(item, name)
		}
		return out
	}
	switch key {
	case "timestamp", "durationMs":
		return float64(0)
	case "sessionId":
		return "<uuid>"
	case "api":
		// A faux provider's api is "faux" in Go and "faux:<time>:<random>" in Pi; both are random per run.
		if text, ok := value.(string); ok && (text == "faux" || strings.HasPrefix(text, "faux:")) {
			return "faux:*"
		}
	}
	return value
}

// viaJSON is a Go value as the decoded JSON a Node program prints.
func viaJSON(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return normalize(out, "")
}

// apiDump is apidump.mjs.
func apiDump(t *testing.T, path string) any {
	t.Helper()
	opened := openApp(t, path)
	defer func() {
		if err := opened.Close(background); err != nil {
			t.Fatal(err)
		}
	}()
	conversations := must(durable.Commit(background, opened, func(tx durable.Tx) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
		return tx.ScanConversations(durable.ConversationQuery{}, 100, nil)
	}))
	out := map[string]any{"conversations": []any{}, "sessions": nil}
	if current := must(durable.Snapshot[sessions](background, opened, sessionsDoc)); current != nil {
		out["sessions"] = current
	}
	var all []any
	for _, record := range conversations.Items {
		conversation := must(opened.Conversation(background, record.Id))
		entries := must(conversation.Entries(background, durable.EntryQuery{}, 1000, nil)).Items
		view := must(conversation.Context(background, nil))
		entry := map[string]any{"record": record, "entries": entries, "messages": view.Messages}
		for name, read := range map[string]func() (any, error){
			"agent": func() (any, error) {
				return durable.Snapshot[harness.AgentState](background, opened, harness.AgentDoc, record.Id)
			},
			"live": func() (any, error) {
				return durable.Snapshot[harness.LiveState](background, opened, harness.LiveDoc, record.Id)
			},
			"usage": func() (any, error) {
				return durable.Snapshot[harness.UsageState](background, opened, harness.UsageDoc, record.Id)
			},
			"notes": func() (any, error) { return durable.Snapshot[notes](background, opened, notesDoc, record.Id) },
			"plan":  func() (any, error) { return durable.Snapshot[plan](background, opened, planDoc, record.Id) },
		} {
			value, err := read()
			if err != nil {
				t.Fatal(err)
			}
			entry[name] = value
		}
		all = append(all, entry)
	}
	out["conversations"] = all
	tasks := must(durable.Commit(background, opened, func(tx durable.Tx) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
		return tx.ScanTasks(durable.TaskQuery{}, 1000, nil)
	}))
	out["tasks"] = tasks.Items
	dumped := viaJSON(t, out).(map[string]any)
	// An absent document is an undefined member in the Node dump, which JSON leaves out.
	if dumped["sessions"] == nil {
		delete(dumped, "sessions")
	}
	for _, conversation := range dumped["conversations"].([]any) {
		for name, value := range conversation.(map[string]any) {
			if value == nil {
				delete(conversation.(map[string]any), name)
			}
		}
	}
	return dumped
}

// rawDump is rawdump.mjs.
func rawDump(t *testing.T, path string) any {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	}()
	out := map[string]any{}
	tables := map[string]string{
		"durable_metadata":   "SELECT * FROM durable_metadata ORDER BY singleton",
		"durable_schema":     "SELECT * FROM durable_schema ORDER BY singleton",
		"record_ids":         "SELECT * FROM record_ids ORDER BY id",
		"conversations":      "SELECT * FROM conversations ORDER BY id",
		"entries":            "SELECT * FROM entries ORDER BY id",
		"tasks":              "SELECT * FROM tasks ORDER BY id",
		"submissions":        "SELECT * FROM submissions ORDER BY id",
		"documents":          "SELECT * FROM documents ORDER BY id",
		"document_revisions": "SELECT * FROM document_revisions ORDER BY document_id, seq",
	}
	for table, query := range tables {
		rows, err := database.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := rows.Columns()
		var all []any
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			row := map[string]any{}
			for i, name := range columns {
				switch v := values[i].(type) {
				case int64:
					row[name] = float64(v)
				case []byte:
					row[name] = string(v)
				default:
					row[name] = v
				}
				if text, ok := row[name].(string); ok && (name == "record" || name == "content") {
					var parsed any
					if err := json.Unmarshal([]byte(text), &parsed); err != nil {
						t.Fatalf("%s.%s: %v", table, name, err)
					}
					row[name] = normalize(parsed, "")
				}
			}
			all = append(all, row)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		out[table] = all
	}
	return viaJSON(t, out)
}

// runScenario2 is scenario2.mjs.
func runScenario2(t *testing.T, path string) {
	t.Helper()
	opened := openApp(t, path)
	root := rootOf(t, opened)
	if err := root.Configure(background, harness.AgentChange{Instructions: harness.SetTo("Be brief"), Cwd: harness.SetTo("/work")}); err != nil {
		t.Fatal(err)
	}
	must(opened.Commit(background, func(tx durable.Tx) (any, error) {
		steps, err := durable.TxDoc[plan](tx, planDoc, root.Id())
		if err != nil {
			return nil, err
		}
		if _, err := steps.Array("steps").Push("one"); err != nil {
			return nil, err
		}
		body, err := durable.TxDoc[blob](tx, blobDoc, root.Id(), "a1", blob{Content: "v1"})
		if err != nil {
			return nil, err
		}
		return nil, body.Set("content", "v2")
	}))
	say(t, root, "hello")
	must(opened.Commit(background, func(tx durable.Tx) (any, error) {
		steps, err := durable.TxDoc[plan](tx, planDoc, root.Id())
		if err != nil {
			return nil, err
		}
		_, err = steps.Array("steps").Push("two")
		return nil, err
	}))
	say(t, root, "delegate this")
	say(t, root, "boom")
	entries := must(root.Entries(background, durable.EntryQuery{}, 100, nil)).Items
	at := entries[len(entries)-1].Id
	fork := must(root.Fork(background, at, harness.ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}))
	say(t, fork, "fork turn")
	waitTasks(t, opened)
	if err := opened.Close(background); err != nil {
		t.Fatal(err)
	}
}
