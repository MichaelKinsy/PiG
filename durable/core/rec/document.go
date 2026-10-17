// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package rec

import "github.com/MichaelKinsy/PiG/durable/core/jv"

// ScopeKind is the owner kind of a document (spec §3.2).
type ScopeKind uint8

// Document scopes; the documents.scope_kind column holds the name.
const (
	ScopeSession ScopeKind = iota + 1
	ScopeConversation
	ScopeTask
)

// scopeNames is read-only.
var scopeNames = [...]string{"", "session", "conversation", "task"}

// String returns the persisted scope name.
func (k ScopeKind) String() string {
	if int(k) < len(scopeNames) {
		return scopeNames[k]
	}
	return ""
}

// Document is a document record (spec §3.2 DocumentRecord): `{id, kind, key?, scope, history?, fork?, createdAt,
// retiredAt?}`. A DocumentCreate is the same record without createdAt and retiredAt.
type Document struct{ V *jv.Object }

// NewDocumentCreate builds the DocumentCreate of src/session/transaction.ts documentCreate and forks.ts
// collectCopies: `{id, kind, key?, scope, history?, fork?}`. key is nil for a singleton; history and fork are empty for
// session and task scopes. scopeOwner is the conversation or task ID (0 for the session).
func NewDocumentCreate(docID int64, kind string, key *string, scope ScopeKind, scopeOwner int64, history, fork string) *Document {
	o := jv.NewObjectCap(6)
	o.Set("id", id(docID))
	o.Set("kind", kind)
	if key != nil {
		o.Set("key", *key)
	}
	sc := jv.NewObjectCap(2)
	sc.Set("kind", scope.String())
	switch scope {
	case ScopeConversation:
		sc.Set("conversationId", id(scopeOwner))
	case ScopeTask:
		sc.Set("taskId", id(scopeOwner))
	}
	o.Set("scope", sc)
	if history != "" {
		o.Set("history", history)
	}
	if fork != "" {
		o.Set("fork", fork)
	}
	return &Document{V: o}
}

// ParseDocument reads a stored document record.
func ParseDocument(b []byte) (*Document, error) {
	o, err := parseObject(b)
	if err != nil {
		return nil, err
	}
	return DocumentOf(o)
}

// DocumentOf reads a document record that is already a value: ParseDocument without the text, with the same checks.
func DocumentOf(o *jv.Object) (*Document, error) {
	if o == nil {
		return nil, ErrShape
	}
	d := &Document{V: o}
	if _, ok := idOf(o, "id"); !ok {
		return nil, ErrShape
	}
	if _, ok := stringOf(o, "kind"); !ok {
		return nil, ErrShape
	}
	if k, _ := d.Scope(); k == 0 {
		return nil, ErrShape
	}
	return d, nil
}

// Append appends the record's JSON.stringify.
func (d *Document) Append(dst []byte) []byte { return jv.Append(dst, d.V) }

// ID is the incarnation ID.
func (d *Document) ID() int64 { n, _ := idOf(d.V, "id"); return n }

// Kind is the definition kind.
func (d *Document) Kind() string { s, _ := stringOf(d.V, "kind"); return s }

// Key is the family member key; ok is false for a singleton.
func (d *Document) Key() (string, bool) { return stringOf(d.V, "key") }

// Scope returns the scope kind and its owner ID (the conversation or task; 0 for the session); kind is 0 when the scope
// is malformed.
func (d *Document) Scope() (kind ScopeKind, owner int64) {
	sc := objectOf(d.V, "scope")
	if sc == nil {
		return 0, 0
	}
	name, _ := stringOf(sc, "kind")
	switch name {
	case "session":
		return ScopeSession, 0
	case "conversation":
		n, _ := idOf(sc, "conversationId")
		return ScopeConversation, n
	case "task":
		n, _ := idOf(sc, "taskId")
		return ScopeTask, n
	}
	return 0, 0
}

// History is "latest" or "rewindable" for a conversation-scoped document, "" otherwise.
func (d *Document) History() string { s, _ := stringOf(d.V, "history"); return s }

// Fork is the fork policy of a conversation-scoped document, "" otherwise.
func (d *Document) Fork() string { s, _ := stringOf(d.V, "fork"); return s }

// CreatedAt is the commit that created the incarnation; ok is false on a DocumentCreate.
func (d *Document) CreatedAt() (int64, bool) { return idOf(d.V, "createdAt") }

// RetiredAt is the commit that retired the incarnation; ok is false while current.
func (d *Document) RetiredAt() (int64, bool) { return idOf(d.V, "retiredAt") }

// CurrentOnly is storage.ts isCurrentOnly: only the current content is retained (no as-of reads).
func (d *Document) CurrentOnly() bool {
	k, _ := d.Scope()
	return k != ScopeConversation || d.History() == "latest"
}

// AliveAt is storage.ts isAliveAt for a historical point; current is the current-incarnation test.
func (d *Document) AliveAt(current bool, at int64) bool {
	retired, hasRetired := d.RetiredAt()
	if current {
		return !hasRetired
	}
	created, _ := d.CreatedAt()
	return created <= at && (!hasRetired || at < retired)
}

// Stamped returns the stored record of a create commit: `{...create, createdAt: seq, ...(retire ? {retiredAt: seq})}`.
func (d *Document) Stamped(seq int64, retire bool) *Document {
	o := with(d.V, "createdAt", id(seq))
	if retire {
		o.Set("retiredAt", id(seq))
	}
	return &Document{V: o}
}

// Retired returns `{...record, retiredAt: seq}`.
func (d *Document) Retired(seq int64) *Document { return &Document{V: with(d.V, "retiredAt", id(seq))} }

// Address is the document address columns (storage.ts addressParts): kind and key hold JSON.stringify(value).
type Address struct {
	Kind      string // documents.kind
	ScopeKind string // documents.scope_kind
	OwnerID   int64  // documents.owner_id: 0 for the session
	Family    int64  // documents.family: 1 when the record has a key
	KeyValue  string // documents.key_value
}

// Address returns the record's address columns.
func (d *Document) Address() Address {
	k, owner := d.Scope()
	key, has := d.Key()
	a := Address{Kind: IndexedString(d.Kind()), ScopeKind: k.String(), OwnerID: owner, KeyValue: IndexedString(key)}
	if has {
		a.Family = 1
	}
	return a
}

// AddressKey identifies the logical address of a record (storage.ts addressKey).
func (a Address) AddressKey() string {
	var b []byte
	b = append(b, a.Kind...)
	b = append(b, 0)
	b = append(b, a.ScopeKind...)
	b = append(b, 0)
	b = jv.AppendNumber(b, float64(a.OwnerID))
	b = append(b, 0, byte('0'+a.Family), 0)
	b = append(b, a.KeyValue...)
	return string(b)
}
