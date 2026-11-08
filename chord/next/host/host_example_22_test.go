package host

// pi: packages/chord/test/next/examples/22-migrations.ts

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// --- 22-migrations.ts ----------------------------------------------------------------------------------------------

func TestExample22Migrations(t *testing.T) {
	// Version 1, the base, written in full.
	editsV1, err := store.DefineTable("edits", store.TableOptions{
		Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Str()), store.Col("file", store.Str()), store.Col("at", store.Int())}, Key: []string{"id"},
	})
	must(t, err)
	// Version 2: `file` becomes `path`, a `dir` column is added and filled from each row (the data step).
	editsV2, err := store.EvolveTable(editsV1, func(e *store.TableEditor) store.TableTailEditor {
		return e.Rename("file", "path").Add("dir", store.Str(contracts.Default(""))).Rows(func(row store.Row) (any, error) {
			path := row["path"].(string)
			return store.Row{"dir": path[:strings.LastIndex(path, "/")]}, nil
		})
	})
	must(t, err)
	// Version 3: `dir` is dropped again, an index is added. Data functions stay as deployed; history only grows.
	editsV3, err := store.EvolveTable(editsV2, func(e *store.TableEditor) store.TableTailEditor {
		return e.Drop("dir").Index("by_path", []string{"path", "at"})
	})
	must(t, err)
	history := exLocal("example.history", contracts.Params{},
		contracts.Mem("record", contracts.NewMethod(exObj(exProp("id", exStr()), exProp("at", exInt())))),
		contracts.Mem("dump", contracts.NewMethod(exObj(), exArr(exStr()))))
	// facetOf is the facet of one deployment: its newest table version, and how it writes a row.
	facetOf := func(definition *store.TableDefinition, columns []string, row func(id string, at int64) store.Row) func(FacetHost, FacetSelf) error {
		return func(h FacetHost, _ FacetSelf) error {
			return h.Serve(history, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				edits := self.(StoreSelf).Table(definition)
				return exImpl(nil, map[string]contracts.MethodFunction{
					"record": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						_, err := self.Tx(func(tx documents.Tx) (any, error) {
							return nil, tx.Put(edits, row(exParam(args, "id"), int64(args.Value("at").(float64))))
						}, ctx)
						return nil, err
					}),
					"dump": exMethod(func(_ *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						lines := []any{}
						rows := edits.Rows(ctx, store.NewQuery(definition))
						for {
							entry, ok, err := rows.Next(ctx)
							if err != nil {
								return nil, err
							}
							if !ok {
								return lines, nil
							}
							// JSON.stringify(row): the keys in column order.
							ordered := delta.NewJsonObject(len(columns))
							for _, column := range columns {
								ordered.Set(column, exNumber(entry[column]))
							}
							lines = append(lines, exText(ordered))
						}
					}),
				}), nil
			}, ServeOptions{})
		}
	}
	// deployment is a host on the store at path serving facet, run until work is done.
	deployment := func(path string, facet func(FacetHost, FacetSelf) error, work func(ref *Reference)) {
		st := exFileStore(t, path)
		// Objects with empty params activate at load: the migration runs before `load` resolves.
		host, _ := exOpenHost(t, loadedOptions{Store: st}, facet)
		work(exGet(t, host, history, jsonObject()))
		exCloseHost(t, host)
		exCloseStore(t, st)
	}
	path := t.TempDir() + "/history.db"
	deployment(path, facetOf(editsV1, []string{"id", "file", "at"}, func(id string, at int64) store.Row {
		return store.Row{"id": id, "file": "src/" + id + ".ts", "at": at}
	}), func(ref *Reference) {
		for i := range 3 {
			exCall(t, ref, "record", jsonObject("id", fmt.Sprintf("e%d", i), "at", float64(i)))
		}
		exEqual(t, "version 1", exCall(t, ref, "dump", jsonObject()),
			exJSON(t, `["{\"id\":\"e0\",\"file\":\"src/e0.ts\",\"at\":0}","{\"id\":\"e1\",\"file\":\"src/e1.ts\",\"at\":1}","{\"id\":\"e2\",\"file\":\"src/e2.ts\",\"at\":2}"]`))
	})
	// The next deployment declares version 3: the store goes through 2 (rename, add, rows) and 3 (drop, index).
	deployment(path, facetOf(editsV3, []string{"id", "path", "at"}, func(id string, at int64) store.Row {
		return store.Row{"id": id, "path": "lib/" + id + ".ts", "at": at}
	}), func(ref *Reference) {
		exCall(t, ref, "record", jsonObject("id", "e9", "at", float64(9)))
		exEqual(t, "version 3", exCall(t, ref, "dump", jsonObject()),
			exJSON(t, `["{\"id\":\"e0\",\"path\":\"src/e0.ts\",\"at\":0}","{\"id\":\"e1\",\"path\":\"src/e1.ts\",\"at\":1}","{\"id\":\"e2\",\"path\":\"src/e2.ts\",\"at\":2}","{\"id\":\"e9\",\"path\":\"lib/e9.ts\",\"at\":9}"]`))
	})
}
