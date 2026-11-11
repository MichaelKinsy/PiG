// Ports packages/durable/test/harness-tool-parks.test.ts (Pi 473515f9c): tools that park (spec suspendable §4): a sleep on
// signals or time, a wait on owned tasks, the parked slot, the run after the wake, invalid parks, cleanup through Cancel,
// recovery, and wrappers. See harness_tasks_test.go for the Go mappings that apply to every case; a park is
// ToolExecutionResult.Park, and a crash is the process's storage going dead (thOpen), as in harness_tool_hooks_test.go.

package harness

// pi: packages/durable/src/harness/tool.ts, parks.ts

import (
	"context"
	"encoding/json"
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
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/schema"
)

// tpText is the value of tpDone.
type tpText struct {
	Text string `json:"text"`
}

// tpDone is Done of the Pi file: `defineSignal({ name: "test.done", version: 1, schema: obj({ text: str() }) })`.
var tpDone = durable.DefineSignal(durable.SignalDefinition[tpText]{
	Name: "test.done", Version: 1,
	Schema: schema.Obj(schema.Properties{schema.Prop("text", schema.Str())}),
})

func tpTool(name string, execute tlExecute, edit ...func(*durable.ToolRegistration)) *durable.ToolRegistration {
	return ownTool(name, name, emptyObjectSchema(), execute, edit...)
}

func tpCall(name string) ai.FauxResponseStep { return trCall(name, "c1") }

func tpOpen(t *testing.T, setup *chatState, store ...durable.Storage) (Harness, Conversation) {
	t.Helper()
	var chosen durable.Storage = storage.NewMemoryStorage()
	if len(store) > 0 {
		chosen = store[0]
	}
	harness, root := openChat(t, chosen, setup)
	harness.Resume()
	return harness, root
}

// tpLive is the root conversation's pi.live.
func tpLive(t *testing.T, harness Harness, conversationId durable.ConversationId) *LiveState {
	t.Helper()
	live, err := durable.Snapshot[LiveState](testContext, harness, LiveDoc, conversationId)
	if err != nil {
		t.Fatal(err)
	}
	if live == nil {
		return &LiveState{}
	}
	return live
}

// tpSlot is the first tool slot of the root conversation; nil when there is none.
func tpSlot(t *testing.T, harness Harness) *ToolSlot {
	t.Helper()
	live := tpLive(t, harness, 1)
	if len(live.Tools) == 0 {
		return nil
	}
	return &live.Tools[0]
}

// tpParkedTask is the tool task of the first slot, once it is status.
func tpParkedTask(t *testing.T, harness Harness, status ...durable.TaskStatus) durable.TaskId {
	t.Helper()
	want := durable.TaskSleeping
	if len(status) > 0 {
		want = status[0]
	}
	var id durable.TaskId
	waitFor(t, func() bool {
		slot := tpSlot(t, harness)
		if slot == nil || slot.TaskId == nil {
			return false
		}
		id = *slot.TaskId
		record, err := harness.GetTask(testContext, id)
		return err == nil && record != nil && record.State.Status == want
	})
	return id
}

// tpRun is what one run of a parking tool saw.
type tpRun struct {
	wake       *durable.WakeReason
	checkpoint durable.JsonValue
}

type tpRuns struct {
	mu   sync.Mutex
	runs []tpRun
}

func (log *tpRuns) add(run tpRun) int {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.runs = append(log.runs, run)
	return len(log.runs)
}

func (log *tpRuns) all() []tpRun {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.runs)
}

func tpSeen(api durable.ToolExecutionApi) tpRun {
	checkpoint, _ := api.Checkpoint()
	return tpRun{wake: api.Wake(), checkpoint: checkpoint}
}

// tpJSON is value as its JSON round trip, which compares the objects the Harness hands back with plain expectations.
func tpJSON(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		panic(err)
	}
	return decoded
}

func tpExpect(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(tpJSON(got), tpJSON(want)) {
		t.Fatalf("%s: got %s, want %s", what, tpMarshal(got), tpMarshal(want))
	}
}

func tpMarshal(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func tpExpectRuns(t *testing.T, got []tpRun, want ...tpRun) {
	t.Helper()
	view := func(runs []tpRun) any {
		out := make([]map[string]any, len(runs))
		for index, run := range runs {
			out[index] = map[string]any{"wake": run.wake, "checkpoint": run.checkpoint}
		}
		return out
	}
	tpExpect(t, "runs", view(got), view(want))
}

// tpSignalTool is signalTool of the Pi file: a tool that reports, then parks on tpDone until the key holds a value, and
// answers with it.
func tpSignalTool(runs *tpRuns, edit ...func(*durable.ToolRegistration)) *durable.ToolRegistration {
	return tpTool("ci", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		count := runs.add(tpSeen(api))
		state, err := durable.ReadSignal(ctx, api, tpDone, "done")
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if state != nil && !state.Expired {
			return ownText(state.Value.Text), nil
		}
		api.Output("started\n")
		api.Diagnostic(durable.ToolDiagnostic{Severity: durable.SeverityInfo, Message: fmt.Sprintf("run %d", count)})
		if err := api.Details(ctx, map[string]any{"build": float64(count)}); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{
			Checkpoint: map[string]any{"build": float64(count)}, HasCheckpoint: true, Signals: []durable.SignalKey{tpDone.Key("done")},
		}}}, nil
	}, edit...)
}

