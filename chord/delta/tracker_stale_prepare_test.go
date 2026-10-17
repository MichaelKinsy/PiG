package delta

import (
	"errors"
	"testing"
)

// upstream: packages/chord/src/delta/tracker.ts ChangeImpl.prepare calls assertWritable before its try block, so on a change that a
// competing adoption made stale (#invalidate) it throws "Cannot use a settled overlay" without settling: a second prepare throws the
// same error, abort() then settles it while abortContext leaves the stale status alone, and only after that does prepare report
// "Change has already been settled". retention.worker.ts "stale-unprepared-change" relies on the same rule: the stale change still
// holds its context after the failed prepare, so only the adoption's release can free the obsolete revision.
func TestStalePrepareLeavesTheChangeUnsettledAsPi(t *testing.T) {
	tracker := Track(JsonObjectOf("rows", []any{JsonObjectOf("value", 1.0)}))
	stale := tracker.BeginChange()
	mustNoErr(t, stale.State().Array("rows").Object(0).Set("value", -1.0))
	if err := tracker.Adopt(tracker.PrepareReplace(JsonObjectOf("rows", []any{}))); err != nil {
		t.Fatal(err)
	}
	for attempt := range 2 {
		if _, err := stale.Prepare(); !errors.Is(err, ErrRevoked) {
			t.Fatalf("stale prepare %d = %v, want Pi's %q", attempt+1, err, ErrRevoked)
		}
	}
	if stale.ctx == nil || stale.ctx.status != statusStale {
		t.Fatalf("a failed stale prepare settled the change or changed its status: ctx=%v", stale.ctx)
	}
	stale.Abort()
	if stale.ctx != nil || !stale.settled {
		t.Fatal("abort did not settle the stale change")
	}
	if _, err := stale.Prepare(); err == nil || err.Error() != "Change has already been settled" {
		t.Fatalf("prepare after abort = %v, want %q", err, "Change has already been settled")
	}
}
