package store

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/test/store/host-data.test.ts: tests of a host's data in a store (decision "Documents and table
// rows can belong to a host"; spec `docs/host-keyed/spec.md` §4): the export of one address's records and rows, the
// checks of a host's data archive, its import into an empty store and into one with data (refused, replaced, copied
// under a new address), the refusals of a table at another version and of a row taking another address's key, and the
// delete, each one commit of ordinary writes.
//
// Go forms: Pi's tests serve an object type (`Sessions`) on a host to write and read the data; the host is a later
// package, so the data is written through the store's own API (the tables' handles and `CreateDocument`) and read back
// by the tables' SQL and `FindDocument`, which is what the object's methods do. The case "keeps import and delete off
// the store's public surface" is about the TypeScript module's export list; Go has no such list (the host package is
// the only caller of ImportHostData and DeleteHostData by construction of its API), so it is designed out.
//
// pi: packages/chord/src/store/host-data.ts

const (
	hostDataOwner  = "test.sessions"
	hostDataLabels = hostDataOwner + ":labels"
	hostDataPins   = hostDataOwner + ":pins"
)

// hostDataFixture is the tables and the host kind of the cases.
type hostDataFixture struct {
	t      *testing.T
	kind   *contracts.HostKind
	labels *TableDefinition
	pins   *TableDefinition
}

func newHostDataFixture(t *testing.T) *hostDataFixture {
	t.Helper()
	kind, err := contracts.NewHostKind("session", contracts.Prop("id", contracts.Str()))
	must(t, err)
	return &hostDataFixture{
		t:    t,
		kind: kind,
		labels: qDefine(t, "labels", TableOptions{
			Version: 1,
			Columns: []ColumnProp{
				Col("session", Host(kind)), Col("label", Str()),
				Col("note", Json(contracts.Obj(contracts.Prop("text", contracts.Str())), JsonColumnOptions{Version: 1})),
			},
			Key: []string{"session", "label"},
		}),
		// The host column is not the key's first: the table gets the index `host`.
		pins: qDefine(t, "pins", TableOptions{
			Version: 1,
			Columns: []ColumnProp{Col("id", Str()), Col("session", Host(kind))},
			Key:     []string{"id"},
		}),
	}
}

// address is the session address with id.
func (f *hostDataFixture) address(id string) contracts.Address {
	return f.kind.Address(delta.JsonObjectOf("id", id))
}

// key is the canonical address key of session id.
func (f *hostDataFixture) key(id string) string {
	key, ok := contracts.KeyOfAddress(f.kind, f.address(id))
	if !ok {
		f.t.Fatalf("no key for session %q", id)
	}
	stored, err := StoredText(key, "key")
	must(f.t, err)
	return stored
}

// tables creates the tables in store (as an object serving `Sessions` does) and returns their handles.
func (f *hostDataFixture) tables(store *ChordStore) (labels, pins *TableHandle) {
	f.t.Helper()
	declared := []DeclaredTable{{Definition: f.labels, Table: hostDataLabels}, {Definition: f.pins, Table: hostDataPins}}
	must(f.t, PrepareTables(declared, store, openFence, hostDataOwner, background))
	return NewTableHandle(f.labels, hostDataLabels, store, tableOwner()), NewTableHandle(f.pins, hostDataPins, store, tableOwner())
}

// documents are the documents of session id: its `meta` record (belonging to the host) and a string-keyed `plain`
// one whose key looks like an address: it never belongs to a host.
func (f *hostDataFixture) documents(id, title string) []StagedWrite {
	key := f.key(id)
	base := func(value delta.JsonValue) DocumentBase {
		return DocumentBase{Version: 1, Fingerprint: "f", Value: value}
	}
	meta := delta.JsonObjectOf("title", title)
	plain := delta.JsonObjectOf("text", title)
	return []StagedWrite{
		CreateDocument(DocumentAddress{Owner: hostDataOwner, Namespace: "chord", Kind: "meta", Key: &key, Host: &key}, base(meta), nexttest.JSONText(f.t, meta), nil),
		CreateDocument(DocumentAddress{Owner: hostDataOwner, Namespace: "chord", Kind: "plain", Key: &key}, base(plain), nexttest.JSONText(f.t, plain), nil),
	}
}