func tpSleep(signals ...durable.SignalKey) durable.ToolExecutionResult {
	return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{Signals: signals}}}
}

func tpSignal(t *testing.T, harness Harness, id durable.TaskId, key, text string) durable.SignalResult {
	t.Helper()
	result, err := Signal(testContext, harness, id, tpDone, key, tpText{Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func tpWait(t *testing.T, submission durable.Submission) durable.SubmissionStatus {
	t.Helper()
	settled, err := submission.Wait(testContext)
	if err != nil {
		t.Fatal(err)
	}
	return settled.Status
}

func tpStoredSubmission(t *testing.T, harness Harness, id durable.SubmissionId) durable.Submission {
	t.Helper()
	submission, err := harness.Submission(testContext, id)
	if err != nil || submission == nil {
		t.Fatalf("Submission(%d) %v %v", id, submission, err)
	}
	return submission
}

func tpIdle(t *testing.T, harness Harness) {
	t.Helper()
	waitFor(t, func() bool { return !harness.Wake().Active })
}

func tpReports(setup *chatState) []string {
	var messages []string
	for _, err := range setup.Reports.all() {
		messages = append(messages, err.Error())
	}
	return messages
}

func tpExpectReported(t *testing.T, setup *chatState, message string) {
	t.Helper()
	if !slices.Contains(tpReports(setup), message) {
		t.Fatalf("reports %q, want one %q", tpReports(setup), message)
	}
}

func tpWaitTask(t *testing.T, harness Harness, id durable.TaskId) durable.SettledTask[durable.JsonValue] {
	t.Helper()
	settled, err := harness.WaitForTask(testContext, id)
	if err != nil {
		t.Fatal(err)
	}
	return settled
}

type tpPhase struct {
	Phase string `json:"phase"`
}

func TestToolParksOnASignal(t *testing.T) {
	// harness-tool-parks.test.ts:119 it("holds no invocation, shows the slot sleeping without output, and runs again once woken")
	t.Run("holds no invocation, shows the slot sleeping without output, and runs again once woken", func(t *testing.T) {
		setup := chatSetup(t)
		runs := &tpRuns{}
		addTool(t, setup.Registry, tpSignalTool(runs))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall("ci"), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		id := tpParkedTask(t, harness)
		record := tkTaskRecord(t, harness, id)
		tpExpect(t, "state", record.State, map[string]any{
			"status":     "sleeping",
			"checkpoint": map[string]any{"phase": "parked", "arguments": map[string]any{}, "replay": "unsafe", "checkpoint": map[string]any{"build": float64(1)}},
			"signals":    []any{map[string]any{"signal": "test.done", "key": "done"}},
		})
		// Details and diagnostics stay, the output goes.
		slot := tpSlot(t, harness)
		if slot.Status != ToolSlotSleeping || slot.Details == nil {
			t.Fatalf("slot %+v, want sleeping with details", slot)
		}
		tpExpect(t, "details", *slot.Details, map[string]any{"build": float64(1)})
		tpExpect(t, "diagnostics", slot.Diagnostics, []any{map[string]any{"severity": "info", "message": "run 1"}})
		if slot.Output != nil {
			t.Fatalf("output %q, want none", *slot.Output)
		}
		tpIdle(t, harness)
		if wake := harness.Wake(); wake.Active || wake.At != nil {
			t.Fatalf("wake %+v, want {active: false}", wake)
		}

		if result := tpSignal(t, harness, id, "done", "passed"); result != durable.SignalDelivered {
			t.Fatalf("signal %q, want delivered", result)
		}
		if status := tpWait(t, submission); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		tpExpectRuns(t, runs.all(),
			tpRun{},
			tpRun{wake: &durable.WakeReason{Reason: durable.WakeSignal, Signal: "test.done", Key: "done"}, checkpoint: map[string]any{"build": float64(1)}},
		)
		result := tlResults(allEntries(t, root))[0]
		// The result keeps the details and diagnostics the parked run reported.
		if result.IsError {
			t.Fatalf("result %+v, want no error", result)
		}
		tpExpect(t, "details", result.Details, map[string]any{"build": float64(1)})
		if got := trText(result); got != "passed|<harness>\n[info] run 1\n</harness>" {
			t.Fatalf("text %q", got)
		}
		if result.DurationMs == nil || *result.DurationMs < 0 {
			t.Fatalf("durationMs %v, want >= 0", result.DurationMs)
		}
		mustClose(t, harness)
	})

	// harness-tool-parks.test.ts:156 it("parks on time and wakes with reason time")
	t.Run("parks on time and wakes with reason time", func(t *testing.T) {
		setup := chatSetup(t)
		clock := &testClock{now: 1_000}
		setup.SetNow(clock.Now)
		wakes := &tpRuns{}
		addTool(t, setup.Registry, tpTool("poll", func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			wakes.add(tpSeen(api))
			if api.Wake() == nil {
				until := api.Now() + 500
				return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{Until: &until}}}, nil
			}
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall("poll"), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		tpParkedTask(t, harness)
		waitFor(t, func() bool {
			wake := harness.Wake()
			return wake.At != nil && *wake.At == 1_500 && !wake.Active
		})
		clock.advance(500)
		harness.Resume()
		if status := tpWait(t, submission); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		tpExpectRuns(t, wakes.all(), tpRun{}, tpRun{wake: &durable.WakeReason{Reason: durable.WakeTime}})
		result := tlResults(allEntries(t, root))[0]
		// The span from the call's first run to its result, on the Harness clock.
		if result.DurationMs == nil || *result.DurationMs != 500 {
			t.Fatalf("durationMs %v, want 500", result.DurationMs)
		}
		mustClose(t, harness)
	})

	// harness-tool-parks.test.ts:183 it("stays parked across reopen, running no tool code, and wakes on a later delivery")
	t.Run("stays parked across reopen, running no tool code, and wakes on a later delivery", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		runs := &tpRuns{}
		addTool(t, setup.Registry, tpSignalTool(runs))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall("ci"), tlDone()})
		harness, root := trOpen(t, path, setup)
		submissionId := ownSubmit(t, root, ownInput("go")).Id()
		id := tpParkedTask(t, harness)
		mustClose(t, harness)
		harness, _ = trOpen(t, path, setup)
		tpIdle(t, harness)
		if got := len(runs.all()); got != 1 {
			t.Fatalf("%d runs, want 1", got)
		}
		tpSignal(t, harness, id, "done", "later")
		if status := tpWait(t, tpStoredSubmission(t, harness, submissionId)); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		if got := len(runs.all()); got != 2 {
			t.Fatalf("%d runs, want 2", got)
		}
		mustClose(t, harness)
	})
}

