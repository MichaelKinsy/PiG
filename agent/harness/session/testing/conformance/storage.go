package conformance

import (
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
	sessiontesting "github.com/MichaelKinsy/PiG/agent/harness/session/testing"
)

var (
	testName  = session.MustValue[string]("test.session.name", "")
	testValue = func(key string) session.Value[session.JsonValue] {
		return session.MustValue[session.JsonValue]("test.value", key)
	}
	testValuePrefix = func(prefix string) session.Value[session.JsonValue] {
		return session.MustValue[session.JsonValue]("test.value", prefix)
	}
	testList = func(key string) session.ValueList[session.JsonValue] {
		return session.MustList[session.JsonValue]("test.list", key)
	}
)

type storageTest func(t *testing.T, storage session.Storage)

func storageCase(factory func() (sessiontesting.StorageFixture, error), group, name string, test storageTest) sessiontesting.ConformanceCase {
	return sessiontesting.ConformanceCase{Group: group, Name: name, Run: func(t *testing.T) {
		fixture, err := factory()
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := fixture.Close(); err != nil {
				t.Error(err)
			}
		}()
		test(t, fixture.Storage)
	}}
}

func commit(t *testing.T, storage session.Storage, writes ...session.Write) session.CommitResult {
	t.Helper()
	return must(storage.Commit(background, writes))(t)
}

func assertCommitStats(t *testing.T, storage session.Storage, result session.CommitResult) {
	t.Helper()
	assertJSONEqual(t, result.Stats, must(storage.GetStats(background))(t))
}

func assertStrictlyIncreasing(t *testing.T, values []int64) {
	t.Helper()
	for index := 1; index < len(values); index++ {
		if values[index-1] >= values[index] {
			t.Fatalf("expected %v to be strictly increasing", values)
		}
	}
}

func getValue[T any](t *testing.T, reader session.SessionReader, address session.Value[T]) *session.StoredValue[T] {
	t.Helper()
	return must(session.GetValue(background, reader, address))(t)
}

func asc() session.EntryScan      { return session.EntryScan{Order: session.OrderAsc} }
func ascUsage() session.UsageScan { return session.UsageScan{Order: session.OrderAsc} }

// CreateStorageConformance creates fresh, runner-independent cases for the
// durable Storage contract.
func CreateStorageConformance(factory func() (sessiontesting.StorageFixture, error)) []sessiontesting.ConformanceCase {
	cases := []struct {
		group, name string
		test        storageTest
	}{
		{"transactions", "commits mixed writes atomically in write order", testMixedWrites},
		{"transactions", "rolls back every store when a mixed transaction fails", testMixedRollback},
		{"transactions", "preserves overwritten and deleted values when a transaction fails", testValueRollback},
		{"transactions", "enforces one shared entry and usage id namespace", testSharedIDNamespace},
		{"transactions", "resolves parents only from prior entries and earlier writes", testParentResolution},
		{"transactions", "places pending content under its reserved entry id", testPendingPlacement},
		{"values", "sets, replaces, deletes, and recreates values without tombstones", testValueLifecycle},
		{"values", "applies same-transaction value and list operations in write order", testWriteOrder},
		{"values", "does not change historical stores during value-only commits", testValueOnlyCommits},
		{"lists", "pages appends by global sequence and deletes whole lists", testListPaging},
		{"lists", "clamps one read page without limiting list growth", testListClamp},
		{"lists", "commits mixed list writes atomically and rolls them back with siblings", testListAtomicity},
		{"entry queries", "stores custom entries with and without data", testCustomEntryData},
		{"entry queries", "scans global entries with explicit ranges, filters, orders, and limits", testScanEntries},
		{"branch queries", "applies stops before filters and cursors before limits", testBranchQuery},
		{"branch queries", "returns branch structure without payload fields", testBranchStructure},
		{"branch queries", "applies branch query semantics to structure scans", testStructureQuery},
		{"usage and stats", "scans the usage ledger with explicit ranges, orders, and limits", testScanUsage},
		{"usage and stats", "keeps stats equal to message count and ledger totals", testStats},
		{"serialization", "serializes back-to-back commits in admission order", testSerialization},
		{"lifecycle", "seals admission, drains admitted commits, and closes idempotently", testStorageLifecycle},
	}
	out := make([]sessiontesting.ConformanceCase, 0, len(cases))
	for _, testCase := range cases {
		out = append(out, storageCase(factory, testCase.group, testCase.name, testCase.test))
	}
	return out
}

