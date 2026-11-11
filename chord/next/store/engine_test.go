package store

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Ports packages/chord/test/next/store/engine.test.ts: the engine (slice 6a §10, §11, slice 6b §1.5): documents as
// bases and deltas, their keys in code point order, the history rules at a new base and at retirement (R1), reads at a
// point, records inserted as they are.

const engineLimit = MaxSafeInteger

var engineOwner = DocumentAddress{Owner: "app.store", Namespace: "chord", Kind: "notes"}

// engineKit is a store on `:memory:` (or on `on`) and the helpers of the tests.
type engineKit struct {
	t     *testing.T
	store *ChordStore
}

func newEngine(t *testing.T, on ...*ChordStore) *engineKit {
	t.Helper()
	if len(on) > 0 {
		return &engineKit{t: t, store: on[0]}
	}
	return &engineKit{t: t, store: memoryStore(t)}
}

// commit commits writes and returns the created ids.
func (kit *engineKit) commit(writes ...StagedWrite) ([]int64, error) {
	kit.t.Helper()
	var created []int64
	fence := CommitFence{
		Admit: func() bool { return true },
		Adopt: func(_ int64, ids []int64) error { created = ids; return nil },
	}
	_, err := kit.store.Commit(writes, fence, background)
	return created, err
}

func (kit *engineKit) mustCommit(writes ...StagedWrite) []int64 {
	kit.t.Helper()
	created, err := kit.commit(writes...)
	noError(kit.t, err)
	return created
}

func (kit *engineKit) at(key string) DocumentAddress {
	address := engineOwner
	address.Key = &key
	return address
}

func engineBase(value delta.JsonValue, version int64) DocumentBase {
	return DocumentBase{Version: version, Fingerprint: fmt.Sprintf("f%d", version) + strings.Repeat("0", 14), Value: value}
}

func engineJSON(t *testing.T, value any) string { return nexttest.JSONText(t, value) }

func (kit *engineKit) create(key string, value delta.JsonValue) StagedWrite {
	return CreateDocument(kit.at(key), engineBase(value, 1), engineJSON(kit.t, value), nil)
}

func (kit *engineKit) delta(id, revision int64, opsText string) StagedWrite {
	return ChangeDocument(id, revision, DocumentContent{Kind: "delta", Version: 1, Ops: opsOf(kit.t, opsText)}, opsText)
}

func opsOf(t *testing.T, text string) []delta.Op {
	t.Helper()
	parsed, ok := nexttest.ParseJSON(t, text).([]any)
	if !ok {
		t.Fatalf("ops %s are no array", text)
	}
	ops := make([]delta.Op, len(parsed))
	for index, op := range parsed {
		ops[index] = op.([]any)
	}
	return ops
}

func emptyObject() delta.JsonValue { return delta.NewJsonObject(0) }

func engineRows(t *testing.T, store *ChordStore, sql string, params ...any) []sqlite.SqliteRow {
	t.Helper()
	rows, err := Read(store, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
		return executor.All(sql, params...)
	}, background)
	noError(t, err)
	return rows
}

func engineFind(t *testing.T, store *ChordStore, address DocumentAddress, at DocumentPoint) *DocumentRecord {
	t.Helper()
	record, err := Read(store, func(executor sqlite.SqliteExecutor) (*DocumentRecord, error) {
		return FindDocument(executor, address, at)
	}, background)
	noError(t, err)
	return record
}

func engineLoad(t *testing.T, store *ChordStore, record *DocumentRecord, at DocumentPoint) (StoredDocument, error) {
	t.Helper()
	return Read(store, func(executor sqlite.SqliteExecutor) (StoredDocument, error) {
		return LoadDocument(executor, *record, at)
	}, background)
}

// stagedApply is a staged write applying apply in the step (an adapter's composite).
func stagedApply(apply func(step *Step) ([]StoreWrite, error)) StagedWrite {
	return stagedWrite{order: OrderWrite, apply: apply}
}