func TestToolParksNestedCalls(t *testing.T) {
	// harness-tool-parks.test.ts:204 it("parks a nested call, shown sleeping, while its caller awaits it, and returns its result once woken")
	t.Run("parks a nested call, shown sleeping, while its caller awaits it, and returns its result once woken", func(t *testing.T) {
		setup := chatSetup(t)
		// A clock that runs, as progress pacing needs, and jumps two seconds while the call is parked.
		var offset atomic.Int64
		setup.SetNow(func() float64 { return float64(time.Now().UnixMilli() + offset.Load()) })
		runs := &tpRuns{}
		addTool(t, setup.Registry, tpSignalTool(runs))
		var nested atomic.Pointer[durable.NestedToolExecutionResult]
		addTool(t, setup.Registry, tpTool("outer", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			key := "ci"
			result, err := api.ExecuteTool(ctx, "ci", map[string]any{}, durable.NestedCallOptions{Key: &key})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			nested.Store(&result)
			return ownText(fmt.Sprint(result.StructuredOutput)), nil
		}, func(tool *durable.ToolRegistration) { tool.Replay = durable.ReplaySafe }))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall("outer"), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		var inner durable.TaskId
		waitFor(t, func() bool {
			live := tpLive(t, harness, root.Id())
			if len(live.NestedTools) == 0 {
				return false
			}
			inner = live.NestedTools[0].TaskId
			return live.NestedTools[0].Status == ToolSlotSleeping
		})
		offset.Store(2_000)
		tpSignal(t, harness, inner, "done", "nested passed")
		if status := tpWait(t, submission); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		got := nested.Load()
		if got == nil || got.IsError {
			t.Fatalf("nested %+v, want no error", got)
		}
		if got.DurationMs == nil || *got.DurationMs < 2_000 {
			t.Fatalf("nested durationMs %v, want >= 2000", got.DurationMs)
		}
		if text := trText(tlResults(allEntries(t, root))[0]); text != "nested passed" {
			t.Fatalf("text %q", text)
		}
		if got := len(runs.all()); got != 2 {
			t.Fatalf("%d runs, want 2", got)
		}
		mustClose(t, harness)
	})
}

