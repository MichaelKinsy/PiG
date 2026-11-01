package session_test

// Ports packages/durable/test/session-group-commit.test.ts: group commit on the Session line (docs/group-commit.md):
// batching, gates, outcome fences, publication, failures per part, upkeep through the writer, backpressure, bounds, and a
// randomized interleaving against an oracle. The cases that need Go to change what a transaction adopts, or what the read
// caches do at publication, are in session_group_commit_internal_test.go.
//
// Not ported: the two `${value}` cases that reject with `undefined` and `null` (a Go error is never nil, so a rejection value
// of no value does not exist; "rejects every part of a call with its rejection value" and "rejects a refused part with its
// refusal" are ported with an error value in the cases below).

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

func TestGroupCommitBatching(t *testing.T) {
	t.Run("stores the commits admitted while a call is in flight in one call, each with its seq, published in order", func(t *testing.T) {
		g := openGC(t, 3)
		a, b, c := g.ids[0], g.ids[1], g.ids[2]
		held := g.storage.hold()
		var log order
		first := g.bumpNamed(&log, "a", a)
		<-held.Entered()
		ran := make(chan string, 2)
		second := g.commitNamed(&log, "b", func(tx durable.Tx) (any, error) {
			if _, err := bumpChange(b)(tx); err != nil {
				return nil, err
			}
			ran <- "b"
			return nil, nil
		})
		third := g.commitNamed(&log, "c", func(tx durable.Tx) (any, error) {
			if _, err := bumpChange(c)(tx); err != nil {
				return nil, err
			}
			ran <- "c"
			return nil, nil
		})
		// Their callbacks run while the first commit is in flight: they touch nothing it writes.
		<-ran
		<-ran
		quiesce(second, third)
		g.awaitQueued(2)
		if g.published.len() != 0 {
			t.Fatalf("%d commits published while the first call is in flight", g.published.len())
		}
		held.Release()
		for _, started := range []*call{first, second, third} {
			if _, err := started.wait(); err != nil {
				t.Fatal(err)
			}
		}
		expectNames(t, log.snapshot(), "a", "b", "c")
		if sizes := g.storage.callSizes(2); !slices.Equal(sizes, []int{1, 2}) {
			t.Fatalf("the last two calls had %v batches, want [1 2]", sizes)
		}
		var seqs []durable.Seq
		for _, commit := range g.published.all() {
			seqs = append(seqs, commit.Seq)
		}
		if len(seqs) != 3 || !slices.IsSorted(seqs) || seqs[0] == seqs[1] || seqs[1] == seqs[2] {
			t.Fatalf("published seqs %v, want three distinct ones in order", seqs)
		}
		g.close()
	})
}

