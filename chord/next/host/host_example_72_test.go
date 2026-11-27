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

// Twin of packages/chord/test/examples/72-exists-and-joins.ts (Pi 7cc21c24f6b5): column sets, `exists` and the one-to-one
// join (slice 14 §5, §6). A plugin's contract file declares the columns of one of its tables other plugins may read, its
// table says so (`exposes`); another plugin's query tests `exists` in that set or joins one row of it by its key; a
// condition is handed over as data (`compile`, `whereNode`); a commit of either owner reaches the reads over its table and
// a keyed probe decides whether a read must run again.
//
// The script prints these lines and asserts nothing; here each printed value is a check, with the value the script prints
// on Pi's host (node 26, run from a git archive of 7cc21c24f6b5 in the tern-ci container):
//
//	1. anna (u12): club: s2 s1 | team page: s2 s1 | size: 2
//	   ben (u14):  club: s3 | team page: s3 | size: 1
//	2. a touch of s1 costs the six open reads: 2 runs, 2 probes, 4 skipped
//	   anna's team page: s1 s2
//	3. s3 joins u12: 6 runs, 3 probes, 0 skipped
//	   anna: club: s1 s3 s2 | team page: s1 s3 s2 | size: 3
//	4. refused: example.session-rows: where: example.notes is matched by (session), no leading prefix of its key (id)
//
// Pi's `await write()` ends after the commit step ran the readers; here `lsSettled` stands for it before a counter is read.

var (
	ex72SessionColumns = mustColumns("example.session-columns", store.ColumnSetOptions{
		Columns: []store.ColumnProp{
			store.Col("id", store.Str()), store.Col("app", store.Str()), store.Col("title", store.Str()),
			store.Col("creator", store.Str()), store.Col("lastTouch", store.Int()),
		},
		Key: []string{"id"},
	})
	ex72SessionItem = contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("title", contracts.Str()), contracts.Prop("lastTouch", contracts.Int()))
	ex72Sessions    = exLocal("example.sessions", contracts.Params{},
		// The condition arrives as data: built by server code, checked whole before anything is rendered.
		contracts.Mem("sessions", contracts.Queries(ex72SessionItem, []contracts.Property{
			contracts.Prop("visible", contracts.Obj(contracts.Prop("app", contracts.Str()), contracts.Prop("condition", contracts.Unknown()))),
		})),
		contracts.Mem("put", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("app", contracts.Str()),
			contracts.Prop("title", contracts.Str()), contracts.Prop("creator", contracts.Str())))),
		contracts.Mem("touch", contracts.NewMethod(contracts.Str())),
	)
	// Its server file: the table spreads the public columns in and says so. `cwd` stays private.
	ex72SessionRows = mustTableDef("example.session-rows", store.TableOptions{
		Version: 1,
		Columns: append(append([]store.ColumnProp{}, ex72SessionColumns.Schemas...), store.Col("cwd", store.Str(contracts.Default("/")))),
		Key:     []string{"id"},
		Indexes: store.IndexRecords{{Name: "by_app", Columns: []string{"app", "lastTouch"}}},
		Exposes: ex72SessionColumns,
	})

	// The club plugin: which session is in which team. Its side table is a column set too, with the index others match by.
	ex72TeamSessions = mustColumns("example.team-sessions", store.ColumnSetOptions{
		Columns: []store.ColumnProp{store.Col("team", store.Str()), store.Col("session", store.Str())},
		Key:     []string{"team", "session"},
		Indexes: store.IndexRecords{{Name: "ofSession", Columns: []string{"session"}}},
	})
	ex72TeamRows = mustTableDef("example.team-rows", store.TableOptions{
		Version: 1,
		Columns: append([]store.ColumnProp{}, ex72TeamSessions.Schemas...),
		Key:     []string{"team", "session"},
		Indexes: store.IndexRecords{{Name: "of_session", Columns: []string{"session"}}},
		Exposes: ex72TeamSessions,
	})
	ex72Club = exLocal("example.club", contracts.Params{},
		contracts.Mem("teams", contracts.Queries(ex72SessionItem, []contracts.Property{
			contracts.Prop("ofTeam", contracts.Obj(contracts.Prop("team", contracts.Str()), contracts.Prop("condition", contracts.Unknown()))),
		})),
		contracts.Mem("add", contracts.NewMethod(contracts.Obj(contracts.Prop("team", contracts.Str()), contracts.Prop("session", contracts.Str())))),
	)

	// A person's screens: the club's sessions they see (an `exists`), and their team's page (a join).
	ex72List    = contracts.Obj(contracts.Prop("rows", contracts.Arr(ex72SessionItem)), contracts.Prop("more", contracts.Bool()))
	ex72Screens = exLocal("example.screens", contracts.Params{contracts.Prop("person", contracts.Str()), contracts.Prop("team", contracts.Str())},
		contracts.Mem("club", ex72List), contracts.Mem("team", ex72List), contracts.Mem("size", contracts.Int()),
	)
)

