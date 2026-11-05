package documents

import (
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/next/documents/residency.ts
//
// Residency of keyed documents (slice 6a §10, rule 23): which keyed documents are in memory, and the eviction of
// persisted ones nobody pinned and nobody read or wrote for the retention. One idle structure and one timer per
// object, Durable's (`Session.#touchDocument`): an insertion-ordered list whose oldest entry the timer waits for, a
// touch moving an entry to the back at most once per granule (no timer per key).
//
// Go forced difference: Pi's `Date.now()` and `setTimeout` are the Clock the object gives (the host's loop: Now in
// ms, AfterFunc running its job as a loop section), so a test drives time. A Residency is used from the host's
// sections only.

// Clock is the time a Residency reads and waits on.
type Clock interface {
	// Now is the time in milliseconds.
	Now() int64
	// AfterFunc runs job as a section once d passed, unless cancel was called.
	AfterFunc(d time.Duration, job func()) (cancel func())
}

// Evictor is the slot a Resident belongs to.
type Evictor interface{ Evict(resident *Resident) }

// Resident is one keyed document in memory: its slot, its tracker and, persisted, what is stored of it.
type Resident struct {
	Slot    Evictor
	Key     string
	Tracker *delta.Tracker
	Stored  *Stored
	// Version is the definition version the tracker's value is at: below the slot's, it migrates on its next access.
	Version int
}

// NewResident is a resident of slot at key.
func NewResident(slot Evictor, key string, tracker *delta.Tracker, stored *Stored, version int) *Resident {
	return &Resident{Slot: slot, Key: key, Tracker: tracker, Stored: stored, Version: version}
}

// RetentionMs is how long an unpinned persisted keyed document stays in memory after its last read or write.
const RetentionMs int64 = 10 * 60_000

// granuleMs: a touch moves an entry at most once per granule, a tenth of the retention up to 1 s (Durable's rule).
const granuleMs int64 = 1_000

type idleEntry struct {
	resident *Resident
	since    int64
}

type pendingSweep struct {
	at     int64
	cancel func()
}

// Residency is the idle persisted keyed documents of one object, oldest first, and the one timer evicting them.
type Residency struct {
	clock  Clock
	onLine func(sweep func())
	idle   []idleEntry
	sweep  *pendingSweep
	ended  bool
}

// NewResidency is a residency on clock; onLine runs a sweep on the object's line.
func NewResidency(clock Clock, onLine func(sweep func())) *Residency {
	return &Residency{clock: clock, onLine: onLine}
}

func (r *Residency) index(resident *Resident) int {
	for i, entry := range r.idle {
		if entry.resident == resident {
			return i
		}
	}
	return -1
}

func (r *Residency) remove(resident *Resident) {
	if i := r.index(resident); i >= 0 {
		// slices.Delete clears the vacated slot: a removed resident is not held by the backing array.
		r.idle = slices.Delete(r.idle, i, i+1)
	}
}

// Touch is a use of an unpinned resident: it becomes the newest idle one (once per granule), and a sweep is scheduled.
func (r *Residency) Touch(resident *Resident) {
	now := r.clock.Now()
	if i := r.index(resident); i >= 0 && now-r.idle[i].since < granuleMs {
		// A hot document moves at most once per granule: it may then leave up to a granule early (a reload).
		return
	}
	r.remove(resident)
	r.idle = append(r.idle, idleEntry{resident, now})
	if r.sweep == nil {
		r.schedule()
	}
}

// Forget is: resident is pinned or left memory; it is no longer idle.
func (r *Residency) Forget(resident *Resident) { r.remove(resident) }

// End is: the object ended; nothing is evicted any more, the timer is cleared.
func (r *Residency) End() {
	r.ended = true
	r.idle = nil
	if r.sweep != nil && r.sweep.cancel != nil {
		r.sweep.cancel()
	}
	r.sweep = nil
}

// schedule schedules the eviction of the oldest idle document when its retention expires.
func (r *Residency) schedule() {
	if r.ended || len(r.idle) == 0 {
		return
	}
	at := r.idle[0].since + RetentionMs
	sweep := &pendingSweep{at: at}
	r.sweep = sweep
	delay := max(0, at-r.clock.Now())
	// A timer firing early sweeps nothing and schedules the next one.
	sweep.cancel = r.clock.AfterFunc(time.Duration(delay)*time.Millisecond, func() {
		r.onLine(func() {
			if r.sweep == sweep {
				r.sweep = nil
			}
			r.evictExpired()
		})
	})
}

// evictExpired is, on the line: evicts the idle documents whose retention passed, oldest first, then schedules the
// next sweep. No load in flight needs a pin against it: cold loads run on the line too.
func (r *Residency) evictExpired() {
	if r.ended {
		return
	}
	expired := r.clock.Now() - RetentionMs
	for len(r.idle) > 0 && r.idle[0].since <= expired {
		resident := r.idle[0].resident
		r.idle[0] = idleEntry{}
		r.idle = r.idle[1:]
		resident.Slot.Evict(resident)
	}
	if r.sweep == nil {
		r.schedule()
	}
}
