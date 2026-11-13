package store

// pi: packages/durable/src/storage/store/queries.ts
// TableQueries and the latest-head reads are Pi module internals (its public entry point exports none of them): the cases below
// assert the compiled reads of queries.ts against its text, over the tables StoreStorage declares.

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/durable"
)

func TestTableQueriesFirstAndSelectAreCompiledApartForOneShape(t *testing.T) {
	store, storage := recorded(t)
	var ids []durable.EntryId
	for range 3 {
		id := mintID[durable.EntryId](t, storage)
		ids = append(ids, id)
		value(commitWrites(storage, entryWrite(id)))
	}
	conditions := []Condition{{Column: "conversation_id", Op: chordstore.WhereEq, Value: int64(durable.ROOT_CONVERSATION_ID)}}
	entries := storage.queries.Entries
	// The same shape, read as many rows and as the first row, in both orders and then again: each is its own compiled read.
	for round := range 2 {
		type found struct{ many, first []durable.EntryId }
		got := map[string]found{}
		for name, order := range map[string]chordstore.Order{"descending": descending, "ascending": ascending} {
			result, err := chordstore.Read(store, func(executor sqlite.SqliteExecutor) (found, error) {
				var out found
				rows, err := entries.Select(executor, conditions, order, 10)
				if err != nil {
					return out, err
				}
				records, err := appendRecords[durable.EntryRecord](entries, rows, nil, background)
				if err != nil {
					return out, err
				}
				for _, record := range records {
					out.many = append(out.many, record.Id)
				}
				row, err := entries.First(executor, conditions, order)
				if err != nil || row == nil {
					return out, err
				}
				first, err := recordOf[durable.EntryRecord](entries, *row, background)
				out.first = []durable.EntryId{first.Id}
				return out, err
			}, background)
			must(t, err)
			got[name] = result
		}
		if d := got["descending"]; len(d.many) != 3 || d.many[0] != ids[2] || d.many[2] != ids[0] || len(d.first) != 1 || d.first[0] != ids[2] {
			t.Fatalf("round %d: descending select %v / first %v, want %v newest first", round, d.many, d.first, ids)
		}
		if a := got["ascending"]; len(a.many) != 3 || a.many[0] != ids[0] || a.many[2] != ids[2] || len(a.first) != 1 || a.first[0] != ids[0] {
			t.Fatalf("round %d: ascending select %v / first %v, want %v oldest first", round, a.many, a.first, ids)
		}
	}
	must(t, store.Close(background))
}

func TestTableQueriesSelectBindsItsLimit(t *testing.T) {
	store, storage := recorded(t)
	for range 3 {
		value(commitWrites(storage, entryWrite(mintID[durable.EntryId](t, storage))))
	}
	rows, err := chordstore.Read(store, func(executor sqlite.SqliteExecutor) ([]RecordRow, error) {
		return storage.queries.Entries.Select(executor, nil, ascending, 2)
	}, background)
	must(t, err)
	if len(rows) != 2 {
		t.Fatalf("Select(limit 2) = %d rows, want 2", len(rows))
	}
	must(t, store.Close(background))
}

func TestLatestHeadReadsTheNewestHeadEntryFirst(t *testing.T) {
	_, storage := recorded(t)
	root := durable.ROOT_CONVERSATION_ID
	entry := func(head *durable.EntryId) durable.EntryId {
		id := mintID[durable.EntryId](t, storage)
		value(commitWrites(storage, durable.EntryWrite{Value: durable.EntryRecord{Id: id, ConversationId: root, Kind: "k", Data: delta.JsonObjectOf(), Head: head}}))
		return id
	}
	first := entry(nil)
	firstMarker := entry(&first)
	between := entry(nil)
	secondMarker := entry(&first)
	last := entry(nil)
	cases := []struct {
		name string
		at   *durable.EntryId
		want durable.EntryId
	}{
		{"without a cutoff", nil, secondMarker},
		{"at the newest marker", &secondMarker, secondMarker},
		{"after the newest marker", &last, secondMarker},
		{"before the newest marker", &between, firstMarker},
		{"at the older marker", &firstMarker, firstMarker},
		{"before every marker", &first, 0},
	}
	for _, c := range cases {
		got := value(storage.FindLatestHeadMarker(background, root, c.at))
		var id durable.EntryId
		if got != nil {
			id = got.Id
		}
		if id != c.want {
			t.Errorf("%s: FindLatestHeadMarker = %d, want %d", c.name, id, c.want)
		}
	}
	// The compiled texts: the newest id of the conversation's heads, bound to a conversation, then bounded by an id.
	if !strings.Contains(storage.queries.LatestHeadAny, `ORDER BY "id" DESC LIMIT 1`) || !strings.Contains(storage.queries.LatestHeadAtBefore, `ORDER BY "id" DESC LIMIT 1`) {
		t.Fatalf("latest head reads = %s / %s, want newest first", storage.queries.LatestHeadAny, storage.queries.LatestHeadAtBefore)
	}
}

