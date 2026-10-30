package documents

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/test/next/documents/residency.test.ts
//
// Pi's fake timers (`vi.useFakeTimers`, `advanceTimersByTime`, `getTimerCount`) are the fakeClock of support_test.go.

type evictLog struct{ keys []string }

func (log *evictLog) Evict(resident *Resident) { log.keys = append(log.keys, resident.Key) }

// idle is a residency whose sweeps run at once (no line), and the keys it evicted, in order.
func idle() (*Residency, *fakeClock, func(key string) *Resident, *evictLog) {
	log := &evictLog{}
	clock := newFakeClock(&loop{})
	residency := NewResidency(clock, func(sweep func()) { sweep() })
	resident := func(key string) *Resident {
		return NewResident(log, key, delta.Track(delta.NewJsonObject(0)), nil, 1)
	}
	return residency, clock, resident, log
}

func expectEvicted(t *testing.T, log *evictLog, want ...string) {
	t.Helper()
	if !slices.Equal(log.keys, want) && (len(log.keys) != 0 || len(want) != 0) {
		t.Fatalf("evicted = %v, want %v", log.keys, want)
	}
}

func expectTimers(t *testing.T, clock *fakeClock, want int) {
	t.Helper()
	if got := clock.TimerCount(); got != want {
		t.Fatalf("timers = %d, want %d", got, want)
	}
}

func TestTheIdleStructureEvictsIdleDocumentsOldestFirstOnceTheirRetentionPassedWithOneTimer(t *testing.T) {
	residency, clock, resident, log := idle()
	a, b := resident("a"), resident("b")
	residency.Touch(a)
	clock.Advance(5_000)
	residency.Touch(b)
	expectTimers(t, clock, 1)
	clock.Advance(RetentionMs - 5_000)
	expectEvicted(t, log, "a")
	expectTimers(t, clock, 1)
	clock.Advance(5_000)
	expectEvicted(t, log, "a", "b")
	expectTimers(t, clock, 0)
	clock.Advance(RetentionMs)
	expectEvicted(t, log, "a", "b")
}

func TestTheIdleStructureMovesATouchedDocumentToTheBackAtMostOncePerGranule(t *testing.T) {
	residency, clock, resident, log := idle()
	a, b := resident("a"), resident("b")
	residency.Touch(a)
	residency.Touch(b)
	clock.Advance(500)
	residency.Touch(a)
	clock.Advance(RetentionMs - 500)
	// Within its granule the touch did not move `a`: it leaves up to a granule early.
	expectEvicted(t, log, "a", "b")
	residency.Touch(a)
	residency.Touch(b)
	clock.Advance(1_000)
	residency.Touch(a)
	clock.Advance(RetentionMs - 1_000)
	expectEvicted(t, log, "a", "b", "b")
	clock.Advance(1_000)
	expectEvicted(t, log, "a", "b", "b", "a")
}

func TestTheIdleStructureForgetsPinnedDocumentsAndAfterEndNothingIsEvictedAndNoTimerIsLeft(t *testing.T) {
	residency, clock, resident, log := idle()
	a := resident("a")
	residency.Touch(a)
	residency.Forget(a)
	clock.Advance(RetentionMs)
	expectEvicted(t, log)
	residency.Touch(resident("b"))
	residency.End()
	expectTimers(t, clock, 0)
	clock.Advance(RetentionMs)
	expectEvicted(t, log)
}