func TestGroupCommitGates(t *testing.T) {
	t.Run("makes a read of an entry ID leaked by an unpublished commit wait for its publication", func(t *testing.T) {
		g := openGC(t, 1)
		held := g.storage.hold()
		var leaked atomic.Int64
		appending := g.commitAsync(func(tx durable.Tx) (any, error) {
			entry, err := tx.AppendEntry(g.ids[0], durable.EntryDraft{Kind: "note"})
			leaked.Store(int64(entry.Id))
			return nil, err
		})
		<-held.Entered()
		reading := g.commitAsync(func(tx durable.Tx) (any, error) { return tx.Entry(durable.EntryId(leaked.Load())) })
		if reading.settled() {
			t.Fatal("the read of a leaked entry ID settled before the commit that wrote it was published")
		}
		held.Release()
		if _, err := appending.wait(); err != nil {
			t.Fatal(err)
		}
		value, err := reading.wait()
		must(t, err)
		if entry, _ := value.(*durable.EntryRecord); entry == nil || int64(entry.Id) != leaked.Load() {
			t.Fatalf("read %v, want entry %d", value, leaked.Load())
		}
		g.close()
	})

	t.Run("makes a job wait for an unpublished creation at a document address instead of creating it again", func(t *testing.T) {
		g := openGC(t, 0)
		held := g.storage.hold()
		first := g.commitAsync(func(tx durable.Tx) (any, error) {
			shared, err := tx.Doc(gcShared)
			if err != nil {
				return nil, err
			}
			return nil, shared.Set("n", num(shared.Get("n"))+1)
		})
		<-held.Entered()
		second := g.commitAsync(func(tx durable.Tx) (any, error) {
			shared, err := tx.Doc(gcShared)
			if err != nil {
				return nil, err
			}
			n := num(shared.Get("n")) + 1
			return n, shared.Set("n", n)
		})
		if second.settled() {
			t.Fatal("the second creation settled before the first was published")
		}
		held.Release()
		_, err := first.wait()
		must(t, err)
		value, err := second.wait()
		must(t, err)
		if value != 2.0 {
			t.Fatalf("second read %v, want 2", value)
		}
		creations := 0
		g.storage.mu.Lock()
		for _, call := range g.storage.calls {
			for _, batch := range call {
				for _, write := range batch {
					if create, ok := write.(durable.DocumentCreateWrite); ok && create.Record.Kind == "gc.shared" {
						creations++
					}
				}
			}
		}
		g.storage.mu.Unlock()
		if creations != 1 {
			t.Fatalf("%d creations of the document, want 1", creations)
		}
		g.close()
	})

	t.Run("makes a write to a conversation an unpublished commit creates wait for it", func(t *testing.T) {
		g := openGC(t, 0)
		held := g.storage.hold()
		var leaked atomic.Int64
		creating := g.commitAsync(func(tx durable.Tx) (any, error) {
			record, err := tx.CreateConversation(ownerless())
			leaked.Store(int64(record.Id))
			return nil, err
		})
		<-held.Entered()
		appending := g.commitAsync(func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(durable.ConversationId(leaked.Load()), durable.EntryDraft{Kind: "note"})
		})
		if appending.settled() {
			t.Fatal("the append settled before the conversation it writes to was published")
		}
		held.Release()
		_, err := creating.wait()
		must(t, err)
		value, err := appending.wait()
		must(t, err)
		if entry, _ := value.(durable.EntryRecord); int64(entry.ConversationId) != leaked.Load() {
			t.Fatalf("appended to conversation %d, want %d", entry.ConversationId, leaked.Load())
		}
		g.close()
	})

	t.Run("makes each read of the entries of a conversation an unpublished commit creates wait for its creation", func(t *testing.T) {
		// The Session's own helpers (newest entry, head marker), a transaction's scan, and the line's reads, each alone behind
		// the creation, of a conversation Storage does not have yet.
		reads := []struct {
			name string
			read func(g *gc, id durable.ConversationId) *call
		}{
			{"latestEntryId", func(g *gc, id durable.ConversationId) *call {
				return g.commitAsync(func(tx durable.Tx) (any, error) { return tx.LatestEntryId(id) })
			}},
			{"latestHeadMarker", func(g *gc, id durable.ConversationId) *call {
				return g.commitAsync(func(tx durable.Tx) (any, error) { return tx.LatestHeadMarker(id) })
			}},
			{"tx.scanEntries", func(g *gc, id durable.ConversationId) *call {
				return g.commitAsync(func(tx durable.Tx) (any, error) {
					page, err := tx.ScanEntries(durable.EntryQuery{ConversationId: id}, 1, nil)
					return page.Items, err
				})
			}},
			{"line scanEntries", func(g *gc, id durable.ConversationId) *call {
				return g.readAsync(func(line durable.Storage) (any, error) {
					page, err := line.ScanEntries(ctx, durable.EntryQuery{ConversationId: id}, 1, nil)
					return page.Items, err
				})
			}},
			{"line findLatestHeadMarker", func(g *gc, id durable.ConversationId) *call {
				return g.readAsync(func(line durable.Storage) (any, error) { return line.FindLatestHeadMarker(ctx, id, nil) })
			}},
		}
		for _, read := range reads {
			t.Run(read.name, func(t *testing.T) {
				g := openGC(t, 0)
				held := g.storage.hold()
				var leaked atomic.Int64
				creating := g.commitAsync(func(tx durable.Tx) (any, error) {
					record, err := tx.CreateConversation(ownerless())
					leaked.Store(int64(record.Id))
					return nil, err
				})
				<-held.Entered()
				reading := read.read(g, durable.ConversationId(leaked.Load()))
				quiesce(reading)
				held.Release()
				_, err := creating.wait()
				must(t, err)
				value, err := reading.wait()
				must(t, err)
				switch typed := value.(type) {
				case []durable.EntryRecord:
					if len(typed) != 0 {
						t.Fatalf("read %v, want no entries", typed)
					}
				case *durable.EntryRecord:
					if typed != nil {
						t.Fatalf("read %v, want no marker", typed)
					}
				case *durable.EntryId:
					if typed != nil {
						t.Fatalf("read %v, want no entry", *typed)
					}
				default:
					t.Fatalf("read %T %v", value, value)
				}
				g.close()
			})
		}
	})

	t.Run("makes a scan that cannot be keyed wait for every unpublished commit", func(t *testing.T) {
		g := openGC(t, 0)
		held := g.storage.hold()
		creating := g.commitAsync(func(tx durable.Tx) (any, error) {
			record, err := tx.CreateConversation(ownerless())
			return record.Id, err
		})
		<-held.Entered()
		scanning := g.commitAsync(func(tx durable.Tx) (any, error) {
			page, err := tx.ScanConversations(durable.ConversationQuery{}, 10, nil)
			var ids []durable.ConversationId
			for _, conversation := range page.Items {
				ids = append(ids, conversation.Id)
			}
			return ids, err
		})
		if scanning.settled() {
			t.Fatal("the scan settled before the unpublished creation was published")
		}
		held.Release()
		created, err := creating.wait()
		must(t, err)
		scanned, err := scanning.wait()
		must(t, err)
		if !slices.Contains(scanned.([]durable.ConversationId), created.(durable.ConversationId)) {
			t.Fatalf("the scan %v does not contain the created conversation %v", scanned, created)
		}
		g.close()
	})

	t.Run("makes a line read after an un-awaited commit see it", func(t *testing.T) {
		g := openGC(t, 1)
		held := g.storage.hold()
		var leaked atomic.Int64
		g.commitAsync(func(tx durable.Tx) (any, error) {
			entry, err := tx.AppendEntry(g.ids[0], durable.EntryDraft{Kind: "note"})
			leaked.Store(int64(entry.Id))
			return nil, err
		})
		<-held.Entered()
		reading := g.readAsync(func(line durable.Storage) (any, error) { return line.Entry(ctx, durable.EntryId(leaked.Load())) })
		quiesce(reading)
		held.Release()
		value, err := reading.wait()
		must(t, err)
		if at, _ := value.(*durable.EntryAt); at == nil || int64(at.Entry.Id) != leaked.Load() {
			t.Fatalf("line read %v, want entry %d", value, leaked.Load())
		}
		g.close()
	})

	t.Run("makes a reader of a document whose held changes an unpublished commit stores wait, and store them once", func(t *testing.T) {
		g := openGC(t, 1)
		id := g.ids[0]
		mustBump(t, g.bump(id))
		mustBump(t, g.bump(id, deferredForever))
		// The first commit only reads the held document: storing its held changes is its write of it.
		held := g.storage.hold()
		storing := g.commitAsync(func(tx durable.Tx) (any, error) {
			if _, err := tx.Doc(gcNote, id); err != nil {
				return nil, err
			}
			return tx.AppendEntry(id, durable.EntryDraft{Kind: "a"})
		})
		<-held.Entered()
		var read atomic.Bool
		reading := g.commitAsync(func(tx durable.Tx) (any, error) {
			if _, err := tx.Doc(gcNote, id); err != nil {
				return nil, err
			}
			read.Store(true)
			return tx.AppendEntry(id, durable.EntryDraft{Kind: "b"})
		})
		quiesce(reading)
		if read.Load() {
			t.Fatal("the reader read the document while the commit that stores its held changes was unpublished")
		}
		held.Release()
		mustBump(t, storing)
		mustBump(t, reading)
		var changes []int
		g.storage.mu.Lock()
		for _, call := range g.storage.calls[len(g.storage.calls)-2:] {
			count := 0
			for _, batch := range call {
				count += len(writesOfType(batch, "document.change"))
			}
			changes = append(changes, count)
		}
		g.storage.mu.Unlock()
		if !slices.Equal(changes, []int{1, 0}) {
			t.Fatalf("document.change writes per call %v, want [1 0]", changes)
		}
		record, err := g.storage.FindDocument(ctx, durable.DocumentAddress{Kind: "gc.note", Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: id}}, durable.CurrentPoint)
		must(t, err)
		stored, err := g.storage.Document(ctx, record.Id, durable.CurrentPoint)
		must(t, err)
		if stored.DeltasSinceBase != 1 {
			t.Fatalf("deltasSinceBase %d, want 1", stored.DeltasSinceBase)
		}
		g.close()
	})

	t.Run("makes a reader of a document held by a deferred commit behind a call wait for it, and store it", func(t *testing.T) {
		g := openGC(t, 2)
		a, b := g.ids[0], g.ids[1]
		mustBump(t, g.bump(a))
		held := g.storage.hold()
		first := g.bump(b)
		<-held.Entered()
		deferredCommit := g.commitAsync(func(tx durable.Tx) (any, error) {
			note, err := tx.Doc(gcNote, a)
			if err != nil {
				return nil, err
			}
			return nil, note.Set("n", 5.0)
		}, deferredForever)
		reader := g.bump(a)
		quiesce(deferredCommit, reader)
		// Only the deferred commit is admitted: the reader waits at the gate of the key it holds, with the line.
		g.awaitQueued(1)
		// Nothing is published while the first commit is in flight, the deferred one neither.
		if g.published.len() != 1 {
			t.Fatalf("%d commits published while the first call is in flight, want the 1 before it", g.published.len())
		}
		held.Release()
		mustBump(t, first)
		mustBump(t, deferredCommit)
		if value := mustBump(t, reader); value != 5.0 {
			t.Fatalf("the reader read %v, want 5", value)
		}
		// The reader's own batch stored what the deferred commit held.
		stored := 0
		for _, batch := range g.storage.lastCall() {
			stored += len(writesOfType(batch, "document.change"))
		}
		if stored != 1 {
			t.Fatalf("the last call stored %d document changes, want 1", stored)
		}
		g.close()
		if notes := g.notes(a, b); !slices.Equal(notes, []float64{6, 1}) {
			t.Fatalf("notes %v, want [6 1]", notes)
		}
	})
}

