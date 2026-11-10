package host

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/test/store/host-data.test.ts: the cases of a host's data in a store that serve the object type
// `Sessions` on a host to write and read the data (decision "Documents and table rows can belong to a host"; spec
// `docs/host-keyed/spec.md` §4). The store package cannot import the host, so its half (chord/next/store/host_data_test.go)
// writes the data through the store's own API; this half serves `Sessions` on a host as Pi's test does, and calls the
// store's `ExportHostData`, `ReadHostArchive`, `ImportHostData` and `DeleteHostData` on the same store. The archive
// checks (`archive checks`) and "keeps import and delete off the store's public surface" do not use the host: the first
// are in the store half, the second is designed out there.
//
// pi: packages/chord/src/store/host-data.ts

const (
	shdOwner  = "test.sessions"
	shdLabels = shdOwner + ":labels"
	shdPins   = shdOwner + ":pins"
)

var (
	shdMeta = func() *documents.StateDefinition {
		definition, err := documents.DefineState("meta", documents.StateOptions{
			Schema: contracts.Obj(contracts.Prop("title", contracts.Str())), Version: 1,
			Initial: func() *delta.JsonObject { return jsonObject("title", "") },
			Keyed:   Session, Persist: true,
			CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
		})
		if err != nil {
			panic(err)
		}
		return definition
	}()
	// shdPlain is a string-keyed document whose key looks like an address: it never belongs to a host.
	shdPlain = func() *documents.StateDefinition {
		definition, err := documents.DefineState("plain", documents.StateOptions{
			Schema: contracts.Obj(contracts.Prop("text", contracts.Str())), Version: 1,
			Initial: func() *delta.JsonObject { return jsonObject("text", "") },
			Keyed:   true, Persist: true,
			CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
		})
		if err != nil {
			panic(err)
		}
		return definition
	}()
	shdLabelsTable = func() *store.TableDefinition {
		definition, err := store.DefineTable("labels", store.TableOptions{
			Version: 1,
			Columns: []store.ColumnProp{
				store.Col("session", store.Host(Session)), store.Col("label", store.Str()),
				store.Col("note", store.Json(contracts.Obj(contracts.Prop("text", contracts.Str())), store.JsonColumnOptions{Version: 1})),
			},
			Key: []string{"session", "label"},
		})
		if err != nil {
			panic(err)
		}
		return definition
	}()
	// shdPinsTable has its host column not the key's first: the table gets the index `host`.
	shdPinsTable = func() *store.TableDefinition {
		definition, err := store.DefineTable("pins", store.TableOptions{
			Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Str()), store.Col("session", store.Host(Session))}, Key: []string{"id"},
		})
		if err != nil {
			panic(err)
		}
		return definition
	}()
	// shdSessions is `Sessions`: write(id, title, labels, pin), read(id).
	shdSessions = exLocal(shdOwner, contracts.Params{},
		contracts.Mem("write", contracts.NewMethod(exObj(exProp("id", exStr()), exProp("title", exStr()), exProp("labels", exArr(exStr())), exProp("pin", exStr())))),
		contracts.Mem("read", contracts.NewMethod(exStr(), exObj(
			exProp("title", exStr()), exProp("labels", exArr(exStr())), exProp("notes", exArr(exStr())), exProp("pin", exOpt(exStr()))))))
)

