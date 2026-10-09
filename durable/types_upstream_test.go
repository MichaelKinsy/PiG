package durable

// pi: packages/durable/src/tasks.ts

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Ports packages/durable/test/types.test.ts
//
// Upstream asserts type-level contracts with expectTypeOf and @ts-expect-error. The Go port type-checks each
// expected compile failure against the compiled durable package, and asserts the runtime shape of each valid literal
// through its JSON encoding.

var durableImports = map[string]string{"d": "github.com/MichaelKinsy/PiG/durable"}

func expectCompiles(t *testing.T, name, body string) {
	t.Helper()
	testenv.ExpectCompiles(t, name, durableImports, body)
}

func expectTypeError(t *testing.T, name, body string) {
	t.Helper()
	testenv.ExpectTypeError(t, name, durableImports, body)
}

func jsonOf(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestTypesUpstream(t *testing.T) {
	conversationId := IdFromNumber[ConversationId](1)
	entryId := IdFromNumber[EntryId](2)
	answerId := IdFromNumber[EntryId](3)
	taskId := IdFromNumber[TaskId](4)
	submissionId := IdFromNumber[SubmissionId](5)
	documentId := IdFromNumber[DocumentId](6)
	seq := SeqFromNumber(1)

	// types.test.ts:48. TaskId does not carry its result type in Go (methods cannot take type parameters), so the
	// narrowedTask and TaskResult<typeof taskId> checks are the Go equivalences below: the ID is the same wire number,
	// and the typed result is decoded from the settled outcome (durable/harness TestTypesUpstreamHarness).
	t.Run("brands numeric IDs by record kind and carries task result types", func(t *testing.T) {
		if reflect.TypeOf(conversationId).Kind() != reflect.Int64 {
			t.Fatalf("conversation ID kind = %s", reflect.TypeOf(conversationId).Kind())
		}
		if got := jsonOf(t, taskId); got != "4" {
			t.Fatalf("JSON.stringify(taskId) = %s", got)
		}
		// narrowedTask: the result type is erased at run time, so a narrowed and a widened ID are the same wire number.
		if narrowed := IdFromNumber[TaskId](7); jsonOf(t, narrowed) != "7" || narrowed != TaskId(7) {
			t.Fatalf("narrowed task ID = %v", narrowed)
		}
		expectCompiles(t, "widenedTask", "var widenedTask d.TaskId = d.IdFromNumber[d.TaskId](7)\n_ = widenedTask")
		expectTypeError(t, "conversation IDs are not task IDs", "var wrongTask d.TaskId = d.IdFromNumber[d.ConversationId](1)\n_ = wrongTask")
		expectTypeError(t, "task IDs are not conversation IDs", "var wrongConversation d.ConversationId = d.IdFromNumber[d.TaskId](4)\n_ = wrongConversation")
		expectTypeError(t, "entry IDs are not document IDs", "var wrongDocument d.DocumentId = d.IdFromNumber[d.EntryId](2)\n_ = wrongDocument")
		expectTypeError(t, "entity IDs are not commit sequences", "var wrongSequence d.Seq = d.IdFromNumber[d.EntryId](2)\n_ = wrongSequence")
	})

	// types.test.ts:72. Go record structs carry a discriminant and every member field, so the @ts-expect-error
	// literals that set a member field of another union member (missingReplacement, omissionWithMessages,
	// pendingWithOutcome, terminalWithCheckpoint, terminalWithMemos, sessionWithHistory, conversationWithoutPolicy,
	// taskCreateWithPolicy, baseWithOps, deltaWithValue, createWithDelta, completedWithError, queuedWithEntry,
	// inputWithoutAnswer, writeWithAnswer) compile; TestTypesUpstreamExclusivityLiteralsEncodeAsPiObjects pins each as
	// the JSON of Pi's object for the same inputs. The fields Go types do not declare stay compile errors.
	t.Run("encodes discriminator-dependent fields", func(t *testing.T) {
		omit := ContextEdit{Target: entryId, Action: EditOmit}
		replace := ContextEdit{Target: entryId, Action: EditReplace, Messages: []ai.Message{}}
		pending := TaskState[map[string]any, map[string]any]{Status: TaskPending, Checkpoint: &map[string]any{"phase": "ready"}}
		terminal := TaskState[map[string]any, map[string]any]{
			Status:  TaskTerminal,
			Outcome: &TaskOutcome[map[string]any]{Status: OutcomeCompleted, Result: &map[string]any{"value": 1}},
		}
		completedInput := SubmissionRecord{Id: submissionId, ConversationId: conversationId, Type: SubmissionTypeInput, Status: SubmissionDone, Entry: &entryId, Answer: &answerId}
		completedWrite := SubmissionRecord{Id: submissionId, ConversationId: conversationId, Type: SubmissionTypeWrite, Status: SubmissionDone, Entry: &entryId}
		queuedWriteCreate := SubmissionCreate{ConversationId: conversationId, Type: SubmissionTypeWrite, Status: SubmissionQueued}
		baseContent := DocumentContent{Kind: ContentBase, Version: 1, Value: delta.JsonObjectOf("count", 1)}
		deltaContent := DocumentContent{Kind: ContentDelta, Version: 1, Ops: []Op{{"s", []any{"count"}, 2}}}
		conversationDocument := DocumentCreate{
			Id: documentId, Kind: "test", Scope: DocumentRecordScope{Kind: ScopeConversation, ConversationId: conversationId},
			History: HistoryRewindable, Fork: ForkAsOf,
		}

		for _, tc := range []struct {
			name  string
			value any
			want  string
		}{
			{"omit", omit, `{"target":2,"action":"omit"}`},
			{"replace", replace, `{"target":2,"action":"replace","messages":[]}`},
			{"pending", pending, `{"status":"pending","checkpoint":{"phase":"ready"}}`},
			{"terminal", terminal, `{"status":"terminal","outcome":{"status":"completed","result":{"value":1}}}`},
			{"completedInput", completedInput, `{"id":5,"conversationId":1,"type":"input","status":"done","entry":2,"answer":3}`},
			{"completedWrite", completedWrite, `{"id":5,"conversationId":1,"type":"write","status":"done","entry":2}`},
			{"queuedWriteCreate", queuedWriteCreate, `{"conversationId":1,"type":"write","status":"queued"}`},
			{"baseContent", baseContent, `{"version":1,"kind":"base","value":{"count":1}}`},
			{"deltaContent", deltaContent, `{"version":1,"kind":"delta","ops":[["s",["count"],2]]}`},
			{"conversationDocument", conversationDocument, `{"id":6,"kind":"test","scope":{"kind":"conversation","conversationId":1},"history":"rewindable","fork":"asOf"}`},
		} {
			if got := jsonOf(t, tc.value); got != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got, tc.want)
			}
		}
		_ = seq

		expectTypeError(t, "storage, not the create command, supplies createdAt",
			"_ = d.DocumentCreate{Id: 6, Kind: \"test\", Scope: d.DocumentRecordScope{Kind: d.ScopeSession}, CreatedAt: d.SeqFromNumber(1)}")
		expectTypeError(t, "Session, not the submission create value, assigns its ID",
			"_ = d.SubmissionCreate{Id: 5, ConversationId: 1, Type: d.SubmissionTypeWrite, Status: d.SubmissionQueued}")
	})

	// types.test.ts:248. defineExtension, hook, HooksOf, Harness, and Conversation are durable/harness declarations;
	// this case covers the submission drafts, the typed task with narrowed input, several phases, and custom hooks,
	// its erasure into AnyTask, and the typed task input.
	t.Run("types submissions, task waits, compaction, and tasks erased into extensions", func(t *testing.T) {
		input := SubmissionDraft{Type: SubmissionTypeInput, WhenBusy: WhenBusySteer}
		write := SubmissionDraft{Type: SubmissionTypeWrite, Entry: &EntryDraft{Kind: "note"}}
		if input.Type != SubmissionTypeInput || write.Type != SubmissionTypeWrite {
			t.Fatalf("drafts = %v %v", input.Type, write.Type)
		}

		stepper := DefineTask(stepperDefinition())
		var erased AnyTask = stepper
		definition := erased.AnyDefinition()
		if definition.Name != "test.stepper" || definition.Version != 1 || len(definition.Phases) != 2 {
			t.Fatalf("erased definition = %+v", definition)
		}
		if _, ok := definition.Hooks.(stepperHooks); !ok {
			t.Fatalf("erased hooks = %T", definition.Hooks)
		}
		checkpoint, err := definition.Initial(map[string]any{"steps": float64(2)})
		if err != nil {
			t.Fatal(err)
		}
		if got := jsonOf(t, checkpoint); got != `{"phase":"plan","steps":2}` {
			t.Fatalf("initial checkpoint = %s", got)
		}

		expectCompiles(t, "typed input", stepperSnippet+"\n_, _ = d.CreateTask(tx, stepper, stepperInput{Steps: 2}, d.TaskOptions{Ownership: d.TaskOwnership{Kind: d.TaskOwnedByConversation}})")
		expectTypeError(t, "task input is typed by the definition", stepperSnippet+"\n_, _ = d.CreateTask(tx, stepper, map[string]any{\"steps\": \"two\"}, d.TaskOptions{Ownership: d.TaskOwnership{Kind: d.TaskOwnedByConversation}})")
	})
}

