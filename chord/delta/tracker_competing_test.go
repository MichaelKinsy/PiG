package delta

import (
	"strings"
	"testing"
)

// Ports the "delta tracker competing changes" cases of packages/chord/test/delta-tracker/tracker.test.ts (a9228305). Upstream counts WeakRef constructions; Go counts the weak registrations the tracker's owner holds, which are one per demoted change, so a tracker with one change open at a time registers none.

// weakRegistrations is the number of changes the tracker holds weakly: its open changes other than the most recent.
func weakRegistrations(tracker *Tracker) int {
	if tracker.owner == nil {
		return 0
	}
	return len(tracker.owner.contexts)
}

func TestTrackerTracksOneChangeAtATimeWithoutAWeakRegistration(t *testing.T) {
	tracker := Track(jsonObject(t, `{"value":0,"child":{"rows":[0]}}`))
	expectNone := func(step string) {
		t.Helper()
		if registered := weakRegistrations(tracker); registered != 0 {
			t.Fatalf("%s: the tracker holds %d changes weakly, want none", step, registered)
		}
	}
	for index := 1; index <= 100; index++ {
		number := float64(index)
		change := tracker.BeginChange()
		expectNone("begin")
		state := change.State()
		mustNoErr(t, state.Set("value", number))
		if _, err := state.Object("child").Array("rows").Push(number); err != nil {
			t.Fatal(err)
		}
		mustNoErr(t, state.Set("child", JsonObjectOf("rows", []any{number})))
		mustNoErr(t, tracker.Adopt(mustPrepare(t, change)))
		tracker.BeginChange().Abort()
		aborted := tracker.BeginChange()
		mustNoErr(t, aborted.State().Set("value", -1.0))
		mustPrepare(t, aborted).Abort()
		expectNone("settled")
		mustNoErr(t, tracker.Adopt(tracker.PrepareReplace(JsonObjectOf("value", number, "child", JsonObjectOf("rows", []any{number})))))
	}
	expectJSONText(t, tracker.Value(), `{"value":100,"child":{"rows":[100]}}`)
	if tracker.Revision() != 200 {
		t.Fatalf("revision %d, want 200", tracker.Revision())
	}
}

func TestTrackerMakesTheOlderOfTwoOpenDraftsStaleWhenTheNewerIsAdopted(t *testing.T) {
	tracker := Track(jsonObject(t, `{"value":0,"child":{"value":0}}`))
	older := tracker.BeginChange()
	held := older.State().Object("child")
	mustNoErr(t, held.Set("value", 1.0))
	newer := tracker.BeginChange()
	mustNoErr(t, newer.State().Set("value", 2.0))
	mustNoErr(t, tracker.Adopt(mustPrepare(t, newer)))
	expectJSONText(t, tracker.Value(), `{"value":2,"child":{"value":0}}`)
	expectRevoked(t, func() { held.Get("value") }, "a handle of the older draft")
	expectRevoked(t, func() { older.State() }, "the older change's state")
	if _, err := older.Prepare(); err == nil || !strings.Contains(err.Error(), "settled") {
		t.Fatalf("prepare of the older change: %v, want a settled overlay error", err)
	}
	older.Abort()
}

func TestTrackerMakesTheNewerOfTwoOpenDraftsStaleWhenTheOlderIsAdopted(t *testing.T) {
	tracker := Track(jsonObject(t, `{"value":0,"child":{"value":0}}`))
	older := tracker.BeginChange()
	mustNoErr(t, older.State().Set("value", 1.0))
	newer := tracker.BeginChange()
	held := newer.State().Object("child")
	mustNoErr(t, held.Set("value", 2.0))
	mustNoErr(t, tracker.Adopt(mustPrepare(t, older)))
	expectJSONText(t, tracker.Value(), `{"value":1,"child":{"value":0}}`)
	expectRevoked(t, func() { held.Get("value") }, "a handle of the newer draft")
	if _, err := newer.Prepare(); err == nil || !strings.Contains(err.Error(), "settled") {
		t.Fatalf("prepare of the newer change: %v, want a settled overlay error", err)
	}
	// The tracker moved on: the next change is tracked as the only one again.
	next := tracker.BeginChange()
	if registered := weakRegistrations(tracker); registered != 0 {
		t.Fatalf("the tracker holds %d changes weakly after the adoption, want none", registered)
	}
	mustNoErr(t, next.State().Set("value", 3.0))
	mustNoErr(t, tracker.Adopt(mustPrepare(t, next)))
	expectJSONText(t, tracker.Value(), `{"value":3,"child":{"value":0}}`)
}

func TestTrackerMakesEveryOtherOfThreeDraftsStaleOpenOrPreparedWhicheverIsAdopted(t *testing.T) {
	for winner := range 3 {
		tracker := Track(jsonObject(t, `{"value":0,"rows":[{"value":0}]}`))
		changes := []*Change{tracker.BeginChange(), tracker.BeginChange(), tracker.BeginChange()}
		held := make([]*Object, len(changes))
		for index, change := range changes {
			held[index] = change.State().Array("rows").Object(0)
			mustNoErr(t, held[index].Set("value", float64(index+1)))
		}
		prepared := mustPrepare(t, changes[(winner+1)%3])
		mustNoErr(t, tracker.Adopt(mustPrepare(t, changes[winner])))
		if value := field(t, tracker.Value(), "rows", 0, "value"); value != float64(winner+1) {
			t.Fatalf("winner %d: adopted value %v", winner, value)
		}
		if err := tracker.Adopt(prepared); err == nil || !strings.Contains(err.Error(), "stale") {
			t.Fatalf("winner %d: adopting the prepared competitor: %v, want stale", winner, err)
		}
		open := changes[(winner+2)%3]
		expectRevoked(t, func() { held[(winner+2)%3].Get("value") }, "a handle of the open competitor")
		if _, err := open.Prepare(); err == nil || !strings.Contains(err.Error(), "settled") {
			t.Fatalf("winner %d: prepare of the open competitor: %v, want a settled overlay error", winner, err)
		}
		if value := field(t, prepared.Value(), "rows", 0, "value"); value != float64((winner+1)%3+1) {
			t.Fatalf("winner %d: the prepared competitor's value %v", winner, value)
		}
	}
}

func TestTrackerKeepsAnAbandonedDraftACompetitorOfTheNextChangeThroughOneWeakRegistration(t *testing.T) {
	tracker := Track(jsonObject(t, `{"value":0,"child":{"value":0}}`))
	abandoned := tracker.BeginChange()
	abandonedChild := abandoned.State().Object("child")
	mustNoErr(t, abandonedChild.Set("value", 1.0))
	if registered := weakRegistrations(tracker); registered != 0 {
		t.Fatalf("one open change: %d weak registrations, want none", registered)
	}
	next := tracker.BeginChange()
	if registered := weakRegistrations(tracker); registered != 1 {
		t.Fatalf("two open changes: %d weak registrations, want 1", registered)
	}
	mustNoErr(t, next.State().Set("value", 2.0))
	mustNoErr(t, tracker.Adopt(mustPrepare(t, next)))
	after := tracker.BeginChange()
	mustNoErr(t, after.State().Set("value", 3.0))
	mustNoErr(t, tracker.Adopt(mustPrepare(t, after)))
	expectJSONText(t, tracker.Value(), `{"value":3,"child":{"value":0}}`)
	expectRevoked(t, func() { abandonedChild.Get("value") }, "a handle of the abandoned draft")
	if _, err := abandoned.Prepare(); err == nil || !strings.Contains(err.Error(), "settled") {
		t.Fatalf("prepare of the abandoned change: %v, want a settled overlay error", err)
	}
}