// shdSessionsFacet is the facet serving `Sessions` on a host.
func shdSessionsFacet(t testing.TB) func(FacetHost, FacetSelf) error {
	return func(h FacetHost, _ FacetSelf) error {
		return h.Serve(shdSessions, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			meta := self.State(shdMeta).(*documents.Keyed)
			plain := self.State(shdPlain).(*documents.Keyed)
			rows := self.(StoreSelf).Table(shdLabelsTable)
			pins := self.(StoreSelf).Table(shdPinsTable)
			return exImpl(nil, map[string]contracts.MethodFunction{
				"write": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					_, err := self.Tx(func(tx documents.Tx) (any, error) {
						session := Session.Address(jsonObject("id", exParam(args, "id")))
						title := exParam(args, "title")
						draft, err := tx.Draft(meta, session)
						if err != nil {
							return nil, err
						}
						if err := draft.Set("title", title); err != nil {
							return nil, err
						}
						plainDraft, err := tx.Draft(plain, session.Key)
						if err != nil {
							return nil, err
						}
						if err := plainDraft.Set("text", title); err != nil {
							return nil, err
						}
						for _, label := range args.Value("labels").([]any) {
							if err := tx.Put(rows, store.Row{"session": session, "label": label, "note": jsonObject("text", session.Key+": "+label.(string))}); err != nil {
								return nil, err
							}
						}
						return nil, tx.Put(pins, store.Row{"id": exParam(args, "pin"), "session": session})
					}, ctx)
					return nil, err
				}),
				"read": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					session := Session.Address(jsonObject("id", id.(string)))
					page, err := rows.Page(ctx, store.NewQuery(shdLabelsTable).Where(func(t store.ColumnRefs) store.Condition { return store.Eq(t["session"], session) }), 50, store.PageOptions{})
					if err != nil {
						return nil, err
					}
					pin, err := pins.Page(ctx, store.NewQuery(shdPinsTable).Where(func(t store.ColumnRefs) store.Condition { return store.Eq(t["session"], session) }), 1, store.PageOptions{})
					if err != nil {
						return nil, err
					}
					title := ""
					found, err := meta.Get(ctx, session)
					if err != nil {
						return nil, err
					}
					if found != nil {
						title, _ = found.Value("title").(string)
					}
					labels, notes := []any{}, []any{}
					for _, row := range page.Items {
						labels = append(labels, row["label"])
						notes = append(notes, row["note"].(*delta.JsonObject).Value("text"))
					}
					result := jsonObject("title", title, "labels", labels, "notes", notes)
					if len(pin.Items) > 0 {
						result.Set("pin", pin.Items[0]["id"])
					}
					return result, nil
				},
			}), nil
		}, ServeOptions{})
	}
}

// shdUsing runs work on a host serving `Sessions` on chordStore, then closes it.
func shdUsing(t *testing.T, chordStore *store.ChordStore, work func(data *Reference)) {
	t.Helper()
	host, _ := exOpenHost(t, loadedOptions{Store: chordStore}, shdSessionsFacet(t))
	defer exCloseHost(t, host)
	work(exGet(t, host, shdSessions, jsonObject()))
}

// shdWrite is `data.write({ id, title, labels, pin })`.
func shdWrite(t *testing.T, ref *Reference, id, title string, labels []string, pin string) {
	t.Helper()
	names := make([]any, len(labels))
	for index, label := range labels {
		names[index] = label
	}
	exCall(t, ref, "write", jsonObject("id", id, "title", title, "labels", names, "pin", pin))
}

// shdPopulated is a store with the data of S1 and S2.
func shdPopulated(t *testing.T) *store.ChordStore {
	t.Helper()
	chordStore := memoryStore(t, true)
	shdUsing(t, chordStore, func(ref *Reference) {
		shdWrite(t, ref, "S1", "one", []string{"a", "b"}, "p1")
		shdWrite(t, ref, "S2", "two", []string{"c"}, "p2")
	})
	return chordStore
}

// shdExported is the parts of session id's export from chordStore.
func shdExported(t *testing.T, chordStore *store.ChordStore, id string) []store.ArchivePart {
	t.Helper()
	export, err := store.ExportHostData(chordStore, Session.Address(jsonObject("id", id)), background)
	must(t, err)
	var parts []store.ArchivePart
	for {
		part, ok, err := export.Next(background)
		must(t, err)
		if !ok {
			return parts
		}
		parts = append(parts, part)
	}
}

// shdRead is the data of session id as `Sessions` reads it from chordStore.
func shdRead(t *testing.T, chordStore *store.ChordStore, id string) any {
	t.Helper()
	var result any
	shdUsing(t, chordStore, func(ref *Reference) { result = exCall(t, ref, "read", id) })
	return result
}