// adapterRecord is an adapter's record of history at key, with an explicit id and the step's seq.
func adapterRecord(id int64, key string, history DocumentHistory, createdAt int64) DocumentRecord {
	return DocumentRecord{
		ID: id, Owner: engineOwner.Owner, Namespace: engineOwner.Namespace, Kind: engineOwner.Kind, Key: &key,
		History: history, CreatedAt: createdAt, Revision: 1, Version: 1, Fingerprint: "",
	}
}

// reimported is store's archive with edit applied to its parts, imported into a fresh `:memory:` store.
func reimported(t *testing.T, store *ChordStore, edit func(parts []ArchivePart) []ArchivePart) *ChordStore {
	t.Helper()
	parts := edit(exported(t, store, background))
	db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	noError(t, err)
	imported, err := ImportStore(SqliteBackend(db), SlicePartSource(parts), background, StoreOptions{})
	noError(t, err)
	opened := imported.(*ChordStore)
	t.Cleanup(func() { _ = opened.Close(background) })
	return opened
}

func TestEngineDocuments(t *testing.T) {
	t.Run("load as their base with the deltas after it applied; a new base prunes the older revisions", func(t *testing.T) {
		kit := newEngine(t)
		id := kit.mustCommit(kit.create("a", delta.JsonObjectOf("n", float64(0))))[0]
		kit.mustCommit(kit.delta(id, 1, `[["s",["n"],1]]`))
		kit.mustCommit(kit.delta(id, 2, `[["s",["n"],2]]`))
		found := engineFind(t, kit.store, kit.at("a"), CurrentPoint)
		if found == nil || found.ID != id || found.Revision != 3 || found.Version != 1 || found.History != "none" || found.CreatedAt != 1 {
			t.Fatalf("found = %+v", found)
		}
		loaded, err := engineLoad(t, kit.store, found, CurrentPoint)
		noError(t, err)
		if engineJSON(t, loaded.Value) != `{"n":2}` || loaded.DeltasSinceBase != 2 {
			t.Fatalf("loaded = %s, deltas %d", engineJSON(t, loaded.Value), loaded.DeltasSinceBase)
		}
		next := engineBase(delta.JsonObjectOf("m", float64(9)), 2)
		kit.mustCommit(ChangeDocument(id, 3, DocumentContent{Kind: "base", Version: next.Version, Fingerprint: next.Fingerprint, Value: next.Value}, engineJSON(t, next.Value)))
		rebased := engineFind(t, kit.store, kit.at("a"), CurrentPoint)
		if rebased.Revision != 4 || rebased.Version != 2 || rebased.Fingerprint != next.Fingerprint {
			t.Fatalf("rebased = %+v", rebased)
		}
		loaded, err = engineLoad(t, kit.store, rebased, CurrentPoint)
		noError(t, err)
		if engineJSON(t, loaded.Value) != `{"m":9}` || loaded.DeltasSinceBase != 0 {
			t.Fatalf("rebased load = %s, deltas %d", engineJSON(t, loaded.Value), loaded.DeltasSinceBase)
		}
		rows := engineRows(t, kit.store, `SELECT seq FROM `+TableRevisions)
		if len(rows) != 1 || rows[0]["seq"] != int64(4) {
			t.Fatalf("revisions = %v, want [{seq 4}]", rows)
		}
	})

	t.Run("list live keys in code point order, paging after a key", func(t *testing.T) {
		kit := newEngine(t)
		kit.mustCommit(kit.create("\U0001F600", emptyObject()), kit.create("\uFF5E", emptyObject()), kit.create("b", emptyObject()), kit.create("a", emptyObject()))
		list := func(after *string, limit int64) []string {
			keys, err := Read(kit.store, func(executor sqlite.SqliteExecutor) ([]string, error) {
				return ListDocumentKeys(executor, engineOwner, after, limit)
			}, background)
			noError(t, err)
			return keys
		}
		if got := list(nil, 10); !equalStrings(got, []string{"a", "b", "\uFF5E", "\U0001F600"}) {
			t.Fatalf("keys = %q", got)
		}
		b := "b"
		if got := list(&b, 1); !equalStrings(got, []string{"\uFF5E"}) {
			t.Fatalf("keys after b = %q", got)
		}
	})

	t.Run("retire a none record with its row and revisions (R1); the address takes a new incarnation", func(t *testing.T) {
		kit := newEngine(t)
		id := kit.mustCommit(kit.create("a", delta.JsonObjectOf("n", float64(0))))[0]
		next := kit.mustCommit(RetireDocument(id, 1), kit.create("a", delta.JsonObjectOf("n", float64(1))))[0]
		if next != 3 {
			t.Fatalf("next id = %d, want 3", next)
		}
		if rows := engineRows(t, kit.store, `SELECT id FROM `+TableDocuments); len(rows) != 1 || rows[0]["id"] != int64(3) {
			t.Fatalf("documents = %v", rows)
		}
		if rows := engineRows(t, kit.store, `SELECT document_id FROM `+TableRevisions); len(rows) != 1 || rows[0]["document_id"] != int64(3) {
			t.Fatalf("revisions = %v", rows)
		}
		if found := engineFind(t, kit.store, kit.at("a"), CurrentPoint); found == nil || found.ID != 3 {
			t.Fatalf("found = %+v", found)
		}
	})

	t.Run("refuse two live records at one address and a delta on another version: the step is refused", func(t *testing.T) {
		first := newEngine(t)
		first.mustCommit(first.create("a", emptyObject()))
		if _, err := first.commit(first.create("a", emptyObject())); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
			t.Fatalf("second create = %v, want UNIQUE", err)
		}
		second := newEngine(t)
		id := second.mustCommit(second.create("a", emptyObject()))[0]
		crossing := ChangeDocument(id, 1, DocumentContent{Kind: "delta", Version: 2, Ops: []delta.Op{}}, "[]")
		if _, err := second.commit(crossing); err == nil || !strings.Contains(err.Error(), "not live at revision 1") {
			t.Fatalf("crossing = %v, want not live at revision 1", err)
		}
		one, err := Read(second.store, func(sqlite.SqliteExecutor) (int, error) { return 1, nil }, background)
		if err != nil || one != 1 {
			t.Fatalf("Read after refusal = %v, %v", one, err)
		}
	})
}

