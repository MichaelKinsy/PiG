package store

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/store/import.ts
//
// `ImportStore(backend, parts, ctx, options)` (slice 6a §13, rules 12, 24, 25): a store made from an archive, a trust
// boundary. Every part is checked against the archive schema, its place in the archive's order and its content
// (names, rows against their table's recorded columns, ids and seqs, lifetimes at one address never overlapping,
// revisions in seq order from a base, versions, duplicates, the `end` count, the manifest); everything is applied in
// one backend transaction with the applier, so a refused import leaves the backend as it was. An input that stalls is
// cancelled by `ctx`.
//
// Go forced differences:
//   - Pi's `AsyncIterable<unknown> | Iterable<unknown>` input is a PartSource: Next blocks for the next value (a
//     decoded JSON value, or a typed ArchivePart as an export yields it); SlicePartSource is the Iterable. A wait for the
//     next value ends when `ctx` aborts: the call runs on its own goroutine for that (Pi's `awaitWithContext` of the
//     `next()` promise, which is not cancelled either), and the source is closed in the background, not awaited.
//   - An op of a delta that is not itself an array cannot be held by a typed revision (delta.Op is a list), so such
//     an archive is refused `invalid/args` where Pi would store it.
//
// Async contract: ImportStore blocks until the backend transaction ended; the parts are taken in order, each applied
// before the next is read; `ctx` aborts only a wait for the input (and is checked once at the start), as in Pi.

// PartSource is an import's input: values for CheckPart, one at a time (Pi's iterable of unknowns).
type PartSource interface {
	// Next blocks for the next value; ok is false at the end of the input.
	Next(ctx context.Context) (value any, ok bool, err error)
	// Close ends the input early (Pi's iterator `return()`); called when the import stops before the end.
	Close() error
}

// slicePartSource is a PartSource over a slice (Pi's `Iterable`).
type slicePartSource[T any] struct {
	values []T
	next   int
}

// SlicePartSource is the PartSource of values, in order: decoded JSON values, or typed parts as an export yields them.
func SlicePartSource[T any](values []T) PartSource { return &slicePartSource[T]{values: values} }

func (source *slicePartSource[T]) Next(context.Context) (any, bool, error) {
	if source.next >= len(source.values) {
		return nil, false, nil
	}
	source.next++
	return source.values[source.next-1], true, nil
}

func (source *slicePartSource[T]) Close() error { return nil }

// ImportStore is a store holding the archive `parts` on `backend`, which must not hold a store yet: at the manifest's
// `lastSeq`, its outbox (when `options.Outbox`) starting after it. A refused archive, a stalled input cancelled by
// `ctx` or a backend error is returned; the backend is closed then (rule 12).
func ImportStore(backend *Backend, parts PartSource, ctx context.Context, options StoreOptions) (Store, error) {
	store, err := importStore(backend.DB, parts, ctx, options)
	if err != nil {
		// The import's own failure is the one to report.
		_ = backend.DB.Close()
		return nil, err
	}
	return store, nil
}

func importStore(db sqlite.SqliteDatabase, parts PartSource, ctx context.Context, options StoreOptions) (*ChordStore, error) {
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	existing, err := db.Get(`SELECT name FROM sqlite_master WHERE name = ?`, TableMeta)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, data.Invalid(data.ReasonArgs, "import: the backend holds a store already")
	}
	var meta MetaRow
	err = db.Transaction(func(executor sqlite.SqliteExecutor) error {
		schema, err := ApplyStoreSchema(inTransaction{executor})
		if err != nil {
			return err
		}
		archive := &archiveImport{executor: executor, ctx: ctx, place: placeManifest, tables: map[string]bool{}, ids: map[int64]bool{}, lifetimes: map[string][][2]int64{}}
		if err := importEach(parts, ctx, archive.take); err != nil {
			return err
		}
		meta, err = archive.finish(schema, options.Outbox != nil && *options.Outbox)
		return err
	})
	if err != nil {
		return nil, err
	}
	catalog, err := ReadCatalog(db)
	if err != nil {
		return nil, err
	}
	return NewChordStore(db, meta, catalog), nil
}

