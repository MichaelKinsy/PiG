// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session_test

import (
	"errors"
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// Pi f44707e7 (session.ts document retention) on the core Session: a cached document leaves the cache once it was not used for the
// retention period, and every clock read the bookkeeping makes is made, as the clock is shared with task times.

var retentionDoc = &session.DocDef{
	Kind: "t.retention", Version: 1, Scope: session.ScopeSession,
	Initial: func(any) *jv.Object {
		initial := jv.NewObject()
		initial.Set("n", 0.0)
		return initial
	},
}

type retentionRig struct {
	s     *session.Session
	clock float64
	reads int
}

func newRetentionRig(retention *float64) *retentionRig {
	store := sessiontest.New()
	rig := &retentionRig{clock: 1000}
	rig.s = session.New(store, store.NextID(), func() float64 { rig.reads++; return rig.clock })
	if retention != nil {
		rig.s.DocumentRetention = func() *float64 { return retention }
	}
	return rig
}

func (r *retentionRig) edit(t *testing.T) {
	t.Helper()
	if _, err := r.s.Commit(func(tx *session.Tx) error {
		draft, err := tx.Doc(retentionDoc, session.DocArgs{})
		if err != nil {
			return err
		}
		return draft.Set("n", draft.Get("n").(float64)+1)
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
}

func (r *retentionRig) cached() (int, int) { return r.s.DocumentCacheSizes() }

func TestACachedDocumentLeavesTheCacheAfterTheDefaultRetentionWithoutUse(t *testing.T) {
	rig := newRetentionRig(nil)
	rig.edit(t)
	if cached, idle := rig.cached(); cached != 1 || idle != 1 {
		t.Fatalf("after a use: cached %d idle %d, want 1 and 1", cached, idle)
	}
	rig.clock += session.DefaultDocumentRetentionMs - 1
	rig.edit(t) // a use: the document stays
	if cached, _ := rig.cached(); cached != 1 {
		t.Fatalf("a document used within the retention left the cache")
	}
	rig.clock += session.DefaultDocumentRetentionMs
	if _, err := rig.s.Commit(func(*session.Tx) error { return nil }, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	if cached, idle := rig.cached(); cached != 0 || idle != 0 {
		t.Fatalf("after the retention without use: cached %d idle %d, want none", cached, idle)
	}
}

func TestRetentionZeroDropsTheDocumentRightAfterTheCommitThatUsedIt(t *testing.T) {
	zero := 0.0
	rig := newRetentionRig(&zero)
	rig.edit(t)
	if cached, _ := rig.cached(); cached != 0 {
		t.Fatalf("cached %d after a use under retention 0, want 0", cached)
	}
}

func TestInfiniteRetentionKeepsDocuments(t *testing.T) {
	forever := math.Inf(1)
	rig := newRetentionRig(&forever)
	rig.edit(t)
	rig.clock += 1e15
	rig.edit(t)
	if cached, _ := rig.cached(); cached != 1 {
		t.Fatalf("cached %d, want 1 under infinite retention", cached)
	}
}

func TestARetentionThatIsNotAMillisecondCountMeansTheDefault(t *testing.T) {
	negative := -5.0
	rig := newRetentionRig(&negative)
	rig.edit(t)
	rig.clock += 1000
	rig.edit(t)
	if cached, _ := rig.cached(); cached != 1 {
		t.Fatalf("a negative retention evicted a document used a second ago: cached %d", cached)
	}
}

// session.ts #touchDocument: a hot document moves in the eviction order at most once per granule, a tenth of the retention up to
// 1 s, so its idle time is the first use of the granule.
func TestAHotDocumentMovesInTheEvictionOrderOncePerGranule(t *testing.T) {
	thousand := 1000.0
	rig := newRetentionRig(&thousand)
	rig.edit(t)
	rig.clock += 50 // inside the 100 ms granule: the use does not move the document, which keeps its idle time of 1000
	rig.edit(t)
	rig.clock = 2005 // 1005 ms after the first use, 955 after the second
	if _, err := rig.s.Commit(func(*session.Tx) error { return nil }, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	if cached, _ := rig.cached(); cached != 0 {
		t.Fatalf("cached %d: the second use inside the granule moved the document in the eviction order", cached)
	}
}

// session.ts: the bookkeeping reads the clock for each touch, for the delay of a sweep it schedules, and once per commit outcome.
func TestTheBookkeepingReadsTheClockAsPiDoes(t *testing.T) {
	rig := newRetentionRig(nil)
	rig.edit(t)
	first := rig.reads
	rig.edit(t)
	second := rig.reads - first
	// Pi 97195ec3, first commit: load (not cached: nothing), install touch (1), published-documents touch (1), schedule delay (1),
	// sweep (1) + its schedule (pending: 0); second commit: load touch (1), published touch (1), sweep (1). Task times add none here.
	if first != 4 || second != 3 {
		t.Fatalf("clock reads: first commit %d (want 4), second commit %d (want 3)", first, second)
	}
}

// session.ts #runCommit: every commit outcome sweeps, a rejected callback and a commit with nothing to write too.
func TestEveryCommitOutcomeEvictsWhatExpired(t *testing.T) {
	for name, run := range map[string]func(*session.Session) error{
		"callback fails": func(s *session.Session) error {
			_, err := s.Commit(func(*session.Tx) error { return errors.New("no") }, session.TxScope{})
			return err
		},
		"nothing to write": func(s *session.Session) error {
			_, err := s.Commit(func(*session.Tx) error { return nil }, session.TxScope{})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newRetentionRig(nil)
			rig.edit(t)
			rig.clock += session.DefaultDocumentRetentionMs
			_ = run(rig.s)
			if cached, _ := rig.cached(); cached != 0 {
				t.Fatalf("cached %d after the outcome, want the expired document evicted", cached)
			}
		})
	}
}

// Pi session.ts #scheduleDocumentSweep: the eviction of the oldest idle document starts a line job at once when it is due
// (retention 0, by a microtask) and by an unreferenced timer at its time otherwise; the job counts in documentCacheSizes.sweeps and
// evicts what expired. A pending sweep is not scheduled twice.
func TestTheSweepRunsAsALineJobStartedByAMicrotaskOrATimer(t *testing.T) {
	for _, tc := range []struct {
		name      string
		retention float64
		timer     bool
	}{{"due at once", 0, false}, {"by a timer", 60_000, true}} {
		t.Run(tc.name, func(t *testing.T) {
			retention := tc.retention
			rig := newRetentionRig(&retention)
			var microtasks, jobs []func()
			var timerAt []float64
			var fire func()
			rig.s.Microtask = func(fn func()) { microtasks = append(microtasks, fn) }
			rig.s.EnqueueSweep = func(job func()) { jobs = append(jobs, job) }
			rig.s.ArmTimer = func(at float64, f func()) func() { timerAt = append(timerAt, at); fire = f; return func() {} }
			rig.edit(t)
			if cached, _ := rig.cached(); cached != 1 && tc.retention > 0 {
				t.Fatalf("cached %d after a use, want 1", cached)
			}
			if tc.timer {
				if len(timerAt) != 1 || timerAt[0] != 1000+tc.retention || len(microtasks) != 0 {
					t.Fatalf("timers %v microtasks %d, want one timer at %v", timerAt, len(microtasks), 1000+tc.retention)
				}
				rig.edit(t) // a second use inside the granule schedules nothing more
				if len(timerAt) != 1 {
					t.Fatalf("a pending sweep was scheduled again: %v", timerAt)
				}
				rig.clock += tc.retention
				fire()
			} else {
				if len(microtasks) == 0 || len(timerAt) != 0 {
					t.Fatalf("microtasks %d timers %v, want a microtask and no timer", len(microtasks), timerAt)
				}
				microtasks[0]()
			}
			if len(jobs) != 1 || rig.s.SweepJobs != 1 {
				t.Fatalf("jobs %d, SweepJobs %d, want one", len(jobs), rig.s.SweepJobs)
			}
			jobs[0]()
			if cached, idle := rig.cached(); cached != 0 || idle != 0 {
				t.Fatalf("after the sweep job: cached %d idle %d, want none", cached, idle)
			}
		})
	}
}

// Pi session.ts #pinDocument: a cached incarnation an observer attached to is not idle and never leaves the cache; the release
// unpins it once, and it is idle again after the last one.
func TestAPinnedDocumentStaysCachedUntilTheLastReleaseAndIsIdleAgainAfterIt(t *testing.T) {
	zero := 0.0
	rig := newRetentionRig(&zero)
	rig.s.Microtask = func(fn func()) { fn() }
	rig.s.EnqueueSweep = func(job func()) { job() }
	rig.s.ArmTimer = func(float64, func()) func() { return func() {} }
	rig.edit(t) // retention 0: the document leaves at once
	if cached, _ := rig.cached(); cached != 0 {
		t.Fatalf("cached %d after a use with retention 0", cached)
	}
	if _, err := rig.s.Snapshot(retentionDoc, session.DocArgs{}); err != nil {
		t.Fatal(err)
	}
	rig.edit(t)
	// Load it again and pin it twice.
	rig.s.DocumentRetention = func() *float64 { v := math.Inf(1); return &v }
	if _, err := rig.s.Snapshot(retentionDoc, session.DocArgs{}); err != nil {
		t.Fatal(err)
	}
	first := rig.s.PinDocument(retentionDoc, session.DocArgs{})
	second := rig.s.PinDocument(retentionDoc, session.DocArgs{})
	if first == nil || second == nil {
		t.Fatal("a cached document has no pin")
	}
	if cached, idle := rig.cached(); cached != 1 || idle != 0 || rig.s.DocumentsAttached() != 1 {
		t.Fatalf("pinned: cached %d idle %d attached %d, want 1 0 1", cached, idle, rig.s.DocumentsAttached())
	}
	rig.s.DocumentRetention = func() *float64 { return &zero }
	rig.clock += 1_000_000
	rig.edit(t) // a commit past the retention: the pinned document stays
	if cached, _ := rig.cached(); cached != 1 {
		t.Fatalf("a pinned document left the cache")
	}
	first()
	first() // a second release of the same pin does nothing
	if rig.s.DocumentsAttached() != 1 {
		t.Fatalf("a pin released twice unpinned the other observer")
	}
	second()
	if cached, idle := rig.cached(); rig.s.DocumentsAttached() != 0 || cached != 0 || idle != 0 {
		t.Fatalf("after the last release: cached %d idle %d attached %d, want it evicted (retention 0)", cached, idle, rig.s.DocumentsAttached())
	}
}

// Pi session.ts #touchDocument: a pending sweep for the oldest document stays unless the retention was shortened since; then a use
// of the document moves it up.
func TestAReadAfterTheRetentionWasShortenedMovesThePendingSweepUp(t *testing.T) {
	retention := 600_000.0
	rig := newRetentionRig(&retention)
	var timers []float64
	microtasks := 0
	rig.s.ArmTimer = func(at float64, _ func()) func() { timers = append(timers, at); return func() {} }
	rig.s.Microtask = func(func()) { microtasks++ }
	rig.s.EnqueueSweep = func(func()) {}
	rig.edit(t)
	if len(timers) != 1 || timers[0] != 1000+600_000 {
		t.Fatalf("timers %v, want one for the default retention", timers)
	}
	retention = 0
	rig.clock += 2_000
	if _, err := rig.s.Snapshot(retentionDoc, session.DocArgs{}); err != nil {
		t.Fatal(err)
	}
	if microtasks != 1 {
		t.Fatalf("a read after the retention was cut queued %d sweeps, want the pending one moved up", microtasks)
	}
}