// write is what `Sessions.write` does in one transaction: the session's title, labels (with notes) and pin.
func (f *hostDataFixture) write(store *ChordStore, labels, pins *TableHandle, id, title string, names []string, pin string) {
	f.t.Helper()
	var staged []StagedWrite
	session, key := f.address(id), f.key(id)
	labelChanges := NewTableChanges(labels, &staged)
	for _, label := range names {
		must(f.t, labelChanges.Put(Row{"session": session, "label": label, "note": delta.JsonObjectOf("text", key+": "+label)}))
	}
	must(f.t, NewTableChanges(pins, &staged).Put(Row{"id": pin, "session": session}))
	staged = append(staged, f.documents(id, title)...)
	if _, err := store.Commit(staged, openFence, background); err != nil {
		f.t.Fatalf("write %s: %v", id, err)
	}
}

// populated is a store with the data of S1 and S2.
func (f *hostDataFixture) populated() *ChordStore {
	f.t.Helper()
	store := memoryStore(f.t, true)
	labels, pins := f.tables(store)
	f.write(store, labels, pins, "S1", "one", []string{"a", "b"}, "p1")
	f.write(store, labels, pins, "S2", "two", []string{"c"}, "p2")
	return store
}

// sessionData is the data of one session as the object's `read` shows it.
type sessionData struct {
	Title  string
	Labels []string
	Notes  []string
	Pin    string
}

// read is the data of session id in store.
func (f *hostDataFixture) read(store *ChordStore, id string) sessionData {
	f.t.Helper()
	key := f.key(id)
	result := sessionData{Labels: []string{}, Notes: []string{}}
	if hasTable(store, hostDataLabels) {
		for _, row := range engineRows(f.t, store, `SELECT label, note FROM "`+hostDataLabels+`" WHERE session = ? ORDER BY label`, key) {
			note, err := delta.DecodeJson([]byte(row["note"].(string)))
			must(f.t, err)
			result.Labels = append(result.Labels, row["label"].(string))
			result.Notes = append(result.Notes, note.(*delta.JsonObject).Value("text").(string))
		}
	}
	if hasTable(store, hostDataPins) {
		if pins := engineRows(f.t, store, `SELECT id FROM "`+hostDataPins+`" WHERE session = ?`, key); len(pins) > 0 {
			result.Pin = pins[0]["id"].(string)
		}
	}
	address := DocumentAddress{Owner: hostDataOwner, Namespace: "chord", Kind: "meta", Key: &key}
	if record := engineFind(f.t, store, address, CurrentPoint); record != nil {
		loaded, err := engineLoad(f.t, store, record, CurrentPoint)
		must(f.t, err)
		result.Title = loaded.Value.(*delta.JsonObject).Value("title").(string)
	}
	return result
}

// hasTable reports whether store records table.
func hasTable(store *ChordStore, table string) bool { return store.Table(table) != nil }

// exportedHost is the parts of session id's export from store.
func (f *hostDataFixture) exportedHost(store *ChordStore, id string) []ArchivePart {
	f.t.Helper()
	export, err := ExportHostData(store, f.address(id), background)
	must(f.t, err)
	var parts []ArchivePart
	for {
		part, ok, err := export.Next(background)
		must(f.t, err)
		if !ok {
			return parts
		}
		parts = append(parts, part)
	}
}

// readArchive is parts read as an archive of sessions.
func readHostArchiveOf(t *testing.T, parts []ArchivePart) *HostArchive {
	t.Helper()
	archive, err := ReadHostArchive(partSource(parts), background)
	must(t, err)
	return archive
}

// importInto imports archive into store under address (the archive's own when nil), refusing or replacing existing
// data, in a commit nobody coordinates (no live owner in these tests).
func importInto(store *ChordStore, archive *HostArchive, address *contracts.Address, existing string) (HostDataCounts, error) {
	plan := HostImportPlan{Target: archive.Host, Existing: existing, Histories: map[string]*TableDefinition{}}
	if address != nil {
		plan.Target = address.Key
	}
	return ImportHostData(store, archive, plan, HostCommit{Fence: openFence}, background)
}

