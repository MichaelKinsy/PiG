package host

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Twin of packages/chord/test/examples/66-host-data.ts: data belonging to a host. A server object keeps facts about each
// project (a worker host, `Project({ id })`) in a document keyed by the project's address (`keyed: Project`) and in table
// rows with a `host(Project)` column; `exportHostData` writes them as a store archive, `importHostData` brings them back
// (here as a copy under another address) and `deleteHostData` removes them. The script's five printed steps are the five
// checks of the one test, in order.

func TestExample66HostData(t *testing.T) {
	project := mustKind(contracts.NewHostKind("project", contracts.Prop("id", contracts.Str())))
	address := func(id string) contracts.Address { return project.Address(jsonObject("id", id)) }
	meta := exDefineState("example.meta", documents.StateOptions{
		Schema: exObj(exProp("title", exStr())), Initial: func() *delta.JsonObject { return jsonObject("title", "") },
		Keyed: project, Persist: true,
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	labels, err := store.DefineTable("example.labels", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{store.Col("project", store.Host(project)), store.Col("label", store.Str())},
		Key:     []string{"project", "label"},
	})
	must(t, err)
	projects := exLocal("example.projects", contracts.Params{},
		contracts.Mem("describe", contracts.NewMethod(exObj(exProp("id", exStr()), exProp("title", exStr()), exProp("labels", exArr(exStr()))))),
		contracts.Mem("show", contracts.NewMethod(exStr(), exStr())))

	serverStore := memoryStore(t)
	server, _ := exOpenHost(t, loadedOptions{Store: serverStore}, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(projects, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			handle := self.State(meta).(*documents.Keyed)
			table := self.(StoreSelf).Table(labels)
			return exImpl(nil, map[string]contracts.MethodFunction{
				"describe": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					_, err := self.Tx(func(tx documents.Tx) (any, error) {
						// Keys are addresses: `Project({ id })`, never a string.
						draft, err := tx.Draft(handle, address(exParam(args, "id")))
						if err != nil {
							return nil, err
						}
						if err := draft.Set("title", args.Value("title")); err != nil {
							return nil, err
						}
						for _, label := range args.Value("labels").([]any) {
							if err := tx.Put(table, store.Row{"project": address(exParam(args, "id")), "label": label}); err != nil {
								return nil, err
							}
						}
						return nil, nil
					}, ctx)
					return nil, err
				}),
				"show": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					title := "(none)"
					found, err := handle.Get(ctx, address(id.(string)))
					if err != nil {
						return nil, err
					}
					if found != nil {
						title, _ = found.Value("title").(string)
					}
					rows, err := table.Page(ctx, store.NewQuery(labels).Where(func(t store.ColumnRefs) store.Condition {
						return store.Eq(t["project"], address(id.(string)))
					}), 10, store.PageOptions{})
					if err != nil {
						return nil, err
					}
					names := make([]string, len(rows.Items))
					for index, row := range rows.Items {
						names[index], _ = row["label"].(string)
					}
					return title + " [" + strings.Join(names, ", ") + "]", nil
				},
			}), nil
		}, ServeOptions{})
	})
	reference := exGet(t, server, projects, jsonObject())
	exCall(t, reference, "describe", jsonObject("id", "p1", "title", "Launch", "labels", []any{"urgent", "web"}))
	exCall(t, reference, "describe", jsonObject("id", "p9", "title", "Other", "labels", []any{"later"}))
	exEqual(t, "1. server p1", exCall(t, reference, "show", "p1"), "Launch [urgent, web]")
	exEqual(t, "1. server p9", exCall(t, reference, "show", "p9"), "Other [later]")

	// 2. Export p1: its worker's store (empty here) and the server's records and rows belonging to Project({ id: "p1" }).
	workerStore := memoryStore(t)
	workerExport, err := workerStore.Export(background)
	must(t, err)
	workerArchive := exCollectParts(t, workerExport)
	serverExport, err := server.ExportHostData(address("p1"), background)
	must(t, err)
	serverPart := exCollectParts(t, serverExport)
	var summary []any
	for _, part := range serverPart {
		switch typed := part.(type) {
		case *store.PutWrite:
			summary = append(summary, "put "+exText(typed.Row))
		case *store.ManifestPart:
			summary = append(summary, "manifest "+*typed.Host)
		default:
			summary = append(summary, part.StoreWriteKind())
		}
	}
	exEqual(t, "2. p1's server part", summary, []any{
		"manifest project|p1",
		"table",
		`put {"project":"project|p1","label":"urgent"}`,
		`put {"project":"project|p1","label":"web"}`,
		"document.create",
		"document.revision",
		"end",
	})

	// 3. Import it as p2: the worker's store into a new store, the server's part as a copy (host cells and keys rewritten).
	database, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	must(t, err)
	p2Store, err := store.ImportStore(store.SqliteBackend(database), store.SlicePartSource(workerArchive), background, store.StoreOptions{})
	must(t, err)
	defer func() { must(t, p2Store.Close(background)) }()
	p2 := address("p2")
	counts, err := server.ImportHostData(store.SlicePartSource(serverPart), background, store.HostImportOptions{Address: &p2})
	must(t, err)
	if counts != (store.HostDataCounts{Documents: 1, Rows: 2}) {
		t.Fatalf("3. imported = %+v, want {Documents:1 Rows:2}", counts)
	}
	exEqual(t, "3. p2", exCall(t, reference, "show", "p2"), "Launch [urgent, web]")

	// 4. Importing again is refused: p2 has data now. With `existing: "replace"` it would replace it.
	_, err = server.ImportHostData(store.SlicePartSource(serverPart), background, store.HostImportOptions{Address: &p2})
	if err == nil || err.Error() != `importHostData: project|p2 has data in this store (import with existing: "replace")` {
		t.Fatalf("4. refused = %v, want project|p2 has data in this store", err)
	}

	// 5. Delete p1: its document and rows go; p2 and p9 stay.
	deleted, err := server.DeleteHostData(address("p1"), background)
	must(t, err)
	if deleted != (store.HostDataCounts{Documents: 1, Rows: 2}) {
		t.Fatalf("5. deleted = %+v, want {Documents:1 Rows:2}", deleted)
	}
	exEqual(t, "5. p1", exCall(t, reference, "show", "p1"), "(none) []")
	exEqual(t, "5. p2", exCall(t, reference, "show", "p2"), "Launch [urgent, web]")

	reference.Release()
	exCloseHost(t, server)
}

// exCollectParts is `collect(parts)`: every part of an export.
func exCollectParts(t testing.TB, export *store.StoreExport) []store.ArchivePart {
	t.Helper()
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
