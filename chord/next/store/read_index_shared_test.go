package store

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// pi: packages/chord/src/store/read-index.ts
// pi: packages/chord/src/store/shared-read.ts
// Tests for v1.7.0 store additions:
//   - ReadIndex commit index (eq / inArray + kept row keys, chord.query.skips)
//   - SharedReads identical live-read sharing (rows + count, unsubscribe/close)
//   - DecodedRows identity reuse on shifted windows
//   - ChordStore.SetOutbox runtime outbox toggling

func TestReadIndexCommitIndex(t *testing.T) {
	items, err := DefineTable("items", TableOptions{
		Version: 1,
		Columns: []ColumnProp{
			Col("id", Int()),
			Col("grp", Str()),
			Col("title", Str()),
		},
		Key:     []string{"id"},
		Indexes: IndexRecords{{Name: "by_grp", Columns: []string{"grp"}}},
	})
	must(t, err)

	s := openStoreAt(t, "")
	sqlName := "app.idx:items"
	fence := CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64, int) error { return nil }}
	must(t, PrepareTables([]DeclaredTable{{Definition: items, Table: sqlName}}, s, fence, "app.idx", background))
	handle := NewTableHandle(items, sqlName, s, newTestHandleOwner())

	// Open 20 reads across 20 different groups (grp = "ga" .. "gt").
	batch := state.NewBatch(func(state.BatchError) {}, telemetry.NoopTelemetryMetrics)
	site := ReadSite{Store: s, Batch: batch}
	var queries []*LiveQuery
	for i := range 20 {
		g := "g" + string(rune('a'+i))
		q := NewQuery(items).Where(func(row, _ ColumnRefs) Condition { return Eq(row["grp"], g) })
		lq := NewLiveQuery(site, &liveOwner{}, LiveShape{
			Kind:    "rows",
			Range:   Range{Limit: 10},
			Present: func(rows []delta.JsonValue, more bool) delta.JsonValue { return rows },
		})
		target := LiveTarget{
			Spec: QuerySpec{Query: q, Table: sqlName, Owner: handle},
			Decode: func(stored StoredRead, ctx context.Context) (delta.JsonValue, error) {
				return DecodedRowJSON(items, stored, ctx)
			},
		}
		must(t, lq.Load(target, background))
		queries = append(queries, lq)
	}
	t.Cleanup(func() {
		for _, lq := range queries {
			batch.Section(func() { lq.Detach(nil) })
		}
	})

	commitRow := func(id int64, grp, title string) {
		var staged []StagedWrite
		ch := NewTableChanges(handle, &staged)
		must(t, ch.Put(Row{"id": id, "grp": grp, "title": title}))
		refresh := NewReadRefresh(s.Reads, []string{sqlName}, ch.Changes, background)
		staged = append(staged, refresh.Write)
		_, err := s.Commit(staged, CommitFence{
			Admit: func() bool { return true },
			Adopt: func(int64, []int64, int) error {
				return batch.Within(refresh.Adopt, background)
			},
		}, background)
		must(t, err)
	}

	commitRow(1, "ga", "alpha")
	commitRow(2, "gb", "beta")

	valOf := func(lq *LiveQuery) []delta.JsonValue {
		var v delta.JsonValue
		batch.Section(func() {
			var err error
			v, err = lq.Value()
			must(t, err)
		})
		return v.([]delta.JsonValue)
	}
	if got := len(valOf(queries[0])); got != 1 {
		t.Fatalf("ga rows = %d, want 1", got)
	}
	if got := len(valOf(queries[1])); got != 1 {
		t.Fatalf("gb rows = %d, want 1", got)
	}
	if got := len(valOf(queries[2])); got != 0 {
		t.Fatalf("gc rows = %d, want 0", got)
	}

	// Move row 1 from "ga" to "gc": BOTH ga (kept key 1) and gc (bucket "gc") must update!
	commitRow(1, "gc", "moved")
	if got := len(valOf(queries[0])); got != 0 {
		t.Fatalf("ga rows after move = %d, want 0", got)
	}
	if got := len(valOf(queries[2])); got != 1 {
		t.Fatalf("gc rows after move = %d, want 1", got)
	}

	// Delete row 1: gc must see it removed via kept keys.
	{
		var staged []StagedWrite
		ch := NewTableChanges(handle, &staged)
		must(t, ch.Delete(Row{"id": int64(1)}))
		refresh := NewReadRefresh(s.Reads, []string{sqlName}, ch.Changes, background)
		staged = append(staged, refresh.Write)
		_, err := s.Commit(staged, CommitFence{
			Admit: func() bool { return true },
			Adopt: func(int64, []int64, int) error {
				return batch.Within(refresh.Adopt, background)
			},
		}, background)
		must(t, err)
	}
	if got := len(valOf(queries[2])); got != 0 {
		t.Fatalf("gc rows after delete = %d, want 0", got)
	}
}

