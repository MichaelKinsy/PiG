// Ports packages/durable/test/harness-tool-gates.test.ts (Pi 473515f9c): gates before a tool runs (spec suspendable §5):
// beforeTool sleeps, the call phase asked again on every wake, registry changes and opens that wake parked calls, and the
// deadline path of such a wake. See harness_tasks_test.go for the Go mappings that apply to every case.

package harness

// pi: packages/durable/src/harness/tool.ts

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/schema"
)

type tgApproval struct {
	Allow bool `json:"allow"`
}

// tgApprovalSignal is Approval of the Pi file.
var tgApprovalSignal = durable.DefineSignal(durable.SignalDefinition[tgApproval]{
	Name: "test.approval", Version: 1,
	Schema: schema.Obj(schema.Properties{schema.Prop("allow", schema.Bool())}),
})

const tgDay = 24 * 60 * 60 * 1000

func tgCall(command ...string) ai.FauxResponseStep {
	text := "rm -rf build"
	if len(command) > 0 {
		text = command[0]
	}
	return tlCallsStep(tlCall{"bash", map[string]any{"command": text}, "c1"})
}

// tgBashTool is bashTool of the Pi file: a bash stand-in that counts its runs.
func tgBashTool(runs *atomic.Int64) *durable.ToolRegistration {
	parameters := map[string]any{
		"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}, "required": []any{"command"},
	}
	return ownTool("bash", "bash", parameters, func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		runs.Add(1)
		return ownText(fmt.Sprintf("ran %v", args.(map[string]any)["command"])), nil
	})
}

// tgApprovals is approvals of the Pi file, the approval gate of spec §5.1: it sleeps on an approval keyed by the command,
// with a deadline fixed at its first pass.
func tgApprovals(passes *atomic.Int64) durable.HookRegistration {
	return Hook(ToolTask, &ToolHooks{BeforeTool: func(ctx context.Context, gated ToolHookCall, api HookApi) (*BeforeToolResult, error) {
		passes.Add(1)
		key := strings.ReplaceAll(fmt.Sprint(gated.Arguments["command"]), " ", "_")
		state, err := durable.ReadSignal(ctx, api, tgApprovalSignal, key)
		if err != nil {
			return nil, err
		}
		fixed, err := api.MemoCandidate(ctx, "approvals.until."+key, api.Now()+tgDay)
		if err != nil {
			return nil, err
		}
		until := fixed.(float64)
		switch {
		case state == nil:
			return &BeforeToolResult{Sleep: &GateSleep{Signals: []durable.SignalKey{tgApprovalSignal.Key(key)}, Until: &until}}, nil
		case state.Expired:
			return &BeforeToolResult{Block: new("No approval within a day")}, nil
		case state.Value.Allow:
			return nil, nil
		}
		return &BeforeToolResult{Block: new("Not approved")}, nil
	}})
}

func tgInstall(t *testing.T, setup *chatState, name string, hooks ...durable.HookRegistration) *durable.Extension {
	t.Helper()
	extension := new(durable.Extension{Name: name, Hooks: hooks})
	mustInstall(t, setup.Registry, extension)
	return extension
}