// mustBump waits for a call that must succeed and returns its value as a number.
func mustBump(t *testing.T, started *call) float64 {
	t.Helper()
	value, err := started.wait()
	if err != nil {
		t.Fatal(err)
	}
	number, _ := value.(float64)
	return number
}

func TestGroupCommitOutcomes(t *testing.T) {
	t.Run("delivers a read, a commit without writes, a callback error, and a deferred commit after an earlier commit", func(t *testing.T) {
		g := openGC(t, 2)
		a, b := g.ids[0], g.ids[1]
		held := g.storage.hold()
		var log order
		first := g.bumpNamed(&log, "first", a)
		<-held.Entered()
		_ = first
		calls := []*call{
			g.readNamed(&log, "read", func(durable.Storage) (any, error) { return "read", nil }),
			g.commitNamed(&log, "none", func(durable.Tx) (any, error) { return "none", nil }),
			g.commitNamed(&log, "error", func(durable.Tx) (any, error) { return nil, errors.New("own") }),
			g.bumpNamed(&log, "deferred", b, deferredForever),
		}
		quiesce(calls...)
		g.awaitQueued(1)
		expectNames(t, log.snapshot())
		held.Release()
		for _, started := range calls {
			_, _ = started.wait()
		}
		// A callback's own error affects only its job.
		expectNames(t, log.snapshot(), "first", "read", "none", "error!", "deferred")
		g.close()
	})

	t.Run("makes every outcome after a fatal failure SessionFailed, and keeps the earlier ones", func(t *testing.T) {
		g := openGC(t, 4)
		a, b, c, d := g.ids[0], g.ids[1], g.ids[2], g.ids[3]
		held := g.storage.hold()
		first := g.bump(a)
		<-held.Entered()
		stored := g.bump(b)
		refused := g.commitAsync(appendChange(c, "refuse"))
		after := g.bump(d)
		read := g.readAsync(func(durable.Storage) (any, error) { return "read", nil })
		g.storage.setRefuse(func(writes []durable.StorageWrite) bool {
			for _, write := range writes {
				if entry, ok := write.(durable.EntryWrite); ok && entry.Value.Kind == "refuse" {
					return true
				}
			}
			return false
		})
		// Every callback has run, so the three are admitted while the first call is in flight.
		quiesce(stored, refused, after, read)
		g.awaitQueued(3)
		held.Release()
		if value := mustBump(t, first); value != 0 {
			t.Fatalf("first read %v, want 0", value)
		}
		if value := mustBump(t, stored); value != 0 {
			t.Fatalf("stored read %v, want 0", value)
		}
		_, err := refused.wait()
		expectErrorContains(t, err, "refused")
		if _, failed := isSessionFailed(err); failed {
			t.Fatalf("the refused commit got SessionFailed %v, want its refusal", err)
		}
		_, err = after.wait()
		if _, failed := isSessionFailed(err); !failed {
			t.Fatalf("the commit after the refused one got %v, want SessionFailed", err)
		}
		_, err = read.wait()
		if _, failed := isSessionFailed(err); !failed {
			t.Fatalf("the read after the refused commit got %v, want SessionFailed", err)
		}
		end := g.closed()
		if end.Kind != durable.SessionEndFailed || end.Error == nil || end.Error.Error() != "refused" {
			t.Fatalf("session ended %+v, want failed with the refusal", end)
		}
		// One call stored the first two of the three batches it got; the refused one and the one after it are not stored.
		if sizes := g.storage.callSizes(1); !slices.Equal(sizes, []int{3}) {
			t.Fatalf("the last call had %v batches, want [3]", sizes)
		}
		if notes := g.notes(a, b, d); !slices.Equal(notes, []float64{1, 1, 0}) {
			t.Fatalf("notes %v, want [1 1 0]", notes)
		}
	})
}