func TestHostDataExport(t *testing.T) {
	t.Run("holds only the address's records and rows: tables with a host column, their indexes, no string-keyed record", func(t *testing.T) {
		f := newHostDataFixture(t)
		parts := f.exportedHost(f.populated(), "S1")
		manifest := parts[0].(*ManifestPart)
		if manifest.Host == nil || *manifest.Host != "session|S1" {
			t.Fatalf("manifest = %s", nexttest.JSONText(t, parts[0]))
		}
		var summary []string
		for _, part := range parts[1:] {
			switch typed := part.(type) {
			case *PutWrite:
				summary = append(summary, fmt.Sprintf("put %s %s", typed.Table, nexttest.JSONText(t, typed.Row.Value("session"))))
			case *TableDefinitionWrite:
				summary = append(summary, "table "+typed.Table)
			case *IndexWrite:
				summary = append(summary, "index "+typed.Table)
			case *DocumentCreate:
				summary = append(summary, fmt.Sprintf("create %s %s", typed.Record.Kind, *typed.Record.Host))
			default:
				summary = append(summary, part.StoreWriteKind())
			}
		}
		qEqual(t, summary, []string{
			"table test.sessions:labels",
			`put test.sessions:labels "session|S1"`,
			`put test.sessions:labels "session|S1"`,
			"table test.sessions:pins",
			`put test.sessions:pins "session|S1"`,
			"index test.sessions:pins",
			"create meta session|S1",
			"document.revision",
			"end",
		})
		qEqual(t, parts[len(parts)-1].(*EndPart).Writes, int64(len(parts)-2))
		table := parts[1].(*TableDefinitionWrite)
		column, _ := table.Columns.Get("session")
		qEqual(t, column, ColumnRecord{Type: ColStr, Host: new("session")})
	})
}

