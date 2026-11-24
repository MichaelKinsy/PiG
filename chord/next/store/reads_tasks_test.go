package store

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/internal/storeseam"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Mine: Pi's `void open.load()` (open-query.ts: a binding that changed reads again and nothing awaits the read) is an owned
// task here (AGENTS.md, TypeScript async/Promise parity: an unawaited promise is an owned background task with cancellation,
// error reporting and shutdown draining). The index owns it: its close cancels the task and joins it; the error of the read is
// the read's failed run (`load` never fails). The idle barrier is what Pi's `await flushed()` after a changed binding waits
// for: a test joins the tasks started so far through `storeseam.ReadTasks`, with no polling.

// pi: packages/chord/src/store/reads.ts
func TestReadsTasks(t *testing.T) {
	t.Run("a task runs under a context its parent's end and the index's close both cancel", func(t *testing.T) {
		for _, byClose := range []bool{false, true} {
			reads := NewReads()
			parent, end := context.WithCancel(background)
			started, cancelled := make(chan struct{}), make(chan struct{})
			reads.Go(parent, func(ctx context.Context) {
				close(started)
				<-ctx.Done()
				close(cancelled)
			})
			<-started
			if byClose {
				reads.Close()
			} else {
				end()
				<-cancelled
			}
			<-reads.idleChan()
			select {
			case <-cancelled:
			default:
				t.Fatalf("by close %v: the task's context was not cancelled", byClose)
			}
			end()
			reads.Close()
		}
	})

	t.Run("its close returns once the tasks ended, not before", func(t *testing.T) {
		reads := NewReads()
		var ended atomic.Int32
		started := make(chan struct{}, 3)
		for range 3 {
			reads.Go(background, func(ctx context.Context) {
				started <- struct{}{}
				<-ctx.Done()
				// A task takes a moment to end: what a missing join would not wait for.
				for range 10 {
					runtime.Gosched()
				}
				ended.Add(1)
			})
		}
		for range 3 {
			<-started
		}
		reads.Close()
		reads.mu.Lock()
		running := reads.tasks
		reads.mu.Unlock()
		if got := ended.Load(); got != 3 || running != 0 {
			t.Fatalf("after close: %d of 3 tasks ended, %d still counted running", got, running)
		}
	})

	t.Run("a task offered after the close does not run", func(t *testing.T) {
		reads := NewReads()
		reads.Close()
		var ran atomic.Bool
		reads.Go(background, func(context.Context) { ran.Store(true) })
		<-reads.idleChan()
		if ran.Load() {
			t.Fatal("a task offered after the close ran")
		}
		// And closing twice is the same close.
		reads.Close()
	})

	t.Run("the barrier is closed when no task runs, and closes when the running ones ended", func(t *testing.T) {
		reads := NewReads()
		select {
		case <-reads.idleChan():
		default:
			t.Fatal("no task runs: the barrier is open")
		}
		release, started := make(chan struct{}), make(chan struct{})
		reads.Go(background, func(context.Context) { close(started); <-release })
		<-started
		barrier := reads.idleChan()
		select {
		case <-barrier:
			t.Fatal("a task runs: the barrier is closed")
		default:
		}
		close(release)
		<-barrier
		reads.Close()
	})
}

// holding is a section of the host a test holds: whoever asks for one waits for it, as for the host's loop.
func holdingBatch() (*state.Batch, *sync.Mutex) {
	var section sync.Mutex
	batch := state.NewBatch(func(state.BatchError) {}, telemetry.NoopTelemetryMetrics)
	return batch.UseSection(func(work func()) { section.Lock(); defer section.Unlock(); work() }, nil), &section
}

// reloading is an open query of the world's tagged notes with one read, whose owner reads again on a rebinding as a live
// query's does: `OpenRead.Reload`.
func (w *readsWorld) reloading(t *testing.T, batch *state.Batch, sink ReadSink) (*OpenQuery, *OpenRead) {
	t.Helper()
	must(t, w.tagged.Err())
	var open *OpenRead
	spec := QuerySpec{Query: w.tagged, Table: w.notes.Sql.Table, Projection: Projection{}, Owner: w.notes}
	query := NewOpenQuery(ReadSite{Store: w.store, Batch: batch}, spec, ReadRows, func() { open.Reload() }, background)
	open = query.Read(ReadKind{Kind: "rows", Range: Range{Limit: 5}}, sink)
	return query, open
}

func TestOpenReadReload(t *testing.T) {
	t.Run("the barrier ends with the reads a changed binding started: the read ran and adopted its run", func(t *testing.T) {
		w := newReadsWorld(t)
		batch, _ := holdingBatch()
		sink := &runLog{}
		query, open := w.reloading(t, batch, sink)
		// No set is bound: a failed run, at once.
		open.Load()
		qEqual(t, []any{sink.len(), sink.last().Kind}, []any{1, "failed"})
		w.bind(t)
		<-storeseam.ReadTasks(w.store)
		qEqual(t, []any{sink.len(), sink.last().Kind}, []any{2, "rows"})
		query.Close()
	})

	t.Run("the index's close cancels a reload held before its adoption, joins it, and nothing is adopted after", func(t *testing.T) {
		w := newReadsWorld(t)
		batch, section := holdingBatch()
		sink := &runLog{}
		query, open := w.reloading(t, batch, sink)
		open.Load()
		// The binding arrives while a section is held: the reload's task reads and waits to adopt in a section.
		section.Lock()
		w.bind(t)
		closed := make(chan struct{})
		go func() {
			w.store.Reads.Close()
			close(closed)
		}()
		// The close began: it cancelled the context of the tasks. The section is free from now on.
		<-w.store.Reads.lifetime.Done()
		section.Unlock()
		<-closed
		w.store.Reads.mu.Lock()
		running := w.store.Reads.tasks
		w.store.Reads.mu.Unlock()
		if running != 0 || sink.len() != 1 {
			t.Fatalf("after the close: %d tasks counted running, %d runs adopted (the failed one only is expected)", running, sink.len())
		}
		// A binding that changes now reads nothing.
		open.Reload()
		<-storeseam.ReadTasks(w.store)
		if sink.len() != 1 {
			t.Fatalf("a reload after the close adopted %d runs", sink.len()-1)
		}
		query.Close()
	})

	t.Run("the store's close closes the index: no reload starts after it", func(t *testing.T) {
		w := newReadsWorld(t)
		batch, _ := holdingBatch()
		sink := &runLog{}
		query, open := w.reloading(t, batch, sink)
		open.Load()
		must(t, w.store.Close(background))
		w.bind(t)
		<-storeseam.ReadTasks(w.store)
		if sink.len() != 1 {
			t.Fatalf("a reload after the store's close adopted %d runs", sink.len()-1)
		}
		query.Close()
	})
}