// lifetime creates adapter record 10 at "a" (its base `{ n: 0 }`), then changes and retires it in later commits.
func lifetime(t *testing.T, history DocumentHistory) *engineKit {
	t.Helper()
	kit := newEngine(t)
	text := `{"n":0}`
	kit.mustCommit(stagedApply(func(step *Step) ([]StoreWrite, error) {
		made := adapterRecord(10, "a", history, step.Seq)
		return nil, InsertDocument(step, made, &text)
	}))
	kit.mustCommit(stagedApply(func(step *Step) ([]StoreWrite, error) {
		content := DocumentContent{Kind: "base", Version: 1, Fingerprint: "", Value: delta.JsonObjectOf("n", float64(1))}
		return nil, WriteContent(step, 10, history, 1, content, `{"n":1}`)
	}))
	kit.mustCommit(stagedApply(func(step *Step) ([]StoreWrite, error) {
		content := DocumentContent{Kind: "delta", Version: 1, Ops: opsOf(t, `[["s",["n"],2]]`)}
		return nil, WriteContent(step, 10, history, 2, content, `[["s",["n"],2]]`)
	}))
	// An adapter's retirement (Durable's): the record read in the step, retired by its history's rule.
	kit.mustCommit(stagedApply(func(step *Step) ([]StoreWrite, error) {
		record, err := ReadDocument(step, 10)
		if err != nil {
			return nil, err
		}
		return nil, RetireRecord(step, *record)
	}))
	return kit
}

