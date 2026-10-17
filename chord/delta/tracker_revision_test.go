package delta

import "testing"

// TestTrackerRevisionCountsAdoptions mirrors packages/chord/src/delta/tracker.ts:219-275: `get revision()` starts at 0 and `adopt` adds one per adopted preparation, a no-op preparation included; a preparation made on an older revision is rejected and leaves the revision unchanged.
// mutation-checked: Revision returning a constant, adoption advancing by two, adoption decrementing, and a rejected adoption advancing each fail it.
func TestTrackerRevisionCountsAdoptions(t *testing.T) {
	tracker := Track(JsonObjectOf("a", 1.0))
	if tracker.Revision() != 0 {
		t.Fatalf("initial revision = %d, want 0", tracker.Revision())
	}
	if err := tracker.Adopt(tracker.PrepareReplace(JsonObjectOf("a", 2.0))); err != nil {
		t.Fatal(err)
	}
	if tracker.Revision() != 1 {
		t.Fatalf("revision after one adoption = %d, want 1", tracker.Revision())
	}
	if err := tracker.Adopt(tracker.PrepareReplace(JsonObjectOf("a", 2.0))); err != nil {
		t.Fatal(err)
	}
	if tracker.Revision() != 2 {
		t.Fatalf("revision after a no-op adoption = %d, want 2", tracker.Revision())
	}
	stale := tracker.PrepareReplace(JsonObjectOf("a", 3.0))
	if err := tracker.Adopt(tracker.PrepareReplace(JsonObjectOf("a", 4.0))); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Adopt(stale); err == nil {
		t.Fatal("a preparation of an older revision was adopted")
	}
	if tracker.Revision() != 3 {
		t.Fatalf("revision after a rejected adoption = %d, want 3", tracker.Revision())
	}
}
