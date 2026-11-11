// Ports the case of packages/durable/test/harness-tools-recovery.test.ts that Pi 6d4b7443 (group commit e5c6182b) added to
// "tool recovery": a safe tool whose running slot a crash lost reruns and shows the slot running again. See
// harness_tasks_test.go for the Go mappings that apply to every case.

package harness

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// harness-tools-recovery.test.ts "tool recovery".
func TestToolRecoveryGroupCommit(t *testing.T) {
	// harness-tools-recovery.test.ts it("reruns a safe tool whose running slot a crash after its intent lost, or a sibling stored, and shows it running")
	t.Run("reruns a safe tool whose running slot a crash after its intent lost, or a sibling stored, and shows it running", func(t *testing.T) {
		for _, sibling := range []bool{false, true} {
			name := "no sibling"
			if sibling {
				name = "a sibling stored the slot"
			}
			t.Run(name, func(t *testing.T) {
				path := sqlitePath(t)
				setup := chatSetup(t)
				// The running slot is presentation, held in memory; no deadline stores it before the crash.
				setup.SetSettings(func(settings *HarnessSettings) {
					settings.Progress = &ProgressPolicyPatch{PersistWithinMs: new(infinity())}
				})
				started := deferred()
				var runs atomic.Int64
				var reopened atomic.Pointer[Harness]
				seen := &syncList[string]{}
				addTool(t, setup.Registry, trTool("work", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					if runs.Add(1) == 1 {
						started.resolve()
						return durable.ToolExecutionResult{}, abortedBy(ctx)
					}
					status := ""
					live, err := durable.Snapshot(ctx, *reopened.Load(), LiveDoc, 1)
					if err != nil {
						return durable.ToolExecutionResult{}, err
					}
					if live != nil {
						for _, slot := range live.Tools {
							if slot.TaskId != nil && *slot.TaskId == api.TaskId() {
								status = string(slot.Status)
								break
							}
						}
					}
					seen.add(status)
					return durable.ToolExecutionResult{}, nil
				}, func(tool *durable.ToolRegistration) { tool.Replay = durable.ReplaySafe }))
				// A sibling that ends once `work` started: its result commit reads `pi.live`, which stores what is held there.
				addTool(t, setup.Registry, trTool("quick", func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					if err := started.wait(ctx); err != nil {
						return durable.ToolExecutionResult{}, err
					}
					return durable.ToolExecutionResult{}, nil
				}))
				calls := []tlCall{{"work", map[string]any{}, "w"}}
				if sibling {
					calls = append(calls, tlCall{"quick", map[string]any{}, "q"})
				}
				setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(calls...), tlDone()})
				crashed, crashedRoot := trOpen(t, path, setup)
				id := ownSubmit(t, crashedRoot, ownInput("go")).Id()
				if err := started.wait(testContext); err != nil {
					t.Fatal(err)
				}
				slot := func(harness Harness, name string) *ToolSlot {
					live := liveOf(t, harness, crashedRoot.Id())
					if live == nil {
						return nil
					}
					for index := range live.Tools {
						if live.Tools[index].Name == name {
							return &live.Tools[index]
						}
					}
					return nil
				}
				var workId durable.TaskId
				waitFor(t, func() bool {
					work := slot(crashed, "work")
					if work == nil || work.TaskId == nil {
						return false
					}
					workId = *work.TaskId
					return true
				})
				if sibling {
					waitFor(t, func() bool {
						quick := slot(crashed, "quick")
						return quick != nil && quick.Status == ToolSlotDone
					})
				}
				// The process dies here: another one opens the same database, without the held presentation.
				next, nextRoot := openChat(t, tkOpenSqlite(t, path), setup)
				reopened.Store(&next)
				record := tkTaskRecord(t, next, workId)
				if record.State.Checkpoint == nil || plainObject(*record.State.Checkpoint)["phase"] != "execute" {
					t.Fatalf("checkpoint %v, want phase execute", record.State.Checkpoint)
				}
				wantStatus := ToolSlotPending
				if sibling {
					wantStatus = ToolSlotRunning
				}
				if work := slot(next, "work"); work == nil || work.Status != wantStatus {
					t.Fatalf("work slot after the crash %+v, want %s", work, wantStatus)
				}
				next.Resume()
				submission, err := next.Submission(testContext, id)
				if err != nil || submission == nil {
					t.Fatalf("Submission %v %v", submission, err)
				}
				if settled, err := submission.Wait(testContext); err != nil || settled.Status != durable.SubmissionDone {
					t.Fatalf("input %+v %v, want done", settled, err)
				}
				// The rerun shows the slot running again.
				if got := seen.all(); !reflect.DeepEqual(got, []string{"running"}) {
					t.Fatalf("slot statuses seen by the rerun %v, want [running]", got)
				}
				var work []ai.ToolResultMessage
				for _, result := range tlResults(allEntries(t, nextRoot)) {
					if result.ToolCallID == "w" {
						work = append(work, result)
					}
				}
				if len(work) != 1 || work[0].IsError {
					t.Fatalf("work results %+v, want one without error", work)
				}
				mustClose(t, next)
				mustClose(t, crashed)
			})
		}
	})
}