func TestEngineHistoryRules(t *testing.T) {
	t.Run("keep a retired latest record's row without revisions, and every revision of a rewindable one", func(t *testing.T) {
		latest := lifetime(t, "latest")
		if rows := engineRows(t, latest.store, `SELECT id, retired_at FROM `+TableDocuments); len(rows) != 1 || rows[0]["id"] != int64(10) || rows[0]["retired_at"] != int64(4) {
			t.Fatalf("latest documents = %v", rows)
		}
		if rows := engineRows(t, latest.store, `SELECT seq FROM `+TableRevisions); len(rows) != 0 {
			t.Fatalf("latest revisions = %v, want none", rows)
		}
		rewindable := lifetime(t, "rewindable")
		rows := engineRows(t, rewindable.store, `SELECT seq, kind FROM `+TableRevisions+` ORDER BY seq`)
		want := []struct {
			seq  int64
			kind string
		}{{1, "base"}, {2, "base"}, {3, "delta"}}
		if len(rows) != len(want) {
			t.Fatalf("rewindable revisions = %v", rows)
		}
		for index, row := range rows {
			if row["seq"] != want[index].seq || row["kind"] != want[index].kind {
				t.Fatalf("rewindable revision %d = %v, want %+v", index, row, want[index])
			}
		}
	})

	t.Run("find a record at a point by its half-open lifetime, and load a rewindable one at any point inside it", func(t *testing.T) {
		kit := lifetime(t, "rewindable")
		points := []DocumentPoint{AtSeq(0), AtSeq(1), AtSeq(3), AtSeq(4), CurrentPoint}
		want := []int64{0, 10, 10, 0, 0}
		for index, point := range points {
			found := engineFind(t, kit.store, kit.at("a"), point)
			var id int64
			if found != nil {
				id = found.ID
			}
			if id != want[index] {
				t.Fatalf("find at %+v = %d, want %d", point, id, want[index])
			}
		}
		record := engineFind(t, kit.store, kit.at("a"), AtSeq(1))
		if record.RetiredAt == nil || *record.RetiredAt != 4 {
			t.Fatalf("retiredAt = %v, want 4", record.RetiredAt)
		}
		for _, c := range []struct {
			point int64
			n     string
		}{{1, "0"}, {2, "1"}, {3, "2"}} {
			loaded, err := engineLoad(t, kit.store, record, AtSeq(c.point))
			noError(t, err)
			if engineJSON(t, loaded.Value) != `{"n":`+c.n+`}` || loaded.Version != 1 {
				t.Fatalf("load at %d = %s v%d", c.point, engineJSON(t, loaded.Value), loaded.Version)
			}
		}
		listed, err := Read(kit.store, func(executor sqlite.SqliteExecutor) ([]DocumentRecord, error) {
			return ListDocuments(executor, DocumentScope{Owner: engineOwner.Owner, Namespace: engineOwner.Namespace}, AtSeq(3), 0, 10)
		}, background)
		noError(t, err)
		if len(listed) != 1 || listed[0].ID != 10 {
			t.Fatalf("list at 3 = %+v", listed)
		}
		current, err := Read(kit.store, func(executor sqlite.SqliteExecutor) ([]DocumentRecord, error) {
			return ListDocuments(executor, DocumentScope{Owner: engineOwner.Owner, Namespace: engineOwner.Namespace}, CurrentPoint, 0, 10)
		}, background)
		noError(t, err)
		if len(current) != 0 {
			t.Fatalf("list current = %+v, want none", current)
		}
	})

	t.Run("find a retired latest record at a point, whose content is gone", func(t *testing.T) {
		kit := lifetime(t, "latest")
		found := engineFind(t, kit.store, kit.at("a"), AtSeq(2))
		if found == nil || found.ID != 10 || found.RetiredAt == nil || *found.RetiredAt != 4 || found.History != "latest" {
			t.Fatalf("found = %+v", found)
		}
		// A read returns data (the store stops on a read's throw, slice 6b §1.4): the outcome as a value.
		outcome, err := Read(kit.store, func(executor sqlite.SqliteExecutor) (string, error) {
			if _, err := LoadDocument(executor, *found, AtSeq(2)); err != nil {
				return err.Error(), nil
			}
			return "loaded", nil
		}, background)
		noError(t, err)
		if !strings.Contains(outcome, "no base") {
			t.Fatalf("outcome = %q, want no base", outcome)
		}
	})

	t.Run("insert a create-plus-retire as an empty lifetime that never holds the live address", func(t *testing.T) {
		kit := newEngine(t)
		live := kit.mustCommit(kit.create("a", emptyObject()))[0]
		text := "{}"
		kit.mustCommit(stagedApply(func(step *Step) ([]StoreWrite, error) {
			record := adapterRecord(20, "a", "latest", step.Seq)
			retired := step.Seq
			record.RetiredAt = &retired
			return nil, InsertDocument(step, record, &text)
		}))
		if found := engineFind(t, kit.store, kit.at("a"), CurrentPoint); found == nil || found.ID != live {
			t.Fatalf("found = %+v, want id %d", found, live)
		}
		if rows := engineRows(t, kit.store, `SELECT document_id FROM `+TableRevisions+` WHERE document_id = 20`); len(rows) != 0 {
			t.Fatalf("revisions of 20 = %v", rows)
		}
	})
}

