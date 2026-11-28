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

// Ports packages/chord/test/bench/slice-14.ts (Pi 7cc21c24f6b5, 723 lines; added by 92e1db6ca): benchmark and acceptance of
// slice 14 (docs/slice-14.md §9): the ordinary screens of an app with persons, at the spec's data (10,000 sessions: a club
// of 3,000 in ten teams of 300 and one of 20, a family of 3,000, 4,000 archived elsewhere) and its mix of open reads (10
// family members, 10 club members in two teams each, a guest with 2 shares, a member of the team of 20, 20 workers).
//
// Pi's script prints `ok` / `MISS` lines (medians of 400 runs, the first 100 dropped) against budgets of its own and exits 1
// on a miss. The Go form is one sub-benchmark per measured scenario (Go's own ns/op over b.N runs; this port asserts no time
// budget: the budgets are Pi's, on Pi's machine and runtime), `runs/op`, `probes/op`, `skips/op` metrics from the host's
// counters where Pi prints them, and one Test, TestBenchSlice14Exact, for the numbers Pi checks with `expectExactly` and
// the page bound, which do not depend on the machine.
//
// Scenarios (Pi's line of each):
//
//	BenchmarkSlice14PerRun/...        :494-524 `timed` (nine reads of a page of 50 rows through `page`, 400 runs each)
//	BenchmarkSlice14Touch/...         :557-599 (a touch of club-3 at the mix and with no read open; the probe of old-7; the
//	                                  archive of club-13, reported)
//	BenchmarkSlice14TenTimesMix       :602-608 (the same touch at ten times the mix, reported without a budget)
//	BenchmarkSlice14Subagents/...     :610-722 (a session's 5,000 subagents: the first page, 20 pages loaded, a message)
//
// Run by hand (not in CI): go test -run '^$' -bench BenchmarkSlice14 -benchmem ./chord/next/host/
// Differences from the script: Pi's medians are Go's mean over b.N; the store is the same in-memory SQLite the host tests
// use; a Go sub-benchmark's setup is the script's `mixed(scale)` run outside the timer.

const (
	b14Page  = 50
	b14Since = 9_670
)

var (
	b14Teams  = []string{"u0", "u1", "u2", "u3", "u4", "u5", "u6", "u7", "u8", "u9"}
	b14Active = []any{"working", "waiting", "failed"}
)

var (
	b14SessionColumns = mustColumns("bench.session-columns", store.ColumnSetOptions{
		Columns: []store.ColumnProp{
			store.Col("id", store.Str()), store.Col("app", store.Str()), store.Col("title", store.Str()), store.Col("creator", store.Str()),
			store.Col("private", store.Bool()), store.Col("state", store.Str()), store.Col("lastTouch", store.Int()),
			store.Col("archivedAt", store.Opt(store.Int())),
		},
		Key: []string{"id"},
	})
	b14SessionRows = mustTableDef("session-rows", store.TableOptions{
		Version: 1,
		Columns: append(append([]store.ColumnProp{}, b14SessionColumns.Schemas...), store.Col("cwd", store.Str(contracts.Default("/")))),
		Key:     []string{"id"},
		Indexes: store.IndexRecords{
			{Name: "by_app", Columns: []string{"app", "lastTouch"}},
			{Name: "by_creator", Columns: []string{"app", "creator", "lastTouch"}},
			{Name: "by_state", Columns: []string{"state", "lastTouch"}},
			{Name: "by_touch", Columns: []string{"lastTouch"}},
		},
		Exposes: b14SessionColumns,
	})
	b14TeamSessions = mustColumns("bench.team-sessions", store.ColumnSetOptions{
		Columns: []store.ColumnProp{store.Col("team", store.Str()), store.Col("session", store.Str())},
		Key:     []string{"team", "session"},
		Indexes: store.IndexRecords{{Name: "ofSession", Columns: []string{"session"}}},
	})
	b14TeamRows = mustTableDef("team-rows", store.TableOptions{
		Version: 1,
		Columns: append([]store.ColumnProp{}, b14TeamSessions.Schemas...),
		Key:     []string{"team", "session"},
		Indexes: store.IndexRecords{{Name: "of_session", Columns: []string{"session"}}},
		Exposes: b14TeamSessions,
	})
	b14ShareColumns = mustColumns("bench.shares", store.ColumnSetOptions{
		Columns: []store.ColumnProp{store.Col("person", store.Str()), store.Col("session", store.Str())},
		Key:     []string{"person", "session"},
		Indexes: store.IndexRecords{{Name: "ofSession", Columns: []string{"session"}}},
	})
	b14ShareRows = mustTableDef("share-rows", store.TableOptions{
		Version: 1,
		Columns: append([]store.ColumnProp{}, b14ShareColumns.Schemas...),
		Key:     []string{"person", "session"},
		Indexes: store.IndexRecords{{Name: "of_session", Columns: []string{"session"}}},
		Exposes: b14ShareColumns,
	})
	b14MarkRows = mustTableDef("mark-rows", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{store.Col("person", store.Str()), store.Col("session", store.Str()), store.Col("at", store.Int())},
		Key:     []string{"person", "session"},
	})
	b14NoteRows = mustTableDef("note-rows", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{
			store.Col("id", store.Int()), store.Col("person", store.Str()), store.Col("session", store.Opt(store.Str())), store.Col("at", store.Int()),
		},
		Key:     []string{"id"},
		Indexes: store.IndexRecords{{Name: "of_person", Columns: []string{"person", "at"}}},
	})
)

