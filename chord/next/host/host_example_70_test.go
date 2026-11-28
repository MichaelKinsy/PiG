package host

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Twin of packages/chord/test/examples/70-named-queries.ts (Pi 7cc21c24f6b5): named queries (slice 14 §3). A store offers
// queries under names, with the schema of each one's arguments in its contract; any object on the store's host reads one
// live with `self.liveQuery(query, args, { first })` (its member is `{ rows, more }`), `self.count` is the exact number of
// rows, `page` reads without a live read.
//
// The script prints these lines and asserts nothing; here each printed value is a check, with the value the script prints
// on Pi's host (node 26, run from a git archive of 7cc21c24f6b5 in the tern-ci container):
//
//	1. newest of home: 8 7 5 | more: true | total: 6
//	2. two reads are open (the list and the count):
//	   a note on task 8 (a column neither depends on): 0 runs, 0 probes, 2 skipped
//	   task 8 finished (a mapped column of a row in the list): 1 runs, 0 probes, 1 skipped
//	   task 1 finished (not among the list's rows, and it cannot enter): 0 runs, 0 probes, 2 skipped
//	   a new task at the list's end (the probe asks; the count keeps no rows and runs): 1 runs, 1 probes, 1 skipped
//	   newest of home: 8✓ 7 5
//	3. one frame: 3
//	   total of work: 1
//	4. pages: [ 8, 7 ] [ 5, 4 ]
//
// Pi's `await write()` ends after the commit step ran the readers; here `lsSettled` (the list tests' barrier) stands for it
// before a counter is read, and for Pi's 10 ms after `board.show` (the frame of the switch).

var (
	ex70Task = contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("title", contracts.Str()), contracts.Prop("done", contracts.Bool()))

	ex70Tasks = exLocal("example.tasks", contracts.Params{},
		contracts.Mem("tasks", contracts.Queries(ex70Task, []contracts.Property{
			contracts.Prop("ofList", contracts.Obj(contracts.Prop("list", contracts.Str()))),
			contracts.Prop("one", contracts.Obj(contracts.Prop("id", contracts.Int()))),
		})),
		contracts.Mem("put", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("list", contracts.Str()), contracts.Prop("title", contracts.Str())))),
		contracts.Mem("finish", contracts.NewMethod(contracts.Int())),
		contracts.Mem("note", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("note", contracts.Str())))),
	)

	ex70Rows = mustTableDef("example.task-rows", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{
			store.Col("id", store.Int()), store.Col("list", store.Str()), store.Col("title", store.Str()),
			store.Col("done", store.Bool(contracts.Default(false))), store.Col("note", store.Str(contracts.Default(""))),
		},
		Key: []string{"id"},
		// The index `ofList` runs on: its condition's column, then its order.
		Indexes: store.IndexRecords{{Name: "by_list", Columns: []string{"list", "id"}}},
	})

	ex70Board = exLocal("example.board", contracts.Params{},
		contracts.Mem("newest", contracts.Obj(contracts.Prop("rows", contracts.Arr(ex70Task)), contracts.Prop("more", contracts.Bool()))),
		contracts.Mem("total", contracts.Int()),
		contracts.Mem("show", contracts.NewMethod(contracts.Str())),
	)
)