func TestGroupCommitFailuresPerPart(t *testing.T) {
	t.Run("publishes the parts received before a call rejects, then rejects them with its error", func(t *testing.T) {
		g := openGC(t, 3)
		a, b, c := g.ids[0], g.ids[1], g.ids[2]
		held := g.storage.hold()
		first := g.bump(a)
		<-held.Entered()
		second := g.bump(b)
		third := g.bump(c)
		g.storage.setRejectCall(g.storage.callCount())
		quiesce(second, third)
		g.awaitQueued(2)
		held.Release()
		mustBump(t, first)
		for _, started := range []*call{second, third} {
			_, err := started.wait()
			expectErrorContains(t, err, "connection lost")
		}
		// Durable, so published.
		if g.published.len() != 3 {
			t.Fatalf("%d commits published, want 3", g.published.len())
		}
		end := g.closed()
		if end.Kind != durable.SessionEndFailed || end.Error == nil || end.Error.Error() != "connection lost" {
			t.Fatalf("session ended %+v, want failed with the storage error", end)
		}
		if notes := g.notes(a, b, c); !slices.Equal(notes, []float64{1, 1, 1}) {
			t.Fatalf("notes %v, want [1 1 1]", notes)
		}
	})

	t.Run("fails every job after the first part of a call that rejects, except its other parts, with their storage error", func(t *testing.T) {
		g := openGC(t, 1)
		id := g.ids[0]
		held := g.storage.hold()
		first := g.commitAsync(appendChange(id, "a"))
		<-held.Entered()
		// Between the two parts of the failing call, a job without writes.
		before := g.commitAsync(appendChange(id, "b"))
		between := g.commitAsync(func(durable.Tx) (any, error) { return "no write", nil })
		after := g.commitAsync(appendChange(id, "d"))
		quiesce(before, between, after)
		g.awaitQueued(2)
		g.storage.setRejectCall(g.storage.callCount())
		held.Release()
		if _, err := first.wait(); err != nil {
			t.Fatal(err)
		}
		_, err := before.wait()
		expectErrorContains(t, err, "connection lost")
		_, err = between.wait()
		if _, failed := isSessionFailed(err); !failed {
			t.Fatalf("the job without writes got %v, want SessionFailed", err)
		}
		_, err = after.wait()
		expectErrorContains(t, err, "connection lost")
		// Both parts were received, so they were published before they rejected.
		if sizes := g.storage.callSizes(1); !slices.Equal(sizes, []int{2}) {
			t.Fatalf("the last call had %v batches, want [2]", sizes)
		}
		if g.published.len() != 3 {
			t.Fatalf("%d commits published, want 3", g.published.len())
		}
		end := g.closed()
		if end.Kind != durable.SessionEndFailed || end.Error == nil || end.Error.Error() != "connection lost" {
			t.Fatalf("session ended %+v, want failed with the storage error", end)
		}
	})

	t.Run("reports SessionFailed with the first error for a call that rejects after another failure failed the Session", func(t *testing.T) {
		g := openGC(t, 1)
		id := g.ids[0]
		held := g.storage.hold()
		inFlight := g.commitAsync(appendChange(id, "a"))
		<-held.Entered()
		// A later job's read fails first, and fails the Session; then the call in flight rejects with another error.
		first := errors.New("first failure: read")
		g.storage.mu.Lock()
		g.storage.taskFailure = first
		g.storage.mu.Unlock()
		reading := g.commitAsync(func(tx durable.Tx) (any, error) { return tx.Task(999) })
		quiesce(reading)
		if failure := g.session.Failure(); failure != first {
			t.Fatalf("session failure %v, want %v", failure, first)
		}
		g.storage.setRejectCall(g.storage.callCount() - 1)
		held.Release()
		_, err := reading.wait()
		if err != first {
			t.Fatalf("the failing read got %v, want its own error", err)
		}
		_, err = inFlight.wait()
		cause, failed := isSessionFailed(err)
		if !failed || cause != first {
			t.Fatalf("the call in flight got %v, want SessionFailed caused by the first failure", err)
		}
		// Nothing of the call is published once the Session failed.
		if g.published.len() != 0 {
			t.Fatalf("%d commits published, want none", g.published.len())
		}
		end := g.closed()
		if end.Kind != durable.SessionEndFailed || end.Error != first {
			t.Fatalf("session ended %+v, want failed with the first error", end)
		}
	})

	t.Run("rejects every part of a call with its rejection value", func(t *testing.T) {
		g := openGC(t, 3)
		a, b, c := g.ids[0], g.ids[1], g.ids[2]
		held := g.storage.hold()
		first := g.bump(a)
		<-held.Entered()
		parts := []*call{g.bump(b), g.bump(c)}
		quiesce(parts...)
		g.awaitQueued(2)
		rejection := errors.New("rejection value")
		g.storage.mu.Lock()
		g.storage.rejection = rejection
		g.storage.mu.Unlock()
		g.storage.setRejectCall(g.storage.callCount())
		held.Release()
		mustBump(t, first)
		for _, part := range parts {
			if _, err := part.wait(); err != rejection {
				t.Fatalf("part rejected with %v, want %v", err, rejection)
			}
		}
		if end := g.closed(); end.Error != rejection {
			t.Fatalf("session ended %+v, want failed with %v", end, rejection)
		}
	})

	t.Run("rejects a refused part with its refusal, and the parts after it with SessionFailed", func(t *testing.T) {
		g := openGC(t, 3)
		a, b, c := g.ids[0], g.ids[1], g.ids[2]
		held := g.storage.hold()
		first := g.bump(a)
		<-held.Entered()
		refused := g.commitAsync(appendChange(b, "refuse"))
		after := g.bump(c)
		quiesce(refused, after)
		g.awaitQueued(2)
		refusal := errors.New("refusal value")
		g.storage.mu.Lock()
		g.storage.refusal = refusal
		g.storage.mu.Unlock()
		g.storage.setRefuse(func(writes []durable.StorageWrite) bool {
			for _, write := range writes {
				if entry, ok := write.(durable.EntryWrite); ok && entry.Value.Kind == "refuse" {
					return true
				}
			}
			return false
		})
		held.Release()
		mustBump(t, first)
		if _, err := refused.wait(); err != refusal {
			t.Fatalf("refused part rejected with %v, want %v", err, refusal)
		}
		_, err := after.wait()
		if _, failed := isSessionFailed(err); !failed {
			t.Fatalf("the part after the refused one got %v, want SessionFailed", err)
		}
		if end := g.closed(); end.Error != refusal {
			t.Fatalf("session ended %+v, want failed with %v", end, refusal)
		}
	})
}