var (
	b14Item = contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("title", contracts.Str()), contracts.Prop("state", contracts.Str()),
		contracts.Prop("lastTouch", contracts.Int()))
	b14Session = contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("app", contracts.Str()), contracts.Prop("creator", contracts.Str()),
		contracts.Prop("state", contracts.Str()), contracts.Prop("lastTouch", contracts.Int()), contracts.Prop("archived", contracts.Bool()))
	b14Condition = contracts.Prop("condition", contracts.Unknown())
	b14Strings   = contracts.Arr(contracts.Str())

	// One store for the bench: the tables of several plugins of an app, with the named queries of §8.
	b14Store = exLocal("bench.store", contracts.Params{},
		contracts.Mem("sessions", contracts.Queries(b14Item, []contracts.Property{
			contracts.Prop("visible", contracts.Obj(contracts.Prop("app", contracts.Str()), b14Condition)),
			contracts.Prop("one", contracts.Obj(contracts.Prop("id", contracts.Str()), b14Condition)),
			contracts.Prop("createdBy", contracts.Obj(contracts.Prop("app", contracts.Str()), contracts.Prop("person", contracts.Str()), b14Condition)),
			contracts.Prop("inStates", contracts.Obj(contracts.Prop("states", b14Strings), b14Condition)),
			contracts.Prop("since", contracts.Obj(contracts.Prop("since", contracts.Int()), b14Condition)),
		})),
		contracts.Mem("teams", contracts.Queries(b14Item, []contracts.Property{
			contracts.Prop("ofTeams", contracts.Obj(contracts.Prop("teams", b14Strings), b14Condition)),
		})),
		contracts.Mem("shares", contracts.Queries(b14Item, []contracts.Property{
			contracts.Prop("sessionsOf", contracts.Obj(contracts.Prop("person", contracts.Str()), contracts.Prop("app", contracts.Str()), b14Condition)),
		})),
		contracts.Mem("marks", contracts.Queries(b14Item, []contracts.Property{
			contracts.Prop("ofPerson", contracts.Obj(contracts.Prop("person", contracts.Str()), b14Condition)),
		})),
		contracts.Mem("notes", contracts.Queries(contracts.Obj(contracts.Prop("id", contracts.Int())), []contracts.Property{
			contracts.Prop("forPerson", contracts.Obj(contracts.Prop("person", contracts.Str()), b14Condition)),
		})),
		contracts.Mem("seed", contracts.NewMethod(contracts.Obj(
			contracts.Prop("sessions", contracts.Arr(b14Session)),
			contracts.Prop("teams", contracts.Arr(contracts.Obj(contracts.Prop("team", contracts.Str()), contracts.Prop("session", contracts.Str())))),
			contracts.Prop("shares", contracts.Arr(contracts.Obj(contracts.Prop("person", contracts.Str()), contracts.Prop("session", contracts.Str())))),
		))),
		contracts.Mem("touch", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("at", contracts.Int()), contracts.Prop("state", contracts.Str())))),
		contracts.Mem("archive", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("at", contracts.Int())))),
	)

	b14Listed = contracts.Obj(contracts.Prop("rows", contracts.Arr(b14Item)), contracts.Prop("moreAfter", contracts.Bool()))
	// One open read of a person: `kind` names the screen's read, `arg` its team or session.
	b14Screen = exLocal("bench.screen", contracts.Params{
		contracts.Prop("kind", contracts.Str()), contracts.Prop("person", contracts.Str()), contracts.Prop("app", contracts.Str()),
		contracts.Prop("teams", contracts.Str()), contracts.Prop("arg", contracts.Str()),
	},
		contracts.Mem("list", b14Listed),
		contracts.Mem("one", contracts.Obj(contracts.Prop("rows", contracts.Arr(b14Item)), contracts.Prop("more", contracts.Bool()))),
		contracts.Mem("count", contracts.Int()),
		contracts.Mem("more", contracts.NewMethod()),
	)
)

// b14ItemOf is Pi's `item(row)` (and, from a partner row, `partner(_row, session)`).
func b14ItemOf(id, title, state any, lastTouch int64) delta.JsonValue {
	return jsonObject("id", id, "title", title, "state", state, "lastTouch", float64(lastTouch))
}

