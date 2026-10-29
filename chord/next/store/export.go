package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/src/next/store/export.ts
//
// `store.Export(ctx)` (slice 6a §13, rule 26): the store's state after its `lastSeq` as archive parts, read page by
// page with plain reads while the export holds the commit queue (no commit can interleave, no long read transaction).
// The queue is taken at the first `Next` and given back when the iteration ends, fails, is closed or `ctx` aborts; an
// abort while the consumer is suspended gives it back at once and drops the pages and records the export held, and an
// abort while it waits for the queue ends the wait. Closing the store ends it too (`released`).
//
// Async contract: `Next` blocks until the next part is read (Pi's `next()` promise). It is governed by the context
// the export was made with, as Pi's `next()` takes none: the `ctx` argument only completes the PartIterator contract
// and is not read. One consumer calls Next and Close in turn; the abort and the store's close may end the export from
// other goroutines, so a mutex guards its state (Pi's `#state`, `#release`, `#parts`); no database call runs under
// it. Pi's async generator `parts` is the explicit state machine exportParts, advanced one part per call; the page
// it reads is held only until its parts are returned. An abort listener is a `context.AfterFunc` (Pi's
// `addEventListener("abort")`), stopped when the export ends. Waiting for the queue is abandoned on `ctx` while the
// take goes on in the background: its release is given back as soon as the take settles (Pi's `hold` of an ended
// export).

// ExportPage is the rows and documents an export reads per page.
const ExportPage int64 = 100

// PartIterator is a stream of archive parts: Next blocks for the next part (`ok` false at the end), Close ends the
// stream (Pi's `return()`). StoreExport implements it.
type PartIterator interface {
	Next(ctx context.Context) (part ArchivePart, ok bool, err error)
	Close() error
}

// exportState is Pi's `#state`.
type exportState int

const (
	exportOpen exportState = iota
	exportAborted
	exportClosed
	exportEnded
)

// StoreExport is an export of a store under a context: an iterator of its parts. One pass; after an abort Next
// returns `aborted`, after the store closed `released`.
type StoreExport struct {
	ctx context.Context

	mu sync.Mutex
	// parts are the parts until the export ends; dropped then, with the pages they hold.
	parts *exportParts
	state exportState
	// release gives the commit queue back while the export holds it.
	release func()
	// stopAbort removes the abort listener.
	stopAbort func() bool
}

var _ PartIterator = (*StoreExport)(nil)

// NewStoreExport is an export of store under ctx; it takes the commit queue at the first Next.
func NewStoreExport(store *ChordStore, ctx context.Context) *StoreExport {
	export := &StoreExport{ctx: ctx}
	export.parts = &exportParts{store: store, export: export, ctx: ctx}
	return export
}

// Next is the next part. ok is false once the export ended; after an abort it returns `aborted`, after the store
// closed `released`.
func (export *StoreExport) Next(_ context.Context) (ArchivePart, bool, error) {
	if export.ctx.Err() != nil {
		export.end(exportAborted)
	}
	if err := export.assertOpen(); err != nil {
		return nil, false, err
	}
	export.mu.Lock()
	parts := export.parts
	export.mu.Unlock()
	if parts == nil {
		return nil, false, nil
	}
	part, ok, err := parts.next()
	if err != nil {
		if export.ctx.Err() != nil {
			export.end(exportAborted)
		}
		if openErr := export.assertOpen(); openErr != nil {
			return nil, false, openErr
		}
		export.end(exportEnded)
		return nil, false, err
	}
	// Ended meanwhile (an abort, the store's close): no part after that, even one that needed no read.
	if err := export.assertOpen(); err != nil {
		return nil, false, err
	}
	if !ok {
		export.end(exportEnded)
	}
	return part, ok, nil
}

// Close ends the export (Pi's `return()`): the queue given back, the parts dropped.
func (export *StoreExport) Close() error {
	export.end(exportEnded)
	return nil
}

// hold is called when the parts took the queue: the export holds it until it ends, and an abort ends it; ended
// already, it gives it back.
func (export *StoreExport) hold(release func()) {
	export.mu.Lock()
	if export.state != exportOpen {
		export.mu.Unlock()
		release()
		return
	}
	export.release = release
	export.mu.Unlock()
	// AfterFunc runs the callback at once when ctx is aborted already; end takes the mutex itself.
	stop := context.AfterFunc(export.ctx, func() { export.end(exportAborted) })
	export.mu.Lock()
	if export.state == exportOpen {
		export.stopAbort = stop
	} else {
		stop()
	}
	export.mu.Unlock()
}

// end ends the export once: the queue given back, the abort listener removed, the parts dropped.
func (export *StoreExport) end(state exportState) {
	export.mu.Lock()
	if export.state != exportOpen {
		export.mu.Unlock()
		return
	}
	export.state = state
	release, stop := export.release, export.stopAbort
	export.release, export.stopAbort = nil, nil
	export.parts = nil
	export.mu.Unlock()
	if release != nil {
		release()
	}
	if stop != nil {
		stop()
	}
}