func TestEngineCounters(t *testing.T) {
	t.Run("allocate document ids up to the safe range after an import near it; past it the commit is refused", func(t *testing.T) {
		source := newEngine(t)
		near := func(next int64) *ChordStore {
			return reimported(t, source.store, func(parts []ArchivePart) []ArchivePart {
				manifest := *parts[0].(*ManifestPart)
				manifest.NextID = next
				return append([]ArchivePart{&manifest}, parts[1:]...)
			})
		}
		kit := newEngine(t, near(engineLimit-1))
		if created := kit.mustCommit(kit.create("a", delta.JsonObjectOf("n", float64(0)))); len(created) != 1 || created[0] != engineLimit-1 {
			t.Fatalf("created = %v, want [%d]", created, engineLimit-1)
		}
		if kit.store.NextID() != engineLimit {
			t.Fatalf("nextId = %d, want %d", kit.store.NextID(), engineLimit)
		}
		if _, err := kit.commit(kit.create("b", delta.JsonObjectOf("n", float64(0)))); err == nil || !strings.Contains(err.Error(), "ID space is exhausted") {
			t.Fatalf("create past the range = %v", err)
		}
		// Two creations in one commit where only one id is left: nothing of it is written.
		both := newEngine(t, near(engineLimit-1))
		_, err := both.commit(both.create("a", delta.JsonObjectOf("n", float64(0))), both.create("b", delta.JsonObjectOf("n", float64(0))))
		if err == nil || !strings.Contains(err.Error(), "ID space is exhausted") {
			t.Fatalf("two creations = %v", err)
		}
		if both.store.LastSeq() != 0 || both.store.NextID() != engineLimit-1 {
			t.Fatalf("[lastSeq, nextId] = [%d, %d], want [0, %d]", both.store.LastSeq(), both.store.NextID(), engineLimit-1)
		}
	})

	t.Run("count revisions up to the safe range after an import near it; a change past it is refused", func(t *testing.T) {
		source := newEngine(t)
		source.mustCommit(source.create("a", delta.JsonObjectOf("n", float64(0))))
		store := reimported(t, source.store, func(parts []ArchivePart) []ArchivePart {
			edited := make([]ArchivePart, len(parts))
			for index, part := range parts {
				if create, ok := part.(*DocumentCreate); ok {
					copied := *create
					copied.Record.Revision = engineLimit - 1
					part = &copied
				}
				edited[index] = part
			}
			return edited
		})
		kit := newEngine(t, store)
		found := engineFind(t, store, kit.at("a"), CurrentPoint)
		kit.mustCommit(kit.delta(found.ID, engineLimit-1, `[["s",["n"],1]]`))
		_, err := kit.commit(kit.delta(found.ID, engineLimit, `[["s",["n"],2]]`))
		if err == nil || !strings.Contains(err.Error(), "safe range") {
			t.Fatalf("change past the range = %v", err)
		}
	})
}
