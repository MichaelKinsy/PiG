package session_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// upstreamTypeFixtures is JSON.stringify of the packages/agent/test/harness/
// types.test.ts value fixtures, produced by the upstream modules under Node 24.
type upstreamTypeFixtures struct {
	OperationStates []json.RawMessage `json:"operationStates"`
	Operations      []json.RawMessage `json:"operations"`
	OperationResult json.RawMessage   `json:"operationResult"`
	ValueWrites     []json.RawMessage `json:"valueWrites"`
	UsageRow        json.RawMessage   `json:"usageRow"`
	Writes          []json.RawMessage `json:"writes"`
}

func loadTypeFixtures(t *testing.T) upstreamTypeFixtures {
	t.Helper()
	data, err := os.ReadFile("testdata/upstream-types-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures upstreamTypeFixtures
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	return fixtures
}

var fixtureConfiguration = session.LaneConfiguration{Model: session.ModelRef{Provider: "provider", ModelID: "model"}, ThinkingLevel: ai.ThinkingOff, ActiveToolNames: []string{"read"}}

var fixtureRetryPolicy = session.NormalizedRetryPolicy{MaxAttempts: 3, BaseDelayMs: 100, MaxAgentDelayMs: 30_000}

func fixtureCheckpoint() session.CheckpointData {
	return session.CheckpointData{Continuation: session.Continuation{Kind: session.ContinuationNeedAssistant}, TriggerEntryID: "trigger"}
}

func fixtureStates() []session.OperationState {
	scope := session.OperationScope{
		Control: session.Control{Status: session.ControlRunning},
		Settings: session.RunSettings{
			Compaction:   harness.CompactionSettings{Enabled: true, ReserveTokens: 1000, KeepRecentTokens: 2000},
			SteeringMode: agent.QueueModeAll, FollowUpMode: agent.QueueModeOneAtATime, ToolExecution: session.ToolExecutionParallel,
		},
	}
	generation := session.GenerationContext{
		StepID: "step", TriggerEntryID: "trigger", Configuration: fixtureConfiguration,
		StreamOptions: harness.AgentHarnessStreamOptions{Deferred: &harness.AgentHarnessDeferredOption{Object: true, Window: "1h"}},
		RetryPolicy:   fixtureRetryPolicy,
	}
	summaryContext := session.SummaryContext{ResultEntryID: "summary", Configuration: fixtureConfiguration, RetryPolicy: fixtureRetryPolicy}
	checkpoint := fixtureCheckpoint()
	calls := []session.ToolCall{
		{Status: session.ToolCallPlanned, SourceIndex: 0, ResultEntryID: "result-0"},
		{Status: session.ToolCallEffectPending, SourceIndex: 1, ResultEntryID: "result-1", Replay: harness.ToolReplaySafe},
		{Status: session.ToolCallOutcomeReady, SourceIndex: 2, ResultEntryID: "result-2", Terminate: true},
		{Status: session.ToolCallCompleted, SourceIndex: 3, ResultEntryID: "result-3"},
	}
	leaf := func(state session.OperationState) session.OperationState {
		state.OperationScope = scope
		return state
	}
	return []session.OperationState{
		leaf(session.OperationState{At: session.AtStarting}),
		leaf(session.OperationState{At: session.AtCheckpoint, Continuation: checkpoint.Continuation, TriggerEntryID: checkpoint.TriggerEntryID}),
		leaf(session.OperationState{At: session.AtAssistantReady, GenerationContext: generation, NextAttempt: 1}),
		leaf(session.OperationState{At: session.AtAssistantEffectPending, GenerationContext: generation, Attempt: 1, ResponseEntryID: "response", UsageID: "usage", IntendedOutputLimit: 4096, ContextWindow: 128000}),
		leaf(session.OperationState{At: session.AtAssistantRetryWait, GenerationContext: generation, NextAttempt: 2, NotBefore: 10, ErrorMessage: "retry"}),
		leaf(session.OperationState{At: session.AtTools, Batch: session.ToolBatch{AssistantEntryID: "assistant", Configuration: fixtureConfiguration, TurnID: "turn", Calls: calls}}),
		leaf(session.OperationState{At: session.AtDeferredSuspended, StepID: "step", SourceEntryID: "source", Poll: 0, Configuration: fixtureConfiguration}),
		leaf(session.OperationState{At: session.AtDeferredEffectPending, StepID: "step", SourceEntryID: "source", Poll: 1, ResponseEntryID: "response", UsageID: "usage", Configuration: fixtureConfiguration}),
		leaf(session.OperationState{At: session.AtSummaryDeciding, Task: session.SummaryTask{TaskID: "task", Reason: session.SummaryReasonThreshold, Boundary: session.ResultBoundary{Kind: session.BoundaryResumeCheckpoint, ResumeAfter: &checkpoint}}}),
		leaf(session.OperationState{At: session.AtSummaryReady, Task: session.SummaryTask{TaskID: "task", Reason: session.SummaryReasonManual, CustomInstructions: new("compact"), Boundary: session.ResultBoundary{Kind: session.BoundaryFinish}}, SummaryContext: summaryContext, NextAttempt: 1}),
		leaf(session.OperationState{At: session.AtSummaryEffectPending, Task: session.SummaryTask{TaskID: "task", Boundary: session.ResultBoundary{Kind: session.BoundaryCommitNavigation, TargetID: "target", Label: new("target")}}, SummaryContext: summaryContext, Attempt: 1, Request: &session.SummaryRequest{Index: 0, UsageID: "usage"}}),
		leaf(session.OperationState{At: session.AtSummaryRetryWait, Task: session.SummaryTask{TaskID: "task", Reason: session.SummaryReasonOverflow, Boundary: session.ResultBoundary{Kind: session.BoundaryResumeCheckpoint, ResumeAfter: &checkpoint}}, SummaryContext: summaryContext, NextAttempt: 2, NotBefore: 10, ErrorMessage: "retry"}),
		leaf(session.OperationState{At: session.AtNavigationReadyToCommit}),
	}
}

func compact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return string(mustJSON(t, value))
}