func TestGroupCommitDeferredCommits(t *testing.T) {
	t.Run("publishes a deferred commit only once the commit before it is published", func(t *testing.T) {
		g := openGC(t, 2)
		a, b := g.ids[0], g.ids[1]
		// Only a change of an existing document is held.
		mustBump(t, g.bump(b))
		g.published.reset()
		held := g.storage.hold()
		first := g.bump(a)
		<-held.Entered()
		later := g.bump(b, deferredForever)
		quiesce(later)
		g.awaitQueued(1)
		if g.published.len() != 0 {
			t.Fatalf("%d commits published while the call is in flight", g.published.len())
		}
		held.Release()
		mustBump(t, first)
		mustBump(t, later)
		expectNames(t, g.published.kinds(), "stored", "memory")
		g.close()
	})

	t.Run("never publishes a deferred commit behind a commit that fails", func(t *testing.T) {
		g := openGC(t, 2)
		a, b := g.ids[0], g.ids[1]
		mustBump(t, g.bump(b))
		g.published.reset()
		held := g.storage.hold()
		first := g.commitAsync(appendChange(a, "refuse"))
		<-held.Entered()
		later := g.bump(b, deferredForever)
		quiesce(later)
		g.awaitQueued(1)
		g.storage.setRefuse(func(writes []durable.StorageWrite) bool {
			for _, write := range writes {
				if entry, ok := write.(durable.EntryWrite); ok && entry.Value.Kind == "refuse" {
					return true
				}
			}
			return false
		})
		held.Release()
		_, err := first.wait()
		expectErrorContains(t, err, "refused")
		_, err = later.wait()
		if _, failed := isSessionFailed(err); !failed {
			t.Fatalf("the deferred commit got %v, want SessionFailed", err)
		}
		if g.published.len() != 0 {
			t.Fatalf("%d commits published, want none", g.published.len())
		}
	})
}

func TestGroupCommitPublication(t *testing.T) {
	t.Run("publishes the parts of one call a macrotask apart: what one resolves runs before the next is published", func(t *testing.T) {
		g := openGC(t, 3)
		a, b, c := g.ids[0], g.ids[1], g.ids[2]
		held := g.storage.hold()
		first := g.bump(a)
		<-held.Entered()
		var seenAtSecond atomic.Int64
		seenAtSecond.Store(-1)
		ticket := g.session.TakeLineTicket()
		second := spawn(func() (any, error) {
			value, err := g.session.CommitWithTicket(ticket, ctx, func(tx *session.Transaction) (any, error) { return bumpChange(b)(tx) }, session.TransactionScope{})
			// What the commit's continuation sees: the third part is not published yet.
			seenAtSecond.Store(int64(g.published.len()))
			return value, err
		})
		third := g.bump(c)
		quiesce(second, third)
		g.awaitQueued(2)
		held.Release()
		mustBump(t, first)
		mustBump(t, second)
		mustBump(t, third)
		if sizes := g.storage.callSizes(1); !slices.Equal(sizes, []int{2}) {
			t.Fatalf("the last call had %v batches, want [2]", sizes)
		}
		if seen := seenAtSecond.Load(); seen != 2 {
			t.Fatalf("the continuation of the second commit saw %d published, want 2", seen)
		}
		if g.published.len() != 3 {
			t.Fatalf("%d commits published, want 3", g.published.len())
		}
		g.close()
	})
}

// scopeBox is the publication scope a test sets on a scopedStorage after the Session opened; none runs the publication at once.
type scopeBox struct {
	mu sync.Mutex
	fn func(ctx ctxT, publish func())
}

func (box *scopeBox) set(fn func(ctx ctxT, publish func())) {
	box.mu.Lock()
	box.fn = fn
	box.mu.Unlock()
}

func (box *scopeBox) run(callCtx ctxT, publish func()) {
	box.mu.Lock()
	fn := box.fn
	box.mu.Unlock()
	if fn == nil {
		publish()
		return
	}
	fn(callCtx, publish)
}

func openScoped(t *testing.T, conversations int, hooks session.Hooks) (*gc, *scopeBox) {
	t.Helper()
	box := &scopeBox{}
	storage := newBatchStorage()
	g := openGCHooks(t, storage, conversations, func(storage *batchStorage) durable.Storage {
		return scopedStorage{batchStorage: storage, scope: box.run}
	}, hooks)
	return g, box
}

