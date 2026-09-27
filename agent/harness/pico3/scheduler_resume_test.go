package pico3

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
)

// Pi's harness.ts:366-389 captures the orphan set before resume returns. Registry changes after that call do not change the set, even while persistence and scheduler startup are pending.
func TestResumeSnapshotsOrphansBeforeReturning(t *testing.T) {
	for _, name := range []string{"known", "new", "unknown"} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := must(OpenHarness(bg, NewMemoryStorage(), HarnessOptions{}))
				t.Cleanup(func() { check(t, h.Close(bg)) })
				root := must(h.Root(bg))
				release := must(h.Hold())
				makeKind := func(label string) *Kind {
					return &Kind{Name: "resume-reload", Initial: func(context.Context, Task, *Runtime) (Step, error) {
						return done(Completed(label)), nil
					}}
				}
				old := makeKind("old")
				off := must(h.RegisterTaskKind(old))
				create := func() TaskRef {
					return must(HostCommit(bg, root, func(_ context.Context, tx *Tx) (TaskRef, error) {
						return tx.CreateTask(old, nil, TaskOptions{ConversationId: &root.Id, Background: true})
					}))
				}
				var ref TaskRef
				if name != "new" {
					ref = create()
				}
				if name == "unknown" {
					off()
				}
				check(t, h.Resume())
				if name == "unknown" {
					must(h.RegisterTaskKind(makeKind("replacement")))
				}
				if name == "new" {
					ref = create()
				}
				off()
				synctest.Wait()
				held := must(h.GetTask(bg, ref.Id)).Status
				wantHeld := TaskPending
				want := &Outcome{Status: OutcomeCompleted, Result: "replacement"}
				if name == "unknown" {
					wantHeld = TaskTerminal
					want = &Outcome{Status: OutcomeOrphaned}
				}
				equal(t, held, wantHeld, "resume-time orphan set")
				if name != "unknown" {
					must(h.RegisterTaskKind(makeKind("replacement")))
				}
				release()
				task := must(h.WaitForTask(bg, ref.Id))
				equal(t, task.Outcome, want, "replacement outcome")
				result := "-"
				if task.Outcome.Result != nil {
					result = task.Outcome.Result.(string)
				}
				fmt.Printf("PICO3_RESUME %s %s %s %s\n", name, held, task.Outcome.Status, result)
			})
		})
	}
}

func BenchmarkResumeHoldReplacement(b *testing.B) {
	kind := &Kind{Name: "resume-bench", Initial: func(context.Context, Task, *Runtime) (Step, error) {
		return done(Completed("replacement")), nil
	}}
	for b.Loop() {
		h := must(OpenHarness(bg, NewMemoryStorage(), HarnessOptions{}))
		release := must(h.Hold())
		if err := h.Resume(); err != nil {
			b.Fatal(err)
		}
		must(h.RegisterTaskKind(kind))
		root := must(h.Root(bg))
		ref := must(HostCommit(bg, root, func(_ context.Context, tx *Tx) (TaskRef, error) {
			return tx.CreateTask(kind, nil, TaskOptions{ConversationId: &root.Id, Background: true})
		}))
		release()
		task := must(h.WaitForTask(bg, ref.Id))
		if task.Outcome.Status != OutcomeCompleted {
			b.Fatalf("outcome: %+v", task.Outcome)
		}
		if err := h.Close(bg); err != nil {
			b.Fatal(err)
		}
	}
}
