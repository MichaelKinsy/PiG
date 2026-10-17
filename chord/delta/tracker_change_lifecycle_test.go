package delta

import (
	"errors"
	"testing"
)

// commitEdit runs edit on a fresh Change of tracker and adopts the preparation, as a caller of tracker.ts's beginChange/prepare/adopt does.
func commitEdit(t *testing.T, tracker *Tracker, edit func(state *Object)) *Prepared {
	t.Helper()
	return adoptChange(t, tracker, tracker.BeginChange(), edit)
}

func setCount(t *testing.T, state *Object, value float64) {
	t.Helper()
	if err := state.Set("count", value); err != nil {
		t.Fatal(err)
	}
}

func adoptChange(t *testing.T, tracker *Tracker, change *Change, edit func(state *Object)) *Prepared {
	t.Helper()
	edit(change.State())
	prepared, err := change.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.Adopt(prepared); err != nil {
		t.Fatal(err)
	}
	return prepared
}

// TestTrackerChangesCommitInOrderAndAbortedChangesLeaveNoTrace mirrors packages/chord/src/delta/tracker.ts beginChange/prepare/adopt/abort and
// delta.test.ts:47-60: each adopted Change advances the Tracker one revision and replaces its value, while an aborted Change (and one begun after
// it) leaves the committed value and revision as they were. Mutation-checked: Abort leaving the Change usable, Adopt not advancing the revision,
// and the Tracker value not following the preparation each fail it.
func TestTrackerChangesCommitInOrderAndAbortedChangesLeaveNoTrace(t *testing.T) {
	tracker := Track(JsonObjectOf("count", 1.0))
	commitEdit(t, tracker, func(state *Object) { setCount(t, state, 2.0) })
	if got, _ := tracker.Value().Get("count"); got != 2.0 || tracker.Revision() != 1 {
		t.Fatalf("after one adopted change: count = %v, revision = %d, want 2 and 1", got, tracker.Revision())
	}
	aborted := tracker.BeginChange()
	setCount(t, aborted.State(), 99.0)
	aborted.Abort()
	func() {
		defer func() {
			if recovered, _ := recover().(error); !errors.Is(recovered, ErrRevoked) {
				t.Fatal("an aborted change handed out its state again")
			}
		}()
		aborted.State()
	}()
	if _, err := aborted.Prepare(); err == nil {
		t.Fatal("an aborted change prepared a revision")
	}
	if got, _ := tracker.Value().Get("count"); got != 2.0 || tracker.Revision() != 1 {
		t.Fatalf("after an aborted change: count = %v, revision = %d, want 2 and 1", got, tracker.Revision())
	}
	prepared := commitEdit(t, tracker, func(state *Object) { setCount(t, state, 3.0) })
	if got, _ := tracker.Value().Get("count"); got != 3.0 || tracker.Revision() != 2 || len(prepared.Ops()) != 1 {
		t.Fatalf("after the next change: count = %v, revision = %d, ops = %v", got, tracker.Revision(), prepared.Ops())
	}
}
