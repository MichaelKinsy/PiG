package harness

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// groupGoroutines is the stacks among stacks of goroutines started with sync.WaitGroup.Go: the scheduler's passes.
func groupGoroutines(stacks []string) []string {
	var group []string
	for _, stack := range stacks {
		if strings.Contains(stack, "sync.(*WaitGroup).Go") {
			group = append(group, stack)
		}
	}
	return group
}

// A commit's publication runs the Session's own observers, then the host's commit observers, then the work the observers
// deferred to its end: there a scheduled pass takes its place on the Session line. A Close on another goroutine runs
// between the two: it seals the scheduler, and from then on Join may wait for the background group. A pass that joins the
// group only when the publication ends comes after that: Join does not wait for it, and an Add that meets Join's Wait at
// a zero counter is the WaitGroup's "reused before previous Wait has returned" panic.
func TestSchedulerStartsNoPassAfterTheSealWhenAPublicationEndsAfterIt(t *testing.T) {
	for _, entry := range []struct {
		name string
		// schedule schedules the pass in the publication underway and reports whether none was scheduled before.
		schedule func(impl *harnessImpl) bool
	}{
		// The scheduler was not resumed, so no commit queued a reservation pass.
		{"reservation", func(impl *harnessImpl) bool {
			impl.tasks.mu.Lock()
			fresh := !impl.tasks.draining
			impl.tasks.mu.Unlock()
			impl.Resume()
			return fresh
		}},
		{"reconcile", func(impl *harnessImpl) bool {
			impl.tasks.mu.Lock()
			defer impl.tasks.mu.Unlock()
			fresh := !impl.tasks.reconcileScheduled
			impl.tasks.scheduleReconcileLocked()
			return fresh
		}},
		{"sweep", func(impl *harnessImpl) bool {
			impl.tasks.mu.Lock()
			defer impl.tasks.mu.Unlock()
			fresh := !impl.tasks.sweepScheduled
			impl.tasks.scheduleSweepLocked()
			return fresh
		}},
	} {
		t.Run(entry.name, func(t *testing.T) {
			step := oneStep("test.pass-after-seal")
			harness, _, _ := openTasks(t, storage.NewMemoryStorage(), []durable.AnyTask{step.Erased()})
			impl := harness.(*harnessImpl)
			tasks := impl.tasks
			root := mustRoot(t, harness, nil)
			create := func(tx durable.Tx) (durable.TaskId, error) {
				return durable.CreateTask(tx, step, nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
			}
			// The first task below a conversation queues a sweep of the scheduler's index, a pass that waits on the Session line
			// behind the commit that queued it. This commit takes it; the one observed below queues no pass of its own.
			if _, err := durable.Commit(testContext, root, create); err != nil {
				t.Fatal(err)
			}

			inObserver, release, ended := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var baseline testenv.GoroutineBaseline
			var late []string
			var once sync.Once
			impl.ObserveCommits(commitsObserver{advance: func(context.Context, durable.CommitPublication) {
				once.Do(func() {
					// The deferred work runs in the order it was deferred. The scheduler's mutex is held from before the scheduled pass
					// takes its place on the line until after: a pass that starts there parks at its first step, where the goroutines
					// are listed.
					impl.DeferUntilPublished(func() {
						baseline = testenv.BaselineGoroutines()
						tasks.mu.Lock()
					})
					if !entry.schedule(impl) {
						t.Errorf("a %s pass was scheduled before the observer scheduled one", entry.name)
					}
					impl.DeferUntilPublished(func() {
						late = groupGoroutines(baseline.Outliving())
						tasks.mu.Unlock()
						close(ended)
					})
					close(inObserver)
					<-release
				})
			}})
			created := async(func() (durable.TaskId, error) { return durable.Commit(testContext, root, create) })
			<-inObserver
			closing := asyncErr(func() error { return harness.Close(testContext) })
			// The seal completed: Join may wait for the background group.
			<-tasks.sealed
			close(release)
			<-ended
			if _, err := closing.wait(); err != nil {
				t.Fatal(err)
			}
			if _, err := created.wait(); err != nil {
				t.Fatal(err)
			}
			if len(late) > 0 {
				t.Fatalf("%d pass(es) joined the scheduler's background group after the seal completed, when the publication that scheduled them ended:\n\n%s", len(late), strings.Join(late, "\n\n"))
			}
		})
	}
}