// shdData is the JSON `Sessions.read` returns.
func shdData(t *testing.T, text string) any { return nexttest.ParseJSON(t, text) }

// shdReadArchive is parts read as an archive of sessions.
func shdReadArchive(t *testing.T, parts []store.ArchivePart) *store.HostArchive {
	t.Helper()
	values := make([]any, len(parts))
	for index, part := range parts {
		values[index] = part
	}
	archive, err := store.ReadHostArchive(store.SlicePartSource(values), background)
	must(t, err)
	return archive
}

// shdImportInto imports archive into chordStore under address (the archive's own when nil), refusing or replacing
// existing data, in a commit nobody coordinates (no live owner in these tests).
func shdImportInto(chordStore *store.ChordStore, archive *store.HostArchive, address *contracts.Address, existing string) (store.HostDataCounts, error) {
	plan := store.HostImportPlan{Target: archive.Host, Existing: existing, Histories: map[string]*store.TableDefinition{}}
	if address != nil {
		plan.Target = address.Key
	}
	return store.ImportHostData(chordStore, archive, plan, store.HostCommit{Fence: openFence()}, background)
}

// shdRefusal asserts err contains message (and is invalid/args when code is set).
func shdRefusal(t *testing.T, err error, message string, invalidArgs bool) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got none", message)
	}
	if !strings.Contains(err.Error(), message) {
		t.Fatalf("error %q does not contain %q", err.Error(), message)
	}
	if invalidArgs {
		chordErr, ok := data.IsChordErrorCode(err, data.CodeInvalid)
		if !ok || chordErr.Reason != data.ReasonArgs {
			t.Fatalf("err = %v, want invalid/args", err)
		}
	}
}

// shdSessionData is the JSON of one session's data.
func shdSessionData(t *testing.T, title string, labels []string, notes []string, pin string) any {
	t.Helper()
	text := fmt.Sprintf(`{"title":%s,"labels":%s,"notes":%s`, nexttest.JSONText(t, title), nexttest.JSONText(t, shdStrings(labels)), nexttest.JSONText(t, shdStrings(notes)))
	if pin != "" {
		text += `,"pin":` + nexttest.JSONText(t, pin)
	}
	return nexttest.ParseJSON(t, text+"}")
}

func shdStrings(values []string) []any {
	out := make([]any, len(values))
	for index, value := range values {
		out[index] = value
	}
	return out
}