func TestHostDataImport(t *testing.T) {
	t.Run("makes the data in an empty store: fresh ids and seqs, the values as bases", func(t *testing.T) {
		f := newHostDataFixture(t)
		archive := readHostArchiveOf(t, f.exportedHost(f.populated(), "S1"))
		target := memoryStore(t)
		counts, err := importInto(target, archive, nil, "")
		must(t, err)
		qEqual(t, counts, HostDataCounts{Documents: 1, Rows: 3})
		qEqual(t, f.read(target, "S1"), sessionData{Title: "one", Labels: []string{"a", "b"}, Notes: []string{"session|S1: a", "session|S1: b"}, Pin: "p1"})
		records, err := Read(target, func(executor sqlite.SqliteExecutor) ([]DocumentRecord, error) {
			return ListHostRecords(executor, "session|S1", 0, 10)
		}, background)
		must(t, err)
		if len(records) != 1 || *records[0].Key != "session|S1" || *records[0].Host != "session|S1" || records[0].CreatedAt != 1 {
			t.Fatalf("records = %+v", records)
		}
	})

	t.Run("refuses an address with data, leaving the store as it was; replaces it when asked", func(t *testing.T) {
		f := newHostDataFixture(t)
		archive := readHostArchiveOf(t, f.exportedHost(f.populated(), "S1"))
		target := memoryStore(t)
		labels, pins := f.tables(target)
		f.write(target, labels, pins, "S1", "old", []string{"z"}, "p9")
		before := target.LastSeq()
		_, err := importInto(target, archive, nil, "")
		qFails(t, err, `session|S1 has data in this store (import with existing: "replace")`)
		if !archiveIsCode(err, data.CodeInvalid) {
			t.Fatalf("err = %v, want invalid/args", err)
		}
		qEqual(t, target.LastSeq(), before)
		counts, err := importInto(target, archive, nil, ExistingReplace)
		must(t, err)
		qEqual(t, counts, HostDataCounts{Documents: 1, Rows: 3})
		qEqual(t, f.read(target, "S1"), sessionData{Title: "one", Labels: []string{"a", "b"}, Notes: []string{"session|S1: a", "session|S1: b"}, Pin: "p1"})
	})

	t.Run("copies under a new address: host cells and keys rewritten, addresses inside JSON untouched", func(t *testing.T) {
		f := newHostDataFixture(t)
		source := f.populated()
		archive := readHostArchiveOf(t, f.exportedHost(source, "S1"))
		var pins *HostTable
		for _, table := range archive.Tables {
			if table.Write.Table == hostDataPins {
				pins = table
			}
		}
		renamed := *archive
		renamed.Tables = nil
		for _, table := range archive.Tables {
			if table == pins {
				row := pins.Puts[0].Row.Clone()
				row.Set("id", "p3")
				copied := *pins
				copied.Puts = []*PutWrite{{Table: pins.Puts[0].Table, Row: row, Version: pins.Puts[0].Version, Columns: pins.Puts[0].Columns}}
				table = &copied
			}
			renamed.Tables = append(renamed.Tables, table)
		}
		address := f.address("S3")
		_, err := importInto(source, &renamed, &address, "")
		must(t, err)
		qEqual(t, f.read(source, "S3"), sessionData{Title: "one", Labels: []string{"a", "b"}, Notes: []string{"session|S1: a", "session|S1: b"}, Pin: "p3"})
		got := f.read(source, "S1")
		qEqual(t, []string{got.Title, got.Pin}, []string{"one", "p1"})
	})

	t.Run("refuses another kind, a table at another version, and a row whose key holds another address's row", func(t *testing.T) {
		f := newHostDataFixture(t)
		source := f.populated()
		archive := readHostArchiveOf(t, f.exportedHost(source, "S1"))
		_, err := importInto(source, archive, &contracts.Address{Key: "user"}, "")
		qFails(t, err, "another kind")
		// S3 in the source: the pin `p1` is S1's.
		address := f.address("S3")
		_, err = importInto(source, archive, &address, "")
		qFails(t, err, `a row with key ["p1"] exists`)
		// A store whose `labels` table is at version 2.
		newer := qEvolve(t, f.labels, func(e *TableEditor) TableTailEditor { return e.Add("extra", Opt(Str())) })
		target := memoryStore(t)
		must(t, PrepareTables([]DeclaredTable{{Definition: newer, Table: hostDataLabels}}, target, openFence, hostDataOwner, background))
		_, err = importInto(target, archive, nil, "")
		qFails(t, err, "at version 1, 2 here; its owner is not live")
		qEqual(t, TablesToMigrate(target, archive), []string{hostDataLabels})
		qEqual(t, TablesToMigrate(source, archive), []string(nil))
	})

	t.Run("is recorded in the outbox as ordinary writes", func(t *testing.T) {
		f := newHostDataFixture(t)
		source := f.populated()
		archive := readHostArchiveOf(t, f.exportedHost(source, "S1"))
		_, err := importInto(source, archive, nil, ExistingReplace)
		must(t, err)
		commits, err := source.Outbox().Read(source.LastSeq()-1, 1, background)
		must(t, err)
		var kinds []string
		for _, write := range commits[0].Writes {
			kinds = append(kinds, write.StoreWriteKind())
		}
		qEqual(t, kinds, []string{"document.retire", "deleteWhere", "deleteWhere", "put", "put", "put", "document.create"})
	})

	t.Run("names the owners whose data it changes", func(t *testing.T) {
		f := newHostDataFixture(t)
		source := f.populated()
		archive := readHostArchiveOf(t, f.exportedHost(source, "S1"))
		owners, err := HostDataOwners(source, "session|S1", archive, background)
		must(t, err)
		qEqual(t, owners, map[string]struct{}{hostDataOwner: {}})
		owners, err = HostDataOwners(memoryStore(t), "session|S1", nil, background)
		must(t, err)
		qEqual(t, len(owners), 0)
	})
}

// hostDataRecord is the JSON of a document record of S1 at id 3, with the given host, key and history.
func hostDataRecord(host, key, history string) string {
	return fmt.Sprintf(
		`{"kind":"document.create","record":{"id":3,"owner":"test.sessions","namespace":"chord","kind":"meta","key":%q,"host":%q,"history":%q,"createdAt":2,"revision":1,"version":1,"fingerprint":"f"}}`,
		key, host, history,
	)
}