// assertOpen is `aborted` after an abort, `released` after the store closed.
func (export *StoreExport) assertOpen() error {
	export.mu.Lock()
	state := export.state
	export.mu.Unlock()
	switch state {
	case exportAborted:
		return exportAbortedError(export.ctx)
	case exportClosed:
		return data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonClosed, Message: "store: closed"}, nil)
	}
	return nil
}

// exportAbortedError is the error of a use of an aborted export: `aborted`, the context's cause as its cause.
func exportAbortedError(ctx context.Context) *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeAborted, Message: "store: export aborted"}, context.Cause(ctx))
}

// exportStage is the place of the parts in archive order.
type exportStage int

const (
	stageStart exportStage = iota
	stageTables
	stageDocuments
	stageEnd
	stageDone
)

// exportParts are the parts in archive order, after taking the queue (the store's close calls the export's end);
// the queue is given back when they end. Pi's `parts` async generator.
type exportParts struct {
	store  *ChordStore
	export *StoreExport
	ctx    context.Context

	stage exportStage
	// pending are the parts read and not yet returned (a page).
	pending []ArchivePart
	// writes counts the parts returned between the manifest and the end.
	writes int64

	tables []*TableRecord
	// table is the table whose group is being read; step its place: 0 the table write, 1 its puts, 2 its indexes.
	table int
	step  int
	// after is the last key of a full page of puts.
	after []sqlite.SqliteValue

	// recordsAfter is the id the next page of records follows; recordsFull whether the last page was full.
	recordsAfter int64
	recordsFull  bool
	records      []DocumentRecord
	// revisions is the record whose revisions are being read, after seq revisionsAfter.
	revisions      *DocumentRecord
	revisionsAfter int64
}

// next is the next part; ok is false once the parts ended. The queue is given back when they end or fail.
func (parts *exportParts) next() (ArchivePart, bool, error) {
	for {
		if len(parts.pending) > 0 {
			part := parts.pending[0]
			parts.pending[0] = nil
			parts.pending = parts.pending[1:]
			if part.StoreWriteKind() != "manifest" {
				parts.writes++
			}
			return part, true, nil
		}
		if parts.stage == stageDone {
			// Past the end part (or after a failure): the queue goes back when the consumer asks for more.
			parts.finish()
			return nil, false, nil
		}
		if err := parts.advance(); err != nil {
			parts.pending = nil
			parts.finish()
			return nil, false, err
		}
	}
}

// finish gives the queue back.
func (parts *exportParts) finish() {
	parts.stage = stageDone
	parts.export.mu.Lock()
	release := parts.export.release
	parts.export.mu.Unlock()
	if release != nil {
		release()
	}
}

// advance reads the parts that follow the pending ones (none pending).
func (parts *exportParts) advance() error {
	switch parts.stage {
	case stageStart:
		return parts.start()
	case stageTables:
		return parts.advanceTables()
	case stageDocuments:
		return parts.advanceDocuments()
	}
	// stageEnd: the end part counts the parts returned before it; the queue is given back after it.
	parts.pending = []ArchivePart{&EndPart{Writes: parts.writes}}
	parts.stage = stageDone
	return nil
}

// start takes the queue and yields the manifest.
func (parts *exportParts) start() error {
	taken := parts.store.Take(func() { parts.export.end(exportClosed) })
	var release func()
	select {
	case release = <-taken:
	case <-parts.ctx.Done():
		// Until the export holds the queue only ctx ends the wait; the take goes on and its release is given back.
		go func() { parts.export.hold(<-taken) }()
		return exportAbortedError(parts.ctx)
	}
	parts.export.hold(release)
	if parts.ctx.Err() != nil {
		return exportAbortedError(parts.ctx)
	}
	lastSeq, nextID := parts.store.LastSeq(), parts.store.NextID()
	parts.pending = []ArchivePart{&ManifestPart{Format: ArchiveFormat, Schema: StoreSchema, LastSeq: lastSeq, NextID: nextID}}
	tables := parts.store.Tables()
	parts.tables = make([]*TableRecord, 0, len(tables))
	for _, record := range tables {
		parts.tables = append(parts.tables, record)
	}
	slices.SortFunc(parts.tables, func(a, b *TableRecord) int { return CompareText(a.Table, b.Table) })
	parts.stage = stageTables
	return nil
}

// advanceTables reads the next group of a table: its `table` write, its `put`s a page at a time, its `index` writes.
func (parts *exportParts) advanceTables() error {
	if parts.table >= len(parts.tables) {
		parts.stage = stageDocuments
		return nil
	}
	record := parts.tables[parts.table]
	switch parts.step {
	case 0:
		parts.pending = []ArchivePart{&TableDefinitionWrite{
			Table: record.Table, Version: record.Version, State: record.State, Columns: record.Columns,
			Key: record.Key, Fingerprint: record.Fingerprint,
		}}
		parts.step = 1
		parts.after = nil
	case 1:
		return parts.putsPage(record)
	default:
		for _, index := range record.Indexes {
			parts.pending = append(parts.pending, &IndexWrite{Table: record.Table, Name: index.Name, Columns: index.Columns})
		}
		parts.table++
		parts.step = 0
	}
	return nil
}