// inTransaction is the store's schema migrations run inside the import's backend transaction (they open none of their
// own).
type inTransaction struct{ sqlite.SqliteExecutor }

func (database inTransaction) Transaction(callback func(transaction sqlite.SqliteExecutor) error) error {
	return callback(database.SqliteExecutor)
}

func (inTransaction) Close() error { return errors.New("import: the backend closes after the import") }

// importPlace is a place in the archive's order: before the manifest, in a table group's puts or indexes, the
// documents, the end.
type importPlace int

const (
	placeManifest importPlace = iota
	placePuts
	placeIndexes
	placeDocuments
	placeEnded
)

// importDocument is the record whose revisions may follow, with the last revision's seq and its base's version.
type importDocument struct {
	record DocumentRecord
	seq    int64
	base   *int64
}

// archiveImport is one archive being imported: the place reached in its order (the manifest, then table groups of a
// `table` write, its `put`s and its `index` writes, then the documents, then `end`), what was seen (tables, ids, live
// addresses, the write count) and the step its writes are applied in.
type archiveImport struct {
	executor sqlite.SqliteExecutor
	ctx      context.Context
	position int
	manifest *ManifestPart
	step     *Step
	// place is where the next part may go.
	place importPlace
	// table is the table whose group is open.
	table  string
	tables map[string]bool
	ids    map[int64]bool
	// lifetimes are the lifetimes seen at each address, `[createdAt, retiredAt)` (MaxInt64 while live).
	lifetimes map[string][][2]int64
	document  *importDocument
	writes    int64
}

// take checks the next part and applies it.
func (archive *archiveImport) take(value any) error {
	position := archive.position
	archive.position++
	part, err := CheckPart(value, position, archive.ctx)
	if err != nil {
		return err
	}
	what := fmt.Sprintf("import: part %d (%s)", position, part.StoreWriteKind())
	if archive.place == placeEnded {
		return data.Invalid(data.ReasonArgs, what+": after the end")
	}
	if _, isManifest := part.(*ManifestPart); isManifest || archive.step == nil {
		return archive.start(part, what)
	}
	if end, isEnd := part.(*EndPart); isEnd {
		if end.Writes != archive.writes {
			return data.Invalid(data.ReasonArgs, fmt.Sprintf("%s: counts %d writes, the archive has %d", what, end.Writes, archive.writes))
		}
		if err := archive.endDocument(what); err != nil {
			return err
		}
		archive.place = placeEnded
		return nil
	}
	place, err := archive.placeOf(part, what)
	if err != nil {
		return err
	}
	archive.place = place
	archive.writes++
	return applyWrite(archive.step, part, what)
}

// finish is the stored metadata once the archive ended: its `lastSeq`, the allocator's next id and the outbox setting.
func (archive *archiveImport) finish(meta MetaRow, outbox bool) (MetaRow, error) {
	if archive.place != placeEnded || archive.step == nil || archive.manifest == nil {
		return MetaRow{}, data.Invalid(data.ReasonArgs, "import: the archive has no end part")
	}
	lastSeq, nextID := archive.manifest.LastSeq, archive.manifest.NextID
	if err := WriteCatalog(archive.step); err != nil {
		return MetaRow{}, err
	}
	flag := int64(0)
	if outbox {
		flag = 1
	}
	err := archive.executor.Run(
		`UPDATE `+TableMeta+` SET last_seq = ?, next_id = ?, outbox = ?, outbox_floor = ? WHERE singleton = 1`,
		lastSeq, nextID, flag, lastSeq,
	)
	if err != nil {
		return MetaRow{}, err
	}
	meta.LastSeq, meta.NextID, meta.Outbox, meta.OutboxFloor = lastSeq, nextID, flag, lastSeq
	return meta, nil
}