// ex72Condition is Pi's `conditionOf(person, teams)`: what a person may see, as a condition over the sessions' columns:
// the family's sessions they created, and the club's sessions of their teams. Server code builds it; `Compile` makes it data.
func ex72Condition(person string, teams []any) delta.JsonValue {
	s := ex72SessionColumns.Refs
	node, err := store.Compile(ex72SessionColumns, store.Or(
		store.And(store.Eq(s["app"], "family"), store.Eq(s["creator"], person)),
		store.And(
			store.Eq(s["app"], "club"),
			store.Exists(ex72TeamSessions, store.Match{{Column: "session", Ref: s["id"]}}, func(t store.ColumnRefs) store.Condition {
				return store.InArray(t["team"], teams)
			}),
		),
	))
	if err != nil {
		panic(err)
	}
	return node.JSON()
}

func ex72SessionsFacet(clock *int64) Facet {
	return DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(ex72Sessions, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
			self := plain.(StoreSelf)
			rows := self.Table(ex72SessionRows)
			sessions := self.Queries(rows, QueriesOptions{
				Map: func(row store.Row, _ PartnerRow) (delta.JsonValue, error) {
					return jsonObject("id", row["id"], "title", row["title"], "lastTouch", float64(row["lastTouch"].(int64))), nil
				},
				Reads: []string{"id", "title", "lastTouch"},
			}, map[string]QueryBuilder{
				"visible": {Build: func(args delta.JsonValue) *store.Query {
					object := args.(*delta.JsonObject)
					return store.NewQuery(ex72SessionRows).
						Where(func(s, _ store.ColumnRefs) store.Condition { return store.Eq(s["app"], object.Value("app")) }).
						WhereNode(ex72SessionColumns, object.Value("condition")).
						OrderBy(func(s, _ store.ColumnRefs) *store.ColumnRef { return s["lastTouch"] }, "desc")
				}},
			})
			return contracts.MembersImplementation{
				Queries: map[string]map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]{"sessions": sessions},
				Methods: map[string]contracts.MethodFunction{
					"put": exMethod(func(row *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						_, err := self.Tx(func(tx documents.Tx) (any, error) {
							*clock++
							return nil, tx.Put(rows, store.Row{"id": row.Value("id"), "app": row.Value("app"), "title": row.Value("title"),
								"creator": row.Value("creator"), "lastTouch": *clock})
						}, ctx)
						return nil, err
					}),
					// A touch names what it changes: the readers that depend on none of it skip for free.
					"touch": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						_, err := self.Tx(func(tx documents.Tx) (any, error) {
							*clock++
							return nil, tx.Update(rows, store.Row{"id": id}, store.Row{"lastTouch": *clock})
						}, ctx)
						return nil, err
					},
				},
			}, nil
		}, ServeOptions{})
	})
}