// putsPage reads one page of the `put`s of a recorded table in key order: the stored values and their versions.
func (parts *exportParts) putsPage(record *TableRecord) error {
	physical, _ := RecordedPut(record.Table, record.Columns)
	selected := strings.Join(quoteAll(physical), ", ")
	order := strings.Join(quoteAll(record.Key), ", ")
	from := ""
	if parts.after != nil {
		from = " WHERE (" + order + ") > (" + strings.TrimSuffix(strings.Repeat("?, ", len(parts.after)), ", ") + ")"
	}
	sql := "SELECT " + selected + " FROM " + Quote(record.Table) + from + " ORDER BY " + order + " LIMIT ?"
	params := append(slices.Clone(parts.after), ExportPage)
	rows, err := Read(parts.store, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
		return executor.All(sql, params...)
	}, parts.ctx)
	if err != nil {
		return err
	}
	for _, stored := range rows {
		put, err := putOf(record, stored)
		if err != nil {
			return err
		}
		parts.pending = append(parts.pending, put)
	}
	if int64(len(rows)) < ExportPage {
		parts.after = nil
		parts.step = 2
		return nil
	}
	last := rows[len(rows)-1]
	parts.after = make([]sqlite.SqliteValue, len(record.Key))
	for index, name := range record.Key {
		parts.after[index] = last[name]
	}
	return nil
}

// putOf is a stored row as its `put`: `NULL` cells absent, booleans from 0 and 1, `json(...)` cells parsed with
// versions. Numbers are float64, as a StoredRow holds them.
func putOf(record *TableRecord, stored sqlite.SqliteRow) (*PutWrite, error) {
	row := delta.NewJsonObject(len(record.Columns))
	versions := delta.NewJsonObject(0)
	for _, entry := range record.Columns {
		value := stored[entry.Name]
		if value == nil {
			continue
		}
		// The recorded columns' types say what SQLite stored (STRICT tables): text for `json(...)` with its version.
		switch entry.Record.Type {
		case ColBool:
			row.Set(entry.Name, value == int64(1))
		case ColJson:
			text, _ := value.(string)
			parsed, err := delta.DecodeJson([]byte(text))
			if err != nil {
				return nil, err
			}
			row.Set(entry.Name, parsed)
			versions.Set(entry.Name, exportNumber(stored[VersionColumn(entry.Name)]))
		default:
			if text, ok := value.(string); ok {
				row.Set(entry.Name, text)
			} else {
				row.Set(entry.Name, exportNumber(value))
			}
		}
	}
	return &PutWrite{Table: record.Table, Row: row, Version: int(exportNumber(stored[RowVersion])), Columns: versions}, nil
}

// exportNumber is a stored number as a JSON number.
func exportNumber(value any) float64 {
	switch typed := value.(type) {
	case int64:
		return float64(typed)
	case float64:
		return typed
	}
	panic(fmt.Sprintf("store: export: a stored number is %T", value))
}

// advanceDocuments reads the next record or page of revisions: every stored record by id, live and retired, as its
// creation without a base, then its kept revisions in seq order: a `none` or `latest` record's current base and its
// deltas, a retired `latest` one none, a `rewindable` one all.
func (parts *exportParts) advanceDocuments() error {
	if parts.revisions != nil {
		return parts.revisionsPage()
	}
	if len(parts.records) > 0 {
		record := parts.records[0]
		parts.records = parts.records[1:]
		parts.pending = []ArchivePart{&DocumentCreate{Record: record}}
		parts.revisions, parts.revisionsAfter = &record, 0
		return nil
	}
	if parts.recordsAfter > 0 && !parts.recordsFull {
		parts.stage = stageEnd
		return nil
	}
	after := parts.recordsAfter
	records, err := Read(parts.store, func(executor sqlite.SqliteExecutor) ([]DocumentRecord, error) {
		return ListRecords(executor, after, ExportPage)
	}, parts.ctx)
	if err != nil {
		return err
	}
	parts.records = records
	parts.recordsFull = int64(len(records)) >= ExportPage
	if len(records) == 0 {
		parts.stage = stageEnd
		return nil
	}
	parts.recordsAfter = records[len(records)-1].ID
	return nil
}

// revisionsPage reads a page of the kept revisions of the current record in seq order (a long history is never one
// array).
func (parts *exportParts) revisionsPage() error {
	id, after := parts.revisions.ID, parts.revisionsAfter
	page, err := Read(parts.store, func(executor sqlite.SqliteExecutor) ([]DocumentRevision, error) {
		return ListRevisions(executor, id, after, ExportPage)
	}, parts.ctx)
	if err != nil {
		return err
	}
	for _, revision := range page {
		parts.pending = append(parts.pending, &DocumentRevisionWrite{ID: revision.ID, Seq: revision.Seq, Content: revision.Content})
	}
	if int64(len(page)) < ExportPage {
		parts.revisions = nil
		return nil
	}
	parts.revisionsAfter = page[len(page)-1].Seq
	return nil
}
