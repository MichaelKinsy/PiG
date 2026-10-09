// SPDX-License-Identifier: MIT

package history

import (
	"slices"
	"testing"
)

// fakeDB is the host side of the read protocol over in-memory rows: it answers a Need the way the SQL in sql.go does.
type fakeDB struct {
	convs    map[int64][]byte
	entries  []fakeRow // ascending id
	rowsRead int
	reads    int
}

type fakeRow struct {
	id, conv, seq int64
	head          int64
	hasHead       bool
	rec           []byte
}

func newFakeDB() *fakeDB { return &fakeDB{convs: map[int64][]byte{}} }

func (db *fakeDB) addEntry(t testing.TB, conv, seq int64, rec []byte) {
	t.Helper()
	sc, err := scanRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	db.entries = append(db.entries, fakeRow{id: sc.id, conv: conv, seq: seq, head: sc.head, hasHead: sc.flags&flagHead != 0, rec: rec})
}

// answer runs one read against the rows and supplies the answer to the store.
func (db *fakeDB) answer(t testing.TB, st *Store, n Need) {
	t.Helper()
	db.reads++
	switch n.Kind {
	case NeedConversation:
		db.rowsRead++
		rec, err := ParseConversation(db.convs[n.Conv])
		if err != nil {
			t.Fatal(err)
		}
		st.AddConversation(rec, false)
	case NeedBounds:
		var maxID, markerID, markerHead int64
		var hasMax, hasMarker bool
		for _, r := range slices.Backward(db.entries) {

			if r.conv != n.Conv || r.id > n.Max {
				continue
			}
			if !hasMax {
				hasMax, maxID = true, r.id
				db.rowsRead++
			}
			if r.hasHead {
				hasMarker, markerID, markerHead = true, r.id, r.head
				db.rowsRead++
				break
			}
		}
		st.SupplyBounds(n.Conv, n.Max, hasMax, maxID, hasMarker, markerID, markerHead)
	case NeedEntries:
		st.BeginLoad(n.Conv, n.Min, n.Max)
		for _, r := range db.entries {
			if r.conv == n.Conv && r.id >= n.Min && r.id <= n.Max {
				db.rowsRead++
				if err := st.AddRow(n.Conv, r.id, r.seq, r.rec); err != nil {
					t.Fatal(err)
				}
			}
		}
	default:
		t.Fatalf("unexpected read %+v", n)
	}
}

// context runs the read loop until the derivation completes.
func (db *fakeDB) context(t testing.TB, st *Store, conv, at int64) (*View, error) {
	t.Helper()
	for range 64 {
		v, need, err := st.Context(conv, at)
		if need == nil {
			return v, err
		}
		db.answer(t, st, *need)
	}
	t.Fatal("read loop does not converge")
	return nil, nil
}
