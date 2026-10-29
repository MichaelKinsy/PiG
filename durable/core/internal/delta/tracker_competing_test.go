package delta

import (
	"runtime"
	"strings"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// Ports the "delta tracker competing changes" cases of packages/chord/test/delta-tracker/tracker.test.ts and the
// "abandoned-draft-released" and "tracker-with-open-draft" scenarios of retention.worker.ts (Pi a9228305) onto the durable
// tracker. Go mapping: Pi registers open drafts with the tracker so adopting one makes the others stale at once. This
// tracker keeps no registry: a draft is stale when its base revision is not the tracker's (assertOpen, Adopt), which is
// the same observable rule because revisions only grow. Pi's WeakRef counts, and its rule that the tracker holds its
// latest open draft until the next change, therefore have no counterpart: the tracker holds no draft, so an abandoned one
// is reclaimed at once.

func expectRevokedUse(t *testing.T, use func(), what string) {
	t.Helper()
	defer func() {
		if recovered, _ := recover().(error); recovered != ErrRevoked {
			t.Fatalf("%s: expected ErrRevoked, got %v", what, recovered)
		}
	}()
	use()
}

func expectPrepareSettled(t *testing.T, change *Change, what string) {
	t.Helper()
	if _, err := change.Prepare(); err == nil || !strings.Contains(err.Error(), "settled") {
		t.Fatalf("%s: prepare = %v, want a settled overlay error", what, err)
	}
}

func rootOf(t *testing.T, text string) *Tracker {
	t.Helper()
	return Track(parse(t, text).(*jv.Object))
}

func adoptChange(t *testing.T, tracker *Tracker, change *Change) {
	t.Helper()
	prepared, err := change.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.Adopt(prepared); err != nil {
		t.Fatal(err)
	}
}

func TestTrackerOlderOfTwoOpenDraftsIsStaleWhenTheNewerIsAdopted(t *testing.T) {
	tracker := rootOf(t, `{"value":0,"child":{"value":0}}`)
	older := tracker.BeginChange()
	held := older.State().Obj("child")
	_ = held.Set("value", 1.0)
	newer := tracker.BeginChange()
	_ = newer.State().Set("value", 2.0)
	adoptChange(t, tracker, newer)
	if got := jv.Stringify(tracker.Value()); got != `{"value":2,"child":{"value":0}}` {
		t.Fatal(got)
	}
	expectRevokedUse(t, func() { held.Get("value") }, "a handle of the older draft")
	expectRevokedUse(t, func() { older.State() }, "the older change's state")
	expectPrepareSettled(t, older, "the older change")
	older.Abort()
}

func TestTrackerNewerOfTwoOpenDraftsIsStaleWhenTheOlderIsAdopted(t *testing.T) {
	tracker := rootOf(t, `{"value":0,"child":{"value":0}}`)
	older := tracker.BeginChange()
	_ = older.State().Set("value", 1.0)
	newer := tracker.BeginChange()
	held := newer.State().Obj("child")
	_ = held.Set("value", 2.0)
	adoptChange(t, tracker, older)
	if got := jv.Stringify(tracker.Value()); got != `{"value":1,"child":{"value":0}}` {
		t.Fatal(got)
	}
	expectRevokedUse(t, func() { held.Get("value") }, "a handle of the newer draft")
	expectPrepareSettled(t, newer, "the newer change")
	next := tracker.BeginChange()
	_ = next.State().Set("value", 3.0)
	adoptChange(t, tracker, next)
	if got := jv.Stringify(tracker.Value()); got != `{"value":3,"child":{"value":0}}` {
		t.Fatal(got)
	}
}

func TestTrackerEveryOtherOfThreeDraftsIsStaleOpenOrPrepared(t *testing.T) {
	for winner := range 3 {
		tracker := rootOf(t, `{"value":0,"rows":[{"value":0}]}`)
		changes := []*Change{tracker.BeginChange(), tracker.BeginChange(), tracker.BeginChange()}
		held := make([]*Obj, len(changes))
		for index, change := range changes {
			held[index] = change.State().Arr("rows").Obj(0)
			_ = held[index].Set("value", float64(index+1))
		}
		prepared, err := changes[(winner+1)%3].Prepare()
		if err != nil {
			t.Fatal(err)
		}
		adoptChange(t, tracker, changes[winner])
		want := `{"value":0,"rows":[{"value":` + string(rune('1'+winner)) + `}]}`
		if got := jv.Stringify(tracker.Value()); got != want {
			t.Fatalf("winner %d: %s, want %s", winner, got, want)
		}
		if err := tracker.Adopt(prepared); err == nil || !strings.Contains(err.Error(), "stale") {
			t.Fatalf("winner %d: adopting the prepared competitor = %v, want stale", winner, err)
		}
		open := (winner + 2) % 3
		expectRevokedUse(t, func() { held[open].Get("value") }, "a handle of the open competitor")
		expectPrepareSettled(t, changes[open], "the open competitor")
		if got := jv.Stringify(prepared.Value()); got != `{"value":0,"rows":[{"value":`+string(rune('1'+(winner+1)%3))+`}]}` {
			t.Fatalf("winner %d: prepared competitor %s", winner, got)
		}
	}
}

func TestTrackerAbandonedDraftIsStaleAfterTheNextAdoptionAndHeldByNothing(t *testing.T) {
	tracker := rootOf(t, `{"value":0,"child":{"value":0}}`)
	abandoned := tracker.BeginChange()
	child := abandoned.State().Obj("child")
	_ = child.Set("value", 1.0)
	next := tracker.BeginChange()
	_ = next.State().Set("value", 2.0)
	adoptChange(t, tracker, next)
	after := tracker.BeginChange()
	_ = after.State().Set("value", 3.0)
	adoptChange(t, tracker, after)
	expectRevokedUse(t, func() { child.Get("value") }, "a handle of the abandoned draft")
	expectPrepareSettled(t, abandoned, "the abandoned change")
}

// retention.worker.ts "abandoned-draft-released" and "tracker-with-open-draft": a dropped draft is reclaimed (here without
// waiting for the tracker's next change), and open changes a caller retains do not keep their tracker alive only where
// Pi's overlay holds the tracker weakly; this overlay holds it strongly, so the second half asserts the draft side only.
func TestTrackerReleasesAnAbandonedDraftWithoutAnotherChange(t *testing.T) {
	tracker := rootOf(t, `{"value":0,"payload":null}`)
	reference := func() weak.Pointer[overlay] {
		change := tracker.BeginChange()
		rows := make([]any, 50_000)
		for index := range rows {
			object := jv.NewObject()
			object.Set("value", float64(index))
			rows[index] = object
		}
		payload := jv.NewObject()
		payload.Set("rows", rows)
		if err := change.State().Set("payload", payload); err != nil {
			t.Fatal(err)
		}
		return weak.Make(change.ctx)
	}()
	for range 30 {
		runtime.GC()
		if reference.Value() == nil {
			break
		}
	}
	if reference.Value() != nil {
		t.Fatal("an abandoned draft is still retained")
	}
	if got := jv.Stringify(tracker.Value()); got != `{"value":0,"payload":null}` {
		t.Fatal(got)
	}
}