func testMixedWrites(t *testing.T, storage session.Storage) {
	row := session.UsageRow{ID: "usage", Usage: usage(2, 3, nil, nil), EntryID: new("entry")}
	result := commit(t, storage, session.InsertEntry(rootUser("entry")), session.SetValue(testName, "session"), session.InsertUsage(row))
	equal(t, len(result.Seqs), 3)
	equal(t, result.FirstSeq, result.Seqs[0])
	assertCommitStats(t, storage, result)
	assertStrictlyIncreasing(t, result.Seqs)
	if result.Timestamp < 0 || result.Timestamp > 1<<53-1 {
		t.Fatalf("timestamp = %d", result.Timestamp)
	}
	assertJSONEqual(t, must(storage.GetEntries(background, []string{"entry"}))(t), map[string]session.Entry{"entry": stamped(rootUser("entry"), result.Seqs[0], result.Timestamp)})
	assertJSONEqual(t, getValue(t, storage, testName), session.StoredValue[string]{Address: testName, Value: "session", Seq: result.Seqs[1]})
	row.Seq = result.Seqs[2]
	assertJSONEqual(t, must(storage.ScanUsage(background, ascUsage()))(t), []session.UsageRow{row})
}

func testMixedRollback(t *testing.T, storage session.Storage) {
	commit(t, storage, session.InsertEntry(rootUser("root")), session.InsertUsage(session.UsageRow{ID: "taken", Usage: usage(1, 1, nil, nil)}))
	entriesBefore := must(storage.ScanEntries(background, asc()))(t)
	usageBefore := must(storage.ScanUsage(background, ascUsage()))(t)
	statsBefore := must(storage.GetStats(background))(t)
	_, err := storage.Commit(background, []session.Write{
		session.SetValue(testName, "transient"),
		session.InsertEntry(note("transient-entry", new("root"))),
		session.InsertUsage(session.UsageRow{ID: "transient-usage", Usage: usage(5, 8, nil, nil), Adjustment: true}),
		session.InsertEntry(note("taken", new("root"))),
	})
	mustErr(t, err, "mixed commit with a duplicate id")
	assertJSONEqual(t, must(storage.ScanEntries(background, asc()))(t), entriesBefore)
	assertJSONEqual(t, must(storage.ScanUsage(background, ascUsage()))(t), usageBefore)
	assertJSONEqual(t, must(storage.GetStats(background))(t), statsBefore)
	if value := getValue(t, storage, testName); value != nil {
		t.Fatalf("transient value = %#v", value)
	}
}

func testValueRollback(t *testing.T, storage session.Storage) {
	commit(t, storage,
		session.SetValue(testValue("overwritten"), session.JsonValue("original")),
		session.SetValue(testValue("deleted"), session.JsonValue(map[string]any{"kept": true})),
		session.InsertEntry(rootUser("taken")),
	)
	overwrittenBefore := getValue(t, storage, testValue("overwritten"))
	deletedBefore := getValue(t, storage, testValue("deleted"))
	_, err := storage.Commit(background, []session.Write{
		session.SetValue(testValue("overwritten"), session.JsonValue("transient")),
		session.DeleteValue(testValue("deleted")),
		session.InsertEntry(note("transient", new("taken"))),
		session.InsertEntry(note("taken", nil)),
	})
	mustErr(t, err, "commit with a duplicate entry id")
	assertJSONEqual(t, getValue(t, storage, testValue("overwritten")), overwrittenBefore)
	assertJSONEqual(t, getValue(t, storage, testValue("deleted")), deletedBefore)
	if _, ok := must(storage.GetEntries(background, []string{"transient"}))(t)["transient"]; ok {
		t.Fatal("transient entry committed")
	}
}