func b14Facet() Facet {
	return DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(b14Store, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
			self := plain.(StoreSelf)
			sessions, teams, shares, marks, notes := self.Table(b14SessionRows), self.Table(b14TeamRows), self.Table(b14ShareRows),
				self.Table(b14MarkRows), self.Table(b14NoteRows)
			reads := []string{"id", "title", "state", "lastTouch"}
			joined := QueriesOptions{
				Map: func(_ store.Row, session PartnerRow) (delta.JsonValue, error) {
					return b14ItemOf(session["id"], session["title"], session["state"], session["lastTouch"].(int64)), nil
				},
				Reads: []string{}, PartnerReads: reads,
			}
			sessionsQueries := self.Queries(sessions, QueriesOptions{
				Map: func(row store.Row, _ PartnerRow) (delta.JsonValue, error) {
					return b14ItemOf(row["id"], row["title"], row["state"], row["lastTouch"].(int64)), nil
				},
				Reads: reads,
			}, map[string]QueryBuilder{
				// by_app (app, lastTouch)
				"visible": {Build: func(args delta.JsonValue) *store.Query {
					a := args.(*delta.JsonObject)
					return store.NewQuery(b14SessionRows).
						Where(func(s, _ store.ColumnRefs) store.Condition { return store.Eq(s["app"], a.Value("app")) }).
						WhereNode(b14SessionColumns, a.Value("condition")).
						OrderBy(func(s, _ store.ColumnRefs) *store.ColumnRef { return s["lastTouch"] }, "desc")
				}},
				"one": {Build: func(args delta.JsonValue) *store.Query {
					a := args.(*delta.JsonObject)
					return store.NewQuery(b14SessionRows).
						Where(func(s, _ store.ColumnRefs) store.Condition { return store.Eq(s["id"], a.Value("id")) }).
						WhereNode(b14SessionColumns, a.Value("condition"))
				}},
				// by_creator (app, creator, lastTouch)
				"createdBy": {Build: func(args delta.JsonValue) *store.Query {
					a := args.(*delta.JsonObject)
					return store.NewQuery(b14SessionRows).
						Where(func(s, _ store.ColumnRefs) store.Condition {
							return store.And(store.Eq(s["app"], a.Value("app")), store.Eq(s["creator"], a.Value("person")))
						}).
						WhereNode(b14SessionColumns, a.Value("condition")).
						OrderBy(func(s, _ store.ColumnRefs) *store.ColumnRef { return s["lastTouch"] }, "desc")
				}},
				// by_state (state, lastTouch): ordered by state first, or SQLite walks every session by last touch.
				"inStates": {Build: func(args delta.JsonValue) *store.Query {
					a := args.(*delta.JsonObject)
					return store.NewQuery(b14SessionRows).
						Where(func(s, _ store.ColumnRefs) store.Condition { return store.InArray(s["state"], a.Value("states")) }).
						WhereNode(b14SessionColumns, a.Value("condition")).
						OrderBy(func(s, _ store.ColumnRefs) *store.ColumnRef { return s["state"] }, "desc").
						OrderBy(func(s, _ store.ColumnRefs) *store.ColumnRef { return s["lastTouch"] }, "desc")
				}},
				// by_touch (lastTouch)
				"since": {Build: func(args delta.JsonValue) *store.Query {
					a := args.(*delta.JsonObject)
					return store.NewQuery(b14SessionRows).
						Where(func(s, _ store.ColumnRefs) store.Condition {
							return store.Gte(s["lastTouch"], int64(a.Value("since").(float64)))
						}).
						WhereNode(b14SessionColumns, a.Value("condition")).
						OrderBy(func(s, _ store.ColumnRefs) *store.ColumnRef { return s["lastTouch"] }, "desc")
				}},
			})
			teamsQueries := self.Queries(teams, joined, map[string]QueryBuilder{
				// The key (team, session): driven from the teams' rows, ordered by the sessions' last touch.
				"ofTeams": {Build: func(args delta.JsonValue) *store.Query {
					a := args.(*delta.JsonObject)
					return store.NewQuery(b14TeamRows).
						Where(func(t, _ store.ColumnRefs) store.Condition { return store.InArray(t["team"], a.Value("teams")) }).
						Join(b14SessionColumns, func(t store.ColumnRefs) store.Match { return store.Match{{Column: "id", Ref: t["session"]}} }).
						Where(func(_, session store.ColumnRefs) store.Condition {
							return store.And(store.Eq(session["app"], "club"), store.IsNull(session["archivedAt"]))
						}).
						WhereNode(b14SessionColumns, a.Value("condition")).
						OrderBy(func(_, session store.ColumnRefs) *store.ColumnRef { return session["lastTouch"] }, "desc")
				}},
			})
			sharesQueries := self.Queries(shares, joined, map[string]QueryBuilder{
				"sessionsOf": {Build: func(args delta.JsonValue) *store.Query {
					a := args.(*delta.JsonObject)
					return store.NewQuery(b14ShareRows).
						Where(func(x, _ store.ColumnRefs) store.Condition { return store.Eq(x["person"], a.Value("person")) }).
						Join(b14SessionColumns, func(x store.ColumnRefs) store.Match { return store.Match{{Column: "id", Ref: x["session"]}} }).
						Where(func(_, session store.ColumnRefs) store.Condition { return store.Eq(session["app"], a.Value("app")) }).
						WhereNode(b14SessionColumns, a.Value("condition")).
						OrderBy(func(_, session store.ColumnRefs) *store.ColumnRef { return session["lastTouch"] }, "desc")
				}},
			})
			marksQueries := self.Queries(marks, joined, map[string]QueryBuilder{
				// The person's own rows drive, ordered by their own column.
				"ofPerson": {Build: func(args delta.JsonValue) *store.Query {
					a := args.(*delta.JsonObject)
					return store.NewQuery(b14MarkRows).
						Where(func(m, _ store.ColumnRefs) store.Condition { return store.Eq(m["person"], a.Value("person")) }).
						Join(b14SessionColumns, func(m store.ColumnRefs) store.Match { return store.Match{{Column: "id", Ref: m["session"]}} }).
						WhereNode(b14SessionColumns, a.Value("condition")).
						OrderBy(func(m, _ store.ColumnRefs) *store.ColumnRef { return m["at"] }, "desc")
				}},
			})
			notesQueries := self.Queries(notes, QueriesOptions{
				Map: func(row store.Row, _ PartnerRow) (delta.JsonValue, error) {
					return jsonObject("id", float64(row["id"].(int64))), nil
				},
				Reads: []string{"id"},
			}, map[string]QueryBuilder{
				"forPerson": {Build: func(args delta.JsonValue) *store.Query {
					a := args.(*delta.JsonObject)
					return store.NewQuery(b14NoteRows).
						Where(func(n, _ store.ColumnRefs) store.Condition {
							return store.And(
								store.Eq(n["person"], a.Value("person")),
								store.Or(store.IsNull(n["session"]),
									store.ExistsNodeOf(b14SessionColumns, store.Match{{Column: "id", Ref: n["session"]}}, a.Value("condition"))),
							)
						}).
						OrderBy(func(n, _ store.ColumnRefs) *store.ColumnRef { return n["at"] }, "desc")
				}},
			})
			tx := func(ctx context.Context, write func(tx documents.Tx) error) (delta.JsonValue, error) {
				_, err := self.Tx(func(tx documents.Tx) (any, error) { return nil, write(tx) }, ctx)
				return nil, err
			}
			return contracts.MembersImplementation{
				Queries: map[string]map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]{
					"sessions": sessionsQueries, "teams": teamsQueries, "shares": sharesQueries, "marks": marksQueries, "notes": notesQueries,
				},
				Methods: map[string]contracts.MethodFunction{
					"seed": exMethod(func(rows *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						return tx(ctx, func(tx documents.Tx) error {
							for _, each := range rows.Value("sessions").([]any) {
								row := each.(*delta.JsonObject)
								id := row.Value("id").(string)
								put := store.Row{
									"id": id, "app": row.Value("app"), "creator": row.Value("creator"), "state": row.Value("state"),
									"lastTouch": int64(row.Value("lastTouch").(float64)), "title": "session " + id, "private": false,
								}
								if row.Value("archived").(bool) {
									put["archivedAt"] = int64(1)
								}
								if err := tx.Put(sessions, put); err != nil {
									return err
								}
							}
							for _, each := range rows.Value("teams").([]any) {
								row := each.(*delta.JsonObject)
								if err := tx.Put(teams, store.Row{"team": row.Value("team"), "session": row.Value("session")}); err != nil {
									return err
								}
							}
							for _, each := range rows.Value("shares").([]any) {
								row := each.(*delta.JsonObject)
								if err := tx.Put(shares, store.Row{"person": row.Value("person"), "session": row.Value("session")}); err != nil {
									return err
								}
							}
							return nil
						})
					}),
					// A touch names what it changes: an `update`, not a `put` (§3.3).
					"touch": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						return tx(ctx, func(tx documents.Tx) error {
							return tx.Update(sessions, store.Row{"id": args.Value("id")},
								store.Row{"lastTouch": int64(args.Value("at").(float64)), "state": args.Value("state")})
						})
					}),
					"archive": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						return tx(ctx, func(tx documents.Tx) error {
							return tx.Update(sessions, store.Row{"id": args.Value("id")}, store.Row{"archivedAt": int64(args.Value("at").(float64))})
						})
					}),
				},
			}, nil
		}, ServeOptions{})
	})
}

