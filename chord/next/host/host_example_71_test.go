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

// Twin of packages/chord/test/examples/71-lists.ts (Pi 7cc21c24f6b5): a list a screen loads more of (slice 14 §4,
// `self.list(name, [[namedQuery, args]], { first })`). The pages tile the query's order; loaded rows stay live; "load
// more" adds only the new rows; a row that moves to the top leaves one page and enters another in one commit; the last
// page is open while no row is known behind it; a reload of the list's plugin opens the pages it had.
//
// The script prints these lines and asserts nothing; here each printed value is a check, with the value the script
// prints on Pi's host (node 26, run from a git archive of 7cc21c24f6b5 in the tern-ci container, 3 runs the same):
//
//	1. the first page: 10 9 8 7 …
//	2. after more:     10 9 8 7 6 5 4 3 … | loaded rows kept: true
//	3. one frame:      5 10 9 8 7 6 4 3 …
//	3. one frame:      1 5 10 9 8 7 6 4 3 …
//	3. one frame:      11 1 5 10 9 8 7 6 4 3 …
//	4. after a reload: 11 1 5 10 9 8 7 6 4 3 …
//	5. everything:     11 1 5 10 9 8 7 6 4 3 2
//
// Pi waits 5 ms after each call for the frame; here the wait is lsSettled (the barrier of the list tests: every read and
// load the lists started has ended and the watch deliveries have run), and the frames are the watch's collected calls.

var (
	ex71Child = contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("name", contracts.Str()), contracts.Prop("lastTouch", contracts.Int()))

	ex71Children = exLocal("example.children", contracts.Params{},
		contracts.Mem("children", contracts.Queries(ex71Child, []contracts.Property{
			contracts.Prop("ofSession", contracts.Obj(contracts.Prop("session", contracts.Str()))),
		})),
		contracts.Mem("spawn", contracts.NewMethod(contracts.Obj(contracts.Prop("session", contracts.Str()), contracts.Prop("count", contracts.Int())))),
		contracts.Mem("message", contracts.NewMethod(contracts.Int())),
	)

	ex71ChildRows = mustTableDef("example.child-rows", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{
			store.Col("id", store.Int()), store.Col("session", store.Str()), store.Col("name", store.Str()), store.Col("lastTouch", store.Int()),
		},
		Key: []string{"id"},
		// The index `ofSession` runs on: its condition's column, then its order.
		Indexes: store.IndexRecords{{Name: "of_session", Columns: []string{"session", "lastTouch"}}},
	})

	ex71Subagents = exLocal("example.subagents",
		contracts.Params{contracts.Prop("session", contracts.Str()), contracts.Prop("view", contracts.Str())},
		contracts.Mem("list", contracts.Obj(contracts.Prop("rows", contracts.Arr(ex71Child)), contracts.Prop("moreAfter", contracts.Bool()))),
		contracts.Mem("more", contracts.NewMethod()),
	)
)

func ex71StoreFacet(clock, nextID *int64) Facet {
	return DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(ex71Children, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
			self := plain.(StoreSelf)
			rows := self.Table(ex71ChildRows)
			children := self.Queries(rows, QueriesOptions{
				Map: func(row store.Row, _ PartnerRow) (delta.JsonValue, error) {
					return jsonObject("id", float64(row["id"].(int64)), "name", row["name"], "lastTouch", float64(row["lastTouch"].(int64))), nil
				},
				Reads: []string{"id", "name", "lastTouch"},
			}, map[string]QueryBuilder{
				"ofSession": {Build: func(args delta.JsonValue) *store.Query {
					session := args.(*delta.JsonObject).Value("session").(string)
					return store.NewQuery(ex71ChildRows).
						Where(func(r, _ store.ColumnRefs) store.Condition { return store.Eq(r["session"], session) }).
						OrderBy(func(r, _ store.ColumnRefs) *store.ColumnRef { return r["lastTouch"] }, "desc")
				}},
			})
			return contracts.MembersImplementation{
				Queries: map[string]map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]{"children": children},
				Methods: map[string]contracts.MethodFunction{
					"spawn": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						session, count := args.Value("session").(string), int(args.Value("count").(float64))
						_, err := self.Tx(func(tx documents.Tx) (any, error) {
							for range count {
								*nextID++
								*clock++
								if err := tx.Put(rows, store.Row{"id": *nextID, "session": session, "name": fmt.Sprintf("agent %d", *nextID), "lastTouch": *clock}); err != nil {
									return nil, err
								}
							}
							return nil, nil
						}, ctx)
						return nil, err
					}),
					// A message touches its child: an `update` of the column that changes.
					"message": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						_, err := self.Tx(func(tx documents.Tx) (any, error) {
							*clock++
							return nil, tx.Update(rows, store.Row{"id": int64(id.(float64))}, store.Row{"lastTouch": *clock})
						}, ctx)
						return nil, err
					},
				},
			}, nil
		}, ServeOptions{})
	})
}