func testSharedIDNamespace(t *testing.T, storage session.Storage) {
	commit(t, storage, session.InsertEntry(rootUser("existing-entry")), session.InsertUsage(session.UsageRow{ID: "existing-usage", Usage: usage(1, 1, nil, nil)}))
	_, err := storage.Commit(background, []session.Write{session.InsertUsage(session.UsageRow{ID: "existing-entry", Usage: usage(2, 2, nil, nil)})})
	mustErr(t, err, "usage row reusing an entry id")
	_, err = storage.Commit(background, []session.Write{session.InsertEntry(note("existing-usage", nil))})
	mustErr(t, err, "entry reusing a usage id")
	for id, writes := range map[string][]session.Write{
		"entry-then-usage": {session.InsertEntry(note("entry-then-usage", nil)), session.InsertUsage(session.UsageRow{ID: "entry-then-usage", Usage: usage(3, 3, nil, nil)})},
		"usage-then-entry": {session.InsertUsage(session.UsageRow{ID: "usage-then-entry", Usage: usage(4, 4, nil, nil)}), session.InsertEntry(note("usage-then-entry", nil))},
	} {
		_, err := storage.Commit(background, writes)
		mustErr(t, err, "duplicate id "+id)
	}
	assertJSONEqual(t, entryIDs(must(storage.ScanEntries(background, asc()))(t)), []string{"existing-entry"})
	assertJSONEqual(t, usageIDs(must(storage.ScanUsage(background, ascUsage()))(t)), []string{"existing-usage"})
}

func testParentResolution(t *testing.T, storage session.Storage) {
	commit(t, storage, session.InsertEntry(rootUser("root")))
	commit(t, storage, session.InsertEntry(note("child", new("root"))), session.InsertEntry(note("grandchild", new("child"))))
	assertJSONEqual(t, entryIDs(must(storage.ScanBranch(background, session.StorageBranchScan{Start: "grandchild", Order: session.OrderOldestFirst}))(t)), []string{"root", "child", "grandchild"})
	_, err := storage.Commit(background, []session.Write{
		session.InsertEntry(note("before-parent", new("later-parent"))),
		session.InsertEntry(note("later-parent", new("root"))),
		session.SetValue(session.EntryLabel("before-parent"), "transient"),
	})
	mustErr(t, err, "entry naming a later parent")
	_, err = storage.Commit(background, []session.Write{session.InsertEntry(note("orphan", new("missing")))})
	mustErr(t, err, "orphan entry")
	commit(t, storage, session.InsertUsage(session.UsageRow{ID: "usage-is-not-parent", Usage: usage(1, 1, nil, nil)}))
	_, err = storage.Commit(background, []session.Write{session.InsertEntry(note("usage-child", new("usage-is-not-parent")))})
	mustErr(t, err, "entry parented to a usage row")
	assertJSONEqual(t, must(storage.GetEntries(background, []string{"before-parent", "later-parent", "orphan", "usage-child"}))(t), map[string]session.Entry{})
	if value := getValue(t, storage, session.EntryLabel("before-parent")); value != nil {
		t.Fatalf("label = %#v", value)
	}
}

func testPendingPlacement(t *testing.T, storage session.Storage) {
	entry := userEntry("reserved", nil, "queued")
	pending := session.PendingEntry{Type: session.PendingEntryMessage, Message: entry.Message}
	commit(t, storage, session.SetValue(session.PendingEntryValue(entry.ID), pending), session.SetValue(session.BranchTip("main"), nil))
	if _, ok := must(storage.GetEntries(background, []string{entry.ID}))(t)[entry.ID]; ok {
		t.Fatal("pending content was placed")
	}
	assertJSONEqual(t, getValue(t, storage, session.PendingEntryValue(entry.ID)).Value, pending)
	if tip := getValue(t, storage, session.BranchTip("main")); tip == nil || tip.Value != nil {
		t.Fatalf("tip = %#v", tip)
	}
	placement := commit(t, storage, session.InsertEntry(entry), session.DeleteValue(session.PendingEntryValue(entry.ID)), session.SetValue(session.BranchTip("main"), &entry.ID))
	assertJSONEqual(t, must(storage.GetEntries(background, []string{entry.ID}))(t), map[string]session.Entry{entry.ID: stamped(entry, placement.Seqs[0], placement.Timestamp)})
	if value := getValue(t, storage, session.PendingEntryValue(entry.ID)); value != nil {
		t.Fatalf("pending value survived placement: %#v", value)
	}
	assertJSONEqual(t, getValue(t, storage, session.BranchTip("main")), session.StoredValue[*string]{Address: session.BranchTip("main"), Value: &entry.ID, Seq: placement.Seqs[2]})
}