// tpSleepingChild is Child of the Pi file: sleeps on tpDone under "child", then completes.
var tpSleepingChild = durable.DefineTask(durable.TaskDefinition[durable.JsonValue, tpPhase, string, any]{
	Name: "test.child", Version: 1,
	Initial: func(durable.JsonValue) tpPhase { return tpPhase{Phase: "sleep"} },
	Phases: map[string]durable.PhaseHandler[durable.JsonValue, tpPhase, string, any]{
		"sleep": func(ctx context.Context, _ durable.RunningTask[durable.JsonValue, tpPhase, string], runtime durable.TaskRuntime[durable.JsonValue, tpPhase, string, any]) error {
			return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[durable.JsonValue, tpPhase, string]) (*durable.NextTaskState[tpPhase, string], error) {
				return &durable.NextTaskState[tpPhase, string]{Status: durable.TaskSleeping, Checkpoint: &tpPhase{Phase: "done"}, Signals: []durable.SignalKey{tpDone.Key("child")}}, nil
			})
		},
		"done": func(ctx context.Context, _ durable.RunningTask[durable.JsonValue, tpPhase, string], runtime durable.TaskRuntime[durable.JsonValue, tpPhase, string, any]) error {
			return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[durable.JsonValue, tpPhase, string]) (*durable.NextTaskState[tpPhase, string], error) {
				return completed[tpPhase, string]("child done"), nil
			})
		},
	},
	Abort: func(ctx context.Context, _ durable.RunningTask[durable.JsonValue, tpPhase, string], runtime durable.TaskRuntime[durable.JsonValue, tpPhase, string, any]) error {
		return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[durable.JsonValue, tpPhase, string]) (*durable.NextTaskState[tpPhase, string], error) {
			return abortedWith[tpPhase, string]("test"), nil
		})
	},
})

func TestToolParksOnTasks(t *testing.T) {
	// harness-tool-parks.test.ts:248 it("waits for a child it owns, with policy any, and runs again with the ended task in its wake")
	t.Run("waits for a child it owns, with policy any, and runs again with the ended task in its wake", func(t *testing.T) {
		setup := chatSetup(t)
		addTask(t, setup.Registry, tpSleepingChild.Erased())
		var child atomic.Int64
		wakes := &tpRuns{}
		addTool(t, setup.Registry, tpTool("spawn", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			wakes.add(tpSeen(api))
			if api.Wake() != nil {
				return ownText("joined"), nil
			}
			id, err := durable.ToolCreateTask(ctx, api, tpSleepingChild, nil, durable.ToolTaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: api.TaskId()}})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			child.Store(int64(id))
			return durable.ToolExecutionResult{Park: &durable.ToolPark{Wait: &durable.ToolParkWait{On: []durable.TaskId{id}, Policy: durable.JoinAny}}}, nil
		}, func(tool *durable.ToolRegistration) { tool.Replay = durable.ReplaySafe }))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall("spawn"), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		tpParkedTask(t, harness, durable.TaskWaiting)
		if slot := tpSlot(t, harness); slot.Status != ToolSlotWaiting {
			t.Fatalf("slot %q, want waiting", slot.Status)
		}
		tpSignal(t, harness, durable.TaskId(child.Load()), "child", "go")
		if status := tpWait(t, submission); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		tpExpectRuns(t, wakes.all(), tpRun{}, tpRun{wake: &durable.WakeReason{Reason: durable.WakeTasks, Ended: []durable.TaskId{durable.TaskId(child.Load())}}})
		mustClose(t, harness)
	})
}

func TestToolParksInvalid(t *testing.T) {
	// harness-tool-parks.test.ts:293 it("ends the call with an invalid_park result, through afterTool, and fails its task")
	t.Run("ends the call with an invalid_park result, through afterTool, and fails its task", func(t *testing.T) {
		parks := []struct {
			name     string
			returned durable.ToolExecutionResult
		}{
			{"mixed", durable.ToolExecutionResult{Park: &durable.ToolPark{
				Sleep: &durable.ToolParkSleep{Until: new(1.0)},
				Wait:  &durable.ToolParkWait{On: []durable.TaskId{}, Policy: durable.JoinAny},
			}}},
			{"result", durable.ToolExecutionResult{
				Park:   &durable.ToolPark{Sleep: &durable.ToolParkSleep{Until: new(1.0)}},
				Output: []ai.ToolResultMessageContent{},
			}},
			{"empty", durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{}}}},
			{"unowned", durable.ToolExecutionResult{Park: &durable.ToolPark{Wait: &durable.ToolParkWait{On: []durable.TaskId{1}, Policy: durable.JoinAny}}}},
		}
		for _, park := range parks {
			setup := chatSetup(t)
			addTool(t, setup.Registry, tpTool(park.name, func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				return park.returned, nil
			}))
			setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall(park.name), tlDone()})
			harness, root := tpOpen(t, setup)
			if status := tpWait(t, ownSubmit(t, root, ownInput("go"))); status != durable.SubmissionDone {
				t.Fatalf("%s: submission %q, want done", park.name, status)
			}
			result := tlResults(allEntries(t, root))[0]
			if !result.IsError {
				t.Fatalf("%s: result %+v, want an error", park.name, result)
			}
			if got := trText(result); !strings.Contains(got, fmt.Sprintf("Tool %s returned an invalid park", park.name)) {
				t.Fatalf("%s: text %q", park.name, got)
			}
			if slot := tpSlot(t, harness); slot != nil {
				t.Fatalf("%s: slot %+v, want none", park.name, slot)
			}
			mustClose(t, harness)
		}
	})
}

