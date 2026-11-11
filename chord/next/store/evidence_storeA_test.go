package store

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Evidence cases for rows of the store port: behavior upstream's tests check that the ported tests did not pin.

// Compile-time assertions for columns.ts 'row types > types rows with optional opt(...) fields'. The upstream case
// is a TypeScript type-level Equal<RowOf<...>> check, designed out under the type-level class: the Go typed API
// is that a nullable column exists only through Opt, which maps a ColumnSchema to a ColumnSchema and sets its
// `Optional` flag, and a row is `Row = map[string]any` in which a NULL optional column is an absent key. These
// declarations fail to compile when Opt, the flag or the row type change shape.
var (
	_ func(ColumnSchema) ColumnSchema = Opt
	_ bool                            = ColumnSchema{}.Optional
	_ bool                            = Column{}.Optional
	_ bool                            = ColumnRecord{}.Optional
	_ Row                             = map[string]any{}
)

// TestEvidenceStoreAColumnsOptionalRowType runs what the declarations above assert: an Opt column is optional, the
// others are not, and its record carries the flag.
func TestEvidenceStoreAColumnsOptionalRowType(t *testing.T) {
	columns, err := DescribeColumns([]ColumnProp{Col("id", Str()), Col("note", Opt(Str()))}, "t")
	if err != nil {
		t.Fatalf("DescribeColumns: %v", err)
	}
	if columns[0].Optional || !columns[1].Optional || !ColumnRecordOf(columns[1]).Optional || ColumnRecordOf(columns[0]).Optional {
		t.Fatalf("columns = %+v", columns)
	}
}

func TestEvidenceStoreAStoredTextNULAtStart(t *testing.T) {
	for _, text := range []string{"\x00", "\x00a"} {
		_, err := StoredText(text, "v")
		_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	}
}

func TestEvidenceStoreAAllocatorRaiseToValue(t *testing.T) {
	ids := NewIdAllocator(10)
	ids.Raise(10)
	if ids.Value() != 11 {
		t.Fatalf("Value after Raise(10) = %d, want 11", ids.Value())
	}
}

// TestEvidenceStoreAClearedJSONRecordsNoVersion: an update that clears a json(...) column records no version for it.
func TestEvidenceStoreAClearedJSONRecordsNoVersion(t *testing.T) {
	store := memoryStore(t, true)
	handle := tableIn(t, store, writesEdits(t))
	commitChanges(t, store, handle, func(changes *TableChanges) {
		noError(t, changes.Put(Row{"id": "a", "path": "x", "meta": delta.NewJsonObject(0)}))
		noError(t, changes.Update(Row{"id": "a"}, SetOf{Assign("meta", Undefined)}))
	})
	commits, err := store.Outbox().Read(0, 10, background)
	noError(t, err)
	want := `[
		{"kind":"put","table":"app.store:edits","row":{"id":"a","path":"x","hits":0,"meta":{}},"version":2,"columns":{"meta":3}},
		{"kind":"update","table":"app.store:edits","key":{"id":"a"},"set":{"meta":null},"columns":{},"count":1}
	]`
	nexttest.RequireJSONEqual(t, writesJSON(t, commits[len(commits)-1].Writes), wantJSON(t, want))
}

// TestEvidenceStoreADropIndexRecord: a dropIndex write removes the index from the table's record, others stay.
func TestEvidenceStoreADropIndexRecord(t *testing.T) {
	store := memoryStore(t, true)
	tableIn(t, store, writesEdits(t))
	_, err := store.Commit([]StagedWrite{
		StageIndex(&IndexWrite{Table: writesTable, Name: "by_hits", Columns: []string{"hits"}}),
		StageIndex(&IndexWrite{Table: writesTable, Name: "by_note", Columns: []string{"note"}}),
	}, openFence, background)
	noError(t, err)
	_, err = store.Commit([]StagedWrite{StageEdit(writesTable, 2, TableEdit{Kind: "dropIndex", Name: "by_hits"})}, openFence, background)
	noError(t, err)
	record := store.Table(writesTable)
	for name, want := range map[string]bool{"by_path": true, "by_hits": false, "by_note": true} {
		if _, ok := record.Indexes.Get(name); ok != want {
			t.Fatalf("index %s present = %v, want %v (indexes %+v)", name, ok, want, record.Indexes)
		}
	}
}

// TestEvidenceStoreAOutboxBounds: Read refuses a non-positive limit and an afterSeq outside floor..lastSeq (the
// bounds themselves are fine); Trim through lastSeq is allowed, past it refused.
func TestEvidenceStoreAOutboxBounds(t *testing.T) {
	store := memoryStore(t, true)
	outboxCreated(t, store, "a")
	outboxCreated(t, store, "b")
	outbox := store.Outbox()
	_, err := outbox.Read(0, 0, background)
	_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	_, err = outbox.Read(3, 1, background)
	_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	noError(t, outbox.Trim(1, background))
	_, err = outbox.Read(0, 1, background)
	_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	commits, err := outbox.Read(1, 5, background)
	noError(t, err)
	if len(commits) != 1 || commits[0].Seq != 2 {
		t.Fatalf("commits after the floor = %+v", commits)
	}
	commits, err = outbox.Read(2, 5, background)
	noError(t, err)
	if len(commits) != 0 {
		t.Fatalf("commits after lastSeq = %+v", commits)
	}
	noError(t, outbox.Trim(2, background))
	if outbox.Floor() != 2 {
		t.Fatalf("floor = %d, want 2", outbox.Floor())
	}
	_ = rejected(t, outbox.Trim(3, background), data.CodeInvalid, data.ReasonArgs)
}

// TestEvidenceStoreABatchSeqGap: a commit that skips a seq does not follow the one before it.
func TestEvidenceStoreABatchSeqGap(t *testing.T) {
	all := batchCommits(t, batchEveryKind(t))
	gapped := []OutboxCommit{all[0], all[1]}
	gapped[1].Seq += 5
	err := ApplyBatch(memoryStore(t), gapped, background)
	refused := rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	if !strings.Contains(refused.Error(), "does not follow") {
		t.Fatalf("error = %v, want it to say the commit does not follow", refused)
	}
}