func TestStoreHostDataExportHoldsOnlyTheAddressSRecordsAndRowsTablesWithAHostColumnTheirIndexesNoStringKeyedRecord(t *testing.T) {
	parts := shdExported(t, shdPopulated(t), "S1")
	manifest := parts[0].(*store.ManifestPart)
	if manifest.Host == nil || *manifest.Host != "session|S1" {
		t.Fatalf("manifest = %s", nexttest.JSONText(t, parts[0]))
	}
	var summary []string
	for _, part := range parts[1:] {
		switch typed := part.(type) {
		case *store.PutWrite:
			summary = append(summary, fmt.Sprintf("put %s %s", typed.Table, nexttest.JSONText(t, typed.Row.Value("session"))))
		case *store.TableDefinitionWrite:
			summary = append(summary, "table "+typed.Table)
		case *store.IndexWrite:
			summary = append(summary, "index "+typed.Table)
		case *store.DocumentCreate:
			summary = append(summary, fmt.Sprintf("create %s %s", typed.Record.Kind, *typed.Record.Host))
		default:
			summary = append(summary, part.StoreWriteKind())
		}
	}
	nexttest.RequireJSONEqual(t, shdStrings(summary), []any{
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
	if got := parts[len(parts)-1].(*store.EndPart).Writes; got != int64(len(parts)-2) {
		t.Fatalf("end writes = %d, want %d", got, len(parts)-2)
	}
	table := parts[1].(*store.TableDefinitionWrite)
	column, _ := table.Columns.Get("session")
	if column.Type != store.ColStr || column.Host == nil || *column.Host != "session" {
		t.Fatalf("session column = %+v", column)
	}
}

func TestStoreHostDataImportMakesTheDataInAnEmptyStoreFreshIdsAndSeqsTheValuesAsBases(t *testing.T) {
	archive := shdReadArchive(t, shdExported(t, shdPopulated(t), "S1"))
	target := memoryStore(t)
	counts, err := shdImportInto(target, archive, nil, "")
	must(t, err)
	if counts != (store.HostDataCounts{Documents: 1, Rows: 3}) {
		t.Fatalf("counts = %+v", counts)
	}
	nexttest.RequireJSONEqual(t, shdRead(t, target, "S1"), shdSessionData(t, "one", []string{"a", "b"}, []string{"session|S1: a", "session|S1: b"}, "p1"))
	records, err := store.Read(target, func(executor sqlite.SqliteExecutor) ([]store.DocumentRecord, error) {
		return store.ListHostRecords(executor, "session|S1", 0, 10)
	}, background)
	must(t, err)
	if len(records) != 1 || *records[0].Key != "session|S1" || *records[0].Host != "session|S1" || records[0].CreatedAt != 1 {
		t.Fatalf("records = %+v", records)
	}
}

func TestStoreHostDataImportRefusesAnAddressWithDataLeavingTheStoreAsItWasReplacesItWhenAsked(t *testing.T) {
	archive := shdReadArchive(t, shdExported(t, shdPopulated(t), "S1"))
	target := memoryStore(t)
	shdUsing(t, target, func(ref *Reference) { shdWrite(t, ref, "S1", "old", []string{"z"}, "p9") })
	before := target.LastSeq()
	_, err := shdImportInto(target, archive, nil, "")
	shdRefusal(t, err, `session|S1 has data in this store (import with existing: "replace")`, true)
	if target.LastSeq() != before {
		t.Fatalf("lastSeq = %d, want %d", target.LastSeq(), before)
	}
	counts, err := shdImportInto(target, archive, nil, store.ExistingReplace)
	must(t, err)
	if counts != (store.HostDataCounts{Documents: 1, Rows: 3}) {
		t.Fatalf("counts = %+v", counts)
	}
	nexttest.RequireJSONEqual(t, shdRead(t, target, "S1"), shdSessionData(t, "one", []string{"a", "b"}, []string{"session|S1: a", "session|S1: b"}, "p1"))
}

func TestStoreHostDataImportCopiesUnderANewAddressHostCellsAndKeysRewrittenAddressesInsideJSONUntouched(t *testing.T) {
	source := shdPopulated(t)
	archive := shdReadArchive(t, shdExported(t, source, "S1"))
	var pins *store.HostTable
	for _, table := range archive.Tables {
		if table.Write.Table == shdPins {
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
			copied.Puts = []*store.PutWrite{{Table: pins.Puts[0].Table, Row: row, Version: pins.Puts[0].Version, Columns: pins.Puts[0].Columns}}
			table = &copied
		}
		renamed.Tables = append(renamed.Tables, table)
	}
	address := Session.Address(jsonObject("id", "S3"))
	_, err := shdImportInto(source, &renamed, &address, "")
	must(t, err)
	nexttest.RequireJSONEqual(t, shdRead(t, source, "S3"), shdSessionData(t, "one", []string{"a", "b"}, []string{"session|S1: a", "session|S1: b"}, "p3"))
	got := shdRead(t, source, "S1").(*delta.JsonObject)
	nexttest.RequireJSONEqual(t, []any{got.Value("title"), got.Value("pin")}, []any{"one", "p1"})
}

func TestStoreHostDataImportRefusesAnotherKindATableAtAnotherVersionAndARowWhoseKeyHoldsAnotherAddressSRow(t *testing.T) {
	source := shdPopulated(t)
	archive := shdReadArchive(t, shdExported(t, source, "S1"))
	_, err := shdImportInto(source, archive, &contracts.Address{Key: "user"}, "")
	shdRefusal(t, err, "another kind", false)
	// S3 in the source: the pin `p1` is S1's.
	address := Session.Address(jsonObject("id", "S3"))
	_, err = shdImportInto(source, archive, &address, "")
	shdRefusal(t, err, `a row with key ["p1"] exists`, false)
	// A store whose `labels` table is at version 2.
	newer, evolveErr := store.EvolveTable(shdLabelsTable, func(e *store.TableEditor) store.TableTailEditor { return e.Add("extra", store.Opt(store.Str())) })
	must(t, evolveErr)
	target := memoryStore(t)
	host, _ := exOpenHost(t, loadedOptions{Store: target}, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(shdSessions, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.(StoreSelf).Table(newer)
			return exImpl(nil, map[string]contracts.MethodFunction{
				"write": exMethod(func(*delta.JsonObject, context.Context) (delta.JsonValue, error) { return nil, nil }),
				"read": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					return jsonObject("title", "", "labels", []any{}, "notes", []any{}), nil
				},
			}), nil
		}, ServeOptions{})
	})
	exCloseHost(t, host)
	_, err = shdImportInto(target, archive, nil, "")
	shdRefusal(t, err, "at version 1, 2 here; its owner is not live", false)
}

