// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session

import "github.com/MichaelKinsy/PiG/durable/core/internal/delta"

// HostDoc is Doc for a host that keeps its own draft (ABI section 8, `doc`): it acquires the document and returns the
// value the draft starts from and, for a stored document (loaded), the number of deltas after its base (Pi's
// CheckpointInfo). The host's
// operations come back through SetDocOps.
func (tx *Tx) HostDoc(def *DocDef, args DocArgs) (value any, deltasSinceBase int, loaded bool, err error) {
	if _, err := tx.Doc(def, args); err != nil {
		return nil, 0, false, err
	}
	e := tx.latestByAddr[addressID(def.address(args))]
	if e == nil || e.change == nil {
		return nil, 0, false, errorf("Document %s has no open draft", def.Kind)
	}
	if e.target != nil && e.target.kind == targetLoaded {
		deltasSinceBase, loaded = e.target.loaded.deltasSinceBase, true
	}
	// The host's tracker continues from the draft's present content: the core's own edits (the built-in creation hook's
	// agent copy) are already in it.
	if e.draft != nil {
		return e.draft.Snapshot(), deltasSinceBase, loaded, nil
	}
	return e.change.BaseRoot(), deltasSinceBase, loaded, nil
}

// DocEntry is the position of the document's latest entry in the transaction's documents: a host names the incarnation a
// draft belongs to by it, since a retired incarnation's draft outlives the new incarnation at its address.
func (tx *Tx) DocEntry(def *DocDef, args DocArgs) int {
	e := tx.latestByAddr[addressID(def.address(args))]
	for i, d := range tx.documents {
		if d == e {
			return i
		}
	}
	return -1
}

// DocRecordID is the ID of the stored record a document's latest entry loaded, or 0 for a document this transaction
// creates: a host matches the Storage writes of a commit to the revisions its drafts prepared by it.
func (tx *Tx) DocRecordID(def *DocDef, args DocArgs) ID {
	e := tx.latestByAddr[addressID(def.address(args))]
	if e == nil || e.target == nil || e.target.kind != targetLoaded {
		return 0
	}
	return ID(mustInt(e.target.loaded.record, "id"))
}

// SetDocOps records the operations a host's tracker produced for the open draft of a document (ABI section 8,
// `doc.write`); settlement prepares the draft from them. A non-negative entry names the incarnation by DocEntry; a
// negative one the latest at the address.
func (tx *Tx) SetDocOps(def *DocDef, args DocArgs, entry int, ops []delta.Op) error {
	if err := tx.assertOpen(); err != nil {
		return err
	}
	e := tx.latestByAddr[addressID(def.address(args))]
	if entry >= 0 && entry < len(tx.documents) {
		e = tx.documents[entry]
	}
	if e == nil || e.change == nil {
		return errorf("Document %s has no open draft", def.Kind)
	}
	e.external, e.hasExternal = ops, true
	return nil
}

// DocumentAddress returns the document address a definition and arguments name.
func DocumentAddress(def *DocDef, args DocArgs) Address { return def.address(args) }

// DocAddress returns the address a definition and arguments name; hosts key their per-transaction state by it.
func DocAddress(def *DocDef, args DocArgs) string { return addressID(def.address(args)) }

// SnapshotAsOf is Session.snapshotAsOf: the value of a conversation document as of the commit of entry at, which must
// be visible from the conversation, or nil when the document did not exist then.
func (s *Session) SnapshotAsOf(def *DocDef, args DocArgs, at ID) (any, error) {
	if err := s.assertUsable(); err != nil {
		return nil, err
	}
	addr := def.address(args)
	if addr.Scope.Kind != ScopeConversation {
		return nil, &namedError{"Session.snapshotAsOf() requires a conversation document", ErrTypeError}
	}
	if err := s.assertHealthy(); err != nil {
		return nil, err
	}
	entry, seq, err := s.store.EntryIn(addr.Scope.Owner, at)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, errorf("Entry %d is not visible from conversation %d", at, addr.Scope.Owner)
	}
	addr.Scope.Owner = mustInt(entry, "conversationId")
	rec, err := s.store.FindDocument(addr, AsOf(seq))
	if err != nil || rec == nil {
		return nil, err
	}
	stored, err := s.store.Document(mustInt(rec, "id"), AsOf(seq))
	if err != nil {
		return nil, err
	}
	if stored == nil {
		kind, _ := getStr(rec, "kind")
		return nil, errorf("Historical document %d (%s) cannot be read", mustInt(rec, "id"), kind)
	}
	return materialize(def, stored.Record, stored.Version, stored.Value)
}
