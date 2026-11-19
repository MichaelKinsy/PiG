package codingagent

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// RecordChangedError is the error ReadEntries returns for an entry whose record cannot be read back as it was indexed.
type RecordChangedError = sessionentry.RecordChangedError

// BranchRef locates one entry of a branch without reading its body. It is a plain value: it holds no file handle and no part of the
// Session's snapshot, so it can be kept, copied and compared. An id stays valid for the life of the Session (the log is append-only):
// moving the leaf changes what BranchRefs returns, never what ReadEntries returns for an id.
//
// pig additive (D655): Pi holds every entry decoded, so getBranch needs no such view; the reference exists for the split between
// resident and file-backed entries (D112), and a Go program that shows a page of a long session reads only that page's bodies.
type BranchRef struct {
	ID       string
	ParentID *string
	// Type is the entry's type: "message", "compaction", "custom", and so on.
	Type string
	// Role is the message's role, "" for an entry that is no message.
	Role string
	// Timestamp is the entry's timestamp, as its record carries it.
	Timestamp string
}

// BranchRefs is GetBranch's path (the same entries in the same order; fromID, when given, starts the path there, and an id that more
// than one entry carries is resolved as GetBranch resolves it) as references, and reads no entry body: a branch of 90,000 entries costs
// the walk, not 90,000 reads. It is nil for an empty session, a leaf that is not set, and an unknown id.
//
// pig additive (D655): see BranchRef.
func (s *Session) BranchRefs(fromID ...string) []BranchRef {
	s.mu.RLock()
	defer s.mu.RUnlock()
	leaf := s.leafID
	if len(fromID) > 0 {
		leaf = &fromID[0]
	}
	if leaf == nil {
		return nil
	}
	path := s.pathIndexesLocked(*leaf)
	if path == nil {
		return nil
	}
	refs := make([]BranchRef, len(path))
	for k, i := range path {
		slot := &s.slots[i]
		refs[k] = BranchRef{ID: s.idOf(slot), ParentID: s.parentPtrLocked(int(i)), Type: slot.typ(), Role: slot.role(), Timestamp: s.timestampLocked(int(i))}
	}
	return refs
}

// ReadEntries reads and decodes the entries with the given ids and returns them in the order of ids (an id given twice is returned twice).
// The records are read in one batch in file order, without the Session's lock: the lock is held only to find the entries, so an append or a
// leaf move does not wait for the read, and the entries it returns are the ones the ids named when it began.
//
// An id that no entry has, a record that cannot be read back as it was indexed (an error that is a *RecordChangedError, which errors.As
// finds), and a ctx that is done before the read or between the runs of records of one source each return an error and no entries.
// The error of a done ctx is ctx's. No read panics.
//
// pig additive (D655): see BranchRef.
func (s *Session) ReadEntries(ctx context.Context, ids []string) ([]SessionEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := s.entryHandlesOf(ids)
	if err != nil {
		return nil, err
	}
	return sessionentry.MaterializeAllContext(ctx, entries)
}

// entryHandlesOf maps ids to their entries under the lock: a resident entry as it is, a message that is not resident as a
// sessionentry.LazyEntry, which the caller reads without the lock. An entry of another type that is not resident is read here, and a
// record that fails that read is the error of the call.
func (s *Session) entryHandlesOf(ids []string) ([]SessionEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	indexes := make([]int32, len(ids))
	for k, id := range ids {
		i, ok := s.slotOf(id)
		if !ok {
			return nil, fmt.Errorf("Entry %s not found", id)
		}
		indexes[k] = int32(i)
	}
	lazy := 0
	for _, i := range indexes {
		if s.resident.get(int(i)) == nil && s.lazyAtLocked(int(i)) {
			lazy++
		}
	}
	s.slotReads.Add(int64(lazy)) // the reads the caller makes through the handles
	var stale atomic.Int64
	entries := s.entryHandlesStaleLocked(indexes, &stale)
	if failed := stale.Load(); failed > 0 {
		return nil, &RecordChangedError{ID: s.idOf(&s.slots[failed-1]), Err: errRecordChanged}
	}
	return entries, nil
}