const stepperSnippet = `type stepperInput struct{ Steps int }
type checkpoint struct{ Phase string; Steps, Step int }
var tx d.Tx
stepper := d.DefineTask(d.TaskDefinition[stepperInput, checkpoint, struct{}, struct{}]{Name: "test.stepper", Version: 1, Initial: func(input stepperInput) checkpoint { return checkpoint{Phase: "plan", Steps: input.Steps} }})`

type stepperInput struct {
	Steps int `json:"steps"`
}

type stepperCheckpoint struct {
	Phase string `json:"phase"`
	Steps int    `json:"steps,omitempty"`
	Step  int    `json:"step,omitempty"`
}

type stepperResult struct {
	Ran int `json:"ran"`
}

type stepperHooks struct {
	BeforeStep func(step int) *struct{ Skip bool }
}

func stepperDefinition() TaskDefinition[stepperInput, stepperCheckpoint, stepperResult, stepperHooks] {
	return TaskDefinition[stepperInput, stepperCheckpoint, stepperResult, stepperHooks]{
		Name:    "test.stepper",
		Version: 1,
		Initial: func(input stepperInput) stepperCheckpoint {
			return stepperCheckpoint{Phase: "plan", Steps: input.Steps}
		},
		Phases: map[string]PhaseHandler[stepperInput, stepperCheckpoint, stepperResult, stepperHooks]{
			"plan": func(ctx context.Context, task RunningTask[stepperInput, stepperCheckpoint, stepperResult], runtime TaskRuntime[stepperInput, stepperCheckpoint, stepperResult, stepperHooks]) error {
				return runtime.Commit(ctx, func(Tx, RunningTask[stepperInput, stepperCheckpoint, stepperResult]) (*NextTaskState[stepperCheckpoint, stepperResult], error) {
					return &NextTaskState[stepperCheckpoint, stepperResult]{Status: TaskRunning, Checkpoint: &stepperCheckpoint{Phase: "run", Step: task.Input.Steps}}, nil
				})
			},
			"run": func(ctx context.Context, task RunningTask[stepperInput, stepperCheckpoint, stepperResult], runtime TaskRuntime[stepperInput, stepperCheckpoint, stepperResult, stepperHooks]) error {
				ran := 0
				if err := runtime.Hooks().Each("beforeStep", func(handlers stepperHooks) error {
					if handlers.BeforeStep != nil && handlers.BeforeStep(task.State.Checkpoint.Step) == nil {
						ran++
					}
					return nil
				}); err != nil {
					return err
				}
				return runtime.Commit(ctx, func(Tx, RunningTask[stepperInput, stepperCheckpoint, stepperResult]) (*NextTaskState[stepperCheckpoint, stepperResult], error) {
					return &NextTaskState[stepperCheckpoint, stepperResult]{Status: TaskTerminal, Outcome: &TaskOutcome[stepperResult]{Status: OutcomeCompleted, Result: &stepperResult{Ran: ran}}}, nil
				})
			},
		},
		Abort: func(context.Context, RunningTask[stepperInput, stepperCheckpoint, stepperResult], TaskRuntime[stepperInput, stepperCheckpoint, stepperResult, stepperHooks]) error {
			return nil
		},
	}
}