func TestGroupCommitTheStoragesPublicationScope(t *testing.T) {
	t.Run("publishes each commit inside the storage's scope, the next only once one it runs later ran", func(t *testing.T) {
		g, box := openScoped(t, 3, session.Hooks{})
		a, b, c := g.ids[0], g.ids[1], g.ids[2]
		var (
			mu     sync.Mutex
			scopes []string
			depth  int
			inside []bool
		)
		// Runs each publication on another goroutine, inside a scope of its own.
		box.set(func(_ ctxT, publish func()) {
			go func() {
				mu.Lock()
				depth++
				scopes = append(scopes, fmt.Sprintf("open %d", g.published.len()))
				mu.Unlock()
				publish()
				mu.Lock()
				scopes = append(scopes, fmt.Sprintf("close %d", g.published.len()))
				depth--
				mu.Unlock()
			}()
		})
		g.session.FollowCommits(func(_ ctxT, _ session.ObservedCommit) {
			mu.Lock()
			inside = append(inside, depth == 1)
			mu.Unlock()
		})
		held := g.storage.hold()
		first := g.bump(a)
		<-held.Entered()
		rest := []*call{g.bump(b), g.bump(c)}
		quiesce(rest...)
		g.awaitQueued(2)
		mu.Lock()
		inside = nil
		mu.Unlock()
		held.Release()
		mustBump(t, first)
		for _, part := range rest {
			mustBump(t, part)
		}
		mu.Lock()
		defer mu.Unlock()
		if !slices.Equal(inside, []bool{true, true, true}) {
			t.Fatalf("own observers inside the scope %v, want all", inside)
		}
		expectNames(t, scopes, "open 0", "close 1", "open 1", "close 2", "open 2", "close 3")
	})

	t.Run("reports a scope that throws instead of running the publication, and publishes the commit", func(t *testing.T) {
		var (
			mu      sync.Mutex
			reports []error
		)
		g, box := openScoped(t, 1, session.Hooks{Report: func(_ ctxT, err error, _ durable.ReportInfo) {
			mu.Lock()
			reports = append(reports, err)
			mu.Unlock()
		}})
		mu.Lock()
		reports = nil
		mu.Unlock()
		noScope := errors.New("no scope")
		box.set(func(ctxT, func()) { panic(noScope) })
		if value := mustBump(t, g.bump(g.ids[0])); value != 0 {
			t.Fatalf("read %v, want 0", value)
		}
		if g.published.len() != 1 {
			t.Fatalf("%d commits published, want 1", g.published.len())
		}
		mu.Lock()
		got := slices.Clone(reports)
		mu.Unlock()
		if len(got) != 1 || got[0] != noScope {
			t.Fatalf("reports %v, want the scope's error once", got)
		}
		g.close()
	})
}

func TestGroupCommitAFailureBetweenTwoPublications(t *testing.T) {
	t.Run("publishes no part of a call after a failure that a continuation of an earlier part caused", func(t *testing.T) {
		g := openGC(t, 1)
		id := g.ids[0]
		held := g.storage.hold()
		lead := g.commitAsync(appendChange(id, "lead"))
		<-held.Entered()
		// What `one` resolves runs in the hop before `two` is published: a read there fails the Session.
		read := errors.New("read failed between publications")
		g.storage.mu.Lock()
		g.storage.taskFailure = read
		g.storage.mu.Unlock()
		ticket := g.session.TakeLineTicket()
		one := spawn(func() (any, error) {
			value, err := g.session.CommitWithTicket(ticket, ctx, func(tx *session.Transaction) (any, error) { return appendChange(id, "one")(tx) }, session.TransactionScope{})
			if err == nil {
				_, err = g.session.CommitWith(ctx, func(tx *session.Transaction) (any, error) { return tx.Task(999) }, session.TransactionScope{})
				if !errors.Is(err, read) {
					return value, fmt.Errorf("the continuation's read got %w, want %w", err, read)
				}
				err = nil
			}
			return value, err
		})
		two := g.commitAsync(appendChange(id, "two"))
		quiesce(one, two)
		g.awaitQueued(2)
		held.Release()
		_, _ = lead.wait()
		_, err := one.wait()
		must(t, err)
		_, err = two.wait()
		cause, failed := isSessionFailed(err)
		if !failed || cause != read {
			t.Fatalf("the part after the failure got %v, want SessionFailed caused by %v", err, read)
		}
		expectNames(t, g.published.entryKinds(), "lead", "one")
	})

	t.Run("publishes no part of a call after the Session fails in a continuation of an earlier part", func(t *testing.T) {
		// Sol's: `one`'s continuation fails the Session itself.
		g := openGC(t, 1)
		id := g.ids[0]
		held := g.storage.hold()
		lead := g.commitAsync(appendChange(id, "lead"))
		<-held.Entered()
		failure := errors.New("failed between publications")
		ticket := g.session.TakeLineTicket()
		one := spawn(func() (any, error) {
			value, err := g.session.CommitWithTicket(ticket, ctx, func(tx *session.Transaction) (any, error) { return appendChange(id, "one")(tx) }, session.TransactionScope{})
			g.session.Fail(ctx, failure)
			return value, err
		})
		two := g.commitAsync(appendChange(id, "two"))
		quiesce(one, two)
		g.awaitQueued(2)
		held.Release()
		_, _ = lead.wait()
		_, _ = one.wait()
		_, err := two.wait()
		if _, failed := isSessionFailed(err); !failed {
			t.Fatalf("the part after the failure got %v, want SessionFailed", err)
		}
		expectNames(t, g.published.entryKinds(), "lead", "one")
	})

	t.Run("checks the failure when a storage's scope runs a publication it deferred, not when it was asked to", func(t *testing.T) {
		g, box := openScoped(t, 1, session.Hooks{})
		id := g.ids[0]
		failure := errors.New("failed before a deferred publication ran")
		var calls atomic.Int32
		// The second publication's scope is entered before the failure; it runs the publication only after it.
		box.set(func(_ ctxT, publish func()) {
			if calls.Add(1) == 2 {
				g.session.Fail(ctx, failure)
			}
			go publish()
		})
		held := g.storage.hold()
		lead := g.commitAsync(appendChange(id, "lead"))
		<-held.Entered()
		one := g.commitAsync(appendChange(id, "one"))
		quiesce(one)
		g.awaitQueued(1)
		held.Release()
		_, _ = lead.wait()
		_, err := one.wait()
		if _, failed := isSessionFailed(err); !failed {
			t.Fatalf("the second part got %v, want SessionFailed", err)
		}
		expectNames(t, g.published.entryKinds(), "lead")
	})
}

