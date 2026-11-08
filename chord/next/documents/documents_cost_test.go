package documents

import (
	"context"
	"sync/atomic"
	"testing"
)

// Ports `it("cost nothing on the committed path: no check of the context beyond the commit's own (cost guard)")` of
// the describe `transactions their context's end cancelled` of packages/chord/test/documents/documents.test.ts. Pi
// spies on AbortSignal's `aborted` getter; here the context's Err is counted. Pi's commit reads nothing of the
// signal; Go's commit reads Err once (its own check), and a transaction that did not commit reads it again to be
// reported. The guard is the same: the committed changes' reads do not grow with anything but the commit itself.
// pi: packages/chord/src/documents/documents.ts

type errCountingContext struct {
	context.Context
	errs atomic.Int64
}

func (c *errCountingContext) Err() error {
	c.errs.Add(1)
	return c.Context.Err()
}

func TestTransactionsTheirContextsEndCancelledCostNothingOnTheCommittedPathNoCheckOfTheContextBeyondTheCommitsOwn(t *testing.T) {
	listDefinition, _ := documentsDefinitions(t)
	var list *Singleton
	o, _ := activated(t, objectOptions{}, func(a *attempt) { list = a.Singleton(listDefinition) })
	live := &errCountingContext{Context: background}
	const changes = 50
	for n := range changes {
		if _, err := list.Change(live, slotPush(string(rune('a'+n%26)))); err != nil {
			t.Fatalf("change %d: %v", n, err)
		}
	}
	committed := live.errs.Load()
	if committed > changes {
		t.Fatalf("%d committed changes read the context's Err %d times, want at most one each (the commit's own)", changes, committed)
	}

	cancelled, cancel := context.WithCancelCause(background)
	cancel(nil)
	dead := &errCountingContext{Context: cancelled}
	if _, err := list.Change(dead, slotPush("lost")); err == nil {
		t.Fatal("a change under a cancelled context committed")
	}
	if got := dead.errs.Load(); got < 1 {
		t.Fatalf("the cancelled change read Err %d times, want at least the report's", got)
	}
	if got := o.Cancelled(); len(got) != 1 || len(o.Failures()) != 0 {
		t.Fatalf("reports = %v failures = %v, want one cancellation", got, o.Failures())
	}
}