func TestHostDataArchiveChecks(t *testing.T) {
	const manifest = `{"kind":"manifest","format":1,"schema":3,"lastSeq":5,"nextId":9,"host":"session|S1"}`
	const table = `{"kind":"table","table":"a:b","version":1,"state":"ready","columns":%s,"key":[%q],"fingerprint":"f"}`
	cases := []struct {
		name    string
		parts   []string
		message string
	}{
		{"no host", []string{`{"kind":"manifest","format":1,"schema":3,"lastSeq":5,"nextId":9}`, `{"kind":"end","writes":0}`}, "not a host's data archive"},
		{"no end", []string{manifest}, "no end part"},
		{
			"a table without a host column",
			[]string{manifest, fmt.Sprintf(table, `{"id":{"type":"str"}}`, "id"), `{"kind":"end","writes":1}`},
			"no single host column of session",
		},
		{
			"a row of another address",
			[]string{
				manifest, fmt.Sprintf(table, `{"s":{"type":"str","host":"session"}}`, "s"),
				`{"kind":"put","table":"a:b","row":{"s":"session|S2"},"version":1,"columns":{}}`, `{"kind":"end","writes":2}`,
			},
			"does not belong to session|S1",
		},
		{
			"a record of another address",
			[]string{manifest, hostDataRecord("session|S2", "session|S2", "none"), `{"kind":"end","writes":1}`},
			"does not belong to session|S1",
		},
		{
			"a record that is not Chord's",
			[]string{manifest, hostDataRecord("session|S1", "session|S1", "latest"), `{"kind":"end","writes":1}`},
			"not a live Chord record",
		},
		{
			"a record without a base",
			[]string{manifest, hostDataRecord("session|S1", "session|S1", "none"), `{"kind":"end","writes":1}`},
			"has no base at its version",
		},
	}
	for _, c := range cases {
		t.Run("refuses "+c.name, func(t *testing.T) {
			values := make([]any, len(c.parts))
			for index, text := range c.parts {
				values[index] = archiveJSON(text)
			}
			_, err := ReadHostArchive(SlicePartSource(values), background)
			qFails(t, err, c.message)
			if !archiveIsCode(err, data.CodeInvalid) {
				t.Fatalf("err = %v, want invalid", err)
			}
		})
	}

	t.Run("folds a record's base and deltas into its value", func(t *testing.T) {
		parts := []string{
			manifest, hostDataRecord("session|S1", "session|S1", "none"),
			`{"kind":"document.revision","id":3,"seq":2,"content":{"kind":"base","version":1,"value":{"title":"a"}}}`,
			`{"kind":"document.revision","id":3,"seq":4,"content":{"kind":"delta","version":1,"ops":[["s",["title"],"b"]]}}`,
			`{"kind":"end","writes":3}`,
		}
		values := make([]any, len(parts))
		for index, text := range parts {
			values[index] = archiveJSON(text)
		}
		archive, err := ReadHostArchive(SlicePartSource(values), background)
		must(t, err)
		if len(archive.Documents) != 1 || nexttest.JSONText(t, archive.Documents[0].Value) != `{"title":"b"}` {
			t.Fatalf("documents = %+v", archive.Documents)
		}
	})
}