var ex72ClubFacet = DefineFacet(func(host FacetHost, _ FacetSelf) error {
	return host.Serve(ex72Club, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		self := plain.(StoreSelf)
		rows := self.Table(ex72TeamRows)
		teams := self.Queries(rows, QueriesOptions{
			// The mapping gets the team row (no column of it: `reads: []`) and its session's projected columns.
			Map: func(_ store.Row, session PartnerRow) (delta.JsonValue, error) {
				return jsonObject("id", session["id"], "title", session["title"], "lastTouch", float64(session["lastTouch"].(int64))), nil
			},
			Reads:        []string{},
			PartnerReads: []string{"id", "title", "lastTouch"},
		}, map[string]QueryBuilder{
			// The team page: driven from the team's rows (key (team, session)), one session per row by its key, ordered
			// by the sessions' last touch. The person's condition applies on the joined side: a stale team row grants nothing.
			"ofTeam": {Build: func(args delta.JsonValue) *store.Query {
				object := args.(*delta.JsonObject)
				return store.NewQuery(ex72TeamRows).
					Where(func(t, _ store.ColumnRefs) store.Condition { return store.Eq(t["team"], object.Value("team")) }).
					Join(ex72SessionColumns, func(t store.ColumnRefs) store.Match { return store.Match{{Column: "id", Ref: t["session"]}} }).
					Where(func(_, session store.ColumnRefs) store.Condition { return store.Eq(session["app"], "club") }).
					WhereNode(ex72SessionColumns, object.Value("condition")).
					OrderBy(func(_, session store.ColumnRefs) *store.ColumnRef { return session["lastTouch"] }, "desc")
			}},
		})
		return contracts.MembersImplementation{
			Queries: map[string]map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]{"teams": teams},
			Methods: map[string]contracts.MethodFunction{
				"add": exMethod(func(row *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					_, err := self.Tx(func(tx documents.Tx) (any, error) {
						return nil, tx.Put(rows, store.Row{"team": row.Value("team"), "session": row.Value("session")})
					}, ctx)
					return nil, err
				}),
			},
		}, nil
	}, ServeOptions{})
})

var ex72ScreensFacet = DefineFacet(func(host FacetHost, _ FacetSelf) error {
	return host.Serve(ex72Screens, func(params *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
		person, team := params.Value("person").(string), params.Value("team").(string)
		condition := ex72Condition(person, []any{team})
		ofTeam := self.Get(ex72Club, jsonObject()).Queries("teams")["ofTeam"]
		return contracts.MembersImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"club": self.LiveQuery(self.Get(ex72Sessions, jsonObject()).Queries("sessions")["visible"], jsonObject("app", "club", "condition", condition), 10),
				"team": self.LiveQuery(ofTeam, jsonObject("team", team, "condition", condition), 10),
				// A count over what a key bounds: the team's rows.
				"size": self.Count(ofTeam, jsonObject("team", team, "condition", condition)),
			},
		}, nil
	}, ServeOptions{})
})