// b14Access is Pi's `access(person, app, teams)` (:282): what a person may see, and their rule in one app alone (what a
// list's dense source walks): both compiled to data.
func b14Access(person, app string, teams []string) (rule, condition delta.JsonValue) {
	s := b14SessionColumns.Refs
	teamValues := make([]any, len(teams))
	for i, team := range teams {
		teamValues[i] = team
	}
	family := store.Or(store.Eq(s["private"], false), store.Eq(s["creator"], person))
	club := store.Or(
		store.Eq(s["creator"], person),
		store.Exists(b14TeamSessions, store.Match{{Column: "session", Ref: s["id"]}}, func(t store.ColumnRefs) store.Condition {
			return store.InArray(t["team"], teamValues)
		}),
	)
	shared := store.And(
		store.InArray(s["app"], []any{"family"}),
		store.Exists(b14ShareColumns, store.Match{{Column: "session", Ref: s["id"]}}, func(x store.ColumnRefs) store.Condition {
			return store.Eq(x["person"], person)
		}),
	)
	chosen := store.Or()
	switch app {
	case "family":
		chosen = family
	case "club":
		chosen = club
	}
	own := []store.Condition{}
	if app != "none" {
		own = append(own, store.And(store.Eq(s["app"], app), chosen))
	}
	ruleNode, err := store.Compile(b14SessionColumns, chosen)
	if err != nil {
		panic(err)
	}
	conditionNode, err := store.Compile(b14SessionColumns, store.Or(append(own, shared)...))
	if err != nil {
		panic(err)
	}
	return ruleNode.JSON(), conditionNode.JSON()
}

func b14ScreenFacet() Facet {
	return DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(b14Screen, func(params *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
			kind, person, app := params.Value("kind").(string), params.Value("person").(string), params.Value("app").(string)
			arg := params.Value("arg").(string)
			teams := []string{}
			if joined := params.Value("teams").(string); joined != "" {
				teams = strings.Split(joined, ",")
			}
			teamValues := make([]any, len(teams))
			for i, team := range teams {
				teamValues[i] = team
			}
			bench := self.Get(b14Store, jsonObject())
			rule, condition := b14Access(person, app, teams)
			none := exConstant(0.0)
			nothing := exConstant(jsonObject("rows", []any{}, "more", false))
			listed := func(query contracts.NamedQuery[delta.JsonValue, delta.JsonValue], args delta.JsonValue, count chord.Watchable[delta.JsonValue]) contracts.MembersImplementation {
				list := self.List("rows", []ListSource{{Query: query, Args: args}}, ListOptions{First: b14Page})
				return contracts.MembersImplementation{
					State:   map[string]chord.Watchable[delta.JsonValue]{"list": list.Value(), "one": nothing, "count": count},
					Methods: map[string]contracts.MethodFunction{"more": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return nil, list.More(ctx) }},
				}
			}
			switch kind {
			case "team":
				args := jsonObject("teams", []any{arg}, "condition", condition)
				ofTeams := bench.Queries("teams")["ofTeams"]
				return listed(ofTeams, args, self.Count(ofTeams, args)), nil
			case "club-teams":
				return listed(bench.Queries("teams")["ofTeams"], jsonObject("teams", teamValues, "condition", condition), none), nil
			case "created":
				return listed(bench.Queries("sessions")["createdBy"], jsonObject("app", app, "person", person, "condition", condition), none), nil
			case "rule":
				return listed(bench.Queries("sessions")["visible"], jsonObject("app", arg, "condition", rule), none), nil
			case "shares":
				return listed(bench.Queries("shares")["sessionsOf"], jsonObject("person", person, "app", arg, "condition", condition), none), nil
			case "active":
				return listed(bench.Queries("sessions")["inStates"], jsonObject("states", b14Active, "condition", condition), none), nil
			case "recent":
				return listed(bench.Queries("sessions")["since"], jsonObject("since", float64(b14Since), "condition", condition), none), nil
			case "marks":
				return listed(bench.Queries("marks")["ofPerson"], jsonObject("person", person, "condition", condition), none), nil
			default:
				// A worker following its session (`principal.kind === "worker"`: nothing is hidden from it).
				all, err := store.Compile(b14SessionColumns, store.And())
				if err != nil {
					return contracts.MembersImplementation{}, err
				}
				one := self.LiveQuery(bench.Queries("sessions")["one"], jsonObject("id", arg, "condition", all.JSON()), 1)
				return contracts.MembersImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{
						"list": exConstant(jsonObject("rows", []any{}, "moreAfter", false)), "one": one, "count": none,
					},
					Methods: map[string]contracts.MethodFunction{"more": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
				}, nil
			}
		}, ServeOptions{})
	})
}

