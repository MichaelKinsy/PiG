package store

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Mine (the lists lane's finding; Pi has no case: its one thread runs a constructor to its end, so no binding changes while
// an open query is made). `NewOpenQuery` watches the sets of its query and compiles it: a binding that changes in between
// runs `recompile` on another goroutine, which wrote the plan under the query's lock while the constructor wrote it with
// none (a data race) and could then be overwritten by the constructor's older compile (a query that stays compiled for a
// binding that is gone). Both compile under one lock now, so the later compile is the one that stays.
// pi: packages/chord/src/store/open-query.ts
func TestOpenQueryABindingChangingWhileTheQueryIsMadeLeavesItCompiledForTheBindingThatStays(t *testing.T) {
	w := newReadsWorld(t)
	bound := Bound{Table: w.tags.Sql.Table, Definition: w.tags.Definition}
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			default:
			}
			w.store.Reads.Unbind(w.set.Name, w.tags)
			if err := w.store.Reads.Bind(w.set.Name, bound, w.tags); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	batch := state.NewBatch(func(state.BatchError) {}, telemetry.NoopTelemetryMetrics)
	spec := QuerySpec{Query: w.tagged, Table: w.notes.Sql.Table, Projection: Projection{}, Owner: w.notes}
	var made []*OpenQuery
	for range 400 {
		made = append(made, NewOpenQuery(ReadSite{Store: w.store, Batch: batch}, spec, ReadRows, func() {}, background))
	}
	close(stop)
	<-stopped
	// The binding that stays: the set is bound, and every query, whenever it was made, is compiled for it.
	w.bind(t)
	for index, query := range made {
		if err := query.Compiled().Err; err != nil {
			t.Fatalf("query %d of %d is compiled for a binding that is gone: %v", index, len(made), err)
		}
		query.Close()
	}
}