func TestStoreHostDataImportIsRecordedInTheOutboxAsOrdinaryWrites(t *testing.T) {
	source := shdPopulated(t)
	archive := shdReadArchive(t, shdExported(t, source, "S1"))
	_, err := shdImportInto(source, archive, nil, store.ExistingReplace)
	must(t, err)
	commits, err := source.Outbox().Read(source.LastSeq()-1, 1, background)
	must(t, err)
	var kinds []any
	for _, write := range commits[0].Writes {
		kinds = append(kinds, write.StoreWriteKind())
	}
	nexttest.RequireJSONEqual(t, kinds, []any{"document.retire", "deleteWhere", "deleteWhere", "put", "put", "put", "document.create"})
}

func TestStoreHostDataDeleteRetiresTheAddressSRecordsAndDeletesItsRowsInOneCommitOtherAddressesUntouched(t *testing.T) {
	chordStore := shdPopulated(t)
	counts, err := store.DeleteHostData(chordStore, "session|S1", store.HostCommit{Fence: openFence()}, background)
	must(t, err)
	if counts != (store.HostDataCounts{Documents: 1, Rows: 3}) {
		t.Fatalf("counts = %+v", counts)
	}
	nexttest.RequireJSONEqual(t, shdRead(t, chordStore, "S1"), shdSessionData(t, "", nil, nil, ""))
	nexttest.RequireJSONEqual(t, shdRead(t, chordStore, "S2"), shdSessionData(t, "two", []string{"c"}, []string{"session|S2: c"}, "p2"))
	commits, err := chordStore.Outbox().Read(chordStore.LastSeq()-1, 1, background)
	must(t, err)
	retired := commits[0].Writes[0].(*store.DocumentRetire)
	nexttest.RequireJSONEqual(t, []any{
		nexttest.JSONText(t, commits[0].Writes[0]), nexttest.JSONText(t, commits[0].Writes[1]), nexttest.JSONText(t, commits[0].Writes[2]),
	}, []any{
		fmt.Sprintf(`{"kind":"document.retire","id":%d,"revision":1}`, retired.ID),
		`{"kind":"deleteWhere","table":"test.sessions:labels","where":{"eq":["session","session|S1"]},"count":2}`,
		`{"kind":"deleteWhere","table":"test.sessions:pins","where":{"eq":["session","session|S1"]},"count":1}`,
	})
	// The string-keyed document with the same key is no host's: still there.
	plain, err := store.Read(chordStore, func(executor sqlite.SqliteExecutor) (sqlite.SqliteRow, error) {
		return executor.Get("SELECT count(*) AS n FROM chord_documents WHERE kind = 'plain'")
	}, background)
	must(t, err)
	if plain["n"] != int64(2) {
		t.Fatalf("plain documents = %v, want 2", plain["n"])
	}
}