// b14Data is Pi's `data()` (:351): the spec's data, 10,000 session rows, the teams' rows, the guest's two shares.
func b14Data() (sessions, teams, shares []any) {
	n := 0
	row := func(id, app string, archived bool) {
		state := "idle"
		if !archived && n%30 == 0 {
			state = b14Active[n%3].(string)
		}
		sessions = append(sessions, jsonObject("id", id, "app", app, "creator", fmt.Sprintf("p%d", n%40), "state", state,
			"lastTouch", float64(1+(n*7919)%9_999), "archived", archived))
		n++
	}
	for index := range 3_000 {
		id := fmt.Sprintf("club-%d", index)
		row(id, "club", false)
		teams = append(teams, jsonObject("team", b14Teams[index%10], "session", id))
	}
	for index := range 20 {
		id := fmt.Sprintf("tiny-%d", index)
		row(id, "club", false)
		teams = append(teams, jsonObject("team", "tiny", "session", id))
	}
	for index := range 3_000 {
		row(fmt.Sprintf("family-%d", index), "family", false)
	}
	for index := range 3_980 {
		row(fmt.Sprintf("old-%d", index), "other", true)
	}
	shares = []any{jsonObject("person", "guest", "session", "family-5"), jsonObject("person", "guest", "session", "family-900")}
	return sessions, teams, shares
}

type b14Person struct {
	person, app string
	teams       []string
}

// b14Persons is Pi's `persons(scale)` (:386): the persons of the mix, `scale` times.
func b14Persons(scale int) []b14Person {
	var all []b14Person
	for copy := range scale {
		for index := range 10 {
			all = append(all,
				b14Person{fmt.Sprintf("f%d-%d", copy, index), "family", nil},
				b14Person{fmt.Sprintf("c%d-%d", copy, index), "club", []string{b14Teams[index], b14Teams[(index+1)%10]}})
		}
		guest := "guest"
		if copy != 0 {
			guest = fmt.Sprintf("guest%d", copy)
		}
		all = append(all, b14Person{guest, "none", nil}, b14Person{fmt.Sprintf("t%d", copy), "club", []string{"tiny"}})
	}
	return all
}

// b14Screens is Pi's `screens(person, workers)` (:400): the reads each person has open, as `Screen` params.
func b14Screens(p b14Person, workers []string) []*delta.JsonObject {
	params := func(kind, arg string) *delta.JsonObject {
		return jsonObject("kind", kind, "person", p.person, "app", p.app, "teams", strings.Join(p.teams, ","), "arg", arg)
	}
	open := []*delta.JsonObject{params("active", ""), params("recent", ""), params("marks", "pinned"), params("marks", "unread")}
	if p.app == "club" {
		open = append(open, params("club-teams", ""), params("created", ""))
		for _, team := range p.teams {
			open = append(open, params("team", team))
		}
	} else {
		open = append(open, params("rule", "family"), params("shares", "family"))
	}
	for _, session := range workers {
		open = append(open, params("one", session))
	}
	return open
}

// b14Mix is Pi's `mixed(scale)` result (:430): a host over a seeded store with the mix `scale` times open, and its counters.
type b14Mix struct {
	tb      testing.TB
	host    Host
	app     *Reference
	ctx     context.Context
	meter   *nexttest.Meter
	errs    *hostErrors
	reads   int
	held    []*Reference
	closed  bool
	queries struct{ sessions, teams, shares map[string]*ReferenceQuery }
}

func (m *b14Mix) call(method string, args *delta.JsonObject) {
	m.tb.Helper()
	if _, err := m.app.Call(method, args, m.ctx); err != nil {
		m.tb.Fatalf("%s: %v", method, err)
	}
}

func (m *b14Mix) touch(id string, at int, state string) {
	m.tb.Helper()
	m.call("touch", jsonObject("id", id, "at", float64(at), "state", state))
}

func (m *b14Mix) close() {
	if m.closed {
		return
	}
	m.closed = true
	for _, each := range m.held {
		each.Release()
	}
	m.app.Release()
	must(m.tb, m.host.Close(m.ctx))
	if m.errs.Len() > 0 {
		m.tb.Errorf("background errors: %v", m.errs.All())
	}
}

func b14Mixed(tb testing.TB, scale int) *b14Mix {
	tb.Helper()
	meter := nexttest.Metered(tb)
	errs := &hostErrors{}
	host := newHost(tb, HostOptions{Store: memoryStore(tb), Telemetry: meter.Telemetry, OnError: errs.push})
	if _, err := host.Load([]HostFacet{{Name: "@bench/store", Facet: b14Facet()}, {Name: "@bench/screens", Facet: b14ScreenFacet()}}, meter.Context); err != nil {
		tb.Fatal(err)
	}
	app, err := host.Get(meter.Context, b14Store, jsonObject(), GetOptions{})
	must(tb, err)
	mix := &b14Mix{tb: tb, host: host, app: app, ctx: meter.Context, meter: meter, errs: errs}
	sessions, teams, shares := b14Data()
	for from := 0; from < len(sessions); from += 1_000 {
		chunk := jsonObject("sessions", sessions[from:min(from+1_000, len(sessions))], "teams", []any{}, "shares", []any{})
		if from == 0 {
			chunk = jsonObject("sessions", sessions[:1_000], "teams", teams, "shares", shares)
		}
		mix.call("seed", chunk)
	}
	// The touched session, recent and working: on the first page of its team (u3), of its members' lists and sidebars.
	mix.touch("club-3", 20_000, "working")
	everyone := b14Persons(scale)
	// 20 workers per mix, each following a session: the touched one and others.
	workers := make([]string, 20*scale)
	for index := range workers {
		workers[index] = fmt.Sprintf("family-%d", index)
	}
	if len(workers) > 0 {
		workers[0] = "club-3"
	}
	for index, person := range everyone {
		var own []string
		for worker, session := range workers {
			if worker%len(everyone) == index {
				own = append(own, session)
			}
		}
		for _, params := range b14Screens(person, own) {
			held, err := host.Get(meter.Context, b14Screen, params, GetOptions{})
			must(tb, err)
			mix.held = append(mix.held, held)
		}
	}
	// Every screen is one read; a team page also counts its team.
	mix.reads = len(mix.held)
	for _, person := range everyone {
		if person.app == "club" {
			mix.reads += len(person.teams)
		}
	}
	mix.queries.sessions = app.Queries("sessions")
	mix.queries.teams = app.Queries("teams")
	mix.queries.shares = app.Queries("shares")
	return mix
}