func testValueLifecycle(t *testing.T, storage session.Storage) {
	first := commit(t, storage,
		session.SetValue(testValue("prefix/b"), session.JsonValue(1)),
		session.SetValue(testValue("prefix/a"), session.JsonValue(2)),
		session.SetValue(testValue("other"), session.JsonValue(3)),
		session.SetValue(testValue("prefix/"), session.JsonValue(4)),
		session.SetValue(testValue("prefix/\U00010000"), session.JsonValue(5)),
		session.SetValue(testValue("prefix/a"), session.JsonValue(nil)),
	)
	nullValue := getValue(t, storage, testValue("prefix/a"))
	if nullValue == nil || nullValue.Value != nil || nullValue.Seq != first.Seqs[5] {
		t.Fatalf("null value = %#v", nullValue)
	}
	second := commit(t, storage,
		session.DeleteValue(testValue("prefix/a")),
		session.DeleteValue(testValue("absent")),
		session.SetValue(testValue("prefix/a"), session.JsonValue("recreated")),
	)
	assertJSONEqual(t, must(session.ScanValues(background, storage, testValuePrefix("prefix/")))(t), []session.StoredValue[session.JsonValue]{
		{Address: testValue("prefix/a"), Value: "recreated", Seq: second.Seqs[2]},
		{Address: testValue("prefix/b"), Value: 1, Seq: first.Seqs[0]},
		{Address: testValue("prefix/"), Value: 4, Seq: first.Seqs[3]},
		{Address: testValue("prefix/\U00010000"), Value: 5, Seq: first.Seqs[4]},
	})
	if value := getValue(t, storage, testValue("absent")); value != nil {
		t.Fatalf("absent value = %#v", value)
	}
}

func testWriteOrder(t *testing.T, storage session.Storage) {
	keptValue, deletedValue := testValue("write-order/kept"), testValue("write-order/deleted")
	keptList, deletedList := testList("write-order/kept"), testList("write-order/deleted")
	result := commit(t, storage,
		session.SetValue(deletedValue, session.JsonValue("transient")),
		session.DeleteValue(deletedValue),
		session.SetValue(keptValue, session.JsonValue("transient")),
		session.SetValue(keptValue, session.JsonValue("kept")),
		session.AppendList(keptList, session.JsonValue("transient")),
		session.DeleteList(keptList),
		session.AppendList(keptList, session.JsonValue("kept")),
		session.AppendList(deletedList, session.JsonValue("transient")),
		session.DeleteList(deletedList),
	)
	if value := getValue(t, storage, deletedValue); value != nil {
		t.Fatalf("deleted value = %#v", value)
	}
	assertJSONEqual(t, getValue(t, storage, keptValue), session.StoredValue[session.JsonValue]{Address: keptValue, Value: "kept", Seq: result.Seqs[3]})
	assertJSONEqual(t, must(session.ReadList(background, storage, keptList, nil))(t), []session.ListElement[session.JsonValue]{{Seq: result.Seqs[6], Value: "kept"}})
	assertJSONEqual(t, must(session.ReadList(background, storage, deletedList, nil))(t), []session.ListElement[session.JsonValue]{})
}