func TestOperationStateLeavesMatchUpstreamJSONByteForByte(t *testing.T) {
	fixtures := loadTypeFixtures(t)
	states := fixtureStates()
	if len(states) != len(fixtures.OperationStates) || len(states) != len(session.OperationAts()) {
		t.Fatalf("fixture count %d, upstream %d, leaves %d", len(states), len(fixtures.OperationStates), len(session.OperationAts()))
	}
	for index, state := range states {
		encoded := mustJSON(t, state)
		if string(encoded) != string(fixtures.OperationStates[index]) {
			t.Errorf("%s\n got: %s\nwant: %s", state.At, encoded, fixtures.OperationStates[index])
		}
		var decoded session.OperationState
		if err := json.Unmarshal(fixtures.OperationStates[index], &decoded); err != nil {
			t.Fatalf("%s decode: %v", state.At, err)
		}
		if string(mustJSON(t, decoded)) != string(fixtures.OperationStates[index]) {
			t.Errorf("%s round trip = %s", state.At, mustJSON(t, decoded))
		}
	}
	if err := json.Unmarshal([]byte(`{"at":"finished"}`), new(session.OperationState)); err == nil {
		t.Fatal("unknown leaf decoded")
	}
}

func TestOperationMetaResultAndUsageRowMatchUpstreamJSON(t *testing.T) {
	fixtures := loadTypeFixtures(t)
	operations := []session.OperationMeta{
		{OperationID: "run", Lane: "main", StartedAt: 1, Intent: session.OperationIntent{Kind: session.OperationKindRun, PromptEntryIDs: []string{"prompt"}}},
		{OperationID: "compaction", Lane: "main", SourceTipID: new("source"), StartedAt: 2, Intent: session.OperationIntent{Kind: session.OperationKindCompaction, CustomInstructions: new("compact")}},
		{OperationID: "navigation", Lane: "main", SourceTipID: new("source"), StartedAt: 3, Intent: session.OperationIntent{Kind: session.OperationKindNavigation, TargetID: new("target"), Summarize: true, Label: new("target")}},
	}
	for index, operation := range operations {
		if got := string(mustJSON(t, operation)); got != string(fixtures.Operations[index]) {
			t.Errorf("operation %d\n got: %s\nwant: %s", index, got, fixtures.Operations[index])
		}
	}
	result := session.OperationResultRecord{OperationID: "run", Kind: session.OperationKindRun, Status: session.TerminalCompleted, TipID: new("leaf"), StartedAt: 1, EndedAt: 2}
	if got := string(mustJSON(t, result)); got != string(fixtures.OperationResult) {
		t.Errorf("result\n got: %s\nwant: %s", got, fixtures.OperationResult)
	}
	details := session.JsonValue(map[string]any{"attempt": 1})
	row := session.UsageRow{ID: "usage", Seq: 2, Usage: ai.Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, TotalTokens: 10}, EntryID: new("entry"), Details: &details}
	if got := string(mustJSON(t, row)); got != string(fixtures.UsageRow) {
		t.Errorf("usage row\n got: %s\nwant: %s", got, fixtures.UsageRow)
	}
}

