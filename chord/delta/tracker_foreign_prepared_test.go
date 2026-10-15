package delta

import "testing"

// tracker.ts:256 adopt: `if (context?.owner !== this.#owner) throw new Error("Prepared change belongs to a different tracker")`. The owner token is made by a tracker's first change, so a tracker that has never opened one has no token and refuses any prepared value; the ported test (tracker.test.ts:257-265) only reaches that case. A tracker that does have a token must refuse a prepared value of another tracker the same way, leave its own revision untouched, and leave the prepared value adoptable by its own tracker.
//
// mutation-checked: dropping the owner comparison in Adopt lets the second tracker take the first tracker's prepared value.
func TestAdoptRefusesAPreparedValueOfAnotherTrackerThatAlreadyHasAnOwner(t *testing.T) {
	first, second := Track(jsonObject(t, `{"value":0}`)), Track(jsonObject(t, `{"value":0}`))
	first.BeginChange().Abort()
	second.BeginChange().Abort()
	prepared := first.PrepareReplace(jsonObject(t, `{"value":1}`))
	expectErr(t, second.Adopt(prepared), "different tracker")
	if second.Revision() != 0 {
		t.Fatalf("the refused adoption changed the second tracker: revision %d", second.Revision())
	}
	if value, _ := second.Value().Get("value"); value != float64(0) {
		t.Fatalf("the refused adoption changed the second tracker's value to %v", value)
	}
	mustNoErr(t, first.Adopt(prepared))
	if value, _ := first.Value().Get("value"); value != float64(1) || first.Revision() != 1 {
		t.Fatalf("the prepared value was not adoptable by its own tracker: value %v revision %d", value, first.Revision())
	}
}