func testValueOnlyCommits(t *testing.T, storage session.Storage) {
	commit(t, storage, session.InsertEntry(rootUser("root")), session.InsertUsage(session.UsageRow{ID: "historical-usage", Usage: usage(2, 3, nil, nil)}))
	entriesBefore := must(storage.ScanEntries(background, asc()))(t)
	usageBefore := must(storage.ScanUsage(background, ascUsage()))(t)
	statsBefore := must(storage.GetStats(background))(t)
	result := commit(t, storage, session.SetValue(testName, "first"), session.SetValue(testName, "second"))
	assertJSONEqual(t, must(storage.ScanEntries(background, asc()))(t), entriesBefore)
	assertJSONEqual(t, must(storage.ScanUsage(background, ascUsage()))(t), usageBefore)
	assertJSONEqual(t, must(storage.GetStats(background))(t), statsBefore)
	assertJSONEqual(t, getValue(t, storage, testName), session.StoredValue[string]{Address: testName, Value: "second", Seq: result.Seqs[1]})
}

func readList(t *testing.T, storage session.Storage, address session.ValueList[session.JsonValue], options *session.ListReadOptions) []session.ListElement[session.JsonValue] {
	t.Helper()
	return must(session.ReadList(background, storage, address, options))(t)
}

func testListPaging(t *testing.T, storage session.Storage) {
	address := testList("events")
	equal(t, len(readList(t, storage, address, nil)), 0)
	result := commit(t, storage,
		session.AppendList(address, session.JsonValue("a")),
		session.SetValue(testName, "gap"),
		session.AppendList(address, session.JsonValue("b")),
		session.AppendList(address, session.JsonValue("c")),
	)
	a := session.ListElement[session.JsonValue]{Seq: result.Seqs[0], Value: "a"}
	b := session.ListElement[session.JsonValue]{Seq: result.Seqs[2], Value: "b"}
	c := session.ListElement[session.JsonValue]{Seq: result.Seqs[3], Value: "c"}
	assertJSONEqual(t, readList(t, storage, address, nil), []any{a, b, c})
	assertJSONEqual(t, readList(t, storage, address, &session.ListReadOptions{Limit: new(2)}), []any{a, b})
	assertJSONEqual(t, readList(t, storage, address, &session.ListReadOptions{Cursor: &session.ListCursor{Seq: result.Seqs[0]}, Limit: new(2)}), []any{b, c})
	assertJSONEqual(t, readList(t, storage, address, &session.ListReadOptions{Order: session.OrderDesc, Limit: new(2)}), []any{c, b})
	assertJSONEqual(t, readList(t, storage, address, &session.ListReadOptions{Order: session.OrderDesc, Cursor: &session.ListCursor{Seq: result.Seqs[3]}, Limit: new(2)}), []any{b, a})
	_, err := storage.ReadList(background, address.StoredAddressBase, &session.ListReadOptions{Limit: new(0)})
	mustErr(t, err, "zero list limit")
	_, err = storage.ReadList(background, address.StoredAddressBase, &session.ListReadOptions{Limit: new(1 << 62)})
	mustErr(t, err, "unsafe list limit")
	commit(t, storage, session.DeleteList(address), session.DeleteList(testList("absent")), session.AppendList(address, session.JsonValue("new")))
	assertJSONEqual(t, listValues(must(storage.ReadList(background, address.StoredAddressBase, nil))(t)), []any{"new"})
}

func testListClamp(t *testing.T, storage session.Storage) {
	address := testList("large")
	writes := make([]session.Write, 0, 10_001)
	for index := range 10_001 {
		writes = append(writes, session.AppendList(address, session.JsonValue(index)))
	}
	commit(t, storage, writes...)
	firstPage := readList(t, storage, address, nil)
	equal(t, len(firstPage), 1_000)
	equal(t, len(readList(t, storage, address, &session.ListReadOptions{Limit: new(20_000)})), 10_000)
	equal(t, len(readList(t, storage, address, &session.ListReadOptions{Cursor: &session.ListCursor{Seq: firstPage[len(firstPage)-1].Seq}})), 1_000)
}

