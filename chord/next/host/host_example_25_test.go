package host

// pi: packages/chord/test/next/examples/25-export-import.ts

import (
	"context"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// --- 25-export-import.ts -------------------------------------------------------------------------------------------

// Ports packages/chord/test/next/examples/25-export-import.ts: the factory declares its table with `self.table(Visits)`
// (25-export-import.ts:49, StoreSelf.Table), the original store's `lastSeq` is read at 25-export-import.ts:89 and the
// store closed at packages/chord/test/next/examples/25-export-import.ts:90, all through the store.Store interface.
func TestExample25ExportImport(t *testing.T) {
	visits, err := store.DefineTable("visits", store.TableOptions{
		Version: 1, Columns: []store.ColumnProp{store.Col("page", store.Str()), store.Col("count", store.Int())}, Key: []string{"page"},
	})
	must(t, err)
	title := exDefineState("title", documents.StateOptions{
		Schema:         exObj(exProp("text", exStr())),
		Initial:        func() *delta.JsonObject { return jsonObject("text", "untitled") },
		Persist:        true,
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	site := exLocal("example.site", contracts.Params{},
		contracts.Mem("title", exObj(exProp("text", exStr()))), contracts.Mem("visit", contracts.NewMethod(exStr(), exInt())),
		contracts.Mem("rename", contracts.NewMethod(exStr())))
	facet := func(h FacetHost, _ FacetSelf) error {
		return h.Serve(site, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			table := self.(StoreSelf).Table(visits)
			titleDoc := stateOf(self, title)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"title": titleDoc.Member()}, map[string]contracts.MethodFunction{
				"visit": func(page delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					count, err := self.Tx(func(tx documents.Tx) (any, error) {
						row, err := tx.Get(table, store.Row{"page": page})
						if err != nil {
							return nil, err
						}
						next := int64(1)
						if row != nil {
							next = row["count"].(int64) + 1
						}
						return next, tx.Put(table, store.Row{"page": page, "count": next})
					}, ctx)
					if err != nil {
						return nil, err
					}
					return float64(count.(int64)), nil
				},
				"rename": func(text delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, titleDoc.Change(func(draft *delta.Object) { _ = draft.Set("text", text) }, ctx)
				},
			}), nil
		}, ServeOptions{})
	}
	// serve is a host on st serving the site, run until work is done.
	serve := func(st store.Store, work func(ref *Reference)) {
		host, _ := exOpenHost(t, loadedOptions{Store: st}, facet)
		work(exGet(t, host, site, jsonObject()))
		exCloseHost(t, host)
	}
	directory := t.TempDir()
	original := exFileStore(t, directory+"/a.db")
	serve(original, func(ref *Reference) {
		exCall(t, ref, "rename", "Chord")
		exCall(t, ref, "visit", "/")
		exCall(t, ref, "visit", "/")
		exCall(t, ref, "visit", "/docs")
	})
	// Export to a local file: the queue is held only while the parts are read.
	archive := directory + "/archive.jsonl"
	export, err := original.Export(background)
	must(t, err)
	encoded := store.EncodeJsonl(export)
	var written []byte
	for {
		chunk, ok, err := encoded.Next(background)
		must(t, err)
		if !ok {
			break
		}
		written = append(written, chunk...)
	}
	must(t, os.WriteFile(archive, written, 0o600))
	if len(written) == 0 {
		t.Fatal("archive: 0 bytes")
	}
	seq := original.LastSeq()
	exCloseStore(t, original)
	// Import into an empty backend: every part is checked; a refused archive leaves the backend empty.
	content, err := os.ReadFile(archive)
	must(t, err)
	database, err := node.OpenNodeSqliteDatabase(directory+"/b.db", node.NodeSqliteDatabaseOptions{})
	must(t, err)
	copied, err := store.ImportStore(store.SqliteBackend(database), store.DecodeJsonl(&exBytesSource{content: content, chunk: 64 * 1024}), background, store.StoreOptions{})
	must(t, err)
	if copied.LastSeq() != seq {
		t.Fatalf("imported at seq %d, want %d", copied.LastSeq(), seq)
	}
	serve(copied, func(ref *Reference) {
		exEqual(t, "title", exValue(t, ref, "title").(*delta.JsonObject).Value("text"), "Chord")
		exEqual(t, "next visit of /", exCall(t, ref, "visit", "/"), float64(3))
	})
	exCloseStore(t, copied)
}
