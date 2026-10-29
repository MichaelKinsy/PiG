package store

import (
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/store/engine.ts
//
// The documents engine (S13, slice 6a §8, slice 6b §1.5): one record per incarnation of an address, revisions made of
// bases and deltas, a history rule per record (R1); its writes inside the commit step, checked by the writes
// themselves (the partial unique index on the live address, the counted conditional `UPDATE` on the record's
// revision), and its reads, of the current state or at a point.
//
// Go forced differences:
//   - Pi's `DocumentWrite` union is four pointer types (*DocumentCreate, *DocumentChange, *DocumentRetire,
//     *DocumentRevisionWrite), each a StoreWrite whose JSON is Pi's, in Pi's key order. `DocumentRecord` is flat (Pi
//     spreads a `DocumentAddress` into it); an absent optional field (`key`, `retiredAt`, `meta`) is a nil pointer.
//   - Pi's `DocumentPoint = number | "current"` is a struct whose zero value is "current"; AtSeq(n) is the number.
//   - A read takes the narrow executor it uses (rowGetter, rowLister): a SqliteExecutor and a *Step both are one.
//   - Async contract: every Pi function here is `async` over one backend call sequence; here each blocks and returns
//     `(value, error)` in the same order. Nothing runs concurrently: a write runs inside the commit step, a read on
//     the caller's goroutine through the store's guarded reads.

// rowGetter is what a point read needs of an executor.
type rowGetter interface {
	Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error)
}

// rowLister is what a list read needs of an executor.
type rowLister interface {
	All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error)
}

// DocumentAddress is a document's address: the owner name, the adapter's namespace (claimed with the owner: Chord's
// `chord`, `chord.jobs`, `chord.job-states`; Durable's scopes), the kind and, for a keyed document, its key; a
// singleton has none (nil). The column keeps `""` for it, which a keyed document's empty key shares (its definition
// says which it is).
type DocumentAddress struct {
	Owner     string
	Namespace string
	Kind      string
	Key       *string
}

// DocumentHistory is what a record keeps (R1), applied at a new base and at retirement: `none` (Chord) drops older
// revisions at a new base and the row and its revisions at retirement; `latest` drops older revisions at a new base
// and its revisions at retirement, keeping the row; `rewindable` keeps everything.
type DocumentHistory string

// The history rules.
const (
	HistoryNone       DocumentHistory = "none"
	HistoryLatest     DocumentHistory = "latest"
	HistoryRewindable DocumentHistory = "rewindable"
)

// DocumentRecord is a stored record: one incarnation of an address, live from CreatedAt until RetiredAt
// (half-open). Fields serialize in Pi's key order.
type DocumentRecord struct {
	ID        int64   `json:"id"`
	Owner     string  `json:"owner"`
	Namespace string  `json:"namespace"`
	Kind      string  `json:"kind"`
	Key       *string `json:"key,omitempty"`
	// History is the record's history rule.
	History DocumentHistory `json:"history"`
	// CreatedAt is the seq that created it.
	CreatedAt int64 `json:"createdAt"`
	// RetiredAt is the seq that retired it, once retired.
	RetiredAt *int64 `json:"retiredAt,omitempty"`
	// Meta is the adapter's details (Durable: its fork policy and history).
	Meta *string `json:"meta,omitempty"`
	// Revision counts its changes: a change names the revision it was prepared on.
	Revision int64 `json:"revision"`
	// Version and Fingerprint are the version and schema fingerprint of its current base.
	Version     int64  `json:"version"`
	Fingerprint string `json:"fingerprint"`
}

// Address is the record's address.
func (record DocumentRecord) Address() DocumentAddress {
	return DocumentAddress{Owner: record.Owner, Namespace: record.Namespace, Kind: record.Kind, Key: record.Key}
}

// DocumentBase is a base: a whole value at a version.
type DocumentBase struct {
	Version     int64           `json:"version"`
	Fingerprint string          `json:"fingerprint"`
	Value       delta.JsonValue `json:"value"`
}