func TestGroupCommitUpkeepThroughTheWriter(t *testing.T) {
	t.Run("stores held changes for an unload after the call in flight", func(t *testing.T) {
		g := openGC(t, 2)
		a, b := g.ids[0], g.ids[1]
		mustBump(t, g.bump(a))
		mustBump(t, g.bump(b, deferredForever))
		held := g.storage.hold()
		first := g.bump(a)
		<-held.Entered()
		unloading := spawn(func() (any, error) { return nil, g.session.UnloadDocuments() })
		if unloading.settled() {
			t.Fatal("the unload settled while a call was in flight")
		}
		held.Release()
		mustBump(t, first)
		if _, err := unloading.wait(); err != nil {
			t.Fatal(err)
		}
		last := g.storage.lastCall()
		if len(last) != 1 || len(last[0]) != 1 || durableWriteType(last[0][0]) != "document.change" {
			t.Fatalf("the last call %v, want one document.change", last)
		}
		value, err := g.session.SnapshotErased(ctx, gcNote, b)
		must(t, err)
		if num(value.Value("n")) != 1 {
			t.Fatalf("note %v, want 1", value)
		}
		g.close()
		if notes := g.notes(a, b); !slices.Equal(notes, []float64{2, 1}) {
			t.Fatalf("notes %v, want [2 1]", notes)
		}
	})

	t.Run("stores held changes for a version reload inside a callback after the call in flight", func(t *testing.T) {
		noteV2 := defineDoc("gc.note", 2, latestScope(durable.ForkInitial), func() obj { return delta.JsonObjectOf("n", 0.0, "migrated", true) },
			withMigrate(func(value obj, _ int) obj { return delta.JsonObjectOf("n", value.Value("n"), "migrated", true) }))
		g := openGC(t, 2)
		a, b := g.ids[0], g.ids[1]
		mustBump(t, g.bump(a))
		mustBump(t, g.bump(b))
		mustBump(t, g.bump(b, deferredForever))
		held := g.storage.hold()
		first := g.bump(a)
		<-held.Entered()
		reloading := g.commitAsync(func(tx durable.Tx) (any, error) {
			note, err := tx.Doc(noteV2, b)
			if err != nil {
				return nil, err
			}
			return num(note.Get("n")), nil
		})
		if reloading.settled() {
			t.Fatal("the version reload settled while a call was in flight")
		}
		held.Release()
		mustBump(t, first)
		// The reload found what memory held, stored first.
		if value := mustBump(t, reloading); value != 2 {
			t.Fatalf("the reload read %v, want 2", value)
		}
		g.close()
	})

	t.Run("stores held changes at their deadline after the call in flight", func(t *testing.T) {
		g := openGC(t, 2)
		a, b := g.ids[0], g.ids[1]
		mustBump(t, g.bump(a))
		held := g.storage.hold()
		first := g.bump(a)
		<-held.Entered()
		zero := 0.0
		later := g.bump(b, durable.CommitOptions{Persistence: &durable.CommitPersistence{Kind: durable.CommitDeferred, WithinMs: &zero}})
		quiesce(later)
		g.awaitQueued(1)
		calls := g.storage.callCount()
		held.Release()
		mustBump(t, first)
		mustBump(t, later)
		if got := g.storage.callCount(); got != calls+1 {
			t.Fatalf("%d storage calls, want %d", got, calls+1)
		}
		last := g.storage.lastCall()
		if len(last) != 1 || len(last[0]) != 1 || durableWriteType(last[0][0]) != "document.create" {
			t.Fatalf("the last call %v, want one document.create", last)
		}
		g.close()
	})

	t.Run("drains the calls in flight and stores held changes when it closes", func(t *testing.T) {
		g := openGC(t, 2)
		a, b := g.ids[0], g.ids[1]
		held := g.storage.hold()
		first := g.bump(a)
		<-held.Entered()
		later := g.bump(b, deferredForever)
		quiesce(later)
		g.awaitQueued(1)
		closing := spawn(func() (any, error) { return nil, g.session.Close(ctx) })
		held.Release()
		mustBump(t, first)
		mustBump(t, later)
		if _, err := closing.wait(); err != nil {
			t.Fatal(err)
		}
		if end := g.closed(); end.Kind != durable.SessionEndClosed {
			t.Fatalf("session ended %+v, want closed", end)
		}
		if notes := g.notes(a, b); !slices.Equal(notes, []float64{1, 1}) {
			t.Fatalf("notes %v, want [1 1]", notes)
		}
	})
}

func durableWriteType(write durable.StorageWrite) string { return durable.StorageWriteType(write) }