// start is the manifest, first: a format and schema this code reads.
func (archive *archiveImport) start(part ArchivePart, what string) error {
	manifest, isManifest := part.(*ManifestPart)
	if !isManifest || archive.step != nil {
		return data.Invalid(data.ReasonArgs, what+": the manifest comes first, once")
	}
	if manifest.Format != ArchiveFormat {
		return data.Invalid(data.ReasonArgs, fmt.Sprintf("%s: format %d is not %d", what, manifest.Format, ArchiveFormat))
	}
	if manifest.Schema > StoreSchema {
		return data.Invalid(data.ReasonArgs, fmt.Sprintf("%s: schema %d is newer than %d", what, manifest.Schema, StoreSchema))
	}
	archive.manifest = manifest
	archive.step = NewStep(archive.executor, manifest.LastSeq, NewIdAllocator(manifest.NextID), map[string]*TableRecord{})
	return nil
}

// placeOf is where `part` puts the archive, refused when out of order, a duplicate, or outside the manifest's ranges.
func (archive *archiveImport) placeOf(part ArchivePart, what string) (importPlace, error) {
	place := archive.place
	switch typed := part.(type) {
	case *TableDefinitionWrite:
		if place == placeDocuments {
			return 0, data.Invalid(data.ReasonArgs, what+": tables come before the documents")
		}
		if archive.tables[typed.Table] {
			return 0, data.Invalid(data.ReasonArgs, fmt.Sprintf("%s: table %s occurs twice", what, typed.Table))
		}
		archive.tables[typed.Table] = true
		archive.table = typed.Table
		return placePuts, nil
	case *PutWrite:
		if place != placePuts || typed.Table != archive.table {
			return 0, data.Invalid(data.ReasonArgs, what+": a put follows its table's table write, before its indexes")
		}
		return placePuts, nil
	case *IndexWrite:
		if (place != placePuts && place != placeIndexes) || typed.Table != archive.table {
			return 0, data.Invalid(data.ReasonArgs, what+": an index follows its table's table write and puts")
		}
		return placeIndexes, nil
	case *DocumentCreate:
		return placeDocuments, archive.checkDocument(typed, what)
	case *DocumentRevisionWrite:
		return placeDocuments, archive.checkRevision(typed, what)
	}
	// CheckPart admits no other kind.
	return 0, data.Invalid(data.ReasonArgs, what+": not an archive part")
}

// checkDocument is a record within the manifest's ids and seqs (an id equal to a saturated `nextId` too), unique, its
// lifetime overlapping no other at its address; its address and meta well-formed text without NUL (SQLite would cut
// a NUL off, slice 6a §8). The record before it must have ended well (`endDocument`).
func (archive *archiveImport) checkDocument(part *DocumentCreate, what string) error {
	if err := archive.endDocument(what); err != nil {
		return err
	}
	record := part.Record
	lastSeq, nextID := archive.manifest.LastSeq, archive.manifest.NextID
	saturated := nextID == MaxSafeInteger && record.ID == nextID
	if record.ID >= nextID && !saturated {
		return data.Invalid(data.ReasonArgs, fmt.Sprintf("%s: id %d is not below %d", what, record.ID, nextID))
	}
	if archive.ids[record.ID] {
		return data.Invalid(data.ReasonArgs, fmt.Sprintf("%s: id %d occurs twice", what, record.ID))
	}
	createdAt := record.CreatedAt
	if createdAt > lastSeq || (record.RetiredAt != nil && (*record.RetiredAt < createdAt || *record.RetiredAt > lastSeq)) {
		return data.Invalid(data.ReasonArgs, what+": a lifetime outside the manifest's lastSeq")
	}
	// A singleton's record has no key; its column keeps `""`.
	key := ""
	if record.Key != nil {
		key = *record.Key
	}
	for _, text := range []string{record.Namespace, record.Owner, record.Kind, key} {
		stored, err := StoredText(text, what)
		if err != nil {
			return err
		}
		if stored != text {
			return data.Invalid(data.ReasonArgs, what+": an address is not well-formed text")
		}
	}
	if record.Meta != nil {
		stored, err := StoredText(*record.Meta, what)
		if err != nil {
			return err
		}
		if stored != *record.Meta {
			return data.Invalid(data.ReasonArgs, what+": the meta is not well-formed text")
		}
	}
	encoded, err := jsstring.MarshalJSON([]string{record.Owner, record.Namespace, record.Kind, key})
	if err != nil {
		return err
	}
	address := string(encoded)
	// math.MaxInt64 stands for Pi's Infinity: a live record's lifetime never ends.
	lifetime := [2]int64{createdAt, math.MaxInt64}
	if record.RetiredAt != nil {
		lifetime[1] = *record.RetiredAt
	}
	seen := archive.lifetimes[address]
	// Two live records are two infinite lifetimes, which overlap; an empty one (`createdAt = retiredAt`) overlaps none.
	if lifetime[0] < lifetime[1] {
		for _, other := range seen {
			if other[0] < lifetime[1] && lifetime[0] < other[1] {
				return data.Invalid(data.ReasonArgs, what+": a second record at one address for an overlapping lifetime")
			}
		}
	}
	archive.lifetimes[address] = append(seen, lifetime)
	archive.ids[record.ID] = true
	archive.document = &importDocument{record: record}
	return nil
}