// The content kinds of a revision and of a change.
const (
	ContentBase  = "base"
	ContentDelta = "delta"
)

// DocumentContent is a change's content: a new base (Kind "base": Version, Fingerprint, Value), or a delta on the
// current base's version (Kind "delta": Version, Ops).
type DocumentContent struct {
	Kind        string
	Version     int64
	Fingerprint string
	Value       delta.JsonValue
	Ops         []delta.Op
}

// MarshalJSON is the content in Pi's key order.
func (content DocumentContent) MarshalJSON() ([]byte, error) {
	if content.Kind == ContentBase {
		return jsstring.MarshalJSON(delta.JsonObjectOf("kind", ContentBase, "version", content.Version, "fingerprint", content.Fingerprint, "value", content.Value))
	}
	return jsstring.MarshalJSON(delta.JsonObjectOf("kind", content.Kind, "version", content.Version, "ops", opsOrEmpty(content.Ops)))
}

// RevisionContent is a stored revision's content: a base's value (Kind "base": Version, Value) or a delta's ops (Kind
// "delta": Version, Ops), at a version (a revision keeps no fingerprint).
type RevisionContent struct {
	Kind    string
	Version int64
	Value   delta.JsonValue
	Ops     []delta.Op
}

// MarshalJSON is the content in Pi's key order.
func (content RevisionContent) MarshalJSON() ([]byte, error) {
	if content.Kind == ContentBase {
		return jsstring.MarshalJSON(delta.JsonObjectOf("kind", ContentBase, "version", content.Version, "value", content.Value))
	}
	return jsstring.MarshalJSON(delta.JsonObjectOf("kind", content.Kind, "version", content.Version, "ops", opsOrEmpty(content.Ops)))
}

func opsOrEmpty(ops []delta.Op) []delta.Op {
	if ops == nil {
		return []delta.Op{}
	}
	return ops
}

// DocumentRevision is one stored revision as archives carry it: its document, its seq, its content.
type DocumentRevision struct {
	ID      int64           `json:"id"`
	Seq     int64           `json:"seq"`
	Content RevisionContent `json:"content"`
}

// DocumentCreate is the `document.create` write. A creation carries its base in a commit (stored at CreatedAt); in an
// archive it has none (nil) and its `document.revision`s follow it.
type DocumentCreate struct {
	Record DocumentRecord
	Base   *DocumentBase
}

// StoreWriteKind is "document.create".
func (*DocumentCreate) StoreWriteKind() string { return "document.create" }

type documentCreateJSON struct {
	Kind   string         `json:"kind"`
	Record DocumentRecord `json:"record"`
	Base   *DocumentBase  `json:"base,omitempty"`
}

// MarshalJSON is the write in Pi's key order.
func (write *DocumentCreate) MarshalJSON() ([]byte, error) {
	return jsstring.MarshalJSON(documentCreateJSON{"document.create", write.Record, write.Base})
}

// DocumentChange is the `document.change` write: a change of document ID prepared on Revision.
type DocumentChange struct {
	ID       int64
	Revision int64
	Content  DocumentContent
}

// StoreWriteKind is "document.change".
func (*DocumentChange) StoreWriteKind() string { return "document.change" }

type documentChangeJSON struct {
	Kind     string          `json:"kind"`
	ID       int64           `json:"id"`
	Revision int64           `json:"revision"`
	Content  DocumentContent `json:"content"`
}

// MarshalJSON is the write in Pi's key order.
func (write *DocumentChange) MarshalJSON() ([]byte, error) {
	return jsstring.MarshalJSON(documentChangeJSON{"document.change", write.ID, write.Revision, write.Content})
}

// DocumentRetire is the `document.retire` write.
type DocumentRetire struct {
	ID       int64
	Revision int64
}

// StoreWriteKind is "document.retire".
func (*DocumentRetire) StoreWriteKind() string { return "document.retire" }

type documentRetireJSON struct {
	Kind     string `json:"kind"`
	ID       int64  `json:"id"`
	Revision int64  `json:"revision"`
}

