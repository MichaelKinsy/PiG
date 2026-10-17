package session

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// History is how a conversation document keeps its past.
type History string

// The conversation document histories.
const (
	HistoryLatest     History = "latest"
	HistoryRewindable History = "rewindable"
)

// ForkPolicy is how a conversation document behaves when its conversation is forked.
type ForkPolicy string

// The fork policies.
const (
	ForkAsOf    ForkPolicy = "asOf"
	ForkCurrent ForkPolicy = "current"
	ForkInitial ForkPolicy = "initial"
)

// DocDef defines a document kind: its scope and semantics, the stored shape
// version and the functions that create, migrate and checkpoint its value.
// A definition is immutable after construction and shared by every Session.
type DocDef struct {
	Kind    string
	Version int
	Scope   ScopeKind
	// History and Fork apply to conversation documents only.
	History History
	Fork    ForkPolicy
	// Family marks a keyed family; Initial then receives the member's seed.
	Family bool
	// Initial returns the value of a new document. seed is nil for a singleton.
	Initial func(seed any) *jv.Object
	// Migrate converts a value stored at an older version.
	Migrate func(value *jv.Object, from int) *jv.Object
	// HostInitial and HostMigrate replace Initial and Migrate when the definition's code runs in a host: an error (a
	// *NeedRead asks the host for the value) fails the call that needed it.
	HostInitial func(seed any) (*jv.Object, error)
	HostMigrate func(value *jv.Object, from int) (*jv.Object, error)
	// CheckpointWhen returns true to store an ordinary change as a base.
	CheckpointWhen func(value *jv.Object, ops []delta.Op, deltasSinceBase int) bool
}

// DocArgs addresses a document of a definition.
type DocArgs struct {
	Owner ID     // conversation or task ID for those scopes
	Key   string // family member
	Seed  any    // family seed
}

// DocumentCreate is the record of a new document.
func (d *DocDef) address(a DocArgs) Address {
	ad := Address{Kind: d.Kind, Scope: Scope{Kind: d.Scope}, Family: d.Family}
	if d.Scope != ScopeSession {
		ad.Scope.Owner = a.Owner
	}
	if d.Family {
		ad.Key = a.Key
	}
	return ad
}

// addressID is documents.ts addressId.
func addressID(a Address) string {
	var buf [96]byte
	b := append(buf[:0], '[')
	b = jv.AppendString(b, a.Kind)
	b = append(b, ',')
	b = jv.AppendString(b, string(a.Scope.Kind))
	b = append(b, ',')
	if a.Scope.Kind != ScopeSession {
		b = jv.AppendNumber(b, idv(a.Scope.Owner))
	} else {
		b = append(b, "null"...)
	}
	b = append(b, ',')
	if a.Family {
		b = jv.AppendString(b, a.Key)
	} else {
		b = append(b, "null"...)
	}
	return string(append(b, ']'))
}

// recordAddress returns the address of a document record.
func recordAddress(rec *jv.Object) Address {
	scope := getObj(rec, "scope")
	kind, _ := getStr(rec, "kind")
	ad := Address{Kind: kind}
	sk, _ := getStr(scope, "kind")
	ad.Scope.Kind = ScopeKind(sk)
	switch ad.Scope.Kind {
	case ScopeConversation:
		ad.Scope.Owner = mustInt(scope, "conversationId")
	case ScopeTask:
		ad.Scope.Owner = mustInt(scope, "taskId")
	}
	if k, ok := getStr(rec, "key"); ok {
		ad.Key, ad.Family = k, true
	}
	return ad
}

func scopeValue(s Scope) *jv.Object {
	switch s.Kind {
	case ScopeConversation:
		return obj("kind", "conversation", "conversationId", idv(s.Owner))
	case ScopeTask:
		return obj("kind", "task", "taskId", idv(s.Owner))
	}
	return obj("kind", "session")
}

// documentCreate is documents.ts documentCreate: the record of a new document.
func documentCreate(d *DocDef, a Address, id ID) *jv.Object {
	rec := obj("id", idv(id), "kind", a.Kind)
	if a.Family {
		rec.Set("key", a.Key)
	}
	rec.Set("scope", scopeValue(a.Scope))
	if a.Scope.Kind == ScopeConversation {
		rec.Set("history", string(d.History))
		rec.Set("fork", string(d.Fork))
	}
	return rec
}

func checkRecordScope(d *DocDef, rec *jv.Object) error {
	scope := getObj(rec, "scope")
	sk, _ := getStr(scope, "kind")
	if ScopeKind(sk) != d.Scope {
		return recordMismatch(rec)
	}
	if d.Scope == ScopeConversation {
		h, _ := getStr(rec, "history")
		f, _ := getStr(rec, "fork")
		if History(h) != d.History || ForkPolicy(f) != d.Fork {
			return recordMismatch(rec)
		}
	}
	return nil
}

func recordMismatch(rec *jv.Object) error {
	kind, _ := getStr(rec, "kind")
	return errorf("Document %d (%s) does not match the supplied definition semantics", mustInt(rec, "id"), kind)
}

func checkRecordVersion(d *DocDef, rec *jv.Object, version int) error {
	kind, _ := getStr(rec, "kind")
	if version > d.Version {
		return errorf("Document %d (%s) has newer version %d than %d", mustInt(rec, "id"), kind, version, d.Version)
	}
	if version < d.Version && d.Migrate == nil && d.HostMigrate == nil {
		return errorf("Document %d (%s) requires migration from version %d", mustInt(rec, "id"), kind, version)
	}
	return nil
}

// materialize returns the value of a stored document under the definition,
// migrating a value stored at an older version.
func materialize(d *DocDef, rec *jv.Object, version int, value *jv.Object) (*jv.Object, error) {
	if err := checkRecordScope(d, rec); err != nil {
		return nil, err
	}
	if err := checkRecordVersion(d, rec, version); err != nil {
		return nil, err
	}
	if version == d.Version {
		return value, nil
	}
	var migrated *jv.Object
	if d.HostMigrate != nil {
		var err error
		if migrated, err = d.HostMigrate(value, version); err != nil {
			return nil, err
		}
	} else {
		migrated = d.Migrate(value, version)
	}
	if migrated == nil {
		return nil, errors.New("document migration returned no value")
	}
	return jv.Clone(migrated).(*jv.Object), nil
}

// loadedDoc is a cached document incarnation: its record, the stored version,
// and the tracker that holds its current value.
type loadedDoc struct {
	addressID       string
	record          *jv.Object
	storedVersion   int
	valueVersion    int
	deltasSinceBase int
	tracker         *delta.Tracker
	// unpublished is true from a load inside a line job until the job publishes: Pi sets its document cache only when the
	// load resumes inside the job, so a caller that asks meanwhile (session.ts snapshot) still queues its own line job.
	unpublished bool
}
