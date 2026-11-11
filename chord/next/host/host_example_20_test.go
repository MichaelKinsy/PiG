package host

// pi: packages/chord/test/examples/20-tables.ts

import (
	"context"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// --- 20-tables.ts --------------------------------------------------------------------------------------------------

// Ports packages/chord/test/examples/20-tables.ts: the factory declares its table with `self.table(Edits)`
// (20-tables.ts:45, StoreSelf.Table) and the app closes the store it opened with `store.close(context)`
// (packages/chord/test/examples/20-tables.ts:101, through the store.Store interface OpenStore returned).
func TestExample20Tables(t *testing.T) {
	// Flat columns, defaults, a key of non-opt str or int columns, and indexes, which end with the key.
	edits, err := store.DefineTable("edits", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{
			store.Col("id", store.Str()), store.Col("path", store.Str()), store.Col("at", store.Int()),
			store.Col("hits", store.Int(contracts.Default(0))), store.Col("done", store.Bool(contracts.Default(false))),
			store.Col("note", store.Opt(store.Str())),
		},
		Key:     []string{"id"},
		Indexes: store.IndexRecords{{Name: "by_path", Columns: []string{"path", "at"}}},
	})
	must(t, err)
	history := exLocal("example.history", contracts.Params{},
		contracts.Mem("record", contracts.NewMethod(exObj(exProp("id", exStr()), exProp("path", exStr()), exProp("at", exInt())))),
		contracts.Mem("hit", contracts.NewMethod(exStr())), contracts.Mem("finish", contracts.NewMethod(exStr())),
		contracts.Mem("hits", contracts.NewMethod(exStr(), exInt())),
		contracts.Mem("open", contracts.NewMethod(exObj(exProp("after", exOpt(exStr()))),
			exObj(exProp("ids", exArr(exStr())), exProp("next", exOpt(exStr()))))),
		contracts.Mem("finished", contracts.NewMethod(exObj(), exInt())))
	column := store.NewColumnRef
	facet := func(h FacetHost, _ FacetSelf) error {
		// Tables need a stable identity: a local type with empty params (one object per host).
		return h.Serve(history, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			table := self.(StoreSelf).Table(edits)
			// A query of the table: a condition and an order (the key is the tie-break); immutable, built once.
			open := store.NewQuery(edits).Where(func(row store.ColumnRefs) store.Condition { return store.Eq(row["done"], false) }).
				OrderBy(func(row store.ColumnRefs) *store.ColumnRef { return row["at"] }, "desc")
			return exImpl(nil, map[string]contracts.MethodFunction{
				"record": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					// Writes are staged synchronously (a bad row fails here) and commit together in one step.
					row := store.Row{"id": args.Value("id"), "path": args.Value("path"), "at": int64(args.Value("at").(float64))}
					_, err := self.Tx(func(tx documents.Tx) (any, error) { return nil, tx.Put(table, row) }, ctx)
					return nil, err
				}),
				"hit": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					// A set is values or an expression over the row's columns, computed by the store.
					_, err := self.Tx(func(tx documents.Tx) (any, error) {
						return nil, tx.Update(table, store.Row{"id": id}, store.Row{"hits": store.Add(column("hits"), 1)})
					}, ctx)
					return nil, err
				},
				"finish": func(path delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					// One statement for every matching row; its count is recorded with the write.
					_, err := self.Tx(func(tx documents.Tx) (any, error) {
						return nil, tx.UpdateWhere(table, store.Prefix(column("path"), path.(string)), store.Row{"done": true})
					}, ctx)
					return nil, err
				},
				"hits": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					row, err := table.Get(ctx, store.Row{"id": id})
					if err != nil || row == nil {
						return float64(0), err
					}
					return float64(row["hits"].(int64)), nil
				},
				"open": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					options := store.PageOptions{}
					if after, ok := args.Get("after"); ok && after != nil {
						cursor := after.(string)
						options.After = &cursor
					}
					page, err := table.Page(ctx, open, 1, options)
					if err != nil {
						return nil, err
					}
					ids := []any{}
					for _, row := range page.Items {
						ids = append(ids, row["id"])
					}
					result := jsonObject("ids", ids)
					if page.Next != nil {
						result.Set("next", *page.Next)
					}
					return result, nil
				}),
				"finished": exMethod(func(_ *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					count, err := table.Count(ctx, store.NewQuery(edits).Where(func(row store.ColumnRefs) store.Condition { return store.Eq(row["done"], true) }))
					return float64(count), err
				}),
			}), nil
		}, ServeOptions{})
	}
	// A store over SQLite (`:memory:` here, a file in an app): the app opens it, gives it to one host, and closes it.
	st := exFileStore(t, ":memory:")
	host, _ := exOpenHost(t, loadedOptions{Store: st}, facet)
	ref := exGet(t, host, history, jsonObject())
	for i := range 5 {
		path := "src/b.ts"
		if i%2 == 0 {
			path = "src/a.ts"
		}
		exCall(t, ref, "record", jsonObject("id", fmt.Sprintf("e%d", i), "path", path, "at", float64(i)))
	}
	exCall(t, ref, "hit", "e1")
	exCall(t, ref, "hit", "e1")
	exCall(t, ref, "finish", "src/a")
	exEqual(t, "hits of e1", exCall(t, ref, "hits", "e1"), float64(2))
	// Pages continue from an opaque cursor.
	first := exCall(t, ref, "open", jsonObject()).(*delta.JsonObject)
	exEqual(t, "open, first page", first.Value("ids"), exJSON(t, `["e3"]`))
	next := exCall(t, ref, "open", jsonObject("after", first.Value("next"))).(*delta.JsonObject)
	exEqual(t, "open, next page", next.Value("ids"), exJSON(t, `["e1"]`))
	exEqual(t, "finished", exCall(t, ref, "finished", jsonObject()), float64(3))
	exCloseHost(t, host)
	exCloseStore(t, st)
}