func TestToolParksTheCommitRefuses(t *testing.T) {
	// harness-tool-parks.test.ts:321 it("ends a call that sleeps again on the key that woke it with invalid_park")
	t.Run("ends a call that sleeps again on the key that woke it with invalid_park", func(t *testing.T) {
		setup := chatSetup(t)
		var runs atomic.Int64
		addTool(t, setup.Registry, tpTool("again", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			runs.Add(1)
			return tpSleep(tpDone.Key("k")), nil
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall("again"), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		id := tpParkedTask(t, harness)
		tpSignal(t, harness, id, "k", "x")
		if status := tpWait(t, submission); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		if runs.Load() != 2 {
			t.Fatalf("%d runs, want 2", runs.Load())
		}
		if got := trText(tlResults(allEntries(t, root))[0]); !strings.Contains(got, "it already woke a wait") {
			t.Fatalf("text %q", got)
		}
		record := tkTaskRecord(t, harness, id)
		if record.State.Outcome == nil || record.State.Outcome.Status != durable.OutcomeFailed {
			t.Fatalf("outcome %+v, want failed", record.State.Outcome)
		}
		mustClose(t, harness)
	})
}

func TestToolParksCancel(t *testing.T) {
	// harness-tool-parks.test.ts:345 it("runs when a parked call is aborted, with the park's checkpoint and the environment, and the call ends aborted")
	t.Run("runs when a parked call is aborted, with the park's checkpoint and the environment, and the call ends aborted", func(t *testing.T) {
		setup := chatSetup(t)
		runs, cancels := &tpRuns{}, &tpRuns{}
		var envs syncList[string]
		addTool(t, setup.Registry, tpSignalTool(runs, func(tool *durable.ToolRegistration) {
			tool.Cancel = func(_ context.Context, _ any, api durable.ToolExecutionApi) error {
				cancels.add(tpSeen(api))
				envs.add(api.Env().Cwd())
				return nil
			}
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall("ci"), tlDone()})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup, openChatOptions{env: envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: "/work"})})
		harness.Resume()
		submission := ownSubmit(t, root, ownInput("go"))
		tpParkedTask(t, harness)
		if err := root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		if status := tpWait(t, submission); status != durable.SubmissionUnanswered {
			t.Fatalf("submission %q, want unanswered", status)
		}
		tpExpectRuns(t, cancels.all(), tpRun{checkpoint: map[string]any{"build": float64(1)}})
		tpExpect(t, "envs", envs.all(), []string{"/work"})
		if got := trText(tlResults(allEntries(t, root))[0]); !strings.Contains(got, "Tool ci was aborted") {
			t.Fatalf("text %q", got)
		}
		mustClose(t, harness)
	})

	// harness-tool-parks.test.ts:372 it("runs also under restartLimit 1, and a throw from it is reported while the call still ends aborted")
	t.Run("runs also under restartLimit 1, and a throw from it is reported while the call still ends aborted", func(t *testing.T) {
		setup := chatSetup(t)
		arSetRestartLimit(setup, new(1.0))
		var cancels atomic.Int64
		addTool(t, setup.Registry, tpSignalTool(&tpRuns{}, func(tool *durable.ToolRegistration) {
			tool.Cancel = func(context.Context, any, durable.ToolExecutionApi) error {
				cancels.Add(1)
				return errors.New("cancel failed")
			}
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall("ci"), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		id := tpParkedTask(t, harness)
		if err := root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		tpWait(t, submission)
		if cancels.Load() != 1 {
			t.Fatalf("%d cancels, want 1", cancels.Load())
		}
		record := tkTaskRecord(t, harness, id)
		if record.State.Outcome == nil || record.State.Outcome.Status != durable.OutcomeAborted {
			t.Fatalf("outcome %+v, want aborted", record.State.Outcome)
		}
		tpExpectReported(t, setup, "cancel failed")
		mustClose(t, harness)
	})

	// harness-tool-parks.test.ts:397 it("runs after an abort that cut off a woken run, which cleans up through its context too")
	t.Run("runs after an abort that cut off a woken run, which cleans up through its context too", func(t *testing.T) {
		setup := chatSetup(t)
		cancels := &tpRuns{}
		running := deferred()
		var cleaned syncList[string]
		addTool(t, setup.Registry, tpTool("ci", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			if api.Wake() == nil {
				return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{
					Checkpoint: "first", HasCheckpoint: true, Signals: []durable.SignalKey{tpDone.Key("done")},
				}}}, nil
			}
			running.resolve()
			err := abortedBy(ctx)
			cleaned.add("execute")
			return durable.ToolExecutionResult{}, err
		}, func(tool *durable.ToolRegistration) {
			tool.Cancel = func(_ context.Context, _ any, api durable.ToolExecutionApi) error {
				cancels.add(tpSeen(api))
				return nil
			}
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall("ci"), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		id := tpParkedTask(t, harness)
		tpSignal(t, harness, id, "done", "x")
		if err := running.wait(testContext); err != nil {
			t.Fatal(err)
		}
		if err := root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		tpWait(t, submission)
		tpExpect(t, "cleaned", cleaned.all(), []string{"execute"})
		tpExpectRuns(t, cancels.all(), tpRun{checkpoint: "first"})
		mustClose(t, harness)
	})

	// harness-tool-parks.test.ts:431 it("does not run for a parked call whose tool is gone; the abort is reported")
	t.Run("does not run for a parked call whose tool is gone; the abort is reported", func(t *testing.T) {
		setup := chatSetup(t)
		cancels := &tpRuns{}
		installed := addTool(t, setup.Registry, tpSignalTool(&tpRuns{}, func(tool *durable.ToolRegistration) {
			tool.Cancel = func(_ context.Context, _ any, api durable.ToolExecutionApi) error {
				cancels.add(tpSeen(api))
				return nil
			}
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall("ci"), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		tpParkedTask(t, harness)
		installed.dispose()
		if err := root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		if status := tpWait(t, submission); status != durable.SubmissionUnanswered {
			t.Fatalf("submission %q, want unanswered", status)
		}
		if got := cancels.all(); len(got) != 0 {
			t.Fatalf("cancels %v, want none", got)
		}
		tpExpectReported(t, setup, "Tool ci is not available: its parked call c1 was not cancelled")
		mustClose(t, harness)
	})

	// harness-tool-parks.test.ts:452 it("runs in the run itself when the park's commit finds the call abort-marked")
	t.Run("runs in the run itself when the park's commit finds the call abort-marked", func(t *testing.T) {
		setup := chatSetup(t)
		release, entered := deferred(), deferred()
		cancels := &tpRuns{}
		var cancelContextEnded atomic.Bool
		addTool(t, setup.Registry, tpTool("race", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			entered.resolve()
			// The abort arrives while the tool still runs; it ignores the signal and parks anyway.
			if err := release.wait(testContext); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{
				Checkpoint: "started", HasCheckpoint: true, Signals: []durable.SignalKey{tpDone.Key("k")},
			}}}, nil
		}, func(tool *durable.ToolRegistration) {
			tool.Cancel = func(ctx context.Context, _ any, api durable.ToolExecutionApi) error {
				cancels.add(tpSeen(api))
				cancelContextEnded.Store(ctx.Err() != nil)
				return nil
			}
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall("race"), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		if err := entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		id := *tpSlot(t, harness).TaskId
		aborting := make(chan error, 1)
		go func() {
			_, err := harness.AbortTask(testContext, id)
			aborting <- err
		}()
		waitFor(t, func() bool {
			record, err := harness.GetTask(testContext, id)
			return err == nil && record != nil && record.AbortRequested
		})
		release.resolve()
		if err := <-aborting; err != nil {
			t.Fatal(err)
		}
		tpWaitTask(t, harness, id)
		// The run's cancel with the park's checkpoint; the abort handler finds execute without a wake and runs none.
		tpExpectRuns(t, cancels.all(), tpRun{checkpoint: "started"})
		if cancelContextEnded.Load() {
			t.Fatal("the cancel's context was already cancelled")
		}
		if err := root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		tpWait(t, submission)
		mustClose(t, harness)
	})
}

func TestToolParksWaking(t *testing.T) {
	// harness-tool-parks.test.ts:494 it("ends a woken call whose tool is gone with tool_unavailable, failed, and reports it")
	t.Run("ends a woken call whose tool is gone with tool_unavailable, failed, and reports it", func(t *testing.T) {
		setup := chatSetup(t)
		runs := &tpRuns{}
		installed := addTool(t, setup.Registry, tpSignalTool(runs))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall("ci"), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		id := tpParkedTask(t, harness)
		installed.dispose()
		tpSignal(t, harness, id, "done", "late")
		if status := tpWait(t, submission); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		settled := tpWaitTask(t, harness, id)
		if settled.State.Outcome.Status != durable.OutcomeFailed {
			t.Fatalf("outcome %q, want failed", settled.State.Outcome.Status)
		}
		result := tlResults(allEntries(t, root))[0]
		// Built from the slot: the details and diagnostics the parked run left stay.
		if !result.IsError {
			t.Fatalf("result %+v, want an error", result)
		}
		tpExpect(t, "details", result.Details, map[string]any{"build": float64(1)})
		text := trText(result)
		if !strings.Contains(text, "[info] run 1") || !strings.Contains(text, "Tool ci is not available") {
			t.Fatalf("text %q", text)
		}
		tpExpectReported(t, setup, "Tool ci is not available: its parked call c1 cannot run again")
		if got := len(runs.all()); got != 1 {
			t.Fatalf("%d runs, want 1", got)
		}
		mustClose(t, harness)
	})

	// harness-tool-parks.test.ts:519 it("settles a woken unsafe run cut off by a crash interrupted, after cancel")
	t.Run("settles a woken unsafe run cut off by a crash interrupted, after cancel", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		cancels := &tpRuns{}
		started := deferred()
		var runs atomic.Int64
		addTool(t, setup.Registry, tpTool("ci", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			runs.Add(1)
			if api.Wake() == nil {
				return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{
					Checkpoint: "first", HasCheckpoint: true, Signals: []durable.SignalKey{tpDone.Key("done")},
				}}}, nil
			}
			api.Output("rerun\n")
			if err := api.Details(ctx, map[string]any{"woken": true}); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			started.resolve()
			return durable.ToolExecutionResult{}, abortedBy(ctx)
		}, func(tool *durable.ToolRegistration) {
			tool.Cancel = func(_ context.Context, _ any, api durable.ToolExecutionApi) error {
				cancels.add(tpSeen(api))
				return nil
			}
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall("ci"), tlDone()})
		crashed := thOpen(t, path, setup)
		submissionId := ownSubmit(t, crashed.root, ownInput("go")).Id()
		id := tpParkedTask(t, crashed.harness)
		tpSignal(t, crashed.harness, id, "done", "x")
		if err := started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		thStored()
		// The process dies here: another one opens the same database.
		crashed.kill()
		next := thOpen(t, path, setup)
		if status := tpWait(t, tpStoredSubmission(t, next.harness, submissionId)); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		if runs.Load() != 2 {
			t.Fatalf("%d runs, want 2", runs.Load())
		}
		tpExpectRuns(t, cancels.all(), tpRun{checkpoint: "first"})
		if got := trText(tlResults(allEntries(t, next.root))[0]); !strings.Contains(got, "Tool ci was interrupted and may have partially run") {
			t.Fatalf("text %q", got)
		}
		mustClose(t, next.harness)
	})

	// harness-tool-parks.test.ts:558 it("reruns a woken safe run cut off by a crash, keeping the slot's details and diagnostics, not its output")
	t.Run("reruns a woken safe run cut off by a crash, keeping the slot's details and diagnostics, not its output", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		started := deferred()
		seen := &tpRuns{}
		addTool(t, setup.Registry, tpTool("ci", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			count := seen.add(tpSeen(api))
			if api.Wake() == nil {
				api.Diagnostic(durable.ToolDiagnostic{Severity: durable.SeverityInfo, Message: "parked"})
				if err := api.Details(ctx, map[string]any{"build": float64(1)}); err != nil {
					return durable.ToolExecutionResult{}, err
				}
				return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{
					Checkpoint: "first", HasCheckpoint: true, Signals: []durable.SignalKey{tpDone.Key("done")},
				}}}, nil
			}
			if count == 2 {
				api.Output("cut off\n")
				started.resolve()
				return durable.ToolExecutionResult{}, abortedBy(ctx)
			}
			return durable.ToolExecutionResult{}, nil
		}, func(tool *durable.ToolRegistration) { tool.Replay = durable.ReplaySafe }))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall("ci"), tlDone()})
		crashed := thOpen(t, path, setup)
		submissionId := ownSubmit(t, crashed.root, ownInput("go")).Id()
		id := tpParkedTask(t, crashed.harness)
		tpSignal(t, crashed.harness, id, "done", "x")
		if err := started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		thStored()
		crashed.kill()
		next := thOpen(t, path, setup)
		if status := tpWait(t, tpStoredSubmission(t, next.harness, submissionId)); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		wake := &durable.WakeReason{Reason: durable.WakeSignal, Signal: "test.done", Key: "done"}
		tpExpectRuns(t, seen.all(), tpRun{}, tpRun{wake: wake, checkpoint: "first"}, tpRun{wake: wake, checkpoint: "first"})
		// The park stored the details and diagnostics; the rerun starts from them, without the cut-off run's output.
		result := tlResults(allEntries(t, next.root))[0]
		if result.IsError {
			t.Fatalf("result %+v, want no error", result)
		}
		tpExpect(t, "details", result.Details, map[string]any{"build": float64(1)})
		if got := trText(result); got != "<harness>\n[info] parked\n</harness>" {
			t.Fatalf("text %q", got)
		}
		mustClose(t, next.harness)
	})
}