// versionedTable is a table whose record column is at version 2: a version-1 cell migrates on access (conversation 3 becomes 9).
func versionedTable(t *testing.T) *chordstore.OwnerTable {
	t.Helper()
	definition, err := chordstore.DefineTable("versioned", chordstore.TableOptions{
		Version: 1,
		Columns: []chordstore.ColumnProp{
			chordstore.Col("id", chordstore.Int()),
			chordstore.Col("record", chordstore.Json(contracts.Unknown(), chordstore.JsonColumnOptions{
				Version: 2,
				Migrate: func(old delta.JsonValue, _ int) (any, error) {
					return delta.JsonObjectOf("id", float64(9)), nil
				},
			})),
		},
		Key: []string{"id"},
	})
	must(t, err)
	return chordstore.NewOwnerTable(definition, "dur:versioned")
}

func TestTableQueriesSelectTheRecordVersionOnceItsColumnHasVersionsPastOne(t *testing.T) {
	table := versionedTable(t)
	queries, err := NewTableQueries(table, "id")
	must(t, err)
	if queries.versioned == nil || len(queries.columns) != 2 || queries.columns[1] != "record$v" {
		t.Fatalf("columns = %v, versioned = %v, want record and record$v read through the column's reader", queries.columns, queries.versioned != nil)
	}
	if !strings.Contains(queries.byID, `"record$v"`) {
		t.Fatalf("by-id read = %s, want the record version selected", queries.byID)
	}
	sql := queries.shape(nil, ascending, false)
	if !strings.Contains(sql, `"record$v"`) {
		t.Fatalf("shaped read = %s, want the record version selected", sql)
	}
	// A stored row's version is what the reader gets: version 1 migrates, version 2 is read as it is.
	for _, c := range []struct {
		version int64
		want    durable.ConversationId
	}{{1, 9}, {2, 3}} {
		row, err := queries.rowOf(sqlite.SqliteRow{"record": `{"id":3}`, "record$v": c.version}, false)
		must(t, err)
		if row.RecordV != c.version {
			t.Fatalf("RecordV = %d, want %d", row.RecordV, c.version)
		}
		record, err := recordOf[durable.ConversationRecord](queries, *row, background)
		must(t, err)
		if record.Id != c.want {
			t.Fatalf("version %d cell read as conversation %d, want %d", c.version, record.Id, c.want)
		}
	}
	if _, err := queries.rowOf(sqlite.SqliteRow{"record": `{"id":3}`}, false); err == nil {
		t.Fatal("a row of a versioned column without its version is an error")
	}
}

func TestTableQueriesOfAVersion1ColumnReadTheRecordOnly(t *testing.T) {
	_, storage := recorded(t)
	for _, queries := range []*TableQueries{storage.queries.Conversations, storage.queries.Tasks, storage.queries.Submissions} {
		if queries.versioned != nil || len(queries.columns) != 1 || queries.columns[0] != "record" {
			t.Fatalf("columns = %v, versioned = %v, want the record only", queries.columns, queries.versioned != nil)
		}
	}
	if got := storage.queries.Entries.extra; len(got) != 1 || got[0] != "commit_seq" {
		t.Fatalf("entries by-id extras = %v, want commit_seq", got)
	}
	if !strings.Contains(storage.queries.Entries.byID, `"commit_seq"`) || strings.Contains(storage.queries.Conversations.byID, `"commit_seq"`) {
		t.Fatalf("by-id reads = %s / %s, want commit_seq of entries only", storage.queries.Entries.byID, storage.queries.Conversations.byID)
	}
}