func tgSignal(t *testing.T, harness Harness, id durable.TaskId, key string, allow bool) durable.SignalResult {
	t.Helper()
	result, err := Signal(testContext, harness, id, tgApprovalSignal, key, tgApproval{Allow: allow})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// tgGated is the task of the root's first tool slot, once it sleeps at its gates.
func tgGated(t *testing.T, harness Harness) durable.TaskId {
	t.Helper()
	var id durable.TaskId
	waitFor(t, func() bool {
		slot := tpSlot(t, harness)
		if slot == nil || slot.TaskId == nil {
			return false
		}
		id = *slot.TaskId
		return slot.Status == ToolSlotSleeping
	})
	return id
}

func tgExpectCounts(t *testing.T, what string, got []int64, want ...int64) {
	t.Helper()
	tpExpect(t, what, got, want)
}

func TestToolGatesBeforeToolSleep(t *testing.T) {
	// harness-tool-gates.test.ts:112 it("parks the call before it runs, and runs it once approved")
	t.Run("parks the call before it runs, and runs it once approved", func(t *testing.T) {
		setup := chatSetup(t)
		var runs, passes atomic.Int64
		addTool(t, setup.Registry, tgBashTool(&runs))
		tgInstall(t, setup, "approvals", tgApprovals(&passes))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tgCall(), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		id := tgGated(t, harness)
		record := tkTaskRecord(t, harness, id)
		if record.State.Status != durable.TaskSleeping {
			t.Fatalf("state %+v, want sleeping", record.State)
		}
		tpExpect(t, "checkpoint", record.State.Checkpoint, map[string]any{"phase": "call"})
		tpExpect(t, "signals", record.State.Signals, []any{map[string]any{"signal": "test.approval", "key": "rm_-rf_build"}})
		tpIdle(t, harness)
		if runs.Load() != 0 {
			t.Fatalf("%d runs, want 0", runs.Load())
		}
		tgSignal(t, harness, id, "rm_-rf_build", true)
		if status := tpWait(t, submission); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		tgExpectCounts(t, "runs and passes", []int64{runs.Load(), passes.Load()}, 1, 2)
		if got := trText(tlResults(allEntries(t, root))[0]); got != "ran rm -rf build" {
			t.Fatalf("text %q", got)
		}
		mustClose(t, harness)
	})

	// harness-tool-gates.test.ts:137 it("blocks on a denial, and on the deadline, after which a late answer is closed")
	t.Run("blocks on a denial, and on the deadline, after which a late answer is closed", func(t *testing.T) {
		for _, outcome := range []string{"denied", "expired"} {
			setup := chatSetup(t)
			var offset atomic.Int64
			setup.SetNow(func() float64 { return float64(time.Now().UnixMilli() + offset.Load()) })
			var runs, passes atomic.Int64
			addTool(t, setup.Registry, tgBashTool(&runs))
			tgInstall(t, setup, "approvals", tgApprovals(&passes))
			setup.Faux.SetResponses([]ai.FauxResponseStep{tgCall(), tlDone()})
			harness, root := tpOpen(t, setup)
			submission := ownSubmit(t, root, ownInput("go"))
			id := tgGated(t, harness)
			if outcome == "denied" {
				tgSignal(t, harness, id, "rm_-rf_build", false)
			} else {
				offset.Store(tgDay)
				harness.Resume()
			}
			if status := tpWait(t, submission); status != durable.SubmissionDone {
				t.Fatalf("%s: submission %q, want done", outcome, status)
			}
			if runs.Load() != 0 {
				t.Fatalf("%s: %d runs, want 0", outcome, runs.Load())
			}
			want := "Not approved"
			if outcome == "expired" {
				want = "No approval within a day"
			}
			if got := trText(tlResults(allEntries(t, root))[0]); !strings.Contains(got, want) {
				t.Fatalf("%s: text %q, want %q", outcome, got, want)
			}
			if outcome == "expired" {
				if result := tgSignal(t, harness, id, "rm_-rf_build", true); result != durable.SignalClosed {
					t.Fatalf("late signal %q, want closed", result)
				}
			}
			mustClose(t, harness)
		}
	})

	// harness-tool-gates.test.ts:166 it("asks every gate again on each wake, in extension order, and stops at the first that sleeps")
	t.Run("asks every gate again on each wake, in extension order, and stops at the first that sleeps", func(t *testing.T) {
		setup := chatSetup(t)
		var runs, passes, quiet atomic.Int64
		addTool(t, setup.Registry, tgBashTool(&runs))
		tgInstall(t, setup, "approvals", tgApprovals(&passes))
		tgInstall(t, setup, "quiet-hours", Hook(ToolTask, &ToolHooks{BeforeTool: func(context.Context, ToolHookCall, HookApi) (*BeforeToolResult, error) {
			quiet.Add(1)
			return nil, nil
		}}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tgCall(), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		id := tgGated(t, harness)
		tgExpectCounts(t, "passes and quiet", []int64{passes.Load(), quiet.Load()}, 1, 0)
		tgSignal(t, harness, id, "rm_-rf_build", true)
		tpWait(t, submission)
		tgExpectCounts(t, "passes, quiet and runs", []int64{passes.Load(), quiet.Load(), runs.Load()}, 2, 1, 1)
		mustClose(t, harness)
	})

	// harness-tool-gates.test.ts:197 it("ends the call invalid_park when the sleep waits for nothing")
	t.Run("ends the call invalid_park when the sleep waits for nothing", func(t *testing.T) {
		setup := chatSetup(t)
		var runs atomic.Int64
		addTool(t, setup.Registry, tgBashTool(&runs))
		tgInstall(t, setup, "bad", Hook(ToolTask, &ToolHooks{BeforeTool: func(context.Context, ToolHookCall, HookApi) (*BeforeToolResult, error) {
			return &BeforeToolResult{Sleep: &GateSleep{}}, nil
		}}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tgCall(), tlDone()})
		harness, root := tpOpen(t, setup)
		if status := tpWait(t, ownSubmit(t, root, ownInput("go"))); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		if runs.Load() != 0 {
			t.Fatalf("%d runs, want 0", runs.Load())
		}
		if got := trText(tlResults(allEntries(t, root))[0]); !strings.Contains(got, "cannot wait at its gates") {
			t.Fatalf("text %q", got)
		}
		mustClose(t, harness)
	})
}

func TestToolGatesRegistryChangesAndOpens(t *testing.T) {
	// harness-tool-gates.test.ts:214 it("wake a call parked at its gates, so the gates installed then decide")
	t.Run("wake a call parked at its gates, so the gates installed then decide", func(t *testing.T) {
		setup := chatSetup(t)
		var runs, passes atomic.Int64
		addTool(t, setup.Registry, tgBashTool(&runs))
		gate := tgInstall(t, setup, "approvals", tgApprovals(&passes))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tgCall(), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		tgGated(t, harness)
		// Another extension installed: asked again, and still asleep.
		tgInstall(t, setup, "other")
		waitFor(t, func() bool { return passes.Load() == 2 })
		// The gate uninstalled: the next pass runs the call.
		setup.Registry.Uninstall(gate)
		if status := tpWait(t, submission); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		tgExpectCounts(t, "passes and runs", []int64{passes.Load(), runs.Load()}, 2, 1)
		mustClose(t, harness)
	})

	// harness-tool-gates.test.ts:235 it("wake a sleep committed under a snapshot a publication replaced while the gate ran")
	t.Run("wake a sleep committed under a snapshot a publication replaced while the gate ran", func(t *testing.T) {
		setup := chatSetup(t)
		var passes, runs atomic.Int64
		entered, release := deferred(), deferred()
		addTool(t, setup.Registry, tgBashTool(&runs))
		tgInstall(t, setup, "slow", Hook(ToolTask, &ToolHooks{BeforeTool: func(context.Context, ToolHookCall, HookApi) (*BeforeToolResult, error) {
			count := passes.Add(1)
			if count == 1 {
				entered.resolve()
				if err := release.wait(testContext); err != nil {
					return nil, err
				}
			}
			if count < 3 {
				return &BeforeToolResult{Sleep: &GateSleep{Signals: []durable.SignalKey{tgApprovalSignal.Key(fmt.Sprintf("k%d", count))}}}, nil
			}
			return nil, nil
		}}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tgCall(), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		if err := entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		tgInstall(t, setup, "other")
		release.resolve()
		// The first sleep is woken at once; the second one waits for its key.
		id := tgGated(t, harness)
		waitFor(t, func() bool { return passes.Load() == 2 })
		tgSignal(t, harness, id, "k2", true)
		if status := tpWait(t, submission); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		if passes.Load() != 3 {
			t.Fatalf("%d passes, want 3", passes.Load())
		}
		mustClose(t, harness)
	})

	// harness-tool-gates.test.ts:273 it("wake a call parked at its gates at open, before its deadline, so a gate removed meanwhile lets it run")
	t.Run("wake a call parked at its gates at open, before its deadline, so a gate removed meanwhile lets it run", func(t *testing.T) {
		path := sqlitePath(t)
		var passes, runs atomic.Int64
		build := func(withGate bool) *chatState {
			setup := chatSetup(t)
			addTool(t, setup.Registry, tgBashTool(&runs))
			if withGate {
				tgInstall(t, setup, "approvals", tgApprovals(&passes))
			}
			setup.Faux.SetResponses([]ai.FauxResponseStep{tgCall(), tlDone()})
			return setup
		}
		harness, root := trOpen(t, path, build(true))
		submissionId := ownSubmit(t, root, ownInput("go")).Id()
		tgGated(t, harness)
		mustClose(t, harness)
		// The next process installs no approvals: the call runs at open, no signal, no deadline.
		setup := build(false)
		setup.Faux.SetResponses([]ai.FauxResponseStep{tlDone()})
		harness, _ = trOpen(t, path, setup)
		if status := tpWait(t, tpStoredSubmission(t, harness, submissionId)); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		tgExpectCounts(t, "passes and runs", []int64{passes.Load(), runs.Load()}, 1, 1)
		mustClose(t, harness)
	})

	// harness-tool-gates.test.ts:297 it("leave a call parked in execute() alone")
	t.Run("leave a call parked in execute() alone", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		var runs atomic.Int64
		parameters := map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}, "required": []any{"command"}}
		addTool(t, setup.Registry, ownTool("bash", "bash", parameters, func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			runs.Add(1)
			if api.Wake() == nil {
				return tpSleep(tgApprovalSignal.Key("done")), nil
			}
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tgCall(), tlDone()})
		harness, root := trOpen(t, path, setup)
		ownSubmit(t, root, ownInput("go"))
		id := tgGated(t, harness)
		tgInstall(t, setup, "other")
		mustClose(t, harness)
		harness, _ = trOpen(t, path, setup)
		tpIdle(t, harness)
		record := tkTaskRecord(t, harness, id)
		if record.State.Status != durable.TaskSleeping {
			t.Fatalf("state %+v, want sleeping", record.State)
		}
		if phase := plainObject(*record.State.Checkpoint)["phase"]; phase != "parked" {
			t.Fatalf("phase %v, want parked", phase)
		}
		if runs.Load() != 1 {
			t.Fatalf("%d runs, want 1", runs.Load())
		}
		mustClose(t, harness)
	})

	// harness-tool-gates.test.ts:329 it("take the deadline path for a gate whose deadline passed, so its keys expire")
	t.Run("take the deadline path for a gate whose deadline passed, so its keys expire", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		var offset atomic.Int64
		setup.SetNow(func() float64 { return float64(time.Now().UnixMilli() + offset.Load()) })
		var passes, runs atomic.Int64
		addTool(t, setup.Registry, tgBashTool(&runs))
		tgInstall(t, setup, "approvals", tgApprovals(&passes))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tgCall(), tlDone()})
		harness, root := trOpen(t, path, setup)
		submissionId := ownSubmit(t, root, ownInput("go")).Id()
		id := tgGated(t, harness)
		mustClose(t, harness)
		// Reopened after the deadline: the open's wake expires the key, and the gate blocks.
		offset.Store(2 * tgDay)
		harness, root = trOpen(t, path, setup)
		if status := tpWait(t, tpStoredSubmission(t, harness, submissionId)); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		if passes.Load() != 2 || runs.Load() != 0 {
			t.Fatalf("passes %d runs %d, want 2 and 0", passes.Load(), runs.Load())
		}
		if got := trText(tlResults(allEntries(t, root))[0]); !strings.Contains(got, "No approval within a day") {
			t.Fatalf("text %q", got)
		}
		if result := tgSignal(t, harness, id, "rm_-rf_build", true); result != durable.SignalClosed {
			t.Fatalf("late signal %q, want closed", result)
		}
		mustClose(t, harness)
	})
}