// shdJSONParts are the parts of an archive as decoded JSON: a copy to edit (Pi's `structuredClone`).
func shdJSONParts(t *testing.T, parts []store.ArchivePart) []any {
	t.Helper()
	values := make([]any, len(parts))
	for index, part := range parts {
		values[index] = nexttest.ParseJSON(t, nexttest.JSONText(t, part))
	}
	return values
}

func shdKind(part any) string {
	kind, _ := part.(*delta.JsonObject).Value("kind").(string)
	return kind
}

func TestStoreHostDataReviewRound2ChecksRowsByTheRecordedHostColumnWhateverTheArchiveSMetadataSaysAMovedHostAnnotation(t *testing.T) {
	chordStore := shdPopulated(t)
	parts := shdJSONParts(t, shdExported(t, chordStore, "S1"))
	for _, part := range parts {
		object := part.(*delta.JsonObject)
		switch {
		case shdKind(part) == "table" && object.Value("table") == shdLabels:
			columns := object.Value("columns").(*delta.JsonObject)
			columns.Set("session", nexttest.ParseJSON(t, `{"type":"str"}`))
			columns.Set("label", nexttest.ParseJSON(t, `{"type":"str","host":"session"}`))
		case shdKind(part) == "put" && object.Value("table") == shdLabels:
			row := object.Value("row").(*delta.JsonObject)
			row.Set("session", "session|VICTIM")
			row.Set("label", "session|S1")
		}
	}
	archive, err := store.ReadHostArchive(store.SlicePartSource(parts), background)
	must(t, err)
	_, err = shdImportInto(chordStore, archive, nil, store.ExistingReplace)
	shdRefusal(t, err, "a row that does not belong to session|S1", false)
	nexttest.RequireJSONEqual(t, shdRead(t, chordStore, "VICTIM"), shdSessionData(t, "", nil, nil, ""))
}

func TestStoreHostDataReviewRound2RefusesATableExportedDuringAMigrationWhenNoHistoryCanResumeIt(t *testing.T) {
	chordStore := shdPopulated(t)
	parts := shdJSONParts(t, shdExported(t, chordStore, "S1"))
	for _, part := range parts {
		if object := part.(*delta.JsonObject); shdKind(part) == "table" && object.Value("table") == shdLabels {
			object.Set("state", "migrating")
		}
	}
	archive, err := store.ReadHostArchive(store.SlicePartSource(parts), background)
	must(t, err)
	_, err = shdImportInto(chordStore, archive, nil, store.ExistingReplace)
	shdRefusal(t, err, "its owner is not live", false)
}

func TestStoreHostDataReviewRound2SelectsByTheCanonicalKeyALoneSurrogateInAnAddressIsUFFFDInCellsKeysAndTheExport(t *testing.T) {
	chordStore := memoryStore(t)
	// A lone surrogate, as Go holds one: the bytes of its surrogate code point.
	lone := "x\xed\xa0\x80"
	shdUsing(t, chordStore, func(ref *Reference) { shdWrite(t, ref, lone, "odd", []string{"a"}, "p") })
	parts := shdExported(t, chordStore, lone)
	if host := parts[0].(*store.ManifestPart).Host; host == nil || *host != "session|x\uFFFD" {
		t.Fatalf("manifest = %s", nexttest.JSONText(t, parts[0]))
	}
	puts, creates := 0, 0
	for _, part := range parts {
		switch part.(type) {
		case *store.PutWrite:
			puts++
		case *store.DocumentCreate:
			creates++
		}
	}
	if puts != 2 || creates != 1 {
		t.Fatalf("puts, creates = %d, %d, want 2, 1", puts, creates)
	}
	counts, err := store.DeleteHostData(chordStore, "session|x\uFFFD", store.HostCommit{Fence: openFence()}, background)
	must(t, err)
	if counts != (store.HostDataCounts{Documents: 1, Rows: 2}) {
		t.Fatalf("counts = %+v", counts)
	}
}
