package store

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/store/export.test.ts: tests of `store.Export(ctx)` (slice 6a §13, rule 26): the
// parts in archive order, lossless; the commit queue held from the first `Next` until the export ends, is closed or
// aborts (also while the consumer is suspended); GC of closed and aborted iterators.
//
// Designed out: the abort listener counts (`getEventListeners(signal, "abort")` has 1 then 0 listeners). A Go context
// has no listener list; the `context.AfterFunc` registration is stopped when the export ends, and what the counts
// proved there (the queue given back at once, nothing left held) is asserted by the commits that follow.

// exportFlushed waits until every goroutine that can make progress did (Pi's `flushed()`: pending promise callbacks).
func exportFlushed() { time.Sleep(50 * time.Millisecond) }

// exportCollected expects the referent to be collected once nothing else holds it (Pi's `expectCollected`).
func exportCollected[T any](t *testing.T, ref weak.Pointer[T]) {
	t.Helper()
	for round := 0; round < 10 && ref.Value() != nil; round++ {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	if ref.Value() != nil {
		t.Fatal("the referent was not collected")
	}
}

// exportHeap is the heap in use once pending work ran and garbage was collected, repeatedly (Pi's `settledHeap`).
func exportHeap() uint64 {
	for range 5 {
		time.Sleep(10 * time.Millisecond)
		runtime.GC()
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// exportCode is the code of a ChordError, "" for any other error.
func exportCode(err error) data.ChordErrorCode {
	if chordErr, ok := data.IsChordError(err); ok {
		return chordErr.Code
	}
	return ""
}

// exportNext is Next of an export, failing the test on an error.
func exportNext(t *testing.T, export *StoreExport) (ArchivePart, bool) {
	t.Helper()
	part, ok, err := export.Next(background)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	return part, ok
}

// exportOf is store's export under ctx.
func exportOf(t *testing.T, store *ChordStore, ctx context.Context) *StoreExport {
	t.Helper()
	export, err := store.Export(ctx)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	return export
}

// exportCommit queues a commit of no writes and returns a poll of its result: settled, and its error.
func exportCommit(store *ChordStore) func() (bool, error) {
	result := store.CommitAsync(nil, openFence, background)
	return func() (bool, error) {
		select {
		case settled := <-result:
			return true, settled.Err
		default:
			return false, nil
		}
	}
}

// exportWaitFor waits for the commit's result.
func exportWaitFor(t *testing.T, done func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if settled, err := done(); settled {
			if err != nil {
				t.Fatalf("commit: %v", err)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the commit did not settle")
}

func TestExportParts(t *testing.T) {
	t.Run("come in archive order: manifest, per table its table write, puts and indexes, each record and its revisions, end", func(t *testing.T) {
		store := archiveStore(t)
		parts := exported(t, store, background)
		manifest, ok := parts[0].(*ManifestPart)
		if !ok || *manifest != (ManifestPart{Format: ArchiveFormat, Schema: StoreSchema, LastSeq: store.LastSeq(), NextID: store.NextID()}) {
			t.Fatalf("parts[0] = %s", nexttest.JSONText(t, parts[0]))
		}
		type run struct {
			kind  string
			count int
		}
		var runs []run
		for _, part := range parts {
			kind := part.StoreWriteKind()
			if kind == "put" || kind == "table" || kind == "index" {
				kind += " " + strings.Split(partJSON(t, part).Value("table").(string), ":")[1]
			}
			if len(runs) > 0 && runs[len(runs)-1].kind == kind {
				runs[len(runs)-1].count++
			} else {
				runs = append(runs, run{kind, 1})
			}
		}
		want := []run{
			{"manifest", 1},
			{"table edits", 1},
			{"put edits", 150},
			{"index edits", 1},
			{"table notes", 1},
			{"put notes", 150},
			{"document.create", 1},
			{"document.revision", 1},
			{"document.create", 1},
			{"document.revision", 3},
			{"document.create", 1},
			{"document.revision", 2},
			{"end", 1},
		}
		if fmt.Sprint(runs) != fmt.Sprint(want) {
			t.Fatalf("runs = %v, want %v", runs, want)
		}
		if end, ok := parts[len(parts)-1].(*EndPart); !ok || end.Writes != int64(len(parts)-2) {
			t.Fatalf("end = %s, want %d writes", nexttest.JSONText(t, parts[len(parts)-1]), len(parts)-2)
		}
	})

	t.Run("are lossless: stored values and versions, NULL cells absent, a migrating table's state, revisions as stored", func(t *testing.T) {
		store := archiveStore(t)
		parts := exported(t, store, background)
		var edits []ArchivePart
		for _, part := range parts {
			if part.StoreWriteKind() == "put" && strings.HasSuffix(partJSON(t, part).Value("table").(string), ":edits") {
				edits = append(edits, part)
			}
		}
		wantEdits := []string{
			`{"kind":"put","table":"app.store:edits","row":{"id":"e000","path":"p/0","at":0,"note":"n0","meta":{"a":0},"done":false},"version":1,"columns":{"meta":1}}`,
			`{"kind":"put","table":"app.store:edits","row":{"id":"e001","path":"p/1","at":1,"done":false},"version":1,"columns":{}}`,
		}
		for index, want := range wantEdits {
			if !nexttest.JSONEqual(partJSON(t, edits[index]), nexttest.ParseJSON(t, want)) {
				t.Fatalf("edits[%d] = %s, want %s", index, nexttest.JSONText(t, edits[index]), want)
			}
		}
		var notesTable ArchivePart
		versionTwo := 0
		for _, part := range parts {
			object := partJSON(t, part)
			table, _ := object.Value("table").(string)
			if part.StoreWriteKind() == "table" && strings.HasSuffix(table, ":notes") {
				notesTable = part
			}
			if part.StoreWriteKind() == "put" && strings.HasSuffix(table, ":notes") && object.Value("version") == float64(2) {
				versionTwo++
			}
		}
		if notesTable == nil || partJSON(t, notesTable).Value("version") != float64(2) || partJSON(t, notesTable).Value("state") != "migrating" {
			t.Fatalf("notes table = %v, want version 2, migrating", notesTable)
		}
		if versionTwo != 100 {
			t.Fatalf("notes rows at version 2 = %d, want 100", versionTwo)
		}
		var documents []any
		for _, part := range parts {
			object := partJSON(t, part)
			switch part.StoreWriteKind() {
			case "document.create":
				record := object.Value("record").(*delta.JsonObject)
				// A singleton's record has no key.
				documents = append(documents, []any{record.Value("key"), record.Value("revision"), record.Value("history")})
			case "document.revision":
				content := object.Value("content").(*delta.JsonObject)
				if content.Value("kind") == "base" {
					documents = append(documents, []any{"base", content.Value("value")})
				} else {
					documents = append(documents, []any{"delta", content.Value("ops")})
				}
			}
		}
		want := `[[null,1,"none"],["base",{"title":""}],["a",3,"none"],["base",{"title":"a"}],["delta",[["s",["title"],"a2"]]],["delta",[["s",["title"],"a3"]]],["b",2,"none"],["base",{"title":"b"}],["delta",[["s",["title"],"b2"]]]]`
		if got := nexttest.JSONText(t, documents); got != want {
			t.Fatalf("documents = %s, want %s", got, want)
		}
	})
}

func TestExportRevisions(t *testing.T) {
	t.Run("a history longer than a page is read page by page and exported whole, in seq order", func(t *testing.T) {
		store := memoryStore(t)
		count := int(ExportPage)*2 + 1
		record := DocumentRecord{
			ID: 2, Owner: "app.store", Namespace: "session", Kind: "long", History: "rewindable",
			CreatedAt: 1, Revision: 1, Version: 1, Fingerprint: "",
		}
		empty := "{}"
		create := stagedWrite{order: OrderWrite, apply: func(step *Step) ([]StoreWrite, error) {
			return nil, InsertDocument(step, record, &empty)
		}}
		if _, err := store.Commit([]StagedWrite{create}, openFence, background); err != nil {
			t.Fatalf("commit: %v", err)
		}
		for n := 1; n < count; n++ {
			ops := fmt.Sprintf(`[["s",["n"],%d]]`, n)
			bump := stagedWrite{order: OrderWrite, apply: func(step *Step) ([]StoreWrite, error) {
				return nil, InsertRevision(step, record.ID, step.Seq, "delta", 1, ops)
			}}
			if _, err := store.Commit([]StagedWrite{bump}, openFence, background); err != nil {
				t.Fatalf("commit: %v", err)
			}
		}
		var seqs []int64
		for _, part := range exported(t, store, background) {
			if revision, ok := part.(*DocumentRevisionWrite); ok {
				seqs = append(seqs, revision.Seq)
			}
		}
		if len(seqs) != count {
			t.Fatalf("revisions = %d, want %d", len(seqs), count)
		}
		for index, seq := range seqs {
			if seq != int64(index+1) {
				t.Fatalf("seqs[%d] = %d, want %d", index, seq, index+1)
			}
		}
	})
}

func TestExportCommitQueue(t *testing.T) {
	t.Run("is held from the first next() until the export ends: a commit waits", func(t *testing.T) {
		store := archiveStore(t)
		export := exportOf(t, store, background)
		exportNext(t, export)
		commit := exportCommit(store)
		exportFlushed()
		if settled, _ := commit(); settled {
			t.Fatal("the commit ran while the export held the queue")
		}
		for {
			if _, ok := exportNext(t, export); !ok {
				break
			}
		}
		exportWaitFor(t, commit)
	})

	t.Run("is given back by return(); the closed iterator keeps nothing", func(t *testing.T) {
		store := archiveStore(t)
		export := exportOf(t, store, background)
		exportNext(t, export)
		exportNext(t, export)
		if err := export.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if _, err := store.Commit(nil, openFence, background); err != nil {
			t.Fatalf("commit: %v", err)
		}
		collected := weak.Make(export)
		export = nil
		exportCollected(t, collected)
	})

	t.Run("is given back at once when ctx aborts while the consumer is suspended; a later next() rejects aborted", func(t *testing.T) {
		store := archiveStore(t)
		cancellable, cancel := chordctx.WithCancel(background)
		export := exportOf(t, store, cancellable)
		exportNext(t, export)
		cancel(errors.New("stop"))
		if _, err := store.Commit(nil, openFence, background); err != nil {
			t.Fatalf("commit: %v", err)
		}
		if _, _, err := export.Next(background); exportCode(err) != data.CodeAborted {
			t.Fatalf("Next error = %v, want aborted", err)
		}
	})

	t.Run("a retained aborted iterator keeps no page", func(t *testing.T) {
		store := memoryStore(t)
		big, err := DefineTable("big", TableOptions{
			Version: 1,
			Columns: []ColumnProp{Col("id", Int()), Col("text", Str())},
			Key:     []string{"id"},
		})
		archiveMust(t, err)
		var staged []StagedWrite
		changes := NewTableChanges(tableIn(t, store, big), &staged)
		for i := range int(ExportPage) {
			prefix := fmt.Sprint(i)
			archiveMust(t, changes.Put(Row{"id": int64(i), "text": prefix + strings.Repeat("x", 8_000-len(prefix))}))
		}
		if _, err := store.Commit(staged, openFence, background); err != nil {
			t.Fatalf("commit: %v", err)
		}
		cancellable, cancel := chordctx.WithCancel(background)
		before := exportHeap()
		export := exportOf(t, store, cancellable)
		// The manifest, the table write, and the first put of a page of 100 rows of 8 KB each.
		for range 3 {
			exportNext(t, export)
		}
		cancel(errors.New("stop"))
		after := exportHeap()
		if after > before && after-before >= 200*1024 {
			t.Fatalf("the aborted export retains %d bytes", after-before)
		}
		if _, _, err := export.Next(background); exportCode(err) != data.CodeAborted {
			t.Fatalf("Next error = %v, want aborted", err)
		}
	})

	t.Run("code that commits while consuming its own export waits until ctx aborts", func(t *testing.T) {
		store := archiveStore(t)
		cancellable, cancel := chordctx.WithCancel(background)
		export := exportOf(t, store, cancellable)
		exportNext(t, export)
		commit := exportCommit(store)
		exportFlushed()
		if settled, _ := commit(); settled {
			t.Fatal("the commit ran while the export held the queue")
		}
		cancel(errors.New("stop"))
		exportWaitFor(t, commit)
	})

	t.Run("closing the store ends a held export: its next() rejects released, also for a part that needs no read", func(t *testing.T) {
		store := archiveStore(t)
		export := exportOf(t, store, background)
		exportNext(t, export)
		if err := store.Close(background); err != nil {
			t.Fatalf("Close: %v", err)
		}
		// The table write would need no read.
		for range 2 {
			if _, _, err := export.Next(background); exportCode(err) != data.CodeReleased {
				t.Fatalf("Next error = %v, want released", err)
			}
		}
	})

	t.Run("an abort while next() waits for the queue rejects at once; no abort listener is left", func(t *testing.T) {
		store := archiveStore(t)
		first := exportOf(t, store, background)
		exportNext(t, first)
		cancellable, cancel := chordctx.WithCancel(background)
		second := exportOf(t, store, cancellable)
		waiting := make(chan error, 1)
		go func() {
			_, _, err := second.Next(background)
			waiting <- err
		}()
		exportFlushed()
		cancel(errors.New("stop"))
		select {
		case err := <-waiting:
			if exportCode(err) != data.CodeAborted {
				t.Fatalf("Next error = %v, want aborted", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the waiting Next did not reject")
		}
		// The first export still holds the queue; once it ends, the aborted one gives the queue back at once.
		if err := first.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if _, err := store.Commit(nil, openFence, background); err != nil {
			t.Fatalf("commit: %v", err)
		}
		if _, _, err := second.Next(background); exportCode(err) != data.CodeAborted {
			t.Fatalf("Next error = %v, want aborted", err)
		}
	})

	t.Run("closing the store before an export's first next() rejects it released: not even a manifest", func(t *testing.T) {
		store := archiveStore(t)
		export := exportOf(t, store, background)
		if err := store.Close(background); err != nil {
			t.Fatalf("Close: %v", err)
		}
		for range 2 {
			if _, _, err := export.Next(background); exportCode(err) != data.CodeReleased {
				t.Fatalf("Next error = %v, want released", err)
			}
		}
	})

	t.Run("closing the store while an export waits for the queue rejects its next() released", func(t *testing.T) {
		store := archiveStore(t)
		first := exportOf(t, store, background)
		exportNext(t, first)
		second := exportOf(t, store, background)
		waiting := make(chan error, 1)
		go func() {
			_, _, err := second.Next(background)
			waiting <- err
		}()
		exportFlushed()
		if err := store.Close(background); err != nil {
			t.Fatalf("Close: %v", err)
		}
		select {
		case err := <-waiting:
			if exportCode(err) != data.CodeReleased {
				t.Fatalf("Next error = %v, want released", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the waiting Next did not reject")
		}
	})
}