func TestGroupCommitBackpressureAndBounds(t *testing.T) {
	t.Run("starts no job past 256 without a delivered outcome, and returns its line state to zero when idle", func(t *testing.T) {
		g := openGC(t, 2)
		a, b := g.ids[0], g.ids[1]
		held := g.storage.hold()
		first := g.bump(a)
		<-held.Entered()
		var ran atomic.Int32
		ranOne := make(chan struct{}, 300)
		appends := make([]*call, 0, 300)
		for range 300 {
			appends = append(appends, g.commitAsync(func(tx durable.Tx) (any, error) {
				ran.Add(1)
				ranOne <- struct{}{}
				return tx.AppendEntry(b, durable.EntryDraft{Kind: "note"})
			}))
		}
		for range 255 {
			<-ranOne
		}
		quiesce(appends...)
		if got := ran.Load(); got != 255 {
			t.Fatalf("%d callbacks ran, want 255", got)
		}
		if sizes := g.session.LineSizes(); sizes.Outstanding != 256 {
			t.Fatalf("outstanding %d, want 256", sizes.Outstanding)
		}
		held.Release()
		mustBump(t, first)
		for _, started := range appends {
			if _, err := started.wait(); err != nil {
				t.Fatal(err)
			}
		}
		if got := ran.Load(); got != 300 {
			t.Fatalf("%d callbacks ran, want 300", got)
		}
		if sizes := g.session.LineSizes(); sizes != (session.LineSizes{}) {
			t.Fatalf("line sizes %+v, want all zero", sizes)
		}
		g.close()
	})
}

// random is a small deterministic generator, so a failing seed reproduces.
func random(seed int64) func() float64 {
	state := seed
	return func() float64 {
		state = (state*1_103_515_245 + 12_345) % 2_147_483_648
		return float64(state) / 2_147_483_648
	}
}

// loggedIndex is the index an entry of the randomized test carries.
func loggedIndex(t *testing.T, data any) int {
	t.Helper()
	encoded, err := json.Marshal(data)
	must(t, err)
	var decoded struct {
		Index *int `json:"index"`
	}
	must(t, json.Unmarshal(encoded, &decoded))
	if decoded.Index == nil {
		return -1
	}
	return *decoded.Index
}

func TestGroupCommitRandomizedInterleavings(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 1_234, 99_991} {
		t.Run(fmt.Sprintf("stores, publishes, and reads only what an order of the commits implies (seed %d)", seed), func(t *testing.T) {
			next := random(seed)
			g := openGC(t, 4)
			log := g.ids[0]
			g.storage.mu.Lock()
			delays := random(seed + 1)
			g.storage.latency = func() time.Duration { return time.Duration(int(delays()*3)) * time.Millisecond }
			g.storage.mu.Unlock()
			refuseAt := -1
			if seed%2 == 0 {
				refuseAt = int(next() * 60)
			}
			g.storage.setRefuse(func(writes []durable.StorageWrite) bool {
				for _, write := range writes {
					if entry, ok := write.(durable.EntryWrite); ok && entry.Value.Data != nil && loggedIndex(t, entry.Value.Data) == refuseAt {
						return true
					}
				}
				return false
			})
			// Per commit, in line order: the notes it increments and the values it read.
			plans := make([][]durable.ConversationId, 0, 60)
			reads := make([][]float64, 60)
			var readMu sync.Mutex
			outcomes := make([]*call, 0, 60)
			for index := range 60 {
				var touched []durable.ConversationId
				for _, id := range g.ids {
					if next() < 0.4 {
						touched = append(touched, id)
					}
				}
				plans = append(plans, touched)
				outcome := g.commitAsync(func(tx durable.Tx) (any, error) {
					values := make([]float64, 0, len(touched))
					for _, id := range touched {
						note, err := tx.Doc(gcNote, id)
						if err != nil {
							return nil, err
						}
						n := num(note.Get("n"))
						if err := note.Set("n", n+1); err != nil {
							return nil, err
						}
						values = append(values, n)
					}
					readMu.Lock()
					reads[index] = values
					readMu.Unlock()
					return tx.AppendEntry(log, durable.EntryDraft{Kind: "log", Data: map[string]any{"index": float64(index)}})
				})
				outcomes = append(outcomes, outcome)
				switch pause := next(); {
				case pause < 0.3:
					_, _ = outcome.wait()
				case pause < 0.5:
					quiesce(outcomes...)
				}
			}
			stored := make([]bool, len(outcomes))
			for index, outcome := range outcomes {
				_, err := outcome.wait()
				stored[index] = err == nil
			}
			if refuseAt >= 0 {
				g.closed()
			} else {
				g.close()
			}
			// Outcomes match storage: a commit resolved exactly when its log entry is stored.
			g.storage.Reopen()
			reopened := session.NewSessionImpl(g.storage, session.Hooks{}, nil, nil)
			logged := map[int]bool{}
			page, err := reopened.Commit(ctx, func(tx durable.Tx) (any, error) {
				return tx.ScanEntries(durable.EntryQuery{ConversationId: log}, 1_000, nil)
			})
			must(t, err)
			for _, entry := range page.(durable.Page[durable.EntryRecord, durable.Cursor]).Items {
				logged[loggedIndex(t, entry.Data)] = true
			}
			for index, ok := range stored {
				if logged[index] != ok {
					t.Fatalf("commit %d: logged %v, resolved %v", index, logged[index], ok)
				}
			}
			// Each callback read every earlier stored increment of what it touched, and nothing else.
			for index, values := range reads {
				if values == nil {
					continue
				}
				for position, id := range plans[index] {
					expected := 0
					for earlier := range index {
						if stored[earlier] && slices.Contains(plans[earlier], id) {
							expected++
						}
					}
					if values[position] != float64(expected) {
						t.Fatalf("commit %d read %v of conversation %d, want %d", index, values[position], id, expected)
					}
				}
			}
			// Storage equals the stored commits replayed in order.
			for _, id := range g.ids {
				increments := 0
				for index, touched := range plans {
					if stored[index] && slices.Contains(touched, id) {
						increments++
					}
				}
				value, err := reopened.SnapshotErased(ctx, gcNote, id)
				must(t, err)
				got := 0.0
				if value != nil {
					got = num(value.Value("n"))
				}
				if got != float64(increments) {
					t.Fatalf("conversation %d note %v, want %d", id, got, increments)
				}
			}
			if sizes := g.session.LineSizes(); sizes != (session.LineSizes{}) {
				t.Fatalf("line sizes %+v, want all zero", sizes)
			}
			must(t, reopened.Close(ctx))
		})
	}
}