func TestToolParksWrappers(t *testing.T) {
	// harness-tool-parks.test.ts:608 it("pass a base's park through, and a park of their own is invalid")
	t.Run("pass a base's park through, and a park of their own is invalid", func(t *testing.T) {
		for _, own := range []bool{false, true} {
			setup := chatSetup(t)
			runs := &tpRuns{}
			base := tpSignalTool(runs)
			addTool(t, setup.Registry, base)
			mustInstall(t, setup.Registry, new(durable.Extension{Name: "wrapper", Wraps: []durable.Wrap{
				WrapTool(base, func(inner *durable.ToolRegistration) *durable.ToolRegistration {
					wrapped := *inner
					wrapped.Execute = func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
						returned, err := inner.Execute(ctx, args, api)
						if own && err == nil && returned.Park != nil && returned.Park.Sleep != nil {
							sleep := *returned.Park.Sleep
							return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &sleep}}, nil
						}
						return returned, err
					}
					return &wrapped
				}),
			}}))
			setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall("ci"), tlDone()})
			harness, root := tpOpen(t, setup)
			submission := ownSubmit(t, root, ownInput("go"))
			if own {
				if status := tpWait(t, submission); status != durable.SubmissionDone {
					t.Fatalf("submission %q, want done", status)
				}
				if got := trText(tlResults(allEntries(t, root))[0]); !strings.Contains(got, "a wrapper returned a park its base did not return") {
					t.Fatalf("text %q", got)
				}
			} else {
				id := tpParkedTask(t, harness)
				tpSignal(t, harness, id, "done", "wrapped")
				if status := tpWait(t, submission); status != durable.SubmissionDone {
					t.Fatalf("submission %q, want done", status)
				}
				if got := len(runs.all()); got != 2 {
					t.Fatalf("%d runs, want 2", got)
				}
			}
			mustClose(t, harness)
		}
	})

	// harness-tool-parks.test.ts:645 it("accept one park object its base returns to calls running at once")
	t.Run("accept one park object its base returns to calls running at once", func(t *testing.T) {
		setup := chatSetup(t)
		shared := &durable.ToolPark{Sleep: &durable.ToolParkSleep{Signals: []durable.SignalKey{tpDone.Key("done")}}}
		base := tpTool("ci", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			state, err := durable.ReadSignal(ctx, api, tpDone, "done")
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if state == nil {
				return durable.ToolExecutionResult{Park: shared}, nil
			}
			return ownText("ok"), nil
		})
		addTool(t, setup.Registry, base)
		mustInstall(t, setup.Registry, new(durable.Extension{Name: "wrapper", Wraps: []durable.Wrap{
			WrapTool(base, func(inner *durable.ToolRegistration) *durable.ToolRegistration { return new(*inner) }),
		}}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"ci", map[string]any{}, "a"}, tlCall{"ci", map[string]any{}, "b"}), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		var ids []durable.TaskId
		waitFor(t, func() bool {
			ids = nil
			for _, slot := range tpLive(t, harness, root.Id()).Tools {
				if slot.Status == ToolSlotSleeping && slot.TaskId != nil {
					ids = append(ids, *slot.TaskId)
				}
			}
			return len(ids) == 2
		})
		for _, id := range ids {
			tpSignal(t, harness, id, "done", "x")
		}
		if status := tpWait(t, submission); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		for _, result := range tlResults(allEntries(t, root)) {
			if result.IsError {
				t.Fatalf("result %+v, want no error", result)
			}
		}
		mustClose(t, harness)
	})

	// harness-tool-parks.test.ts:673 it("refuse a park the wrapper kept from an earlier run instead of asking its base")
	t.Run("refuse a park the wrapper kept from an earlier run instead of asking its base", func(t *testing.T) {
		setup := chatSetup(t)
		base := tpSignalTool(&tpRuns{})
		addTool(t, setup.Registry, base)
		var mu sync.Mutex
		var kept *durable.ToolPark
		mustInstall(t, setup.Registry, new(durable.Extension{Name: "wrapper", Wraps: []durable.Wrap{
			WrapTool(base, func(inner *durable.ToolRegistration) *durable.ToolRegistration {
				wrapped := *inner
				wrapped.Execute = func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					mu.Lock()
					earlier := kept
					mu.Unlock()
					if earlier != nil {
						return durable.ToolExecutionResult{Park: earlier}, nil
					}
					returned, err := inner.Execute(ctx, args, api)
					if err == nil && returned.Park != nil && returned.Park.Sleep != nil {
						mu.Lock()
						kept = returned.Park
						mu.Unlock()
					}
					return returned, err
				}
				return &wrapped
			}),
		}}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tpCall("ci"), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		id := tpParkedTask(t, harness)
		tpSignal(t, harness, id, "done", "x")
		if status := tpWait(t, submission); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		if got := trText(tlResults(allEntries(t, root))[0]); !strings.Contains(got, "a wrapper returned a park its base did not return") {
			t.Fatalf("text %q", got)
		}
		mustClose(t, harness)
	})
}
