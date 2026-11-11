package store

// Ports packages/durable/src/storage/store/documents.ts
//
// Durable's documents in the store's documents engine (slice 6b §1.5, R1): a Durable address as an engine address
// under the storage's owner name (its scope as the namespace, its kind and key JSON-encoded as Durable's indexed
// strings), a Durable record as an engine record and back (the Durable record in `meta`), and a document read at a
// point.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// ScopeNamespaces are the namespaces a storage claims under its owner: Durable's scopes (Chord's namespaces never
// contain a `:`).
var ScopeNamespaces = []string{"session", "conversation:", "task:"}

// NamespaceOf is the engine namespace of a Durable scope: `session`, `conversation:<id>`, `task:<id>`.
func NamespaceOf(scope durable.DocumentRecordScope) string {
	switch scope.Kind {
	case durable.ScopeConversation:
		return fmt.Sprintf("conversation:%d", scope.ConversationId)
	case durable.ScopeTask:
		return fmt.Sprintf("task:%d", scope.TaskId)
	default:
		return "session"
	}
}

// AddressOf is the engine address of a Durable address under owner.
func AddressOf(kind string, scope durable.DocumentRecordScope, key *string, owner string) chordstore.DocumentAddress {
	address := chordstore.DocumentAddress{Owner: owner, Namespace: NamespaceOf(scope), Kind: EncodeIndexed(kind)}
	if key != nil {
		encoded := EncodeIndexed(*key)
		address.Key = &encoded
	}
	return address
}

// HistoryOf is what a record of scope keeps: session and task documents and latest conversation ones their latest
// state.
func HistoryOf(scope durable.DocumentRecordScope, history durable.DocumentHistory) chordstore.DocumentHistory {
	if scope.Kind == durable.ScopeConversation && history == durable.HistoryRewindable {
		return chordstore.HistoryRewindable
	}
	return chordstore.HistoryLatest
}

// EngineRecord is the engine record of a Durable creation at seq (retired at once for an empty lifetime): the
// Durable record itself, its creation seq included, in `meta`, so a read of a live record parses one JSON text from
// one column, as `SqliteStorage`'s does (a retirement later sets only the engine's `retired_at`).
func EngineRecord(create durable.DocumentCreate, owner string, version, seq int64, retired bool) (chordstore.DocumentRecord, error) {
	meta, err := jsstring.MarshalJSON(durable.DocumentRecord{
		Id: create.Id, Kind: create.Kind, Key: create.Key, CreatedAt: durable.Seq(seq),
		Scope: create.Scope, History: create.History, Fork: create.Fork,
	})
	if err != nil {
		return chordstore.DocumentRecord{}, err
	}
	text := string(meta)
	address := AddressOf(create.Kind, create.Scope, create.Key, owner)
	record := chordstore.DocumentRecord{
		ID: int64(create.Id), Owner: address.Owner, Namespace: address.Namespace, Kind: address.Kind, Key: address.Key,
		History: HistoryOf(create.Scope, create.History), CreatedAt: seq, Meta: &text,
		Revision: 1, Version: version, Fingerprint: "",
	}
	if retired {
		record.RetiredAt = &seq
	}
	return record, nil
}

// DurableRecord is the Durable record of an engine record of this storage's owner and scopes, nil for anyone else's
// (a Chord document, another owner's): its id is unknown to this storage.
func DurableRecord(record chordstore.DocumentRecord, owner string) (*durable.DocumentRecord, error) {
	if record.Meta == nil || !ownedHere(record.Owner, record.Namespace, owner) {
		return nil, nil
	}
	found, err := RecordOfEntry(chordstore.DocumentEntry{Meta: record.Meta, RetiredAt: record.RetiredAt})
	if err != nil {
		return nil, err
	}
	return &found, nil
}

// RecordOfEntry is the Durable record of an engine entry read at one of this storage's addresses or scopes (the
// record in `meta`).
func RecordOfEntry(entry chordstore.DocumentEntry) (durable.DocumentRecord, error) {
	if entry.Meta == nil {
		return durable.DocumentRecord{}, errors.New("document entry has no meta")
	}
	// Written by EngineRecord, parsed fresh: a retirement since is set on it in place.
	found, err := parseJSON[durable.DocumentRecord](*entry.Meta)
	if err != nil {
		return durable.DocumentRecord{}, err
	}
	if entry.RetiredAt != nil {
		retired := durable.SeqFromNumber(*entry.RetiredAt)
		found.RetiredAt = &retired
	}
	return found, nil
}

// ownedHere reports whether a record is this storage's: owner's, in one of Durable's scopes (not a Chord document of
// the owner).
func ownedHere(recordOwner, namespace, owner string) bool {
	if recordOwner != owner {
		return false
	}
	return namespace == "session" || strings.HasPrefix(namespace, "conversation:") || strings.HasPrefix(namespace, "task:")
}

// AliveAt reports whether a document is alive at at (the half-open lifetime).
func AliveAt(record durable.DocumentRecord, at durable.DocumentPoint) bool {
	if at.Current {
		return record.RetiredAt == nil
	}
	return record.CreatedAt <= at.Seq && (record.RetiredAt == nil || at.Seq < *record.RetiredAt)
}

// pointOf is the engine's point of a Durable one.
func pointOf(at durable.DocumentPoint) chordstore.DocumentPoint {
	if at.Current {
		return chordstore.CurrentPoint
	}
	return chordstore.AtSeq(int64(at.Seq))
}

// DocumentRead is a document read at a point, as a tagged result: request errors are told after the guarded read
// (slice 6b §1.4), never returned inside it. CurrentOnly is set when the record keeps no history at the point; the
// document is then absent and CurrentOnlyID names it.
type DocumentRead struct {
	Document      *durable.StoredDocument
	CurrentOnly   bool
	CurrentOnlyID durable.DocumentId
}

// readExecutor is what a document read needs of an executor.
type readExecutor interface {
	Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error)
	All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error)
}

// ReadAt is document id of owner at at: absent (unknown, someone else's, not alive at at), a record whose history
// does not reach back to at, or its value from the base at or below at and the deltas up to it.
func ReadAt(executor readExecutor, id durable.DocumentId, at durable.DocumentPoint, owner string) (DocumentRead, error) {
	entry, err := chordstore.ReadDocumentEntry(executor, int64(id))
	if err != nil {
		return DocumentRead{}, err
	}
	if entry == nil || entry.Meta == nil || !ownedHere(entry.Owner, entry.Namespace, owner) {
		return DocumentRead{}, nil
	}
	found, err := RecordOfEntry(entry.DocumentEntry)
	if err != nil {
		return DocumentRead{}, err
	}
	if !at.Current && entry.History != chordstore.HistoryRewindable {
		return DocumentRead{CurrentOnly: true, CurrentOnlyID: id}, nil
	}
	if !AliveAt(found, at) {
		return DocumentRead{}, nil
	}
	loaded, err := chordstore.LoadContent(executor, int64(id), pointOf(at))
	if err != nil {
		return DocumentRead{}, err
	}
	// The engine stores what Durable's commits gave it: JSON objects.
	value, ok := loaded.Value.(*delta.JsonObject)
	if !ok {
		return DocumentRead{}, fmt.Errorf("Document %d does not materialize to an object", id)
	}
	return DocumentRead{Document: &durable.StoredDocument{
		Record: found, Version: int(loaded.Version), Value: value, DeltasSinceBase: loaded.DeltasSinceBase,
	}}, nil
}
