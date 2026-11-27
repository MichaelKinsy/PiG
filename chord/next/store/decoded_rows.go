package store

import (
	"bytes"
	"context"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/store/decoded-rows.ts
//
// Decoded rows: the rows a live source showed last, by key, so that the next run's rows whose stored columns are all
// the same are the same objects, and a frame compares unchanged rows by identity. A live query's shared read keeps one
// (shared_read.go), a list another (host/host_list.go).

type decodedEntry struct {
	stored StoredRead
	row    delta.JsonValue
}

// DecodedRows are the decoded rows of a live source's last value, by key: a row whose stored columns are all the same
// in the next run is that value's object (one comparison per selected column), any other is decoded. So a frame, the
// diff from the value the watches had, compares unchanged rows by identity (a window shifted by one row: one insert and
// one removal, not a comparison of every row with every other).
type DecodedRows struct {
	byKey map[any]decodedEntry
	one   *decodedEntry
	oneID any
}

// Decode is the rows of stored as target decodes them, unchanged ones reused; a row the code cannot read is the error.
// ids is the optional precomputed key identities (ReadResult.IDs).
func (rows *DecodedRows) Decode(stored []StoredRead, target LiveTarget, ctx context.Context, ids ...[]any) ([]delta.JsonValue, error) {
	key := target.Spec.Query.Table.Key
	var pre []any
	if len(ids) > 0 {
		pre = ids[0]
	}
	if len(stored) == 1 {
		one := stored[0]
		var id any
		if pre != nil {
			id = pre[0]
		} else {
			id = Identity(key, one)
		}
		entry, err := rows.reused(id, one, target, ctx)
		if err != nil {
			return nil, err
		}
		rows.byKey = nil
		rows.one = &entry
		rows.oneID = id
		return []delta.JsonValue{entry.row}, nil
	}
	values := make([]delta.JsonValue, len(stored))
	byKey := make(map[any]decodedEntry, len(stored))
	for at, each := range stored {
		var id any
		if pre != nil {
			id = pre[at]
		} else {
			id = Identity(key, each)
		}
		entry, err := rows.reused(id, each, target, ctx)
		if err != nil {
			return nil, err
		}
		values[at] = entry.row
		byKey[id] = entry
	}
	rows.byKey = byKey
	rows.one = nil
	rows.oneID = nil
	return values, nil
}

// Reset forgets the last value's rows: another target decodes its own way, nothing of the previous one is reused.
func (rows *DecodedRows) Reset() {
	rows.byKey = nil
	rows.one = nil
	rows.oneID = nil
}

func (rows *DecodedRows) reused(id any, stored StoredRead, target LiveTarget, ctx context.Context) (decodedEntry, error) {
	var before decodedEntry
	var ok bool
	if rows.byKey != nil {
		before, ok = rows.byKey[id]
	} else if rows.one != nil && rows.oneID == id {
		before, ok = *rows.one, true
	}
	if ok && sameStored(before.stored, stored) {
		return decodedEntry{stored: stored, row: before.row}, nil
	}
	row, err := target.Decode(stored, ctx)
	if err != nil {
		return decodedEntry{}, err
	}
	return decodedEntry{stored: stored, row: row}, nil
}

// sameStored reports whether two stored rows of one statement have the same value in every column (SQLite values compare
// by equality; blobs by bytes).
func sameStored(a, b StoredRead) bool {
	if len(a) != len(b) {
		return false
	}
	for column, left := range a {
		right, ok := b[column]
		if !ok {
			return false
		}
		if blob, isBlob := left.([]byte); isBlob {
			other, otherBlob := right.([]byte)
			if !otherBlob || !bytes.Equal(blob, other) {
				return false
			}
			continue
		}
		if left != right {
			return false
		}
	}
	return true
}
