// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

// storedTasks is a fake task table behind lazyState.read: ids 1..n, every third one live in storage, the rest terminal.
type storedTasks struct {
	n    int
	rows int // rows returned so far
}

func (s *storedTasks) status(id int64) string {
	if id%3 == 0 {
		return "running"
	}
	return "terminal"
}

func (s *storedTasks) row(id int64) []abi.Value {
	st := s.status(id)
	rec := fmt.Sprintf(`{"id":%d,"conversationId":%d,"kind":"k","state":{"status":%q}}`, id, id%5+1, st)
	return []abi.Value{abi.Int(id), abi.Int(id%5 + 1), abi.TextBytes([]byte(`"k"`)), abi.TextBytes([]byte(st)), abi.Int(0), abi.Int(0), {Kind: abi.ValueBlob, Bytes: []byte(rec)}}
}

func (s *storedTasks) read(sql uint16, params []abi.Value) ([][]abi.Value, error) {
	var rows [][]abi.Value
	switch sql {
	case store.SQLTasksPage:
		for id := int64(params[0].Number()) + 1; id <= int64(s.n) && len(rows) < store.PageRows; id++ {
			rows = append(rows, s.row(id))
		}
	case store.SQLTasksDesc:
		for id := min(int64(params[0].Number())-1, int64(s.n)); id >= 1 && len(rows) < store.PageRows; id-- {
			rows = append(rows, s.row(id))
		}
	default:
		return nil, fmt.Errorf("statement %d", sql)
	}
	s.rows += len(rows)
	return rows, nil
}

// A page of a terminal-task scan reads one storage page and parses its own rows, wherever the page lies: the cost of a
// page was every task's read and parse.
func TestScanTasksPageCostIsFlatInTheTaskCount(t *testing.T) {
	for _, desc := range []bool{false, true} {
		var rows [2]int
		var allocs [2]float64
		for k, n := range []int{2000, 16000} {
			st := &storedTasks{n: n}
			q := session.TaskQuery{Status: "terminal", Descending: desc}
			// A page on a mirror that has read nothing: the first page, then a middle one.
			scan := func(after session.ID) {
				m := newMirror()
				m.lazy = lazyState{read: st.read, below: session.ID(n) + 1}
				p, err := m.ScanTasks(q, 50, after)
				if err != nil || len(p.Items) != 50 || p.After == 0 {
					t.Fatalf("page after %d: %d items, after %d, %v", after, len(p.Items), p.After, err)
				}
			}
			scan(0)
			scan(session.ID(n / 2))
			rows[k] = st.rows
			allocs[k] = testing.AllocsPerRun(5, func() { scan(session.ID(n / 2)) })
		}
		if rows[0] > 2*store.PageRows || rows[1] > 2*store.PageRows {
			t.Fatalf("desc=%v: two pages read %v rows, want at most %d", desc, rows, 2*store.PageRows)
		}
		if allocs[1] > 1.2*allocs[0]+10 {
			t.Fatalf("desc=%v: allocations per page %v from 2k to 16k tasks", desc, allocs)
		}
	}
}