// types.test.ts:72 member-exclusivity literals. Upstream's records are TypeScript unions, so each @ts-expect-error
// literal below is rejected only by the compiler: at run time it is a plain object and JSON.stringify(literal) is its
// own JSON. Go's record unions are one struct with a discriminant and every member field, so each literal is a legal
// value whose JSON is, member for member, the JSON of Pi 1.0.0's object for the same inputs (compared as JSON, key
// order aside). The nil/omitted fields Go drops are exactly the members the literal did not set.
func TestTypesUpstreamExclusivityLiteralsEncodeAsPiObjects(t *testing.T) {
	entryId, answerId := IdFromNumber[EntryId](2), IdFromNumber[EntryId](3)
	conversationId := IdFromNumber[ConversationId](1)
	taskId := IdFromNumber[TaskId](4)
	submissionId := IdFromNumber[SubmissionId](5)
	documentId := IdFromNumber[DocumentId](6)
	seq := SeqFromNumber(1)
	completedWith := func(error *TaskOutcomeError) *TaskOutcome[float64] {
		result := 1.0
		return &TaskOutcome[float64]{Status: OutcomeCompleted, Result: &result, Error: error}
	}
	phase := map[string]any{"phase": "ready"}
	createWithDelta := DocumentCreateWrite{
		Record:  DocumentCreate{Id: documentId, Kind: "test", Scope: DocumentRecordScope{Kind: ScopeSession}},
		Content: DocumentContent{Kind: ContentDelta, Version: 1, Ops: []Op{}},
	}

	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"missingReplacement", ContextEdit{Target: entryId, Action: EditReplace},
			`{"target":2,"action":"replace"}`},
		{"omissionWithMessages", ContextEdit{Target: entryId, Action: EditOmit, Messages: []ai.Message{}},
			`{"target":2,"action":"omit","messages":[]}`},
		{"pendingWithOutcome", TaskState[map[string]any, float64]{Status: TaskPending, Checkpoint: &phase, Outcome: completedWith(nil)},
			`{"status":"pending","checkpoint":{"phase":"ready"},"outcome":{"status":"completed","result":1}}`},
		{"terminalWithCheckpoint", TaskState[map[string]any, float64]{Status: TaskTerminal, Checkpoint: &phase, Outcome: completedWith(nil)},
			`{"status":"terminal","checkpoint":{"phase":"ready"},"outcome":{"status":"completed","result":1}}`},
		{"terminalWithMemos", TaskRecord[any, map[string]any, float64]{
			Id: taskId, ConversationId: conversationId, Kind: "test", Version: 1, Input: nil,
			State:      TaskState[map[string]any, float64]{Status: TaskTerminal, Outcome: completedWith(nil)},
			Background: false, AbortRequested: false, Memos: map[string]JsonValue{"retained": true},
		}, `{"id":4,"conversationId":1,"kind":"test","version":1,"input":null,"background":false,"abortRequested":false,"state":{"status":"terminal","outcome":{"status":"completed","result":1}},"memos":{"retained":true}}`},
		{"sessionWithHistory", DocumentRecord{Id: documentId, Kind: "test", CreatedAt: seq, Scope: DocumentRecordScope{Kind: ScopeSession}, History: HistoryLatest, Fork: ForkCurrent},
			`{"id":6,"kind":"test","createdAt":1,"scope":{"kind":"session"},"history":"latest","fork":"current"}`},
		{"conversationWithoutPolicy", DocumentCreate{Id: documentId, Kind: "test", Scope: DocumentRecordScope{Kind: ScopeConversation, ConversationId: conversationId}},
			`{"id":6,"kind":"test","scope":{"kind":"conversation","conversationId":1}}`},
		{"taskCreateWithPolicy", DocumentCreate{Id: documentId, Kind: "test", Scope: DocumentRecordScope{Kind: ScopeTask, TaskId: taskId}, History: HistoryLatest, Fork: ForkInitial},
			`{"id":6,"kind":"test","scope":{"kind":"task","taskId":4},"history":"latest","fork":"initial"}`},
		{"baseWithOps", DocumentContent{Kind: ContentBase, Version: 1, Value: delta.NewJsonObject(0), Ops: []Op{}},
			`{"kind":"base","version":1,"value":{},"ops":[]}`},
		{"deltaWithValue", DocumentContent{Kind: ContentDelta, Version: 1, Ops: []Op{}, Value: delta.NewJsonObject(0)},
			`{"kind":"delta","version":1,"ops":[],"value":{}}`},
		// StorageWrite is a Go interface the storage codecs encode; its write type and two members carry the literal's
		// fields.
		{"createWithDelta", struct {
			Type    string          `json:"type"`
			Record  DocumentCreate  `json:"record"`
			Content DocumentContent `json:"content"`
		}{Type: createWithDelta.storageWriteType(), Record: createWithDelta.Record, Content: createWithDelta.Content},
			`{"type":"document.create","record":{"id":6,"kind":"test","scope":{"kind":"session"}},"content":{"kind":"delta","version":1,"ops":[]}}`},
		{"completedWithError", completedWith(&TaskOutcomeError{Message: "impossible"}),
			`{"status":"completed","result":1,"error":{"message":"impossible"}}`},
		{"queuedWithEntry", SubmissionRecord{Id: submissionId, ConversationId: conversationId, Type: SubmissionTypeInput, Status: SubmissionQueued, Entry: &entryId},
			`{"id":5,"conversationId":1,"type":"input","status":"queued","entry":2}`},
		{"inputWithoutAnswer", SubmissionRecord{Id: submissionId, ConversationId: conversationId, Type: SubmissionTypeInput, Status: SubmissionDone, Entry: &entryId},
			`{"id":5,"conversationId":1,"type":"input","status":"done","entry":2}`},
		{"writeWithAnswer", SubmissionRecord{Id: submissionId, ConversationId: conversationId, Type: SubmissionTypeWrite, Status: SubmissionDone, Entry: &entryId, Answer: &answerId},
			`{"id":5,"conversationId":1,"type":"write","status":"done","entry":2,"answer":3}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got, want any
			if err := json.Unmarshal([]byte(jsonOf(t, tc.value)), &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s encodes as %s, Pi's object is %s", tc.name, jsonOf(t, tc.value), tc.want)
			}
		})
	}
}

// types.ts:671-732: a copy source is { id, at }; a StorageWrite is one of eight discriminated types; TableCommitChange is exactly the
// conversation, entry, task and submission writes; DocumentCommitChange is the "document" and "document.copy" changes. The assignments
// below are compile-time membership proofs, the table pins every discriminator string.
func TestCommitChangeUnionsKeepPiDiscriminatorsAndMembership(t *testing.T) {
	source := DocumentCopySource{Id: 7, At: DocumentPoint{Seq: 3}}
	tables := []TableCommitChange{ConversationWrite{}, EntryWrite{}, TaskWrite{}, SubmissionWrite{}}
	documents := []DocumentCommitChange{DocumentChange{}, DocumentCopyChange{Source: source}}
	var want = []string{"conversation", "entry", "task", "submission"}
	for i, change := range tables {
		if got := CommitChangeType(change); got != want[i] {
			t.Errorf("table change %d type = %q, want %q", i, got, want[i])
		}
		if got := StorageWriteType(change); got != want[i] {
			t.Errorf("table write %d type = %q, want %q", i, got, want[i])
		}
	}
	for i, change := range documents {
		if got, want := CommitChangeType(change), []string{"document", "document.copy"}[i]; got != want {
			t.Errorf("document change %d type = %q, want %q", i, got, want)
		}
	}
	writes := map[string]StorageWrite{
		"document.create": DocumentCreateWrite{}, "document.copy": DocumentCopyWrite{Source: source},
		"document.change": DocumentChangeWrite{}, "document.retire": DocumentRetireWrite{},
	}
	for kind, write := range writes {
		if got := StorageWriteType(write); got != kind {
			t.Errorf("write type = %q, want %q", got, kind)
		}
	}
	if copyWrite := writes["document.copy"].(DocumentCopyWrite); copyWrite.Source != source {
		t.Fatalf("copy source lost: %+v", copyWrite)
	}
	if copyChange := documents[1].(DocumentCopyChange); copyChange.Source != source {
		t.Fatalf("copy change source lost: %+v", copyChange)
	}
}