// The store's plugin: one builder per listed query, a pure function of its arguments. `Reads` lists the columns the
// mapping gets: a change of another column (`note`) is no change of an item.
var ex70StoreFacet = DefineFacet(func(host FacetHost, _ FacetSelf) error {
	return host.Serve(ex70Tasks, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		self := plain.(StoreSelf)
		rows := self.Table(ex70Rows)
		tasks := self.Queries(rows, QueriesOptions{
			Map: func(row store.Row, _ PartnerRow) (delta.JsonValue, error) {
				return jsonObject("id", float64(row["id"].(int64)), "title", row["title"], "done", row["done"]), nil
			},
			Reads: []string{"id", "title", "done"},
		}, map[string]QueryBuilder{
			"ofList": {Build: func(args delta.JsonValue) *store.Query {
				list := args.(*delta.JsonObject).Value("list").(string)
				return store.NewQuery(ex70Rows).
					Where(func(r, _ store.ColumnRefs) store.Condition { return store.Eq(r["list"], list) }).
					OrderBy(func(r, _ store.ColumnRefs) *store.ColumnRef { return r["id"] }, "desc")
			}},
			"one": {Build: func(args delta.JsonValue) *store.Query {
				id := int64(args.(*delta.JsonObject).Value("id").(float64))
				return store.NewQuery(ex70Rows).
					Where(func(r, _ store.ColumnRefs) store.Condition { return store.Eq(r["id"], id) })
			}},
		})
		tx := func(ctx context.Context, write func(tx documents.Tx) error) (delta.JsonValue, error) {
			_, err := self.Tx(func(tx documents.Tx) (any, error) { return nil, write(tx) }, ctx)
			return nil, err
		}
		return contracts.MembersImplementation{
			Queries: map[string]map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]{"tasks": tasks},
			Methods: map[string]contracts.MethodFunction{
				"put": exMethod(func(row *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return tx(ctx, func(tx documents.Tx) error {
						return tx.Put(rows, store.Row{"id": int64(row.Value("id").(float64)), "list": row.Value("list"), "title": row.Value("title")})
					})
				}),
				// A writer names what it changes: an `update` tells readers which columns changed.
				"finish": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return tx(ctx, func(tx documents.Tx) error {
						return tx.Update(rows, store.Row{"id": int64(id.(float64))}, store.Row{"done": true})
					})
				},
				"note": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return tx(ctx, func(tx documents.Tx) error {
						return tx.Update(rows, store.Row{"id": int64(args.Value("id").(float64))}, store.Row{"note": args.Value("note")})
					})
				}),
			},
		}, nil
	}, ServeOptions{})
})

// A reader in another plugin: the newest three tasks of the list it shows, and how many there are. Its arguments follow
// `shown`: when it changes, the member keeps the previous rows until the new read has run.
var ex70BoardFacet = DefineFacet(func(host FacetHost, _ FacetSelf) error {
	return host.Serve(ex70Board, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
		tasks := self.Get(ex70Tasks, jsonObject()).Queries("tasks")
		shown := self.Writable("home")
		args := self.Derive([]any{shown}, func(values []any) (delta.JsonValue, error) { return jsonObject("list", values[0]), nil })
		return contracts.MembersImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"newest": self.LiveQuery(tasks["ofList"], args, 3),
				// Exact and uncapped: count what an index bounds.
				"total": self.Count(tasks["ofList"], args),
			},
			Methods: map[string]contracts.MethodFunction{
				"show": func(list delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					return nil, shown.Set(list.(string))
				},
			},
		}, nil
	}, ServeOptions{})
})