// ScanTasks over storage plus held tasks answers what a naive scan of the merged table does, in both directions, with
// filters, and a paged walk returns every task once.
func TestScanTasksMergesHeldAndStoredTasks(t *testing.T) {
	const n = 3500
	st := &storedTasks{n: n}
	m := newMirror()
	m.lazy = lazyState{read: st.read, below: n + 1}
	rng := rand.New(rand.NewPCG(1, 2))
	// Held: live tasks (as storage) and a mix of ids that shadow storage rows with another status, plus new ones above.
	want := map[int64]string{}
	for id := int64(1); id <= n; id++ {
		want[id] = st.status(id)
		if want[id] != "terminal" { // the mirror holds every live task
			rec := fmt.Sprintf(`{"id":%d,"conversationId":%d,"kind":"k","state":{"status":%q}}`, id, id%5+1, want[id])
			m.putTask(id, taskHead{conv: id%5 + 1, kind: "k", status: want[id]}, nil, []byte(rec))
		}
	}
	for range 400 {
		id := int64(rng.IntN(n+200) + 1)
		status := []string{"running", "terminal", "pending"}[rng.IntN(3)]
		rec := fmt.Sprintf(`{"id":%d,"conversationId":%d,"kind":"k","state":{"status":%q}}`, id, id%5+1, status)
		m.putTask(id, taskHead{conv: id%5 + 1, kind: "k", status: status}, nil, []byte(rec))
		want[id] = status
	}
	for _, status := range []string{"", "terminal", "running"} {
		for _, conv := range []session.ID{0, 3} {
			for _, desc := range []bool{false, true} {
				var ids []int64
				for id := range want {
					if (status == "" || want[id] == status) && (conv == 0 || session.ID(id%5+1) == conv) {
						ids = append(ids, id)
					}
				}
				slices.Sort(ids)
				if desc {
					slices.Reverse(ids)
				}
				for _, limit := range []int{0, 1, 7, 1024, 1025} {
					var got []int64
					var after session.ID
					for pages := 0; ; pages++ {
						if pages > len(ids)+2 {
							t.Fatal("scan does not end")
						}
						p, err := m.ScanTasks(session.TaskQuery{Status: status, ConversationID: conv, Descending: desc}, limit, after)
						if err != nil {
							t.Fatal(err)
						}
						for _, it := range p.Items {
							id := int64(num(it, "id"))
							got = append(got, id)
							if s := str(sub(it, "state"), "status"); s != want[id] {
								t.Fatalf("task %d status %q, want %q", id, s, want[id])
							}
						}
						if p.After == 0 {
							break
						}
						after = p.After
					}
					if !slices.Equal(got, ids) {
						t.Fatalf("status %q conv %d desc %v limit %d: %d tasks, want %d (first diff %v)", status, conv, desc, limit, len(got), len(ids), firstDiff(got, ids))
					}
				}
			}
		}
	}
}

func firstDiff(a, b []int64) string {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return fmt.Sprintf("at %d: %d vs %d", i, a[i], b[i])
		}
	}
	return "length"
}

// A conversation scan by owner reads that owner's conversations, not every conversation's record, and answers as the
// unindexed scan did.
func TestScanConversationsByOwnerUsesTheIndex(t *testing.T) {
	m := newMirror()
	const convs = 3000
	for id := session.ID(2); id < convs; id++ {
		rec := fmt.Sprintf(`{"id":%d,"owner":{"taskId":%d,"conversationId":%d}}`, id, id%7+10, id%11+1)
		if id%13 == 0 {
			rec = fmt.Sprintf(`{"id":%d}`, id)
		}
		m.convs.put(id, parseRaw([]byte(rec)))
	}
	// Re-putting a record with another owner moves it between the indexes.
	m.convs.put(14, parseRaw([]byte(`{"id":14,"owner":{"taskId":99,"conversationId":1}}`)))
	for _, q := range []session.ConversationQuery{
		{}, {Descending: true}, {OwnerTaskID: 12}, {OwnerTaskID: 12, Descending: true}, {OwnerConversationID: 4},
		{OwnerTaskID: 12, OwnerConversationID: 4}, {OwnerTaskID: 99}, {OwnerTaskID: 5},
	} {
		var naive []session.ID
		for _, c := range m.convs.recs {
			o := sub(c, "owner")
			if q.OwnerTaskID != 0 && (o == nil || num(o, "taskId") != q.OwnerTaskID) || q.OwnerConversationID != 0 && (o == nil || num(o, "conversationId") != q.OwnerConversationID) {
				continue
			}
			naive = append(naive, num(c, "id"))
		}
		if q.Descending {
			slices.Reverse(naive)
		}
		for _, limit := range []int{0, 1, 5} {
			var got []session.ID
			var after session.ID
			for {
				p, err := m.ScanConversations(q, limit, after)
				if err != nil {
					t.Fatal(err)
				}
				for _, it := range p.Items {
					got = append(got, num(it, "id"))
				}
				if p.After == 0 {
					break
				}
				after = p.After
			}
			if !slices.Equal(got, naive) {
				t.Fatalf("%+v limit %d: %v, want %v", q, limit, got, naive)
			}
		}
	}
	allocs := testing.AllocsPerRun(10, func() {
		if _, err := m.ScanConversations(session.ConversationQuery{OwnerTaskID: 12}, 1, 0); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 5 {
		t.Fatalf("an owner scan allocates %v", allocs)
	}
}