func TestHostDataDelete(t *testing.T) {
	t.Run("retires the address's records and deletes its rows in one commit, other addresses untouched", func(t *testing.T) {
		f := newHostDataFixture(t)
		store := f.populated()
		counts, err := DeleteHostData(store, "session|S1", HostCommit{Fence: openFence}, background)
		must(t, err)
		qEqual(t, counts, HostDataCounts{Documents: 1, Rows: 3})
		qEqual(t, f.read(store, "S1"), sessionData{Labels: []string{}, Notes: []string{}})
		qEqual(t, f.read(store, "S2"), sessionData{Title: "two", Labels: []string{"c"}, Notes: []string{"session|S2: c"}, Pin: "p2"})
		commits, err := store.Outbox().Read(store.LastSeq()-1, 1, background)
		must(t, err)
		retired := commits[0].Writes[0].(*DocumentRetire)
		qEqual(t, []string{
			nexttest.JSONText(t, commits[0].Writes[0]), nexttest.JSONText(t, commits[0].Writes[1]), nexttest.JSONText(t, commits[0].Writes[2]),
		}, []string{
			fmt.Sprintf(`{"kind":"document.retire","id":%d,"revision":1}`, retired.ID),
			`{"kind":"deleteWhere","table":"test.sessions:labels","where":{"eq":["session","session|S1"]},"count":2}`,
			`{"kind":"deleteWhere","table":"test.sessions:pins","where":{"eq":["session","session|S1"]},"count":1}`,
		})
		// The string-keyed document with the same key is no host's: still there.
		plain := engineRows(t, store, `SELECT count(*) AS n FROM chord_documents WHERE kind = 'plain'`)
		qEqual(t, plain[0]["n"], int64(2))
	})
}

func TestHostDataReviewRound2(t *testing.T) {
	t.Run("checks rows by the recorded host column, whatever the archive's metadata says (a moved host annotation)", func(t *testing.T) {
		f := newHostDataFixture(t)
		store := f.populated()
		parts := jsonParts(t, f.exportedHost(store, "S1"))
		for _, part := range parts {
			object := part.(*delta.JsonObject)
			switch {
			case importKind(part) == "table" && object.Value("table") == hostDataLabels:
				columns := object.Value("columns").(*delta.JsonObject)
				columns.Set("session", archiveJSON(`{"type":"str"}`))
				columns.Set("label", archiveJSON(`{"type":"str","host":"session"}`))
			case importKind(part) == "put" && object.Value("table") == hostDataLabels:
				row := object.Value("row").(*delta.JsonObject)
				row.Set("session", "session|VICTIM")
				row.Set("label", "session|S1")
			}
		}
		archive, err := ReadHostArchive(SlicePartSource(parts), background)
		must(t, err)
		_, err = importInto(store, archive, nil, ExistingReplace)
		qFails(t, err, "a row that does not belong to session|S1")
		qEqual(t, f.read(store, "VICTIM"), sessionData{Labels: []string{}, Notes: []string{}})
	})

	t.Run("refuses a table exported during a migration when no history can resume it", func(t *testing.T) {
		f := newHostDataFixture(t)
		store := f.populated()
		parts := jsonParts(t, f.exportedHost(store, "S1"))
		for _, part := range parts {
			if object := part.(*delta.JsonObject); importKind(part) == "table" && object.Value("table") == hostDataLabels {
				object.Set("state", "migrating")
			}
		}
		archive, err := ReadHostArchive(SlicePartSource(parts), background)
		must(t, err)
		_, err = importInto(store, archive, nil, ExistingReplace)
		qFails(t, err, "its owner is not live")
	})

	t.Run("selects by the canonical key: a lone surrogate in an address is U+FFFD in cells, keys and the export", func(t *testing.T) {
		f := newHostDataFixture(t)
		store := memoryStore(t, true)
		labels, pins := f.tables(store)
		// A lone surrogate, as Go holds one: the bytes of its surrogate code point.
		lone := "x\xed\xa0\x80"
		f.write(store, labels, pins, lone, "odd", []string{"a"}, "p")
		parts := f.exportedHost(store, lone)
		if host := parts[0].(*ManifestPart).Host; host == nil || *host != "session|x\uFFFD" {
			t.Fatalf("manifest = %s", nexttest.JSONText(t, parts[0]))
		}
		puts, creates := 0, 0
		for _, part := range parts {
			switch part.(type) {
			case *PutWrite:
				puts++
			case *DocumentCreate:
				creates++
			}
		}
		qEqual(t, []int{puts, creates}, []int{2, 1})
		counts, err := DeleteHostData(store, "session|x\uFFFD", HostCommit{Fence: openFence}, background)
		must(t, err)
		qEqual(t, counts, HostDataCounts{Documents: 1, Rows: 2})
	})
}
