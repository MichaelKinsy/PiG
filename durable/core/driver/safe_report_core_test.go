package driver_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// The Go port over the core of harness-storage-failure.test.ts "keeps working when onReport throws, and falls back to
// Date.now when the clock throws": Pi's Harness wraps onReport in safeReport and the clock in safeNow (harness.ts).

func TestCoreKeepsWorkingWhenOnReportThrowsAndFallsBackToTheWallClock(t *testing.T) {
	var mu sync.Mutex
	reported := 0
	quick := quickTask("test.quick")
	h, root := openTasksWith(t, harness.HarnessOptions{
		Now: func() float64 { panic(errors.New("clock broke")) },
		OnReport: func(error, durable.ReportInfo) {
			mu.Lock()
			reported++
			mu.Unlock()
			panic(errors.New("report broke"))
		},
	}, quick.Erased())
	id := startTask(t, root, quick.Erased())
	h.Resume()
	record, err := h.WaitForTask(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if record.State.Outcome == nil || record.State.Outcome.Status != durable.OutcomeCompleted {
		t.Fatalf("outcome %+v, want completed", record.State.Outcome)
	}
	if record.StartedAt == nil || *record.StartedAt <= 0 {
		t.Fatalf("startedAt %v, want the wall clock", ptrText(record.StartedAt))
	}
	mu.Lock()
	defer mu.Unlock()
	if reported != 1 {
		t.Fatalf("onReport ran %d times, want the clock's failure reported once", reported)
	}
}

func TestCoreDropsAThrowingOnReportForAFailingListener(t *testing.T) {
	// safeReport: onReport is the last place an error goes, so its own throw is dropped.
	reported := make(chan error, 4)
	h, root := openTasksWith(t, harness.HarnessOptions{OnReport: func(err error, _ durable.ReportInfo) {
		reported <- err
		panic(errors.New("report broke"))
	}})
	watch, err := root.Watch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = watch.Stop() }()
	failure := errors.New("view watch listener failed")
	watch.Start(func(context.Context, harness.ConversationView, []durable.Op) error { return failure })
	appendCoreNote(t, root)
	expectReport(t, reported, failure)
	appendCoreNote(t, root)
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCoreReportsAThrowingCommitOrCloseListenerAndRunsTheOthers(t *testing.T) {
	// harness-storage-failure.test.ts "reports a throwing commit or close listener and runs the others; the commit stands"
	// (session.ts: a throw of a subscribeCommits or subscribeClose listener is reported).
	var mu sync.Mutex
	var reports []string
	var seen []string
	h, root := openTasksWith(t, harness.HarnessOptions{OnReport: func(err error, _ durable.ReportInfo) {
		mu.Lock()
		reports = append(reports, err.Error())
		mu.Unlock()
	}})
	committed := make(chan struct{}, 1)
	h.SubscribeCommits(func(context.Context, durable.CommitPublication) { panic(errors.New("commit listener")) })
	h.SubscribeCommits(func(context.Context, durable.CommitPublication) {
		mu.Lock()
		seen = append(seen, "commit")
		mu.Unlock()
		committed <- struct{}{}
	})
	h.SubscribeClose(func() { panic(errors.New("close listener")) })
	h.SubscribeClose(func() {
		mu.Lock()
		seen = append(seen, "close")
		mu.Unlock()
	})
	appendCoreNote(t, root)
	<-committed
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != "commit" || seen[1] != "close" {
		t.Fatalf("listeners that ran: %v", seen)
	}
	if len(reports) != 2 || reports[0] != "commit listener" || reports[1] != "close listener" {
		t.Fatalf("reports %v, want the commit listener's and the close listener's errors", reports)
	}
}