func TestExample70NamedQueries(t *testing.T) {
	// Work counts through the context of the call that causes it: this one records in memory.
	meter := nexttest.Metered(t)
	context := meter.Context
	errs := &hostErrors{}
	host := newHost(t, HostOptions{Store: memoryStore(t), Telemetry: meter.Telemetry, OnError: errs.push})
	if _, err := host.Load([]HostFacet{{Name: "@example/tasks", Facet: ex70StoreFacet}, {Name: "@example/board", Facet: ex70BoardFacet}}, context); err != nil {
		t.Fatal(err)
	}
	tasks := mustGetWith(t, host, ex70Tasks, context)
	board := mustGetWith(t, host, ex70Board, context)
	call := func(ref *Reference, method string, args delta.JsonValue) {
		t.Helper()
		if _, err := ref.Call(method, args, context); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}

	for _, entry := range []struct {
		id   int
		list string
	}{{1, "home"}, {2, "home"}, {3, "work"}, {4, "home"}, {5, "home"}, {7, "home"}, {8, "home"}} {
		call(tasks, "put", jsonObject("id", float64(entry.id), "list", entry.list, "title", fmt.Sprintf("task %d", entry.id)))
	}
	newest := func() *delta.JsonObject {
		value, err := board.Member("newest").Value()
		must(t, err)
		return value.(*delta.JsonObject)
	}
	titles := func() string {
		parts := []string{}
		for _, task := range newest().Value("rows").([]any) {
			task := task.(*delta.JsonObject)
			part := fmt.Sprint(int(task.Value("id").(float64)))
			if task.Value("done").(bool) {
				part += "✓"
			}
			parts = append(parts, part)
		}
		return strings.Join(parts, " ")
	}
	total := func() delta.JsonValue {
		value, err := board.Member("total").Value()
		must(t, err)
		return value
	}
	expect := func(got, want string) {
		t.Helper()
		if got != want {
			t.Fatalf("%s\nwant %s", got, want)
		}
	}
	// 1. The reader's first rows were read before it went live.
	expect(fmt.Sprintf("1. newest of home: %s | more: %v | total: %v", titles(), newest().Value("more"), total()),
		"1. newest of home: 8 7 5 | more: true | total: 6")

	// 2. What a commit costs the readers: nothing, one keyed probe, or one run.
	cost := func(what string, write func(), want string) {
		t.Helper()
		names := []string{"chord.query.runs", "chord.query.probes", "chord.query.skips"}
		before := meter.Counts()
		write()
		lsSettled(host)
		after := meter.Counts()
		expect(fmt.Sprintf("   %s: %v runs, %v probes, %v skipped", what,
			after[names[0]]-before[names[0]], after[names[1]]-before[names[1]], after[names[2]]-before[names[2]]), want)
	}
	cost("a note on task 8 (a column neither depends on)", func() { call(tasks, "note", jsonObject("id", 8.0, "note", "call back")) },
		"   a note on task 8 (a column neither depends on): 0 runs, 0 probes, 2 skipped")
	cost("task 8 finished (a mapped column of a row in the list)", func() { call(tasks, "finish", 8.0) },
		"   task 8 finished (a mapped column of a row in the list): 1 runs, 0 probes, 1 skipped")
	cost("task 1 finished (not among the list's rows, and it cannot enter)", func() { call(tasks, "finish", 1.0) },
		"   task 1 finished (not among the list's rows, and it cannot enter): 0 runs, 0 probes, 2 skipped")
	cost("a new task at the list's end (the probe asks; the count keeps no rows and runs)", func() {
		call(tasks, "put", jsonObject("id", 0.0, "list", "home", "title", "task 0"))
	}, "   a new task at the list's end (the probe asks; the count keeps no rows and runs): 1 runs, 1 probes, 1 skipped")
	expect("   newest of home: "+titles(), "   newest of home: 8✓ 7 5")

	// 3. The arguments follow a value: the list switches in one frame, from the rows of one list to the rows of the other.
	watch, err := board.Member("newest").Watch(context)
	must(t, err)
	frames := nexttest.ChangesOf(watch)
	call(board, "show", "work")
	lsSettled(host)
	var got []string
	for _, frame := range frames.Items() {
		if frame.Kind != chord.WatchCallValue {
			got = append(got, "3. one frame: "+fmt.Sprint(frame.Kind))
			continue
		}
		got = append(got, "3. one frame: "+titles())
	}
	expect(strings.Join(got, "\n"), "3. one frame: 3")
	expect(fmt.Sprintf("   total of work: %v", total()), "   total of work: 1")
	_, err = watch.Stop()
	must(t, err)

	// 4. A page at a time, not live: for jobs and scripts.
	ofList := tasks.Queries("tasks")["ofList"]
	args := jsonObject("list", "home")
	first, err := ofList.Page(args, 2, context, contracts.QueryPageOptions{})
	must(t, err)
	if first.Next == nil {
		t.Fatalf("the first page has no next")
	}
	second, err := ofList.Page(args, 2, context, contracts.QueryPageOptions{After: first.Next})
	must(t, err)
	ids := func(items []delta.JsonValue) []int {
		out := []int{}
		for _, item := range items {
			out = append(out, int(item.(*delta.JsonObject).Value("id").(float64)))
		}
		return out
	}
	expect(fmt.Sprintf("4. pages: %v %v", ids(first.Items), ids(second.Items)), "4. pages: [8 7] [5 4]")

	board.Release()
	tasks.Release()
	must(t, host.Close(context))
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}

// mustGetWith is `await host.get(Type, {}, context)` under the call's own context.
func mustGetWith(t testing.TB, host Host, typ *contracts.Contract, ctx context.Context) *Reference {
	t.Helper()
	reference, err := host.Get(ctx, typ, jsonObject(), GetOptions{})
	must(t, err)
	return reference
}