func TestWritesMatchUpstreamJSONMembers(t *testing.T) {
	fixtures := loadTypeFixtures(t)
	runState := fixtureStates()[0]
	valueWrites := []session.Write{
		session.SetValue(session.BranchTip("main"), new("leaf")),
		session.SetValue(session.LaneConfig("main"), fixtureConfiguration),
		session.SetValue(session.LaneStateValue("main"), session.LaneState{CurrentOperationID: new("run")}),
		session.SetValue(session.OperationResult("run"), session.OperationResultRecord{OperationID: "run", Kind: session.OperationKindRun, Status: session.TerminalCompleted, TipID: new("leaf"), StartedAt: 1, EndedAt: 2}),
		session.SetValue(session.OperationMetaValue("run"), session.OperationMeta{OperationID: "run", Lane: "main", StartedAt: 1, Intent: session.OperationIntent{Kind: session.OperationKindRun, PromptEntryIDs: []string{"prompt"}}}),
		session.SetValue(session.OperationStateValue("run"), runState),
		session.SetValue(session.OperationToolArgs("run", "step", 0), map[string]session.JsonValue{"path": "file"}),
		session.SetValue(session.OperationPreparation("run", "task"), session.DurableStructuralPreparation{Kind: session.PreparationCompaction, TokensBefore: 100, Settings: harness.CompactionSettings{Enabled: true, ReserveTokens: 1000, KeepRecentTokens: 2000}}),
		session.SetValue(session.PendingEntryValue("pending"), session.PendingEntry{Type: session.PendingEntryCustom, CustomType: "note", CustomPayload: new(any(map[string]any{"text": "pending"}))}),
		session.SetValue(session.SessionName, "session"),
		session.SetValue(session.EntryLabel("entry"), "label"),
		session.SetValue(session.MustValue[any]("test.value", "state"), nil),
	}
	assertMembersEqual(t, valueWrites, fixtures.ValueWrites)
	usage := ai.Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, TotalTokens: 10}
	writes := []session.Write{
		session.InsertEntry(session.Entry{ID: "entry", Type: session.EntryTypeMessage, Message: userText("hello", 1)}),
		session.InsertUsage(session.UsageRow{ID: "usage", Usage: usage, EntryID: new("entry")}),
		valueWrites[0],
		session.DeleteValue(session.EntryLabel("entry")),
	}
	// Upstream stores the user content string "hello" verbatim; Go normalizes
	// user content to text blocks, so compare the entry write separately.
	assertMembersEqual(t, writes[1:], fixtures.Writes[1:])
	var entryWrite struct {
		Kind  string `json:"kind"`
		Entry struct {
			ID       string             `json:"id"`
			ParentID *string            `json:"parentId"`
			Type     string             `json:"type"`
			Message  agent.AgentMessage `json:"message"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(fixtures.Writes[0], &entryWrite); err != nil {
		t.Fatal(err)
	}
	if entryWrite.Kind != "entry" || entryWrite.Entry.ID != "entry" || entryWrite.Entry.ParentID != nil {
		t.Fatalf("upstream entry write = %#v", entryWrite)
	}
	assertJSON(t, writes[0], map[string]any{"kind": "entry", "entry": map[string]any{"id": "entry", "parentId": nil, "type": "message", "message": entryWrite.Entry.Message}})
}

func assertMembersEqual[T any](t *testing.T, got []T, want []json.RawMessage) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("count %d, want %d", len(got), len(want))
	}
	for index := range got {
		var gotValue, wantValue any
		if err := json.Unmarshal(mustJSON(t, got[index]), &gotValue); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(want[index], &wantValue); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotValue, wantValue) {
			t.Errorf("item %d\n got: %s\nwant: %s", index, mustJSON(t, got[index]), compact(t, want[index]))
		}
	}
}

func TestPendingEntryAndEntryJSONKeepNullDistinctFromAbsent(t *testing.T) {
	var pending session.PendingEntry
	if err := json.Unmarshal([]byte(`{"type":"custom","customType":"note","payload":null}`), &pending); err != nil || pending.CustomPayload == nil || *pending.CustomPayload != nil {
		t.Fatalf("null payload = %#v, %v", pending, err)
	}
	if err := json.Unmarshal([]byte(`{"type":"custom","customType":"note"}`), &pending); err != nil || pending.CustomPayload != nil {
		t.Fatalf("absent payload = %#v, %v", pending, err)
	}
	var entry session.Entry
	if err := json.Unmarshal([]byte(`{"id":"e","parentId":null,"seq":1,"timestamp":2,"type":"custom","customType":"x","data":null}`), &entry); err != nil || entry.Data == nil || *entry.Data != nil {
		t.Fatalf("null data = %#v, %v", entry, err)
	}
	if got := string(mustJSON(t, entry)); got != `{"id":"e","parentId":null,"type":"custom","customType":"x","data":null,"seq":1,"timestamp":2}` {
		t.Fatalf("entry JSON = %s", got)
	}
	if err := json.Unmarshal([]byte(`{"type":"unknown"}`), &entry); err == nil {
		t.Fatal("unknown entry type decoded")
	}
}

func TestEntryJSONRoundTripsEveryType(t *testing.T) {
	details := session.JsonValue(map[string]any{"k": "v"})
	entries := []session.Entry{
		{ID: "m", Seq: 1, Timestamp: 5, Type: session.EntryTypeMessage, Message: userText("hi", 1), Terminate: true},
		{ID: "c", ParentID: new("m"), Seq: 2, Timestamp: 5, Type: session.EntryTypeCompaction, Summary: "s", RetainedTail: []agent.AgentMessage{userText("tail", 1)}, TokensBefore: 7, Details: &details, Usage: &ai.Usage{Input: 1}, FromHook: true},
		{ID: "b", ParentID: new("c"), Seq: 3, Timestamp: 5, Type: session.EntryTypeBranchSummary, FromID: nil, Summary: "branch"},
		{ID: "x", ParentID: new("b"), Seq: 4, Timestamp: 5, Type: session.EntryTypeCustom, CustomType: "note"},
	}
	for _, entry := range entries {
		encoded := mustJSON(t, entry)
		var decoded session.Entry
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("%s: %v", entry.Type, err)
		}
		if string(mustJSON(t, decoded)) != string(encoded) {
			t.Fatalf("%s round trip\n got: %s\nwant: %s", entry.Type, mustJSON(t, decoded), encoded)
		}
	}
	if got := string(mustJSON(t, entries[2])); got != `{"id":"b","parentId":"c","type":"branch_summary","fromId":null,"summary":"branch","fromHook":false,"seq":3,"timestamp":5}` {
		t.Fatalf("branch summary JSON = %s", got)
	}
}

func TestStoredValueJSONUsesUpstreamMemberNames(t *testing.T) {
	stored := session.StoredValue[string]{Address: session.SessionName, Value: "name", Seq: 7}
	assertJSON(t, stored, map[string]any{
		"address": map[string]any{"namespace": "pi.session.name", "key": "", "kind": "value"},
		"value":   "name", "seq": 7,
	})
}