func TestExample72ExistsAndJoins(t *testing.T) {
	var clock int64
	meter := nexttest.Metered(t)
	ctx := meter.Context
	errs := &hostErrors{}
	host := newHost(t, HostOptions{Store: memoryStore(t), Telemetry: meter.Telemetry, OnError: errs.push})
	if _, err := host.Load([]HostFacet{
		{Name: "@example/sessions", Facet: ex72SessionsFacet(&clock)},
		{Name: "@example/club", Facet: ex72ClubFacet},
		{Name: "@example/screens", Facet: ex72ScreensFacet},
	}, ctx); err != nil {
		t.Fatal(err)
	}
	sessions := mustGetWith(t, host, ex72Sessions, ctx)
	club := mustGetWith(t, host, ex72Club, ctx)
	call := func(ref *Reference, method string, args delta.JsonValue) {
		t.Helper()
		if _, err := ref.Call(method, args, ctx); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	for _, entry := range [][3]string{{"s1", "club", "u12"}, {"s2", "club", "u12"}, {"s3", "club", "u14"}, {"s4", "family", ""}} {
		id, app, team := entry[0], entry[1], entry[2]
		call(sessions, "put", jsonObject("id", id, "app", app, "title", "session "+id, "creator", "anna"))
		if team != "" {
			call(club, "add", jsonObject("team", team, "session", id))
		}
	}
	expect := func(got, want string) {
		t.Helper()
		if got != want {
			t.Fatalf("%s\nwant %s", got, want)
		}
	}
	// 1. Two persons' screens over the same rows: each sees its team's sessions.
	anna, err := host.Get(ctx, ex72Screens, jsonObject("person", "anna", "team", "u12"), GetOptions{})
	must(t, err)
	ben, err := host.Get(ctx, ex72Screens, jsonObject("person", "ben", "team", "u14"), GetOptions{})
	must(t, err)
	ids := func(screen *Reference, member string) string {
		value, err := screen.Member(member).Value()
		must(t, err)
		parts := []string{}
		for _, row := range value.(*delta.JsonObject).Value("rows").([]any) {
			parts = append(parts, row.(*delta.JsonObject).Value("id").(string))
		}
		return strings.Join(parts, " ")
	}
	size := func(screen *Reference) delta.JsonValue {
		value, err := screen.Member("size").Value()
		must(t, err)
		return value
	}
	expect(fmt.Sprintf("1. anna (u12): club: %s | team page: %s | size: %v", ids(anna, "club"), ids(anna, "team"), size(anna)),
		"1. anna (u12): club: s2 s1 | team page: s2 s1 | size: 2")
	expect(fmt.Sprintf("   ben (u14):  club: %s | team page: %s | size: %v", ids(ben, "club"), ids(ben, "team"), size(ben)),
		"   ben (u14):  club: s3 | team page: s3 | size: 1")

	// 2. A touch of s1 (anna's team). Six reads are open. Anna's two lists show s1 and run; ben's ask one keyed probe each
	// ("does s1 pass this read's condition, in its range?") and skip; the counts depend on no touched column.
	cost := func(what string, write func(), want string) {
		t.Helper()
		names := []string{"chord.query.runs", "chord.query.probes", "chord.query.skips"}
		before := meter.Counts()
		write()
		lsSettled(host)
		after := meter.Counts()
		expect(fmt.Sprintf("%s: %v runs, %v probes, %v skipped", what,
			after[names[0]]-before[names[0]], after[names[1]]-before[names[1]], after[names[2]]-before[names[2]]), want)
	}
	cost("2. a touch of s1 costs the six open reads", func() { call(sessions, "touch", "s1") },
		"2. a touch of s1 costs the six open reads: 2 runs, 2 probes, 4 skipped")
	expect("   anna's team page: "+ids(anna, "team"), "   anna's team page: s1 s2")

	// 3. The club adds s3 to u12: a commit of the other owner. Every read runs: s3 is among the rows ben's lists show (a team
	// row of it may change what their condition says), anna's lists gain it, and a count keeps no rows.
	cost("3. s3 joins u12", func() { call(club, "add", jsonObject("team", "u12", "session", "s3")) },
		"3. s3 joins u12: 6 runs, 3 probes, 0 skipped")
	expect(fmt.Sprintf("   anna: club: %s | team page: %s | size: %v", ids(anna, "club"), ids(anna, "team"), size(anna)),
		"   anna: club: s1 s3 s2 | team page: s1 s3 s2 | size: 3")

	// 4. The bounding rule: tables are matched by a set's key, a leading prefix of it or a declared index (`session` alone is
	// the declared index `ofSession`; `team` alone a prefix of the key), and by nothing else.
	matched := func(base *store.TableDefinition, set *store.ColumnSet, column string, ref func(store.ColumnRefs) *store.ColumnRef) error {
		return store.NewQuery(base).Where(func(row, _ store.ColumnRefs) store.Condition {
			return store.Exists(set, store.Match{{Column: column, Ref: ref(row)}}, nil)
		}).Err()
	}
	must(t, matched(ex72SessionRows, ex72TeamSessions, "session", func(s store.ColumnRefs) *store.ColumnRef { return s["id"] }))
	must(t, matched(ex72TeamRows, ex72TeamSessions, "team", func(s store.ColumnRefs) *store.ColumnRef { return s["team"] }))
	notes := mustColumns("example.notes", store.ColumnSetOptions{
		Columns: []store.ColumnProp{store.Col("id", store.Str()), store.Col("session", store.Str())}, Key: []string{"id"},
	})
	refused := matched(ex72SessionRows, notes, "session", func(s store.ColumnRefs) *store.ColumnRef { return s["id"] })
	if refused == nil {
		t.Fatalf("4. no refusal")
	}
	expect("4. refused: "+refused.Error(), "4. refused: example.session-rows: where: example.notes is matched by (session), no leading prefix of its key (id)")

	anna.Release()
	ben.Release()
	sessions.Release()
	club.Release()
	must(t, host.Close(ctx))
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}
