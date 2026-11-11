package documents

import (
	"context"
	"fmt"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/next/documents/persistence.ts
//
// Persisted documents' store side (slice 6a §8, §10): a slot's address in the documents engine, what is stored of each
// loaded document (Durable's `LoadedDocument` fields), its loads with the version and fingerprint checks and the
// checked `migrate`, and the writes one commit stages for a changed or retired document.
//
// Async contract: Read, Find and Keys block on one guarded store read (cancellable by ctx as store.Read is); Decode
// and Stage are synchronous.

// Stored is what is stored of a loaded document: its record's id and revision, its base's version, the deltas after it.
type Stored struct {
	ID       int64
	Revision int64
	// Version is the version of the stored base: below the definition's, the next change stores a base (rule 1).
	Version         int64
	DeltasSinceBase int
}

// FoundDocument is a live document found without loading it: what is stored of it and the fingerprint it was stored with.
type FoundDocument struct {
	Stored      Stored
	Fingerprint string
}

// Loaded is a loaded document: its value at the definition's version, and what is stored of it.
type Loaded struct {
	Value  *JsonObject
	Stored Stored
}

// DocumentChange is one transaction's change of one document: its prepared draft (if changed), and whether it retires it.
type DocumentChange struct {
	Prepared *delta.Prepared
	Retires  bool
}

// Held is a run state's held ops (D8), stored before the next change's own; Base when a held change asked for a base.
type Held struct {
	Ops  []delta.Op
	Base bool
}

// StoredChangeKind is what one commit stores of a document.
type StoredChangeKind string

// The kinds of StoredChange.
const (
	StoredRetire  StoredChangeKind = "retire"
	StoredCreate  StoredChangeKind = "create"
	StoredChanged StoredChangeKind = "change"
	StoredHeld    StoredChangeKind = "held"
)

// CreatedID is a created document's id, which the step assigns once it applied.
type CreatedID struct{ ID int64 }

// StoredChange is what one commit stores of a document, in the store writes' words: a retirement (nothing stored after
// it), a creation whose id the step assigns (Created.ID once it applied), a change of the record Stored was prepared
// on, or nothing yet (held: a deferred change of a run's state, D8, kept in memory over Stored).
type StoredChange struct {
	Kind     StoredChangeKind
	Version  int64
	Created  *CreatedID
	Stored   Stored
	WithinMs float64
}

// Persistence is the documents of one persisted name on one object: `(<object type>, namespace, name, key)`, the
// namespace `chord` for `self.state`, `chord.jobs` and `chord.job-states` for persisted job runs (slice 6b D5).
type Persistence struct {
	Store   *store.ChordStore
	address store.DocumentAddress
}

// NewPersistence is the persistence of kind under namespace of owner (the object's type, R2).
func NewPersistence(chordStore *store.ChordStore, owner, namespace, kind string) *Persistence {
	return &Persistence{Store: chordStore, address: store.DocumentAddress{Owner: owner, Namespace: namespace, Kind: kind}}
}

// Read is the stored document at key, nil when absent: found and loaded with plain reads (a store error is the
// store's). Counted (`chord.store.loads`).
func (persistence *Persistence) Read(key string, ctx context.Context) (*store.StoredDocument, error) {
	address := persistence.At(key)
	document, err := store.Read(persistence.Store, func(executor sqlite.SqliteExecutor) (*store.StoredDocument, error) {
		record, err := store.FindDocument(executor, address, store.CurrentPoint)
		if err != nil || record == nil {
			return nil, err
		}
		loaded, err := store.LoadDocument(executor, *record, store.CurrentPoint)
		if err != nil {
			return nil, err
		}
		return &loaded, nil
	}, ctx)
	if err != nil {
		return nil, err
	}
	if document != nil {
		if metrics := chordctx.Telemetry(ctx).Metrics(); metrics != nil {
			metrics.Counter("chord.store.loads", telemetry.InstrumentOptions{}).Add(1, nil)
		}
	}
	return document, nil
}

// Decode is document as definition reads it: a stored version newer than the code's, or the same version with another
// fingerprint (D9), fails; an older one is migrated and the result checked once (R8). What fails here is the code's (a
// failing migrate too, whatever it fails with), never the store's.
func (persistence *Persistence) Decode(document *store.StoredDocument, definition AnyDefinition, ctx context.Context) (Loaded, error) {
	record := document.Record
	if err := CheckReadable(record.Version, record.Fingerprint, definition); err != nil {
		return Loaded{}, err
	}
	// The engine stores what commits gave it: JSON objects of the record's version.
	stored, _ := document.Value.(*JsonObject)
	value := stored
	if record.Version != int64(definition.Version) {
		var err error
		if value, err = definition.Migrate(stored, int(record.Version), ctx); err != nil {
			return Loaded{}, err
		}
	}
	return Loaded{Value: value, Stored: Stored{ID: record.ID, Revision: record.Revision, Version: record.Version, DeltasSinceBase: document.DeltasSinceBase}}, nil
}

// Find is what is stored of the live document at key without loading it (a retirement's find) and the schema
// fingerprint it was stored with (CheckReadable takes both), nil when absent.
func (persistence *Persistence) Find(key string, ctx context.Context) (*FoundDocument, error) {
	record, err := store.Read(persistence.Store, func(executor sqlite.SqliteExecutor) (*store.DocumentRecord, error) {
		return store.FindDocument(executor, persistence.At(key), store.CurrentPoint)
	}, ctx)
	if err != nil || record == nil {
		return nil, err
	}
	// The change counter is all a retirement needs; DeltasSinceBase is not read for one.
	return &FoundDocument{Stored: Stored{ID: record.ID, Revision: record.Revision, Version: record.Version}, Fingerprint: record.Fingerprint}, nil
}

// Keys is up to limit keys of live documents after `after`, in code point order.
func (persistence *Persistence) Keys(after *string, limit int, ctx context.Context) ([]string, error) {
	return store.Read(persistence.Store, func(executor sqlite.SqliteExecutor) ([]string, error) {
		return store.ListDocumentKeys(executor, persistence.address, after, int64(limit))
	}, ctx)
}

// Stage stages one commit's writes of the document at key into writes (rule 8: serialized here, before the queue): its
// retirement when retires is stored, then its creation, or the change of stored (a base when created, when the stored
// version is older, or when `checkpointWhen` asks; a delta otherwise), held ops stored before the change's own (D8: a
// held base makes it a base). It returns what the commit stores.
func (persistence *Persistence) Stage(key string, definition AnyDefinition, stored *Stored, change DocumentChange, writes *[]store.StagedWrite, held *Held) (StoredChange, error) {
	if change.Retires && stored != nil {
		*writes = append(*writes, store.RetireDocument(stored.ID, stored.Revision))
	}
	if change.Prepared == nil {
		return StoredChange{Kind: StoredRetire}, nil
	}
	prepared := change.Prepared
	version := int64(definition.Version)
	if change.Retires || stored == nil {
		text, err := jsstring.MarshalJSON(prepared.Value())
		if err != nil {
			return StoredChange{}, err
		}
		base := store.DocumentBase{Version: version, Fingerprint: definition.Fingerprint, Value: prepared.Value()}
		created := &CreatedID{}
		*writes = append(*writes, store.CreateDocument(persistence.At(key), base, string(text), func(id int64) { created.ID = id }))
		return StoredChange{Kind: StoredCreate, Version: version, Created: created}, nil
	}
	info := CheckpointInfo{DeltasSinceBase: stored.DeltasSinceBase}
	base := (held != nil && held.Base) || stored.Version != version ||
		definition.CheckpointWhen(prepared.Value(), prepared.Ops(), info)
	ops := prepared.Ops()
	if held != nil {
		ops = append(append([]delta.Op{}, held.Ops...), prepared.Ops()...)
	}
	var content store.DocumentContent
	var payload any
	if base {
		content = store.DocumentContent{Kind: store.ContentBase, Version: version, Fingerprint: definition.Fingerprint, Value: prepared.Value()}
		payload = prepared.Value()
	} else {
		content = store.DocumentContent{Kind: store.ContentDelta, Version: version, Ops: ops}
		payload = ops
	}
	text, err := jsstring.MarshalJSON(payload)
	if err != nil {
		return StoredChange{}, err
	}
	*writes = append(*writes, store.ChangeDocument(stored.ID, stored.Revision, content, string(text)))
	deltas := stored.DeltasSinceBase + 1
	if base {
		deltas = 0
	}
	return StoredChange{Kind: StoredChanged, Stored: Stored{ID: stored.ID, Revision: stored.Revision + 1, Version: version, DeltasSinceBase: deltas}}, nil
}

// At is the address of key: a singleton's ("") has no key.
func (persistence *Persistence) At(key string) store.DocumentAddress {
	address := persistence.address
	if key != "" {
		address.Key = &key
	}
	return address
}

// CheckReadable fails when definition cannot read a document stored at version and fingerprint: a newer version than
// the code's, or the same version with another fingerprint (D9). A load and a retirement check alike (R15).
func CheckReadable(version int64, fingerprint string, definition AnyDefinition) error {
	code := int64(definition.Version)
	if version > code {
		return fmt.Errorf("%s: stored at version %d, newer than the code's %d", definition.Name, version, code)
	}
	if version == code && fingerprint != definition.Fingerprint {
		return fmt.Errorf("%s: stored with another schema at version %d (a schema changed without a bump)", definition.Name, code)
	}
	return nil
}

// StoredAfter is what is stored after a commit's change was adopted; nil after a retirement.
func StoredAfter(change StoredChange) *Stored {
	switch change.Kind {
	case StoredRetire:
		return nil
	case StoredCreate:
		return &Stored{ID: change.Created.ID, Revision: 1, Version: change.Version}
	}
	stored := change.Stored
	return &stored
}