// MarshalJSON is the write in Pi's key order.
func (write *DocumentRetire) MarshalJSON() ([]byte, error) {
	return jsstring.MarshalJSON(documentRetireJSON{"document.retire", write.ID, write.Revision})
}

// DocumentRevisionWrite is the `document.revision` part of an archive: Pi's `{ kind } & DocumentRevision`.
type DocumentRevisionWrite struct {
	ID      int64
	Seq     int64
	Content RevisionContent
}

// StoreWriteKind is "document.revision".
func (*DocumentRevisionWrite) StoreWriteKind() string { return "document.revision" }

type documentRevisionJSON struct {
	Kind    string          `json:"kind"`
	ID      int64           `json:"id"`
	Seq     int64           `json:"seq"`
	Content RevisionContent `json:"content"`
}

// MarshalJSON is the write in Pi's key order.
func (write *DocumentRevisionWrite) MarshalJSON() ([]byte, error) {
	return jsstring.MarshalJSON(documentRevisionJSON{"document.revision", write.ID, write.Seq, write.Content})
}

// StoredDocument is a loaded document: its record and its value (a base plus deltas applied once) at a point.
type StoredDocument struct {
	Record DocumentRecord
	Value  delta.JsonValue
	// Version is the version of the base the value was built from.
	Version int64
	// DeltasSinceBase are the deltas applied after its base (what `checkpointWhen` sees).
	DeltasSinceBase int
}

// DocumentPoint is where a read looks: the current state (the zero value), or the state after a seq (AtSeq).
type DocumentPoint struct {
	// At is set when Seq is the point.
	At  bool
	Seq int64
}

// CurrentPoint is the current state.
var CurrentPoint = DocumentPoint{}

// AtSeq is the state after seq.
func AtSeq(seq int64) DocumentPoint { return DocumentPoint{At: true, Seq: seq} }

// stagedFunc is a StagedWrite from its order and its apply.
type stagedFunc struct {
	order WriteOrder
	apply func(step *Step) ([]StoreWrite, error)
}

func (write stagedFunc) StageOrder() WriteOrder { return write.order }

func (write stagedFunc) Apply(step *Step) ([]StoreWrite, error) { return write.apply(step) }

// CreateDocument stages the creation of a Chord document at address with base; its id is taken from the allocator
// when it applies (a saturated allocator refuses the commit) and handed to created (its owner adopts it after
// `COMMIT`; nil for none). text is the base's value serialized before the queue (rule 8).
func CreateDocument(address DocumentAddress, base DocumentBase, text string, created func(id int64)) StagedWrite {
	return stagedFunc{order: OrderCreate, apply: func(step *Step) ([]StoreWrite, error) {
		id, err := step.IDs.Mint()
		if err != nil {
			return nil, err
		}
		step.Created = append(step.Created, id)
		if created != nil {
			created(id)
		}
		record := DocumentRecord{
			ID: id, Owner: address.Owner, Namespace: address.Namespace, Kind: address.Kind, Key: address.Key,
			History: HistoryNone, CreatedAt: step.Seq, Revision: 1, Version: base.Version, Fingerprint: base.Fingerprint,
		}
		if err := InsertDocument(step, record, &text); err != nil {
			return nil, err
		}
		return []StoreWrite{&DocumentCreate{Record: record, Base: &base}}, nil
	}}
}