func TestSharedReadsIdenticalLiveReadsShareOneRead(t *testing.T) {
	notes, err := DefineTable("notes", TableOptions{
		Version: 1,
		Columns: []ColumnProp{Col("id", Int()), Col("text", Str())},
		Key:     []string{"id"},
	})
	must(t, err)
	s := openStoreAt(t, "")
	sqlName := "app.shared:notes"
	fence := CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64, int) error { return nil }}
	must(t, PrepareTables([]DeclaredTable{{Definition: notes, Table: sqlName}}, s, fence, "app.shared", background))
	handle := NewTableHandle(notes, sqlName, s, newTestHandleOwner())

	batch := state.NewBatch(func(state.BatchError) {}, telemetry.NoopTelemetryMetrics)
	site := ReadSite{Store: s, Batch: batch}
	shared := NewSharedReads()
	q := NewQuery(notes)
	decodeCalls := 0
	makeTarget := func(args string) LiveTarget {
		return LiveTarget{
			Spec:    QuerySpec{Query: q, Table: sqlName, Owner: handle},
			Sharing: &Sharing{Shared: shared, Args: args},
			Decode: func(stored StoredRead, ctx context.Context) (delta.JsonValue, error) {
				decodeCalls++
				return DecodedRowJSON(notes, stored, ctx)
			},
		}
	}
	shape := LiveShape{
		Kind:    "rows",
		Range:   Range{Limit: 10},
		Present: func(rows []delta.JsonValue, more bool) delta.JsonValue { return rows },
	}

	lq1 := NewLiveQuery(site, &liveOwner{}, shape)
	lq2 := NewLiveQuery(site, &liveOwner{}, shape)
	must(t, lq1.Load(makeTarget(`{"a":1}`), background))
	must(t, lq2.Load(makeTarget(`{"a":1}`), background))

	// Both live queries share ONE registered read in the store index.
	if got := len(s.Reads.On(sqlName)); got != 1 {
		t.Fatalf("registered reads = %d, want 1 (shared)", got)
	}

	// Commit one row: decoded ONCE for both subscribers!
	var staged []StagedWrite
	ch := NewTableChanges(handle, &staged)
	must(t, ch.Put(Row{"id": int64(1), "text": "hello"}))
	refresh := NewReadRefresh(s.Reads, []string{sqlName}, ch.Changes, background)
	staged = append(staged, refresh.Write)
	_, err = s.Commit(staged, CommitFence{
		Admit: func() bool { return true },
		Adopt: func(int64, []int64, int) error { return batch.Within(refresh.Adopt, background) },
	}, background)
	must(t, err)

	if decodeCalls != 1 {
		t.Fatalf("decodeCalls = %d, want 1 (shared decode)", decodeCalls)
	}

	// Detach first subscriber: read stays open for second.
	batch.Section(func() { lq1.Detach(nil) })
	if got := len(s.Reads.On(sqlName)); got != 1 {
		t.Fatalf("registered reads after first detach = %d, want 1", got)
	}
	// Detach second subscriber: read closes and leaves index.
	batch.Section(func() { lq2.Detach(nil) })
	if got := len(s.Reads.On(sqlName)); got != 0 {
		t.Fatalf("registered reads after second detach = %d, want 0", got)
	}
}

// Ports packages/chord/src/store/store.ts:38 and :254 (Store.setOutbox).
func TestChordStoreSetOutbox(t *testing.T) {
	s := openStoreAt(t, t.TempDir()+"/outbox.sqlite")
	var st Store = s
	if st.Outbox() != nil {
		t.Fatalf("initial outbox = %v, want nil", st.Outbox())
	}
	must(t, st.SetOutbox(true, background))
	if s.Outbox() == nil || s.Outbox().Floor() != s.LastSeq() {
		t.Fatalf("outbox after SetOutbox(true) = %v", s.Outbox())
	}
	// Idempotent true.
	must(t, s.SetOutbox(true, background))
	// Turn off.
	must(t, s.SetOutbox(false, background))
	if s.Outbox() != nil {
		t.Fatalf("outbox after SetOutbox(false) = %v, want nil", s.Outbox())
	}
	// Idempotent false.
	must(t, s.SetOutbox(false, background))
}

type testHandleOwner struct{ scope *struct{} }

func newTestHandleOwner() *testHandleOwner     { return &testHandleOwner{scope: new(struct{})} }
func (*testHandleOwner) Label() string         { return "app.test" }
func (o *testHandleOwner) Scope() any          { return o.scope }
func (*testHandleOwner) AssertLive() error     { return nil }
func (*testHandleOwner) Fail(*data.ChordError) {}