// b14Counts is the counters `chord.query.runs`, `chord.query.probes` and `chord.query.skips` now.
func (m *b14Mix) counts() [3]float64 {
	all := m.meter.Counts()
	return [3]float64{all["chord.query.runs"], all["chord.query.probes"], all["chord.query.skips"]}
}

// b14Touched is Pi's `touched(mix, times)` (:539): touches club-3 `times` times and returns the counters of the last touch.
func b14Touched(m *b14Mix, times int) (runs, probes, skips float64) {
	for run := range times {
		before := m.counts()
		state := "working"
		if run%2 == 0 {
			state = "waiting"
		}
		m.touch("club-3", 30_000+run, state)
		after := m.counts()
		runs, probes, skips = after[0]-before[0], after[1]-before[1], after[2]-before[2]
	}
	return runs, probes, skips
}

// BenchmarkSlice14PerRun is Pi's first block (:475-524): "per run (one read of a page of 50 rows through `page`, Chord's decoding
// and mapping included)" over the data with no read open (`mixed(0)`). Pi's budgets: 2 ms each, 0.2 ms for the two sparse reads.
func BenchmarkSlice14PerRun(b *testing.B) {
	mix := b14Mixed(b, 0)
	b.Cleanup(mix.close)
	_, member := b14Access("c0-3", "club", []string{"u3", "u4"})
	family, _ := b14Access("f0-1", "family", nil)
	_, guest := b14Access("guest", "none", nil)
	_, tiny := b14Access("t0", "club", []string{"tiny"})
	_, created := b14Access("p7", "club", []string{"u3"})
	read := func(name string, query *ReferenceQuery, args delta.JsonValue) {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := query.Page(args, b14Page, mix.ctx, contracts.QueryPageOptions{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	// :494
	read("family-members-list-rule-source-dense", mix.queries.sessions["visible"], jsonObject("app", "family", "condition", family))
	// :497
	read("team-page-T300-ordered-by-last-touch", mix.queries.teams["ofTeams"], jsonObject("teams", []any{"u3"}, "condition", member))
	// :500
	read("club-members-list-teams-source-two-teams-600-rows", mix.queries.teams["ofTeams"], jsonObject("teams", []any{"u3", "u4"}, "condition", member))
	// :503
	read("club-members-list-source-of-what-they-created", mix.queries.sessions["createdBy"], jsonObject("app", "club", "person", "p7", "condition", created))
	// :510
	read("sparse-guest-with-2-shares-among-3000", mix.queries.shares["sessionsOf"], jsonObject("person", "guest", "app", "family", "condition", guest))
	// :513
	read("sparse-member-of-one-team-of-20-in-club-of-3000", mix.queries.teams["ofTeams"], jsonObject("teams", []any{"tiny"}, "condition", tiny))
	// :516
	read("sidebar-active-list-club-member", mix.queries.sessions["inStates"], jsonObject("states", b14Active, "condition", member))
	// :519
	read("sidebar-recent-list-club-member", mix.queries.sessions["since"], jsonObject("since", float64(b14Since), "condition", member))
	// :522
	read("sidebar-active-list-guest", mix.queries.sessions["inStates"], jsonObject("states", b14Active, "condition", guest))
}

// b14Report adds the metrics of a counted run: `runs/op`, `probes/op` and `skips/op` (and `reads-open`, the reads held).
func b14Report(b *testing.B, m *b14Mix, before [3]float64) {
	after := m.counts()
	n := float64(b.N)
	b.ReportMetric((after[0]-before[0])/n, "runs/op")
	b.ReportMetric((after[1]-before[1])/n, "probes/op")
	b.ReportMetric((after[2]-before[2])/n, "skips/op")
}

// BenchmarkSlice14Touch is Pi's second block (:557-599): "a touch of a club session at the mix". Pi's budgets: a touch 5 ms
// (every re-run included), a probe 0.02 ms; exact: 9 runs, 101 probes, `reads - 9` skips (TestBenchSlice14Exact checks them).
func BenchmarkSlice14Touch(b *testing.B) {
	// :560-562 `quiet`: `mixed(0)`, the touch with no read open (baseline).
	b.Run("no-read-open", func(b *testing.B) {
		quiet := b14Mixed(b, 0)
		b.Cleanup(quiet.close)
		b.ReportAllocs()
		b.ResetTimer()
		before := quiet.counts()
		for i := 0; b.Loop(); i++ {
			quiet.touch("club-3", 30_000+i, []string{"waiting", "working"}[i%2])
		}
		b14Report(b, quiet, before)
	})
	mix := b14Mixed(b, 1)
	b.Cleanup(mix.close)
	// :563-568 the touch of club-3 at the mix: its runs and keyed probes, and its time.
	b.Run("mix-touch-club-3", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		before := mix.counts()
		for i := 0; b.Loop(); i++ {
			mix.touch("club-3", 30_000+i, []string{"waiting", "working"}[i%2])
		}
		b14Report(b, mix, before)
		b.ReportMetric(float64(mix.reads), "reads-open")
	})
	// :570-581 a touch of a session nobody shows (archived elsewhere): only probes ask about it.
	b.Run("mix-touch-session-nobody-shows", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		before := mix.counts()
		for i := 0; b.Loop(); i++ {
			mix.touch("old-7", 40_000+i, "idle")
		}
		b14Report(b, mix, before)
	})
	// :583-588 a write to another session: the worker following its own session runs nothing.
	b.Run("mix-touch-family-2999", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		before := mix.counts()
		for i := 0; b.Loop(); i++ {
			mix.touch("family-2999", 2, "idle")
		}
		b14Report(b, mix, before)
	})
	// :590-598 reported: an archive of a club session assigns a condition column of the counts (the 21 team counts run).
	b.Run("mix-archive-club-13", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		before := mix.counts()
		for i := 0; b.Loop(); i++ {
			mix.call("archive", jsonObject("id", "club-13", "at", float64(5+i)))
		}
		b14Report(b, mix, before)
	})
}