// InsertDocument inserts record as it is (id, seqs and revision kept: a creation, an import, a batch), with its base
// at CreatedAt when text (the base's value serialized) is given (non-nil); a record created retired (an empty
// lifetime) keeps what its history keeps.
func InsertDocument(step *Step, record DocumentRecord, text *string) error {
	key := ""
	if record.Key != nil {
		key = *record.Key
	}
	var retiredAt, meta any
	if record.RetiredAt != nil {
		retiredAt = *record.RetiredAt
	}
	if record.Meta != nil {
		meta = *record.Meta
	}
	err := step.Run(
		`INSERT INTO `+TableDocuments+`
			(id, owner, namespace, kind, key, history, created_at, retired_at, meta, revision, version, fingerprint)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.Owner, record.Namespace, record.Kind, key, string(record.History), record.CreatedAt, retiredAt, meta,
		record.Revision, record.Version, record.Fingerprint,
	)
	if err != nil {
		return err
	}
	if text != nil {
		if err := InsertRevision(step, record.ID, record.CreatedAt, ContentBase, record.Version, *text); err != nil {
			return err
		}
	}
	if record.RetiredAt != nil {
		return dropRetired(step, record.ID, record.History)
	}
	return nil
}

// ChangeDocument stages a change of Chord document id prepared on revision: a delta on the current base's version, or
// a new base. text is the content serialized before the queue.
func ChangeDocument(id, revision int64, content DocumentContent, text string) StagedWrite {
	write := &DocumentChange{ID: id, Revision: revision, Content: content}
	return stagedFunc{order: OrderWrite, apply: func(step *Step) ([]StoreWrite, error) {
		if err := WriteContent(step, id, HistoryNone, revision, content, text); err != nil {
			return nil, err
		}
		return []StoreWrite{write}, nil
	}}
}

// WriteContent writes content to document id (of history) prepared on revision, in the step's seq: its revision, the
// counted conditional `UPDATE` (a delta only on the base's version), and at a new base the older revisions a `none`
// or `latest` record drops. A record that is not live at revision fails (memory and disk disagree).
func WriteContent(step *Step, id int64, history DocumentHistory, revision int64, content DocumentContent, text string) error {
	// As ids: a revision past the safe range would stop counting exactly.
	if revision >= MaxSafeInteger {
		return fmt.Errorf("document %d: no revision past the safe range", id)
	}
	if err := InsertRevision(step, id, step.Seq, content.Kind, content.Version, text); err != nil {
		return err
	}
	var changed int64
	var err error
	if content.Kind == ContentDelta {
		changed, err = step.RunCounted(
			`UPDATE `+TableDocuments+` SET revision = revision + 1
				WHERE id = ? AND revision = ? AND version = ? AND retired_at IS NULL`,
			id, revision, content.Version,
		)
	} else {
		changed, err = step.RunCounted(
			`UPDATE `+TableDocuments+` SET revision = revision + 1, version = ?, fingerprint = ?
				WHERE id = ? AND revision = ? AND retired_at IS NULL`,
			content.Version, content.Fingerprint, id, revision,
		)
	}
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("document %d: not live at revision %d", id, revision)
	}
	if content.Kind == ContentBase && history != HistoryRewindable {
		return step.Run(`DELETE FROM `+TableRevisions+` WHERE document_id = ? AND seq < ?`, id, step.Seq)
	}
	return nil
}

// RetireDocument stages the retirement of Chord document id (history `none`) at revision: its row and its revisions
// go.
func RetireDocument(id, revision int64) StagedWrite {
	write := &DocumentRetire{ID: id, Revision: revision}
	return stagedFunc{order: OrderRetire, apply: func(step *Step) ([]StoreWrite, error) {
		// A Chord document keeps no history (`none`): its row goes with a conditional, counted DELETE (6a's).
		deleted, err := step.RunCounted(`DELETE FROM `+TableDocuments+` WHERE id = ? AND revision = ? AND retired_at IS NULL`, id, revision)
		if err != nil {
			return nil, err
		}
		if deleted != 1 {
			return nil, fmt.Errorf("document %d: not live at revision %d", id, revision)
		}
		if err := step.Run(`DELETE FROM `+TableRevisions+` WHERE document_id = ?`, id); err != nil {
			return nil, err
		}
		return []StoreWrite{write}, nil
	}}
}

// RetireRecord retires live record in the step's seq by its history (R1): a `none` record's row and revisions go, a
// `latest` one keeps its row with `retired_at` and drops its revisions, a `rewindable` one keeps everything.
func RetireRecord(step *Step, record DocumentRecord) error {
	if record.History != HistoryNone {
		if err := step.Run(`UPDATE `+TableDocuments+` SET retired_at = ? WHERE id = ?`, step.Seq, record.ID); err != nil {
			return err
		}
	}
	return dropRetired(step, record.ID, record.History)
}

// dropRetired is what a retired record of history drops: a `none` row too, every revision unless `rewindable`.
func dropRetired(step *Step, id int64, history DocumentHistory) error {
	if history == HistoryRewindable {
		return nil
	}
	if history == HistoryNone {
		if err := step.Run(`DELETE FROM `+TableDocuments+` WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return step.Run(`DELETE FROM `+TableRevisions+` WHERE document_id = ?`, id)
}

// InsertRevision inserts one revision of document id at seq, text its serialized content.
func InsertRevision(step *Step, id, seq int64, kind string, version int64, text string) error {
	return step.Run(
		`INSERT INTO `+TableRevisions+` (document_id, seq, kind, version, content) VALUES (?, ?, ?, ?, ?)`,
		id, seq, kind, version, text,
	)
}

const recordColumns = "id, owner, namespace, kind, key, history, created_at, retired_at, meta, revision, version, fingerprint"

const readSQL = `SELECT ` + recordColumns + ` FROM ` + TableDocuments + ` WHERE id = ?`

// ReadDocument is the record with id, live or retired; nil when there is none.
func ReadDocument(executor rowGetter, id int64) (*DocumentRecord, error) {
	row, err := executor.Get(readSQL, id)
	if err != nil || row == nil {
		return nil, err
	}
	return storedRecordOf(row)
}

const findCurrentSQL = `SELECT ` + recordColumns + ` FROM ` + TableDocuments + `
	WHERE owner = ? AND namespace = ? AND kind = ? AND key = ? AND retired_at IS NULL`

const findAtSQL = `SELECT ` + recordColumns + ` FROM ` + TableDocuments + `
	WHERE owner = ? AND namespace = ? AND kind = ? AND key = ? AND created_at <= ? AND (retired_at IS NULL OR retired_at > ?)
	ORDER BY created_at DESC LIMIT 1`

// addressKey is the key column of address: `""` for a singleton.
func addressKey(address DocumentAddress) string {
	if address.Key == nil {
		return ""
	}
	return *address.Key
}

// FindDocument is the record at address live at `at` (the half-open lifetime `createdAt <= at < retiredAt`); nil if
// none.
func FindDocument(executor rowGetter, address DocumentAddress, at DocumentPoint) (*DocumentRecord, error) {
	var row sqlite.SqliteRow
	var err error
	if !at.At {
		row, err = executor.Get(findCurrentSQL, address.Owner, address.Namespace, address.Kind, addressKey(address))
	} else {
		row, err = executor.Get(findAtSQL, address.Owner, address.Namespace, address.Kind, addressKey(address), at.Seq, at.Seq)
	}
	if err != nil || row == nil {
		return nil, err
	}
	return storedRecordOf(row)
}

// DocumentEntry is a record as an adapter that keeps its own fields in `meta` reads it: the meta, and, where a read
// can find retired records (at a point, by id), when it was retired. One or two columns: each column a read returns
// costs. Meta is nil for SQL NULL; RetiredAt is nil for SQL NULL or a read that does not select it.
type DocumentEntry struct {
	Meta      *string
	RetiredAt *int64
}

// OwnedDocumentEntry is a document entry with what tells whose it is and what it keeps, as a read by id needs.
type OwnedDocumentEntry struct {
	DocumentEntry
	Owner     string
	Namespace string
	History   DocumentHistory
}

const readEntrySQL = `SELECT meta, retired_at, owner, namespace, history FROM ` + TableDocuments + ` WHERE id = ?`

// ReadDocumentEntry is the entry of the record with id, live or retired, with its owner, namespace and history; nil
// if none.
func ReadDocumentEntry(executor rowGetter, id int64) (*OwnedDocumentEntry, error) {
	row, err := executor.Get(readEntrySQL, id)
	if err != nil || row == nil {
		return nil, err
	}
	entry, err := entryOf(row)
	if err != nil {
		return nil, err
	}
	owner, _ := row["owner"].(string)
	namespace, _ := row["namespace"].(string)
	history, _ := row["history"].(string)
	return &OwnedDocumentEntry{DocumentEntry: entry, Owner: owner, Namespace: namespace, History: DocumentHistory(history)}, nil
}

const findEntryCurrentSQL = `SELECT meta FROM ` + TableDocuments + `
	WHERE owner = ? AND namespace = ? AND kind = ? AND key = ? AND retired_at IS NULL`

const findEntryAtSQL = `SELECT meta, retired_at FROM ` + TableDocuments + `
	WHERE owner = ? AND namespace = ? AND kind = ? AND key = ? AND created_at <= ? AND (retired_at IS NULL OR retired_at > ?)
	ORDER BY created_at DESC LIMIT 1`

// FindDocumentEntry is FindDocument reading only what an adapter keeping its fields in `meta` needs (fewer columns,
// no record built).
func FindDocumentEntry(executor rowGetter, address DocumentAddress, at DocumentPoint) (*DocumentEntry, error) {
	var row sqlite.SqliteRow
	var err error
	if !at.At {
		row, err = executor.Get(findEntryCurrentSQL, address.Owner, address.Namespace, address.Kind, addressKey(address))
	} else {
		row, err = executor.Get(findEntryAtSQL, address.Owner, address.Namespace, address.Kind, addressKey(address), at.Seq, at.Seq)
	}
	if err != nil || row == nil {
		return nil, err
	}
	entry, err := entryOf(row)
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

// entryOf reads the `meta` and `retired_at` columns a row has.
func entryOf(row sqlite.SqliteRow) (DocumentEntry, error) {
	var entry DocumentEntry
	if meta, ok := row["meta"].(string); ok {
		entry.Meta = &meta
	}
	if row["retired_at"] != nil {
		retiredAt, err := sqlite.RowInt(row, "retired_at")
		if err != nil {
			return DocumentEntry{}, err
		}
		entry.RetiredAt = &retiredAt
	}
	return entry, nil
}

const loadSQL = `SELECT seq, kind, version, content FROM ` + TableRevisions + ` WHERE document_id = ? AND seq <= ? AND seq >= (
	SELECT max(seq) FROM ` + TableRevisions + ` WHERE document_id = ? AND kind = 'base' AND seq <= ?) ORDER BY seq`

// LoadDocument loads record at `at`: its newest base at or below it and the deltas after that base up to it, applied
// once; the version is the base's. A record without such a base (one that keeps no history from there) fails.
func LoadDocument(executor rowLister, record DocumentRecord, at DocumentPoint) (StoredDocument, error) {
	content, err := LoadContent(executor, record.ID, at)
	if err != nil {
		return StoredDocument{}, err
	}
	return StoredDocument{Record: record, Value: content.Value, Version: content.Version, DeltasSinceBase: content.DeltasSinceBase}, nil
}

// DocumentContentAt is LoadDocument's content without its record.
type DocumentContentAt struct {
	Value           delta.JsonValue
	Version         int64
	DeltasSinceBase int
}

// LoadContent is LoadDocument's content of document id at `at`, without its record (an adapter reads the fields it
// keeps).
func LoadContent(executor rowLister, id int64, at DocumentPoint) (DocumentContentAt, error) {
	upper := MaxSafeInteger
	if at.At {
		upper = at.Seq
	}
	revisions, err := executor.All(loadSQL, id, upper, id, upper)
	if err != nil {
		return DocumentContentAt{}, err
	}
	if len(revisions) == 0 || revisions[0]["kind"] != ContentBase {
		return DocumentContentAt{}, fmt.Errorf("document %d: no base", id)
	}
	baseVersion, err := sqlite.RowInt(revisions[0], "version")
	if err != nil {
		return DocumentContentAt{}, err
	}
	text, _ := revisions[0]["content"].(string)
	value, err := delta.DecodeJson([]byte(text))
	if err != nil {
		return DocumentContentAt{}, err
	}
	deltas := revisions[1:]
	for _, revision := range deltas {
		version, err := sqlite.RowInt(revision, "version")
		if err != nil {
			return DocumentContentAt{}, err
		}
		if version != baseVersion {
			return DocumentContentAt{}, fmt.Errorf("document %d: a delta crosses a version", id)
		}
		ops, err := decodeOps(revision["content"])
		if err != nil {
			return DocumentContentAt{}, err
		}
		if value, err = delta.Apply(value, ops); err != nil {
			return DocumentContentAt{}, err
		}
	}
	return DocumentContentAt{Value: value, Version: baseVersion, DeltasSinceBase: len(deltas)}, nil
}

// decodeOps is the ops of a stored delta: `JSON.parse(content) as Op[]`.
func decodeOps(content any) ([]delta.Op, error) {
	text, _ := content.(string)
	parsed, err := delta.DecodeJson([]byte(text))
	if err != nil {
		return nil, err
	}
	items, ok := parsed.([]any)
	if !ok {
		return nil, errors.New("store: a delta's content is no array")
	}
	ops := make([]delta.Op, len(items))
	for index, item := range items {
		op, ok := item.([]any)
		if !ok {
			return nil, errors.New("store: a delta's op is no array")
		}
		ops[index] = op
	}
	return ops, nil
}

// ListDocumentKeys is up to limit keys of live documents of kind under owner after `after` (nil: from the first), in
// code point order.
func ListDocumentKeys(executor rowLister, address DocumentAddress, after *string, limit int64) ([]string, error) {
	// The first page has no lower bound: `""` is a key too.
	from := ""
	params := []sqlite.SqliteValue{address.Owner, address.Namespace, address.Kind}
	if after != nil {
		from = " AND key > ?"
		params = append(params, *after)
	}
	params = append(params, limit)
	rows, err := executor.All(
		`SELECT key FROM `+TableDocuments+` WHERE owner = ? AND namespace = ? AND kind = ?`+from+`
			AND retired_at IS NULL ORDER BY key LIMIT ?`,
		params...,
	)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(rows))
	for index, row := range rows {
		keys[index], _ = row["key"].(string)
	}
	return keys, nil
}

// DocumentScope is what ListDocuments scans: one owner's namespace, optionally one kind (nil: every kind).
type DocumentScope struct {
	Owner     string
	Namespace string
	Kind      *string
}

// The statements of ListDocuments, by its shape: with a kind or not, current or at a point.
const (
	listFrom    = `SELECT ` + recordColumns + ` FROM ` + TableDocuments + ` WHERE owner = ? AND namespace = ? AND id > ?`
	listCurrent = ` AND retired_at IS NULL ORDER BY id LIMIT ?`
	listAt      = ` AND created_at <= ? AND (retired_at IS NULL OR retired_at > ?) ORDER BY id LIMIT ?`
)

// ListDocuments is up to limit records of scope live at `at` with ids above after, by id.
func ListDocuments(executor rowLister, scope DocumentScope, at DocumentPoint, after, limit int64) ([]DocumentRecord, error) {
	sql := listFrom
	params := []sqlite.SqliteValue{scope.Owner, scope.Namespace, after}
	if scope.Kind != nil {
		sql += ` AND kind = ?`
		params = append(params, *scope.Kind)
	}
	if !at.At {
		sql += listCurrent
	} else {
		sql += listAt
		params = append(params, at.Seq, at.Seq)
	}
	params = append(params, limit)
	rows, err := executor.All(sql, params...)
	if err != nil {
		return nil, err
	}
	return storedRecordsOf(rows)
}

// ListDocumentEntries is ListDocuments reading only what an adapter keeping its fields in `meta` needs: `meta`, and
// `retired_at` where a read can find retired records (at a point).
func ListDocumentEntries(executor rowLister, scope DocumentScope, at DocumentPoint, after, limit int64) ([]DocumentEntry, error) {
	columns := "meta"
	if at.At {
		columns = "meta, retired_at"
	}
	sql := `SELECT ` + columns + ` FROM ` + TableDocuments + ` WHERE owner = ? AND namespace = ? AND id > ?`
	params := []sqlite.SqliteValue{scope.Owner, scope.Namespace, after}
	if scope.Kind != nil {
		sql += ` AND kind = ?`
		params = append(params, *scope.Kind)
	}
	if !at.At {
		sql += listCurrent
	} else {
		sql += listAt
		params = append(params, at.Seq, at.Seq)
	}
	params = append(params, limit)
	rows, err := executor.All(sql, params...)
	if err != nil {
		return nil, err
	}
	entries := make([]DocumentEntry, len(rows))
	for index, row := range rows {
		if entries[index], err = entryOf(row); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

// ListRecords is up to limit stored records, live and retired, with ids above after, by id (an export's pages).
func ListRecords(executor rowLister, after, limit int64) ([]DocumentRecord, error) {
	rows, err := executor.All(`SELECT `+recordColumns+` FROM `+TableDocuments+` WHERE id > ? ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	return storedRecordsOf(rows)
}

// ListRevisions is up to limit stored revisions of document id after seq `after`, in seq order (an export's page).
func ListRevisions(executor rowLister, id, after, limit int64) ([]DocumentRevision, error) {
	rows, err := executor.All(
		`SELECT seq, kind, version, content FROM `+TableRevisions+` WHERE document_id = ? AND seq > ? ORDER BY seq LIMIT ?`,
		id, after, limit,
	)
	if err != nil {
		return nil, err
	}
	revisions := make([]DocumentRevision, len(rows))
	for index, row := range rows {
		seq, err := sqlite.RowInt(row, "seq")
		if err != nil {
			return nil, err
		}
		version, err := sqlite.RowInt(row, "version")
		if err != nil {
			return nil, err
		}
		kind, _ := row["kind"].(string)
		content := RevisionContent{Kind: kind, Version: version}
		// Written by InsertRevision: a base's value or a delta's ops, as JSON.
		if kind == ContentBase {
			text, _ := row["content"].(string)
			if content.Value, err = delta.DecodeJson([]byte(text)); err != nil {
				return nil, err
			}
		} else if content.Ops, err = decodeOps(row["content"]); err != nil {
			return nil, err
		}
		revisions[index] = DocumentRevision{ID: id, Seq: seq, Content: content}
	}
	return revisions, nil
}

func storedRecordsOf(rows []sqlite.SqliteRow) ([]DocumentRecord, error) {
	records := make([]DocumentRecord, len(rows))
	for index, row := range rows {
		record, err := storedRecordOf(row)
		if err != nil {
			return nil, err
		}
		records[index] = *record
	}
	return records, nil
}

// storedRecordOf is a stored row as a record: `""` is a singleton's key (absent); NULLs are absent fields.
func storedRecordOf(row sqlite.SqliteRow) (*DocumentRecord, error) {
	record := &DocumentRecord{}
	var err error
	if record.ID, err = sqlite.RowInt(row, "id"); err != nil {
		return nil, err
	}
	record.Owner, _ = row["owner"].(string)
	record.Namespace, _ = row["namespace"].(string)
	record.Kind, _ = row["kind"].(string)
	if key, _ := row["key"].(string); key != "" {
		record.Key = &key
	}
	history, _ := row["history"].(string)
	record.History = DocumentHistory(history)
	if record.CreatedAt, err = sqlite.RowInt(row, "created_at"); err != nil {
		return nil, err
	}
	if row["retired_at"] != nil {
		retiredAt, err := sqlite.RowInt(row, "retired_at")
		if err != nil {
			return nil, err
		}
		record.RetiredAt = &retiredAt
	}
	if meta, ok := row["meta"].(string); ok {
		record.Meta = &meta
	}
	if record.Revision, err = sqlite.RowInt(row, "revision"); err != nil {
		return nil, err
	}
	if record.Version, err = sqlite.RowInt(row, "version"); err != nil {
		return nil, err
	}
	record.Fingerprint, _ = row["fingerprint"].(string)
	return record, nil
}
