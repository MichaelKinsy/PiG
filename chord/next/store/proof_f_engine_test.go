package store

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Proof tests of the engine's entry reads (packages/chord/src/next/store/engine.ts): an adapter keeping its fields in
// `meta` reads `ReadDocumentEntry` (by id, with owner, namespace and history), `FindDocumentEntry` (by address, now or
// at a point: the half-open lifetime), `ListDocumentEntries` (a scope, by id, paged) and `LoadContent` (the base and
// the deltas after it).

// entriesKit commits records of owner app.store/chord: 10 ("a", kind notes, rewindable, meta {"m":1}, retired at 2),
// 11 ("b", notes, none, no meta), 12 ("c", kind other, none, meta {"m":3}), 13 (the singleton of kind notes, latest).
func entriesKit(t *testing.T) *engineKit {
	t.Helper()
	kit := newEngine(t)
	kit.mustCommit(stagedApply(func(step *Step) ([]StoreWrite, error) {
		a := adapterRecord(10, "a", "rewindable", step.Seq)
		a.Meta = new(`{"m":1}`)
		b := adapterRecord(11, "b", "none", step.Seq)
		c := adapterRecord(12, "c", "none", step.Seq)
		c.Kind = "other"
		c.Meta = new(`{"m":3}`)
		singleton := adapterRecord(13, "", "latest", step.Seq)
		singleton.Key = nil
		singleton.Meta = new(`{"m":4}`)
		text := `{"n":0}`
		for _, record := range []DocumentRecord{a, b, c, singleton} {
			if err := InsertDocument(step, record, &text); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}))
	kit.mustCommit(stagedApply(func(step *Step) ([]StoreWrite, error) {
		record, err := ReadDocument(step, 10)
		if err != nil {
			return nil, err
		}
		return nil, RetireRecord(step, *record)
	}))
	return kit
}

func proofFMeta(entry *DocumentEntry) string {
	if entry == nil {
		return "<no entry>"
	}
	if entry.Meta == nil {
		return "<null>"
	}
	return *entry.Meta
}

func TestProofFReadDocumentEntryReadsMetaRetirementAndOwnership(t *testing.T) {
	kit := entriesKit(t)
	read := func(id int64) *OwnedDocumentEntry {
		t.Helper()
		entry, err := Read(kit.store, func(executor sqlite.SqliteExecutor) (*OwnedDocumentEntry, error) {
			return ReadDocumentEntry(executor, id)
		}, background)
		must(t, err)
		return entry
	}
	retired := read(10)
	if retired == nil || retired.Meta == nil || *retired.Meta != `{"m":1}` {
		t.Fatalf("entry 10 = %+v, want meta {\"m\":1}", retired)
	}
	if retired.RetiredAt == nil || *retired.RetiredAt != 2 {
		t.Fatalf("entry 10 retiredAt = %v, want 2 (a read by id finds retired records)", retired.RetiredAt)
	}
	if retired.Owner != "app.store" || retired.Namespace != "chord" || retired.History != HistoryRewindable {
		t.Fatalf("entry 10 = owner %q, namespace %q, history %q", retired.Owner, retired.Namespace, retired.History)
	}
	live := read(11)
	if live == nil || live.Meta != nil || live.RetiredAt != nil || live.History != HistoryNone {
		t.Fatalf("entry 11 = %+v, want no meta, live, history none", live)
	}
	other := read(12)
	if other == nil || other.Meta == nil || *other.Meta != `{"m":3}` || other.History != HistoryNone {
		t.Fatalf("entry 12 = %+v", other)
	}
	if latest := read(13); latest == nil || latest.History != HistoryLatest {
		t.Fatalf("entry 13 = %+v, want history latest", latest)
	}
	if missing := read(99); missing != nil {
		t.Fatalf("entry 99 = %+v, want none", missing)
	}
}

func TestProofFFindDocumentEntryByAddressNowAndAtAPoint(t *testing.T) {
	kit := entriesKit(t)
	find := func(address DocumentAddress, at DocumentPoint) *DocumentEntry {
		t.Helper()
		entry, err := Read(kit.store, func(executor sqlite.SqliteExecutor) (*DocumentEntry, error) {
			return FindDocumentEntry(executor, address, at)
		}, background)
		must(t, err)
		return entry
	}
	// Current: the live record only; 10 is retired.
	if entry := find(kit.at("a"), CurrentPoint); entry != nil {
		t.Fatalf("current entry of the retired record = %+v, want none", entry)
	}
	if got := proofFMeta(find(kit.at("b"), CurrentPoint)); got != "<null>" {
		t.Fatalf("current entry of b = %s, want an entry with null meta", got)
	}
	// A record is live at a point from its creation to its retirement, half-open.
	at1 := find(kit.at("a"), AtSeq(1))
	if proofFMeta(at1) != `{"m":1}` || at1.RetiredAt == nil || *at1.RetiredAt != 2 {
		t.Fatalf("entry of a at 1 = %+v, want meta {\"m\":1} retired at 2", at1)
	}
	if entry := find(kit.at("a"), AtSeq(2)); entry != nil {
		t.Fatalf("entry of a at 2 = %+v, want none (retired at 2)", entry)
	}
	if entry := find(kit.at("a"), AtSeq(0)); entry != nil {
		t.Fatalf("entry of a at 0 = %+v, want none (created at 1)", entry)
	}
	if got := find(kit.at("b"), AtSeq(2)); got == nil || got.RetiredAt != nil {
		t.Fatalf("entry of b at 2 = %+v, want a live entry", got)
	}
	// The address is the owner, namespace, kind and key; the singleton's key is the empty one.
	singleton := engineOwner
	if got := proofFMeta(find(singleton, CurrentPoint)); got != `{"m":4}` {
		t.Fatalf("entry of the singleton = %s, want {\"m\":4}", got)
	}
	if entry := find(kit.at("c"), CurrentPoint); entry != nil {
		t.Fatalf("entry of c under kind notes = %+v, want none (it is kind other)", entry)
	}
	otherKind := kit.at("c")
	otherKind.Kind = "other"
	if got := proofFMeta(find(otherKind, CurrentPoint)); got != `{"m":3}` {
		t.Fatalf("entry of c under kind other = %s", got)
	}
	foreign := kit.at("b")
	foreign.Owner = "someone.else"
	if entry := find(foreign, CurrentPoint); entry != nil {
		t.Fatalf("entry of another owner = %+v, want none", entry)
	}
}

func TestProofFListDocumentEntriesByScopeAndPoint(t *testing.T) {
	kit := entriesKit(t)
	list := func(scope DocumentScope, at DocumentPoint, after, limit int64) []DocumentEntry {
		t.Helper()
		entries, err := Read(kit.store, func(executor sqlite.SqliteExecutor) ([]DocumentEntry, error) {
			return ListDocumentEntries(executor, scope, at, after, limit)
		}, background)
		must(t, err)
		return entries
	}
	metas := func(entries []DocumentEntry) string {
		parts := make([]string, len(entries))
		for index := range entries {
			parts[index] = proofFMeta(&entries[index])
		}
		return strings.Join(parts, "|")
	}
	all := DocumentScope{Owner: "app.store", Namespace: "chord"}
	// Current state, by id: 11, 12, 13 (10 is retired).
	if got := metas(list(all, CurrentPoint, 0, 10)); got != `<null>|{"m":3}|{"m":4}` {
		t.Fatalf("current entries = %s", got)
	}
	for _, entry := range list(all, CurrentPoint, 0, 10) {
		if entry.RetiredAt != nil {
			t.Fatalf("a current entry has retiredAt %v", *entry.RetiredAt)
		}
	}
	if got := metas(list(all, CurrentPoint, 11, 10)); got != `{"m":3}|{"m":4}` {
		t.Fatalf("entries after id 11 = %s", got)
	}
	if got := metas(list(all, CurrentPoint, 0, 2)); got != `<null>|{"m":3}` {
		t.Fatalf("entries with limit 2 = %s", got)
	}
	kind := "notes"
	if got := metas(list(DocumentScope{Owner: "app.store", Namespace: "chord", Kind: &kind}, CurrentPoint, 0, 10)); got != `<null>|{"m":4}` {
		t.Fatalf("entries of kind notes = %s", got)
	}
	if got := list(DocumentScope{Owner: "nobody", Namespace: "chord"}, CurrentPoint, 0, 10); len(got) != 0 {
		t.Fatalf("entries of another owner = %v", got)
	}
	if got := list(DocumentScope{Owner: "app.store", Namespace: "elsewhere"}, CurrentPoint, 0, 10); len(got) != 0 {
		t.Fatalf("entries of another namespace = %v", got)
	}
	// At a point the retired record is there while live, with when it was retired.
	at1 := list(all, AtSeq(1), 0, 10)
	if metas(at1) != `{"m":1}|<null>|{"m":3}|{"m":4}` {
		t.Fatalf("entries at 1 = %s", metas(at1))
	}
	if at1[0].RetiredAt == nil || *at1[0].RetiredAt != 2 || at1[1].RetiredAt != nil {
		t.Fatalf("entries at 1: retiredAt = %v, %v; want 2, none", at1[0].RetiredAt, at1[1].RetiredAt)
	}
	if got := metas(list(all, AtSeq(2), 0, 10)); got != `<null>|{"m":3}|{"m":4}` {
		t.Fatalf("entries at 2 = %s, want the retired record gone", got)
	}
	if got := metas(list(DocumentScope{Owner: "app.store", Namespace: "chord", Kind: &kind}, AtSeq(1), 10, 10)); got != `<null>|{"m":4}` {
		t.Fatalf("entries of kind notes at 1 after 10 = %s", got)
	}
}

func TestProofFLoadContentIsTheBasePlusTheDeltasAfterIt(t *testing.T) {
	kit := lifetime(t, "rewindable")
	load := func(id int64, at DocumentPoint) (DocumentContentAt, error) {
		t.Helper()
		// A read returns data (the store stops on a read's error): LoadContent's error is a value here.
		var loadErr error
		content, err := Read(kit.store, func(executor sqlite.SqliteExecutor) (DocumentContentAt, error) {
			var content DocumentContentAt
			content, loadErr = LoadContent(executor, id, at)
			return content, nil
		}, background)
		must(t, err)
		return content, loadErr
	}
	for _, c := range []struct {
		at     DocumentPoint
		value  string
		deltas int
	}{
		{AtSeq(1), `{"n":0}`, 0},
		{AtSeq(2), `{"n":1}`, 0},
		{AtSeq(3), `{"n":2}`, 1},
		{CurrentPoint, `{"n":2}`, 1},
	} {
		content, err := load(10, c.at)
		must(t, err)
		if engineJSON(t, content.Value) != c.value || content.Version != 1 || content.DeltasSinceBase != c.deltas {
			t.Fatalf("content at %+v = %s v%d deltas %d, want %s v1 deltas %d",
				c.at, engineJSON(t, content.Value), content.Version, content.DeltasSinceBase, c.value, c.deltas)
		}
	}
	// Before the first base, and for a document that is not there, there is no base.
	for _, id := range []int64{10, 77} {
		at := AtSeq(0)
		if id == 77 {
			at = CurrentPoint
		}
		if _, err := load(id, at); err == nil || !strings.Contains(err.Error(), "no base") {
			t.Fatalf("content of %d at %+v = %v, want no base", id, at, err)
		}
	}
	// A newer base of another version is the version the content is at; the deltas before it are not applied.
	kit.mustCommit(stagedApply(func(step *Step) ([]StoreWrite, error) {
		return nil, InsertRevision(step, 10, step.Seq, ContentBase, 7, `{"z":1}`)
	}))
	content, err := load(10, CurrentPoint)
	must(t, err)
	if engineJSON(t, content.Value) != `{"z":1}` || content.Version != 7 || content.DeltasSinceBase != 0 {
		t.Fatalf("content after a newer base = %s v%d deltas %d", engineJSON(t, content.Value), content.Version, content.DeltasSinceBase)
	}
	// A delta of another version than its base is refused.
	kit.mustCommit(stagedApply(func(step *Step) ([]StoreWrite, error) {
		return nil, InsertRevision(step, 10, step.Seq, ContentDelta, 8, `[["s",["z"],2]]`)
	}))
	if _, err := load(10, CurrentPoint); err == nil || !strings.Contains(err.Error(), "crosses a version") {
		t.Fatalf("content with a crossing delta = %v, want crosses a version", err)
	}
	if _, err := load(10, AtSeq(5)); err != nil {
		t.Fatalf("content below the crossing delta = %v", err)
	}
}

func TestProofFLoadContentOfALatestRecordHasNoBaseOnceRetired(t *testing.T) {
	kit := lifetime(t, "latest")
	var err error
	_, readErr := Read(kit.store, func(executor sqlite.SqliteExecutor) (DocumentContentAt, error) {
		var content DocumentContentAt
		content, err = LoadContent(executor, 10, AtSeq(2))
		return content, nil
	}, background)
	must(t, readErr)
	if err == nil || !strings.Contains(err.Error(), "document 10: no base") {
		t.Fatalf("LoadContent of a retired latest record = %v, want document 10: no base", err)
	}
}