func testListAtomicity(t *testing.T, storage session.Storage) {
	address := testList("atomic")
	committed := commit(t, storage,
		session.InsertEntry(rootUser("mixed")),
		session.AppendList(address, session.JsonValue("kept")),
		session.SetValue(testName, "kept"),
		session.InsertUsage(session.UsageRow{ID: "mixed-usage", Usage: usage(1, 2, nil, nil)}),
	)
	kept := []session.ListElement[session.JsonValue]{{Seq: committed.Seqs[1], Value: "kept"}}
	assertJSONEqual(t, readList(t, storage, address, nil), kept)
	_, err := storage.Commit(background, []session.Write{session.AppendList(address, session.JsonValue("transient")), session.DeleteValue(testName), session.InsertEntry(rootUser("mixed"))})
	mustErr(t, err, "list write with a duplicate sibling entry")
	assertJSONEqual(t, readList(t, storage, address, nil), kept)
	equal(t, getValue(t, storage, testName).Value, "kept")
}

func testCustomEntryData(t *testing.T, storage session.Storage) {
	withData := customEntry("with-data", new("without-data"), "note", map[string]any{"nested": []any{1, 2}})
	result := commit(t, storage, session.InsertEntry(bareCustom("without-data", nil, "marker")), session.InsertEntry(withData))
	entries := must(storage.GetEntries(background, []string{"without-data", "with-data"}))(t)
	assertJSONEqual(t, entries, map[string]session.Entry{
		"without-data": stamped(bareCustom("without-data", nil, "marker"), result.Seqs[0], result.Timestamp),
		"with-data":    stamped(withData, result.Seqs[1], result.Timestamp),
	})
	if entries["without-data"].Data != nil {
		t.Fatalf("absent data became present: %#v", entries["without-data"].Data)
	}
}

func testScanEntries(t *testing.T, storage session.Storage) {
	result := commit(t, storage,
		session.InsertEntry(rootUser("root")),
		session.InsertEntry(customEntry("note-1", new("root"), "note", map[string]any{"id": "note-1"})),
		session.InsertEntry(customEntry("other", new("note-1"), "other", map[string]any{"id": "other"})),
		session.InsertEntry(customEntry("note-2", new("other"), "note", map[string]any{"id": "note-2"})),
		session.InsertEntry(userEntry("tail", new("note-2"), "tail")),
	)
	assertJSONEqual(t, entryIDs(must(storage.ScanEntries(background, session.EntryScan{Type: session.EntryTypeCustom, CustomType: "note", FromSeq: &result.Seqs[1], ToSeq: &result.Seqs[3], Order: session.OrderDesc}))(t)), []string{"note-2", "note-1"})
	assertJSONEqual(t, entryIDs(must(storage.ScanEntries(background, session.EntryScan{Order: session.OrderAsc, Limit: new(2)}))(t)), []string{"root", "note-1"})
	assertJSONEqual(t, entryIDs(must(storage.ScanEntries(background, session.EntryScan{Order: session.OrderDesc, Limit: new(2)}))(t)), []string{"tail", "note-2"})
}

func commitBranchFixture(t *testing.T, storage session.Storage) session.CommitResult {
	t.Helper()
	return commit(t, storage,
		session.InsertEntry(rootUser("root")),
		session.InsertEntry(customEntry("marker", new("root"), "marker", map[string]any{"id": "marker"})),
		session.InsertEntry(userEntry("middle", new("marker"), "middle")),
		session.InsertEntry(compactionEntry("compact", new("middle"))),
		session.InsertEntry(customEntry("note", new("compact"), "note", map[string]any{"id": "note"})),
		session.InsertEntry(userEntry("leaf", new("note"), "leaf")),
	)
}

func scanIDs(t *testing.T, storage session.Storage, query session.StorageBranchScan) []string {
	t.Helper()
	return entryIDs(must(storage.ScanBranch(background, query))(t))
}