// BenchmarkSlice14TenTimesMix is Pi's third block (:602-608), "reported without a budget: the same touch at ten times the mix".
func BenchmarkSlice14TenTimesMix(b *testing.B) {
	mix := b14Mixed(b, 10)
	b.Cleanup(mix.close)
	b.ReportAllocs()
	b.ResetTimer()
	before := mix.counts()
	for i := 0; b.Loop(); i++ {
		mix.touch("club-3", 30_000+i, []string{"waiting", "working"}[i%2])
	}
	b14Report(b, mix, before)
	b.ReportMetric(float64(mix.reads), "reads-open")
}

// b14Subagents is Pi's fourth block's setup (:610-682): a session's 5,000 subagents, a list by last touch.
type b14Subagents struct {
	tb       testing.TB
	host     Host
	meter    *nexttest.Meter
	store    *store.ChordStore
	children *Reference
	errs     *hostErrors
}

var (
	b14ChildRows = mustTableDef("child-rows", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{store.Col("id", store.Int()), store.Col("session", store.Str()), store.Col("lastTouch", store.Int())},
		Key:     []string{"id"},
		Indexes: store.IndexRecords{{Name: "of_session", Columns: []string{"session", "lastTouch"}}},
	})
	b14Child    = contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("lastTouch", contracts.Int()))
	b14Children = exLocal("bench.children", contracts.Params{},
		contracts.Mem("children", contracts.Queries(b14Child, []contracts.Property{
			contracts.Prop("ofSession", contracts.Obj(contracts.Prop("session", contracts.Str()))),
		})),
		contracts.Mem("spawn", contracts.NewMethod(contracts.Int())),
		contracts.Mem("message", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("at", contracts.Int())))),
	)
	// One list object per screen: it activates when the screen holds it.
	b14Subs = exLocal("bench.subagents", contracts.Params{contracts.Prop("view", contracts.Str())},
		contracts.Mem("list", contracts.Obj(contracts.Prop("rows", contracts.Arr(b14Child)), contracts.Prop("moreAfter", contracts.Bool()))),
		contracts.Mem("more", contracts.NewMethod()),
	)
)

func b14SubagentsFacet() Facet {
	return DefineFacet(func(host FacetHost, _ FacetSelf) error {
		if err := host.Serve(b14Children, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
			self := plain.(StoreSelf)
			rows := self.Table(b14ChildRows)
			children := self.Queries(rows, QueriesOptions{
				Map: func(row store.Row, _ PartnerRow) (delta.JsonValue, error) {
					return jsonObject("id", float64(row["id"].(int64)), "lastTouch", float64(row["lastTouch"].(int64))), nil
				},
				Reads: []string{"id", "lastTouch"},
			}, map[string]QueryBuilder{
				"ofSession": {Build: func(args delta.JsonValue) *store.Query {
					session := args.(*delta.JsonObject).Value("session")
					return store.NewQuery(b14ChildRows).
						Where(func(c, _ store.ColumnRefs) store.Condition { return store.Eq(c["session"], session) }).
						OrderBy(func(c, _ store.ColumnRefs) *store.ColumnRef { return c["lastTouch"] }, "desc")
				}},
			})
			return contracts.MembersImplementation{
				Queries: map[string]map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]{"children": children},
				Methods: map[string]contracts.MethodFunction{
					"spawn": func(count delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						_, err := self.Tx(func(tx documents.Tx) (any, error) {
							for id := int64(1); id <= int64(count.(float64)); id++ {
								if err := tx.Put(rows, store.Row{"id": id, "session": "s", "lastTouch": id}); err != nil {
									return nil, err
								}
							}
							return nil, nil
						}, ctx)
						return nil, err
					},
					"message": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						_, err := self.Tx(func(tx documents.Tx) (any, error) {
							return nil, tx.Update(rows, store.Row{"id": int64(args.Value("id").(float64))}, store.Row{"lastTouch": int64(args.Value("at").(float64))})
						}, ctx)
						return nil, err
					}),
				},
			}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return host.Serve(b14Subs, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
			list := self.List("children", []ListSource{{Query: self.Get(b14Children, jsonObject()).Queries("children")["ofSession"], Args: jsonObject("session", "s")}},
				ListOptions{First: b14Page})
			return contracts.MembersImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"list": list.Value()},
				Methods: map[string]contracts.MethodFunction{"more": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return nil, list.More(ctx) }},
			}, nil
		}, ServeOptions{})
	})
}

func b14OpenSubagents(tb testing.TB) *b14Subagents {
	tb.Helper()
	meter := nexttest.Metered(tb)
	errs := &hostErrors{}
	chordStore := memoryStore(tb)
	host := newHost(tb, HostOptions{Store: chordStore, Telemetry: meter.Telemetry, OnError: errs.push})
	if _, err := host.Load([]HostFacet{{Name: "@bench/children", Facet: b14SubagentsFacet()}}, meter.Context); err != nil {
		tb.Fatal(err)
	}
	children, err := host.Get(meter.Context, b14Children, jsonObject(), GetOptions{})
	must(tb, err)
	if _, err := children.Call("spawn", 5_000.0, meter.Context); err != nil {
		tb.Fatal(err)
	}
	return &b14Subagents{tb: tb, host: host, meter: meter, store: chordStore, children: children, errs: errs}
}

func (s *b14Subagents) screen(view string) *Reference {
	s.tb.Helper()
	screen, err := s.host.Get(s.meter.Context, b14Subs, jsonObject("view", view), GetOptions{})
	must(s.tb, err)
	return screen
}

// rows is `screen.list.value.rows`.
func (s *b14Subagents) rows(screen *Reference) []any {
	rows, _ := mustListValue(s.tb, screen)
	return rows
}

