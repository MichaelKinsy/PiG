package host

import (
	"context"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/host/activity.ts. Keep-alive counts (§3, R26): `host.activity`, a JSON watchable of the
// work keeping a host busy, for the app's supervisor. Counters are kept where the work happens; their changes in one
// microtask turn are one publication.

// ActivityCounts is what `host.activity` counts.
type ActivityCounts struct {
	// References is the holds arriving over links and served here.
	References int
	// Calls is the method calls running on this host, local and remote.
	Calls int
	// Jobs is the running jobs (slice 5).
	Jobs int
	// Lingering is the objects kept live by their type's `linger` after their last reference.
	Lingering int
}

// activityKind names a count of ActivityCounts: Pi's `keyof ActivityCounts`.
type activityKind string

const (
	activityReferences activityKind = "references"
	activityCalls      activityKind = "calls"
	activityJobs       activityKind = "jobs"
	activityLingering  activityKind = "lingering"
)

// activity is `host.activity`. A count marks it changed; while it is watched or derived from, the first change of a
// microtask turn schedules one publication, a batch that gives its watches the counts as they are then (`[["r", counts]]`:
// a slow watch gets the newest counts) and marks derives over it. Its value is built from the counters when read or
// published. It is used inside the host's sections.
type activity struct {
	state.Source
	loop   eventLoop
	counts ActivityCounts
	// value is the counts as last read, until the next change.
	value     *delta.JsonObject
	scheduled bool
}

var _ state.SourceImpl = (*activity)(nil)

// newActivity makes the counts of a host whose batch is batch and whose microtasks queue on loop.
func newActivity(loop eventLoop, batch *state.Batch) *activity {
	a := &activity{loop: loop}
	a.Init(batch, a)
	return a
}

// Value is the counts as a JSON object, the same one until the next change.
func (a *activity) Value() (delta.JsonValue, error) {
	if a.value == nil {
		a.value = delta.JsonObjectOf(
			"references", float64(a.counts.References),
			"calls", float64(a.counts.Calls),
			"jobs", float64(a.counts.Jobs),
			"lingering", float64(a.counts.Lingering),
		)
	}
	return a.value, nil
}

// count adds by to the count of kind; published with the turn's other changes. Nothing is scheduled while nobody watches
// or derives from it: a later watch or derive reads the counts as they are then.
func (a *activity) count(kind activityKind, by int) {
	switch kind {
	case activityReferences:
		a.counts.References += by
	case activityCalls:
		a.counts.Calls += by
	case activityJobs:
		a.counts.Jobs += by
	case activityLingering:
		a.counts.Lingering += by
	}
	a.value = nil
	if a.scheduled || (!a.Watched() && a.DependentSet().Len() == 0) {
		return
	}
	a.scheduled = true
	a.loop.Queue(func() {
		a.scheduled = false
		// The adoption returns no error, and Within returns only an adoption's.
		_ = a.Batch().Within(func() error {
			a.Changed()
			return nil
		}, chordctx.Background())
	})
}

// Deliver gives the watches the counts as they are now, as one frame from whatever they had.
func (a *activity) Deliver(ctx context.Context) error {
	value, _ := a.Value()
	a.Publish(value, []delta.Op{{"r", value}}, false, ctx)
	return nil
}