func testBranchQuery(t *testing.T, storage session.Storage) {
	result := commitBranchFixture(t, storage)
	assertJSONEqual(t, scanIDs(t, storage, session.StorageBranchScan{Start: "leaf", StopAtType: session.EntryTypeCompaction, Type: session.EntryTypeMessage}), []string{"leaf"})
	assertJSONEqual(t, scanIDs(t, storage, session.StorageBranchScan{Start: "leaf", Order: session.OrderOldestFirst, StopAtID: "middle", Type: session.EntryTypeCustom}), []string{"marker"})
	assertJSONEqual(t, scanIDs(t, storage, session.StorageBranchScan{Start: "leaf", Order: session.OrderNewestFirst, Cursor: &session.EntryCursor{Seq: result.Seqs[4]}, Limit: new(2)}), []string{"compact", "middle"})
	assertJSONEqual(t, scanIDs(t, storage, session.StorageBranchScan{Start: "leaf", Order: session.OrderOldestFirst, Cursor: &session.EntryCursor{Seq: result.Seqs[1]}, Limit: new(2)}), []string{"middle", "compact"})
	assertJSONEqual(t, scanIDs(t, storage, session.StorageBranchScan{Start: "leaf", StopAtID: "leaf", Type: session.EntryTypeCustom}), []string{})
	assertJSONEqual(t, scanIDs(t, storage, session.StorageBranchScan{Start: "leaf", CustomType: "note"}), []string{"note"})
	_, err := storage.ScanBranch(background, session.StorageBranchScan{Start: "missing"})
	mustErr(t, err, "scan from a missing start")
}

func testBranchStructure(t *testing.T, storage session.Storage) {
	result := commit(t, storage, session.InsertEntry(rootUser("root")), session.InsertEntry(customEntry("child", new("root"), "note", map[string]any{"id": "child"})))
	assertJSONEqual(t, must(storage.ScanBranchStructure(background, session.StorageBranchScan{Start: "child", Order: session.OrderOldestFirst}))(t), []session.EntryStructure{
		{ID: "root", Seq: result.Seqs[0], Timestamp: result.Timestamp, Type: session.EntryTypeMessage},
		{ID: "child", ParentID: new("root"), Seq: result.Seqs[1], Timestamp: result.Timestamp, Type: session.EntryTypeCustom, CustomType: "note"},
	})
}

func testStructureQuery(t *testing.T, storage session.Storage) {
	result := commitBranchFixture(t, storage)
	assertJSONEqual(t, structureIDs(must(storage.ScanBranchStructure(background, session.StorageBranchScan{Start: "leaf", StopAtType: session.EntryTypeCompaction, Type: session.EntryTypeMessage}))(t)), []string{"leaf"})
	assertJSONEqual(t, structureIDs(must(storage.ScanBranchStructure(background, session.StorageBranchScan{Start: "leaf", Order: session.OrderOldestFirst, Cursor: &session.EntryCursor{Seq: result.Seqs[1]}, Limit: new(2)}))(t)), []string{"middle", "compact"})
	_, err := storage.ScanBranchStructure(background, session.StorageBranchScan{Start: "missing"})
	mustErr(t, err, "structure scan from a missing start")
}

func testScanUsage(t *testing.T, storage session.Storage) {
	result := commit(t, storage,
		session.InsertUsage(session.UsageRow{ID: "usage-1", Usage: usage(1, 1, nil, nil)}),
		session.SetValue(testName, "sequence gap"),
		session.InsertUsage(session.UsageRow{ID: "usage-2", Usage: usage(2, 2, nil, nil)}),
		session.InsertUsage(session.UsageRow{ID: "usage-3", Usage: usage(3, 3, nil, nil), Adjustment: true}),
	)
	assertJSONEqual(t, usageIDs(must(storage.ScanUsage(background, session.UsageScan{FromSeq: &result.Seqs[1], ToSeq: &result.Seqs[2], Order: session.OrderAsc}))(t)), []string{"usage-2"})
	assertJSONEqual(t, usageIDs(must(storage.ScanUsage(background, session.UsageScan{Order: session.OrderDesc, Limit: new(2)}))(t)), []string{"usage-3", "usage-2"})
	assertJSONEqual(t, usageIDs(must(storage.ScanUsage(background, session.UsageScan{Order: session.OrderAsc, Limit: new(2)}))(t)), []string{"usage-1", "usage-2"})
}