// ex71ListFacet is Pi's `listFacet()`: one list object per screen, `view` its own id.
func ex71ListFacet() Facet {
	return DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(ex71Subagents, func(params *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
			source := ListSource{
				Query: self.Get(ex71Children, jsonObject()).Queries("children")["ofSession"],
				Args:  jsonObject("session", params.Value("session")),
			}
			list := self.List("children", []ListSource{source}, ListOptions{First: 4})
			return contracts.MembersImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"list": list.Value()},
				Methods: map[string]contracts.MethodFunction{
					"more": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return nil, list.More(ctx) },
				},
			}, nil
		}, ServeOptions{})
	})
}

func TestExample71Lists(t *testing.T) {
	var clock, nextID int64
	storePlugin := HostFacet{Name: "@example/children", Facet: ex71StoreFacet(&clock, &nextID)}
	errs := &hostErrors{}
	host := newHost(t, HostOptions{Store: memoryStore(t), OnError: errs.push})
	mustLoad(t)(host.Load([]HostFacet{storePlugin, {Name: "@example/list", Facet: ex71ListFacet()}}, background))
	children := exGet(t, host, ex71Children, jsonObject())
	exCall(t, children, "spawn", jsonObject("session", "s1", "count", 10.0))

	// 1. A screen holds its list: the first page, and whether rows may follow.
	screen := exGet(t, host, ex71Subagents, jsonObject("session", "s1", "view", "tab-1"))
	text := func(value delta.JsonValue) string {
		object := value.(*delta.JsonObject)
		ids := []string{}
		for _, child := range object.Value("rows").([]any) {
			ids = append(ids, fmt.Sprint(int(child.(*delta.JsonObject).Value("id").(float64))))
		}
		shown := strings.Join(ids, " ")
		if object.Value("moreAfter").(bool) {
			shown += " …"
		}
		return shown
	}
	shown := func() string {
		value, err := screen.Member("list").Value()
		must(t, err)
		return text(value)
	}
	check := func(label, want string) {
		t.Helper()
		if got := label + shown(); got != want {
			t.Fatalf("%s\nwant %s", got, want)
		}
	}
	check("1. the first page: ", "1. the first page: 10 9 8 7 …")

	// 2. `more` adds one page. The rows already loaded are the same objects: a hold over a link gets only the new ones.
	loaded, _ := mustListValue(t, screen)
	exCall(t, screen, "more", nil)
	now, _ := mustListValue(t, screen)
	if got, want := fmt.Sprintf("2. after more:     %s | loaded rows kept: %v", shown(), now[0] == loaded[0]),
		"2. after more:     10 9 8 7 6 5 4 3 … | loaded rows kept: true"; got != want {
		t.Fatalf("%s\nwant %s", got, want)
	}

	// 3. A child in the second page gets a message and moves to the top: it leaves its page's range and enters the first
	// page's in the same commit: one frame, the list never without it and never with it twice.
	watch, err := screen.Member("list").Watch(background)
	must(t, err)
	frames := nexttest.ChangesOf(watch)
	exCall(t, children, "message", 5.0)
	lsSettled(host)
	// A child nobody loaded yet gets a message: it enters the first page too. A new child appears at the top.
	exCall(t, children, "message", 1.0)
	exCall(t, children, "spawn", jsonObject("session", "s1", "count", 1.0))
	lsSettled(host)
	_, err = watch.Stop()
	must(t, err)
	var got []string
	for _, call := range frames.Items() {
		if call.Kind != chord.WatchCallValue {
			got = append(got, "3. one frame:      "+fmt.Sprint(call.Kind))
			continue
		}
		got = append(got, "3. one frame:      "+text(call.Value))
	}
	want := []string{
		"3. one frame:      5 10 9 8 7 6 4 3 …",
		"3. one frame:      1 5 10 9 8 7 6 4 3 …",
		"3. one frame:      11 1 5 10 9 8 7 6 4 3 …",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("frames:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// 4. A reload of the list's plugin: the successor opens the pages the list had (its bookmarks are the object's).
	_, err = host.Reload([]HostFacet{storePlugin, {Name: "@example/list", Facet: ex71ListFacet()}}, background, ReloadOptions{})
	must(t, err)
	lsSettled(host)
	check("4. after a reload: ", "4. after a reload: 11 1 5 10 9 8 7 6 4 3 …")

	// 5. To the end: the last page is open while no row is behind it, so a child spawned later appears by itself.
	for {
		_, moreAfter := mustListValue(t, screen)
		if !moreAfter {
			break
		}
		exCall(t, screen, "more", nil)
	}
	check("5. everything:     ", "5. everything:     11 1 5 10 9 8 7 6 4 3 2")

	screen.Release()
	children.Release()
	must(t, host.Close(background))
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}