// loadPages is `for (let page = 1; page < 20; page++) await screen.more(context)`.
func (s *b14Subagents) loadPages(screen *Reference) {
	s.tb.Helper()
	for range 19 {
		if _, err := screen.Call("more", nil, s.meter.Context); err != nil {
			s.tb.Fatal(err)
		}
	}
}

func (s *b14Subagents) message(id, at int) {
	s.tb.Helper()
	if _, err := s.children.Call("message", jsonObject("id", float64(id), "at", float64(at)), s.meter.Context); err != nil {
		s.tb.Fatal(err)
	}
}

func (s *b14Subagents) close(screens ...*Reference) {
	for _, screen := range screens {
		screen.Release()
	}
	s.children.Release()
	must(s.tb, s.host.Close(s.meter.Context))
	if s.errs.Len() > 0 {
		s.tb.Errorf("background errors: %v", s.errs.All())
	}
}

// BenchmarkSlice14Subagents is Pi's fourth block (:610-722): "a session's 5,000 subagents (a list by last touch, 20 pages loaded)".
// Pi's budgets: the first page 2 ms, a message 5 ms; exact: 1,000 rows, 2 runs per message, the pages bound (TestBenchSlice14Exact).
func BenchmarkSlice14Subagents(b *testing.B) {
	s := b14OpenSubagents(b)
	b.Cleanup(func() { s.close() })
	// The clock of the messages runs on across the sub-benchmark's calls: a message must touch the child to a time newer than
	// every earlier one, as the script's `++at` does.
	at := 10_000
	// :673-678 the first page of 50 of 5,000, the object's activation included: a screen held and released.
	b.Run("first-page-of-50-of-5000-activation-included", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			s.screen(fmt.Sprintf("tab-%d", i)).Release()
		}
	})
	// :683-702 a message to a child in the middle of what is loaded: it leaves its page and enters the first one.
	b.Run("message-one-child-to-the-top-20-pages-loaded", func(b *testing.B) {
		screen := s.screen("tab")
		defer screen.Release()
		s.loadPages(screen)
		b.ReportAllocs()
		b.ResetTimer()
		before := s.meter.Counts()["chord.query.runs"]
		for b.Loop() {
			at++
			s.message(int(s.rows(screen)[500].(*delta.JsonObject).Value("id").(float64)), at)
		}
		b.ReportMetric((s.meter.Counts()["chord.query.runs"]-before)/float64(b.N), "runs/op")
	})
}

// TestBenchSlice14Exact checks what the script checks with `expectExactly` and with the page bound: numbers of the counters and of
// the store's index of reads, which do not depend on the machine (Pi: :565-567, :588, :683, :701, :712). The times of the script
// are Pi's budgets on Pi's machine and are not asserted.
func TestBenchSlice14Exact(t *testing.T) {
	t.Run("a touch of a club session at the mix", func(t *testing.T) {
		mix := b14Mixed(t, 1)
		defer mix.close()
		runs, probes, skips := b14Touched(mix, 400)
		// Pi v1.7.0 slice-14.ts:565-569: the commit index (slice 15 §3) leaves out the 41 reads whose bucket the
		// touched row is not in (another app's lists, other teams' pages): 9 runs, 79 probes, `mix.reads - 9 - 41` skips.
		if runs != 9 || probes != 79 || skips != float64(mix.reads-9-41) {
			t.Fatalf("a touch: %v runs, %v probes, %v skipped of %d reads, want 9, 79, %d", runs, probes, skips, mix.reads, mix.reads-9-41)
		}
		// A write to another session: the worker following its own session runs nothing (:583-588).
		before := mix.counts()
		mix.touch("family-2999", 2, "idle")
		if after := mix.counts(); after[0]-before[0] != 0 {
			t.Fatalf("runs a touch of a session nobody shows causes: %v, want 0", after[0]-before[0])
		}
	})
	t.Run("a session's 5,000 subagents", func(t *testing.T) {
		s := b14OpenSubagents(t)
		screen := s.screen("tab")
		defer s.close(screen)
		s.loadPages(screen)
		// :683 `expectExactly("rows loaded by 20 pages", ..., 20 * PAGE)`.
		if got := len(s.rows(screen)); got != 20*b14Page {
			t.Fatalf("rows loaded by 20 pages: %d, want %d", got, 20*b14Page)
		}
		// :686-701 a message to a child in the middle of what is loaded: it leaves its page and enters the first one: 2 runs.
		at := 10_000
		var runs float64
		for range 400 {
			at++
			before := s.meter.Counts()["chord.query.runs"]
			s.message(int(s.rows(screen)[500].(*delta.JsonObject).Value("id").(float64)), at)
			runs = s.meter.Counts()["chord.query.runs"] - before
		}
		if runs != 2 {
			t.Fatalf("runs a message causes: %v, want 2", runs)
		}
		// Reported, not in Pi's script: the runs of the next 2,000 messages, by count of runs (the benchmark's `runs/op` averages over
		// all its messages, Pi's check reads the last of 400).
		distribution := map[float64]int{}
		for range 2_000 {
			at++
			before := s.meter.Counts()["chord.query.runs"]
			s.message(int(s.rows(screen)[500].(*delta.JsonObject).Value("id").(float64)), at)
			distribution[s.meter.Counts()["chord.query.runs"]-before]++
		}
		t.Logf("runs per message over 2,000 more messages: %v", distribution)
		// :704-712 10,000 touches of loaded children: the pages they leave and enter split and merge, and stay bounded.
		for touch := range 10_000 {
			rows := s.rows(screen)
			at++
			s.message(int(rows[(touch*37)%len(rows)].(*delta.JsonObject).Value("id").(float64)), at)
		}
		pages := len(s.store.Reads.On("bench.children:child-rows"))
		loaded := len(s.rows(screen))
		if bound := 2*loaded/b14Page + 1; pages > bound {
			t.Fatalf("pages for the loaded rows after 10,000 touches: %d, want at most %d (%d rows loaded)", pages, bound, loaded)
		}
		t.Logf("after 10,000 touches: %d rows loaded in %d pages", loaded, pages)
	})
}