func testStats(t *testing.T, storage session.Storage) {
	assertJSONEqual(t, must(storage.GetStats(background))(t), zeroStats())
	firstUsage := usage(2, 3, new(4), new(1))
	first := commit(t, storage, session.InsertEntry(rootUser("message")), session.InsertUsage(session.UsageRow{ID: "usage-1", Usage: firstUsage}))
	assertCommitStats(t, storage, first)
	assertJSONEqual(t, first.Stats, session.SessionStats{MessageCount: 1, Usage: firstUsage})
	secondUsage := usage(5, 7, new(6), new(2))
	second := commit(t, storage,
		session.InsertEntry(note("custom", new("message"))),
		session.InsertEntry(compactionEntry("compaction", new("custom"))),
		session.InsertUsage(session.UsageRow{ID: "usage-2", Usage: secondUsage, Adjustment: true}),
	)
	assertCommitStats(t, storage, second)
	want := session.SessionStats{MessageCount: 1, Usage: firstUsage}
	want.Usage.Input, want.Usage.Output, want.Usage.CacheRead, want.Usage.CacheWrite = 7, 10, 9, 12
	want.Usage.CacheWrite1h, want.Usage.Reasoning, want.Usage.TotalTokens = new(10), new(3), 17
	want.Usage.Cost.Input += secondUsage.Cost.Input
	want.Usage.Cost.Output += secondUsage.Cost.Output
	want.Usage.Cost.CacheRead += secondUsage.Cost.CacheRead
	want.Usage.Cost.CacheWrite += secondUsage.Cost.CacheWrite
	want.Usage.Cost.Total += secondUsage.Cost.Total
	assertJSONEqual(t, second.Stats, want)
}

// testSerialization admits two concurrent independent commits. Go has no
// synchronous admission prefix for a blocking call, so the case proves that
// commits serialize into distinct increasing sequences with per-commit totals
// rather than a caller-chosen order.
func testSerialization(t *testing.T, storage session.Storage) {
	results := make([]session.CommitResult, 2)
	errs := make([]error, 2)
	var group sync.WaitGroup
	for index, id := range []string{"first", "second"} {
		group.Go(func() {
			results[index], errs[index] = storage.Commit(background, []session.Write{session.InsertEntry(rootUser(id))})
		})
	}
	group.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	slices.SortFunc(results, func(left, right session.CommitResult) int { return int(left.Seqs[0] - right.Seqs[0]) })
	if results[0].Seqs[0] >= results[1].Seqs[0] {
		t.Fatalf("sequences = %v, %v", results[0].Seqs, results[1].Seqs)
	}
	equal(t, results[0].Stats.MessageCount, 1)
	equal(t, results[1].Stats.MessageCount, 2)
	assertCommitStats(t, storage, results[1])
	sequential := commit(t, storage, session.InsertEntry(userEntry("third", new("first"), "third")))
	if sequential.Seqs[0] <= results[1].Seqs[0] {
		t.Fatalf("sequential seq %d not after %d", sequential.Seqs[0], results[1].Seqs[0])
	}
	equal(t, len(must(storage.ScanEntries(background, asc()))(t)), 3)
}

func testStorageLifecycle(t *testing.T, storage session.Storage) {
	admitted := commit(t, storage, session.InsertEntry(rootUser("admitted")))
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			if err := storage.Close(background); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	equal(t, len(admitted.Seqs), 1)
	_, err := storage.Commit(background, nil)
	mustErr(t, err, "commit after close")
	reads := []func() error{
		func() error { _, err := storage.GetEntries(background, nil); return err },
		func() error { _, err := storage.GetValue(background, testName.StoredAddressBase); return err },
		func() error { _, err := storage.ScanValues(background, testName.StoredAddressBase); return err },
		func() error {
			_, err := storage.ReadList(background, testList("events").StoredAddressBase, nil)
			return err
		},
		func() error {
			_, err := storage.ScanBranch(background, session.StorageBranchScan{Start: "admitted"})
			return err
		},
		func() error {
			_, err := storage.ScanBranchStructure(background, session.StorageBranchScan{Start: "admitted"})
			return err
		},
		func() error { _, err := storage.ScanEntries(background, asc()); return err },
		func() error { _, err := storage.ScanUsage(background, ascUsage()); return err },
		func() error { _, err := storage.GetStats(background); return err },
	}
	for index, read := range reads {
		mustErr(t, read(), "read "+string(rune('0'+index))+" after close")
	}
}