// checkRevision is a revision of the record before it, in seq order within its lifetime, from a base, each delta at
// its base's version.
func (archive *archiveImport) checkRevision(part *DocumentRevisionWrite, what string) error {
	document := archive.document
	if document == nil || document.record.ID != part.ID {
		return data.Invalid(data.ReasonArgs, what+": a revision follows its record's create")
	}
	record := document.record
	if record.RetiredAt != nil && record.History != "rewindable" {
		return data.Invalid(data.ReasonArgs, fmt.Sprintf("%s: a retired %s record keeps no revisions", what, record.History))
	}
	last := archive.manifest.LastSeq
	if record.RetiredAt != nil {
		last = *record.RetiredAt
	}
	if part.Seq <= document.seq || part.Seq < record.CreatedAt || part.Seq > last {
		return data.Invalid(data.ReasonArgs, what+": a revision out of seq order or outside its lifetime")
	}
	content := part.Content
	if content.Kind == "base" {
		version := content.Version
		document.base = &version
	} else if document.base == nil || *document.base != content.Version {
		return data.Invalid(data.ReasonArgs, what+": a delta without a base at its version")
	}
	document.seq = part.Seq
	return nil
}

// endDocument: the record before ends well: its last base is at the record's version; only a retired `latest` record
// has none (its content was dropped at retirement; a retired `rewindable` one keeps its history).
func (archive *archiveImport) endDocument(what string) error {
	document := archive.document
	archive.document = nil
	if document == nil {
		return nil
	}
	record := document.record
	contentless := record.RetiredAt != nil && record.History == "latest"
	var broken bool
	if document.base == nil {
		broken = !contentless
	} else {
		broken = *document.base != record.Version
	}
	if broken {
		return data.Invalid(data.ReasonArgs, fmt.Sprintf("%s: record %d has no base at its version", what, record.ID))
	}
	return nil
}

// importNext is the result of one Next of the input.
type importNext struct {
	value any
	ok    bool
	err   error
}

// importEach runs `take` on each part in turn; a wait for the next part ends when `ctx` aborts (the input is closed).
func importEach(parts PartSource, ctx context.Context, take func(part any) error) error {
	done := false
	defer func() {
		// A stalled input is not awaited: the backend transaction ends now.
		if !done {
			go func() { _ = parts.Close() }()
		}
	}()
	for {
		settled := make(chan importNext, 1)
		go func() {
			value, ok, err := parts.Next(ctx)
			settled <- importNext{value, ok, err}
		}()
		var result importNext
		select {
		case result = <-settled:
		case <-ctx.Done():
			return importAborted(ctx)
		}
		if result.err != nil {
			if ctx.Err() != nil {
				return importAborted(ctx)
			}
			return result.err
		}
		if !result.ok {
			done = true
			return nil
		}
		if err := take(result.value); err != nil {
			return err
		}
	}
}

// importAborted is the error of an import `ctx` aborted: `aborted`, the context's cause as its cause.
func importAborted(ctx context.Context) error {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeAborted, Message: "import: aborted"}, context.Cause(ctx))
}
