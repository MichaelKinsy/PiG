package host

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// pi: packages/chord/src/host/named-query.ts
// Ports packages/chord/test/host/named-query.test.ts (Pi main 0db9861ec018; slice 14 §3.1, §5, test list 2, 7, 11, 18): what
// `self.queries` returns implements a `queries` member; a joined query's mapping gets the row and its partner, projected;
// `page` reads without a live read; the member check and staging; and a column set's binding across its publisher's reloads.
//
// Forced differences: a queries member is a Go map of NamedQuery values, so Pi's `5` (not an object) has two Go forms, a nil
// map and a map of a foreign NamedQuery (the same refusal); `self.queries` takes builders as a map of QueryBuilder (a plain
// builder: Args nil, the contract lists the query; `[argsSchema, builder]`: Args set).

var (
	nqTeamRows = hdTable("team-rows", store.Col("team", store.Str()), store.Col("session", store.Host(Session)))
	nqTeamItem = exObj(exProp("session", exStr()), exProp("title", exStr()), exProp("touched", exInt()))
	nqClub     = exLocal("app.club", contracts.Params{},
		contracts.Mem("teams", contracts.Queries(nqTeamItem, []contracts.Property{exProp("ofTeam", exObj(exProp("team", exStr())))})),
		contracts.Mem("add", contracts.NewMethod(exObj(exProp("team", exStr()), exProp("session", exStr())))))
)

// nqNamed is what a queries member of the club implements.
type nqNamed = map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]

// nqForeign is a NamedQuery that `self.queries` did not return.
type nqForeign struct{}

func (nqForeign) Page(delta.JsonValue, int64, context.Context, contracts.QueryPageOptions) (contracts.QueryPage[delta.JsonValue], error) {
	return contracts.QueryPage[delta.JsonValue]{}, nil
}

// nqBroken is the failure messages of an attempt of the club whose factory returns `teams` for its queries member
// (`broken`): the activation failures' messages, any other report's kind.
func nqBroken(t *testing.T, teams func(self StoreSelf) nqNamed) []string {
	t.Helper()
	chordStore := memoryStore(t)
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(nqClub, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
			return contracts.MembersImplementation{
				Queries: map[string]nqNamed{"teams": teams(plain.(StoreSelf))},
				Methods: map[string]contracts.MethodFunction{"add": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
			}, nil
		}, ServeOptions{})
	}, loadedOptions{Store: chordStore})
	// The page's refusal is the report: Pi's `.catch(() => {})`.
	_, _ = host.Get(background, nqClub, jsonObject(), GetOptions{})
	var messages []string
	for _, err := range errs.All() {
		if err.Failure.Error != nil {
			messages = append(messages, err.Failure.Error.Message)
		} else {
			messages = append(messages, string(err.Kind))
		}
	}
	return messages
}

func nqExpectOne(t *testing.T, messages []string, want string) {
	t.Helper()
	if len(messages) != 1 || !strings.Contains(messages[0], want) {
		t.Fatalf("messages %q, want one containing %q", messages, want)
	}
}

func TestNamedQueryTheMemberCheckRefusesAnythingButWhatSelfQueriesReturnedAListedQueryWithoutABuilderAndASecondSchema(t *testing.T) {
	nqExpectOne(t, nqBroken(t, func(StoreSelf) nqNamed { return nqNamed{} }),
		`teams: no builder for the listed query "ofTeam"`)
	// Pi's `5`: no object at all, and an object holding what self.queries did not return.
	nqExpectOne(t, nqBroken(t, func(StoreSelf) nqNamed { return nil }),
		"teams: a queries member is what self.queries returned")
	nqExpectOne(t, nqBroken(t, func(StoreSelf) nqNamed { return nqNamed{"ofTeam": nqForeign{}} }),
		"teams: a queries member is what self.queries returned")
	twice := nqBroken(t, func(self StoreSelf) nqNamed {
		return self.Queries(self.Table(nqTeamRows), QueriesOptions{Map: func(store.Row, PartnerRow) (delta.JsonValue, error) { return nil, nil }},
			map[string]QueryBuilder{"ofTeam": {
				Args:  exObj(exProp("team", exStr())),
				Build: func(delta.JsonValue) *store.Query { return store.NewQuery(nqTeamRows) },
			}})
	})
	nqExpectOne(t, twice, "the contract has its argument schema; give only the builder")
	// Mine (Pi's check has no case of its own): a query the contract does not list brings its schema.
	private := nqBroken(t, func(self StoreSelf) nqNamed {
		build := func(delta.JsonValue) *store.Query { return store.NewQuery(nqTeamRows) }
		return self.Queries(self.Table(nqTeamRows), QueriesOptions{}, map[string]QueryBuilder{"ofTeam": {Build: build}, "extra": {Build: build}})
	})
	nqExpectOne(t, private, `teams: query "extra" is not listed and brings no argument schema`)
}

var (
	nqPlain   = hdTable("plain", store.Col("id", store.Int()), store.Col("n", store.Int()))
	nqCounted = exLocal("app.counted", contracts.Params{}, contracts.Mem("n", exInt()))
)

// nqServeCounted serves Counted, its member n the value declare returns (declare runs in the factory, the declarations of
// the case first).
func nqServeCounted(h FacetHost, declare func(self StoreSelf)) error {
	return h.Serve(nqCounted, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		self := plain.(StoreSelf)
		declare(self)
		return exImpl(map[string]chord.Watchable[delta.JsonValue]{
			"n": self.Derive(nil, func([]any) (delta.JsonValue, error) { return float64(1), nil }),
		}, nil), nil
	}, ServeOptions{})
}

// nqRefused is the refusal of `declare` by an attempt (the Get of a host serving it) and by staging (the reload of a host
// that served Counted without it).
func nqRefused(t *testing.T, declare func(self StoreSelf)) (attempt, staged string) {
	t.Helper()
	live, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return nqServeCounted(h, declare) }, loadedOptions{Store: memoryStore(t)})
	_, err := live.Get(background, nqCounted, jsonObject(), GetOptions{})
	if err != nil {
		attempt = err.Error()
	}
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return nqServeCounted(h, func(StoreSelf) {}) }, loadedOptions{Store: memoryStore(t)})
	report := reloadMust(t)(reloadApp(host, func(h FacetHost) error { return nqServeCounted(h, declare) }))
	for _, failure := range report.Failed {
		staged += failure.Error.Error()
	}
	return attempt, staged
}

// nqListed is a named query of this object, as a list or a live query reads it.
func nqListed(self StoreSelf) contracts.NamedQuery[delta.JsonValue, delta.JsonValue] {
	named := self.Queries(self.Table(nqPlain), QueriesOptions{}, map[string]QueryBuilder{"all": {Args: contracts.Obj(), Build: func(delta.JsonValue) *store.Query { return store.NewQuery(nqPlain) }}})
	return named["all"]
}

func TestNamedQueryStagingRefusesTheQueriesAnAttemptRefusesTheNamedBuildersAndTheTable(t *testing.T) {
	for _, each := range []struct {
		what    string
		declare func(self StoreSelf)
		message string
	}{
		{"a table the factory did not declare", func(self StoreSelf) {
			self.Queries(nil, QueriesOptions{}, nil)
		}, "self.queries: the table is not one this factory declared"},
		{"a table of another definition", func(self StoreSelf) {
			self.Table(nqPlain)
			self.Queries(store.NewTableHandle(nqTeamRows, "x", nil, nil), QueriesOptions{}, nil)
		}, "self.queries: the table is not one this factory declared"},
		// Mine: a handle of a recording that stands for a table this factory did not declare.
		{"a recorded handle of another definition", func(self StoreSelf) {
			self.Table(nqPlain)
			self.Queries(store.NewTableHandle(nqTeamRows, "x", nil, inertTableOwner{}), QueriesOptions{}, nil)
		}, "self.queries: the table is not one this factory declared"},
		// Pi's `self.count(table)` and `self.liveQuery(table)`: a table cannot be given in Go, a foreign query stands for it.
		{"a count of what is no named query of this object", func(self StoreSelf) {
			self.Count(nqForeign{}, jsonObject())
		}, "self.count reads a named query; a table of this object is read with self.query"},
		{"a live query of what is no named query of this object", func(self StoreSelf) {
			self.LiveQuery(nqForeign{}, jsonObject(), 1)
		}, "self.liveQuery reads a named query"},
		// Pi's staging case 1 (the page size of `self.liveQuery`; its `{} as never` is a first of 0).
		{"a live query without a page size", func(self StoreSelf) {
			named := self.Queries(self.Table(nqPlain), QueriesOptions{}, map[string]QueryBuilder{"all": {Args: contracts.Obj(), Build: func(delta.JsonValue) *store.Query { return store.NewQuery(nqPlain) }}})
			self.LiveQuery(named["all"], jsonObject(), 0)
		}, "self.liveQuery: { first: n } is required"},
		// Pi's staging case 1, the two arms of `self.list`: the list has a name, and one list per name.
		{"a list without a name", func(self StoreSelf) {
			self.List("", []ListSource{{Query: nqListed(self), Args: jsonObject()}}, ListOptions{First: 2})
		}, "a list has a name"},
		{"a second list of a name", func(self StoreSelf) {
			sources := []ListSource{{Query: nqListed(self), Args: jsonObject()}}
			self.List("a", sources, ListOptions{First: 2})
			self.List("a", sources, ListOptions{First: 2})
		}, `list "a" is declared twice`},
		// Pi's staging case 2, its arm of `self.list` (a table cannot be given in Go: a foreign query stands for it).
		{"a list of what is no named query of this object", func(self StoreSelf) {
			self.List("l", []ListSource{{Query: nqForeign{}, Args: jsonObject()}}, ListOptions{First: 1})
		}, `self.list "l" reads a named query`},
		// Mine: Pi's namedBuilders check has no case of its own.
		{"a builder that is none", func(self StoreSelf) {
			self.Queries(self.Table(nqPlain), QueriesOptions{}, map[string]QueryBuilder{"all": {}})
		}, `self.queries: "all" has no builder`},
	} {
		t.Run("refuses "+each.what, func(t *testing.T) {
			attempt, staged := nqRefused(t, each.declare)
			for _, got := range []string{attempt, staged} {
				if !strings.Contains(got, each.message) {
					t.Fatalf("refusal %q, want %q", got, each.message)
				}
			}
		})
	}
}

// --- the sessions' column set and the club ---------------------------------------------------------------------------------

func nqMust[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

var (
	nqSessionColumns = nqMust(store.DefineColumns("sessions.columns", store.ColumnSetOptions{
		Columns: []store.ColumnProp{store.Col("id", store.Host(Session)), store.Col("app", store.Str()), store.Col("title", store.Str()), store.Col("lastTouch", store.Int())},
		Key:     []string{"id"},
	}))
	nqRows     = nqTableWith(nqSessionColumns, store.Col("cwd", store.Str(contracts.Default("/"))))
	nqSessions = exLocal("app.sessions", contracts.Params{},
		contracts.Mem("put", contracts.NewMethod(exObj(exProp("id", exStr()), exProp("app", exStr()), exProp("title", exStr()), exProp("lastTouch", exInt())))),
		contracts.Mem("touch", contracts.NewMethod(exObj(exProp("id", exStr()), exProp("at", exInt())))))
)

// nqTableWith is the sessions' table: the column set's columns and extra, exposing the set.
func nqTableWith(set *store.ColumnSet, extra ...store.ColumnProp) *store.TableDefinition {
	return nqMust(store.DefineTable("rows", store.TableOptions{
		Version: 1, Columns: append(append([]store.ColumnProp{}, set.Schemas...), extra...), Key: []string{"id"}, Exposes: set,
	}))
}

// nqTeamPage is a screen over the club's page of a team: a live query of the club's named query.
var nqTeamPage = exLocal("app.team-page", contracts.Params{contracts.Prop("team", contracts.Str())},
	contracts.Mem("sessions", exObj(exProp("rows", exArr(exObj(exProp("session", exStr()), exProp("title", exStr()), exProp("touched", exInt())))), exProp("more", exBool()))))

func nqSession(id string) contracts.Address { return Session.Address(jsonObject("id", id)) }

// nqServeSessions is the sessions' store: the publisher of the column set. table is the version its code declares; declare,
// when set, declares more after it (Pi's `serveSessions(h, table, declare)`).
func nqServeSessions(h FacetHost, table *store.TableDefinition, declare func(self StoreSelf)) error {
	return h.Serve(nqSessions, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		self := plain.(StoreSelf)
		rows := self.Table(table)
		if declare != nil {
			declare(self)
		}
		tx := func(ctx context.Context, write func(tx documents.Tx) error) (delta.JsonValue, error) {
			_, err := self.Tx(func(tx documents.Tx) (any, error) { return nil, write(tx) }, ctx)
			return nil, err
		}
		return exImpl(nil, map[string]contracts.MethodFunction{
			"put": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
				return tx(ctx, func(tx documents.Tx) error {
					return tx.Put(rows, store.Row{"id": nqSession(args.Value("id").(string)), "app": args.Value("app"), "title": args.Value("title"), "lastTouch": int64(args.Value("lastTouch").(float64))})
				})
			}),
			"touch": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
				return tx(ctx, func(tx documents.Tx) error {
					return tx.Update(rows, store.Row{"id": nqSession(args.Value("id").(string))}, store.Row{"lastTouch": int64(args.Value("at").(float64))})
				})
			}),
		}), nil
	}, ServeOptions{})
}

// nqProbe is what the club's mapping was handed, by call: the sorted names of the partner's columns.
type nqProbe struct {
	mu       sync.Mutex
	partners [][]string
}

func (p *nqProbe) add(names []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.partners = append(p.partners, names)
}

func (p *nqProbe) last() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.partners) == 0 {
		return nil
	}
	return p.partners[len(p.partners)-1]
}

func nqKeys(row map[string]any) []string {
	names := make([]string, 0, len(row))
	for name := range row {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// nqServeClub is the club: its team rows joined with the sessions' column set, a query per team.
func nqServeClub(h FacetHost, probe *nqProbe, set *store.ColumnSet) error {
	if err := h.Serve(nqClub, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		self := plain.(StoreSelf)
		table := self.Table(nqTeamRows)
		teams := self.Queries(table, QueriesOptions{
			Map: func(row store.Row, session PartnerRow) (delta.JsonValue, error) {
				probe.add(nqKeys(session))
				return jsonObject("session", row["session"].(contracts.Address).Params.Value("id"), "title", session["title"], "touched", float64(session["lastTouch"].(int64))), nil
			},
			PartnerReads: []string{"title", "lastTouch"},
		}, map[string]QueryBuilder{"ofTeam": {Build: func(args delta.JsonValue) *store.Query {
			team := args.(*delta.JsonObject).Value("team").(string)
			return store.NewQuery(nqTeamRows).
				Where(func(t, _ store.ColumnRefs) store.Condition { return store.Eq(t["team"], team) }).
				Join(set, func(t store.ColumnRefs) store.Match { return store.Match{{Column: "id", Ref: t["session"]}} }).
				Where(func(_, session store.ColumnRefs) store.Condition { return store.Eq(session["app"], "club") }).
				OrderBy(func(_, session store.ColumnRefs) *store.ColumnRef { return session["lastTouch"] }, "desc")
		}}})
		return contracts.MembersImplementation{
			Queries: map[string]nqNamed{"teams": teams},
			Methods: map[string]contracts.MethodFunction{"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
				_, err := self.Tx(func(tx documents.Tx) (any, error) {
					return nil, tx.Put(table, store.Row{"team": args.Value("team"), "session": nqSession(args.Value("session").(string))})
				}, ctx)
				return nil, err
			})},
		}, nil
	}, ServeOptions{}); err != nil {
		return err
	}
	return h.Serve(nqTeamPage, func(params *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		self := plain.(StoreSelf)
		page := self.LiveQuery(self.Get(nqClub, jsonObject()).Queries("teams")["ofTeam"], jsonObject("team", params.Value("team")), 10)
		return exImpl(map[string]chord.Watchable[delta.JsonValue]{"sessions": page}, nil), nil
	}, ServeOptions{})
}

// nqPublisher is the sessions' plugin at a table version: only the publisher's code changes in these reloads.
func nqPublisher(table *store.TableDefinition) HostFacet {
	return HostFacet{Name: "@test/sessions", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return nqServeSessions(h, table, nil) })}
}

// nqClubPlugin is the club's plugin.
func nqClubPlugin(probe *nqProbe) HostFacet {
	return HostFacet{Name: "@test/club", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return nqServeClub(h, probe, nqSessionColumns) })}
}

// nqHosted is a host with both plugins, three sessions (two of the club) and a team of the three.
func nqHosted(t *testing.T, chordStore *store.ChordStore, probe *nqProbe) (Host, *hostErrors) {
	t.Helper()
	return nqHostedWith(t, chordStore, nqClubPlugin(probe))
}

// nqHostedWith is nqHosted with the club plugin given: a reload that hands the same value back leaves the club unchanged.
func nqHostedWith(t *testing.T, chordStore *store.ChordStore, club HostFacet) (Host, *hostErrors) {
	t.Helper()
	errs := &hostErrors{}
	host := newHost(t, HostOptions{Store: chordStore, OnError: errs.push})
	_, err := host.Load([]HostFacet{nqPublisher(nqRows), club}, background)
	must(t, err)
	sessions, teams := mustGet(t, host, nqSessions, jsonObject()), mustGet(t, host, nqClub, jsonObject())
	for index, app := range []string{"club", "club", "family"} {
		id := "s" + strconv.Itoa(index)
		_, err := sessions.Call("put", jsonObject("id", id, "app", app, "title", "t"+strconv.Itoa(index), "lastTouch", float64(index)), background)
		must(t, err)
		_, err = teams.Call("add", jsonObject("team", "u12", "session", id), background)
		must(t, err)
	}
	return host, errs
}

// nqItems is the sessions of a page's items, in order, each with its title and touch.
func nqItems(items []delta.JsonValue) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		row := item.(*delta.JsonObject)
		out = append(out, row.Value("session").(string)+"/"+row.Value("title").(string)+"/"+strconv.Itoa(int(row.Value("touched").(float64))))
	}
	return out
}

func TestNamedQueryAJoinedQueryReadsAPageAtATimeWithoutALiveRead(t *testing.T) {
	host, _ := nqHosted(t, memoryStore(t), &nqProbe{})
	teams := mustGet(t, host, nqClub, jsonObject()).Queries("teams")["ofTeam"]
	args := jsonObject("team", "u12")
	first, err := teams.Page(args, 1, background, contracts.QueryPageOptions{})
	must(t, err)
	if !slices.Equal(nqItems(first.Items), []string{"s1/t1/1"}) || first.Next == nil {
		t.Fatalf("first = %v next %v", nqItems(first.Items), first.Next)
	}
	second, err := teams.Page(args, 1, background, contracts.QueryPageOptions{After: first.Next})
	must(t, err)
	if !slices.Equal(nqItems(second.Items), []string{"s0/t0/0"}) || second.Next != nil {
		t.Fatalf("second = %v next %v", nqItems(second.Items), second.Next)
	}
	nope := "nope"
	_, err = teams.Page(args, 1, background, contracts.QueryPageOptions{After: &nope})
	wantChordError(t, err, data.CodeInvalid, data.ReasonArgs, "")
}

func TestNamedQueryTheMappingsProjectionGetsOnlyTheColumnsReadsAndPartnerReadsListAlsoWhenTheQueryOrdersOrMatchesByOthers(t *testing.T) {
	// Review round 1 (Sol 7): the statement selects the key, the order and the match columns for the store; the mapping does
	// not see them.
	var seen [][]string
	var mu sync.Mutex
	plain := nqMust(store.DefineTable("plain", store.TableOptions{
		Version: 1, Key: []string{"id"},
		Columns: []store.ColumnProp{store.Col("id", store.Int()), store.Col("group", store.Str()), store.Col("text", store.Str()), store.Col("session", store.Host(Session))},
	}))
	projected := exLocal("app.projected", contracts.Params{},
		contracts.Mem("rows", contracts.Queries(exObj(exProp("text", exStr())), []contracts.Property{exProp("all", exObj())})),
		contracts.Mem("put", contracts.NewMethod(exInt())))
	host := newHost(t, HostOptions{Store: memoryStore(t)})
	_, err := host.Load([]HostFacet{nqPublisher(nqRows), {Name: "@test/projected", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(projected, func(_ *delta.JsonObject, plainSelf Self) (contracts.MembersImplementation, error) {
			self := plainSelf.(StoreSelf)
			table := self.Table(plain)
			rows := self.Queries(table, QueriesOptions{
				Map: func(row store.Row, session PartnerRow) (delta.JsonValue, error) {
					mu.Lock()
					defer mu.Unlock()
					seen = append(seen, append(append(nqKeys(row), "|"), nqKeys(session)...))
					return jsonObject("text", row["text"]), nil
				},
				Reads:        []string{"text"},
				PartnerReads: []string{"title"},
			}, map[string]QueryBuilder{"all": {Build: func(delta.JsonValue) *store.Query {
				return store.NewQuery(plain).
					Join(nqSessionColumns, func(row store.ColumnRefs) store.Match { return store.Match{{Column: "id", Ref: row["session"]}} }).
					OrderBy(func(row, _ store.ColumnRefs) *store.ColumnRef { return row["group"] }, "asc").
					OrderBy(func(_, session store.ColumnRefs) *store.ColumnRef { return session["lastTouch"] }, "asc")
			}}})
			return contracts.MembersImplementation{
				Queries: map[string]nqNamed{"rows": rows},
				Methods: map[string]contracts.MethodFunction{"put": func(arg delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					row := store.Row{"id": int64(arg.(float64)), "group": "g", "text": "t", "session": nqSession("s0")}
					_, err := self.Tx(func(tx documents.Tx) (any, error) { return nil, tx.Put(table, row) }, ctx)
					return nil, err
				}},
			}, nil
		}, ServeOptions{})
	})}}, background)
	must(t, err)
	_, err = mustGet(t, host, nqSessions, jsonObject()).Call("put", jsonObject("id", "s0", "app", "club", "title", "t0", "lastTouch", float64(1)), background)
	must(t, err)
	rows := mustGet(t, host, projected, jsonObject())
	_, err = rows.Call("put", float64(1), background)
	must(t, err)
	page, err := rows.Queries("rows")["all"].Page(jsonObject(), 5, background, contracts.QueryPageOptions{})
	must(t, err)
	if len(page.Items) != 1 || page.Items[0].(*delta.JsonObject).Value("text") != "t" {
		t.Fatalf("items = %v", page.Items)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.EqualFunc(seen, [][]string{{"text", "|", "title"}}, slices.Equal) {
		t.Fatalf("the mapping saw %q, want [[text | title]]", seen)
	}
}

func TestNamedQueryAPublisherWhoseNewCodeDeclaresNoTableServesItsSetNoMore(t *testing.T) {
	// Sol 2, Astra S1: the binding must not outlive the last table. As Pi's case, the reload hands the same `club` back
	// (a plugin value that is not the same is new code: the club and the page restart, see the next test).
	chordStore := memoryStore(t)
	club := nqClubPlugin(&nqProbe{})
	host, _ := nqHostedWith(t, chordStore, club)
	page := mustGet(t, host, nqTeamPage, jsonObject("team", "u12"))
	if _, bound := chordStore.Reads.Get(nqSessionColumns.Name); !bound {
		t.Fatal("the set is not bound while its publisher serves it")
	}
	report := reloadMust(t)(host.Reload([]HostFacet{nqBareSessions(), club}, background, ReloadOptions{}))
	if len(report.Failed) != 0 {
		t.Fatalf("failed = %+v", report.Failed)
	}
	// The caller waits on the new attempt being live: a call to the sessions parks until it is, and runs on it.
	_, err := mustGet(t, host, nqSessions, jsonObject()).Call("touch", jsonObject("id", "s0", "at", float64(1)), background)
	must(t, err)
	if _, bound := chordStore.Reads.Get(nqSessionColumns.Name); bound {
		t.Fatal("the set is bound after its publisher declared no table")
	}
	// The page's read of the set failed when the set was unbound: Pi's `await flushed()` lets the failed run reach the member
	// (Pi's `void load()`); here that run is on a task of its own, so the caller waits on the member's failure.
	nqWaitFails(t, host, page, `column set "sessions.columns" is not served`)
}

// nqBareSessions is the sessions' plugin whose new code declares no table.
func nqBareSessions() HostFacet {
	return HostFacet{Name: "@test/sessions", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(nqSessions, func(*delta.JsonObject, Self) (contracts.MembersImplementation, error) {
			noop := func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }
			return exImpl(nil, map[string]contracts.MethodFunction{"put": noop, "touch": noop}), nil
		}, ServeOptions{})
	})}
}

// nqWaitFails is Pi's `await flushed()` that lets the failed run reach the member (Pi's `void load()`; the run here is on a
// task the host's join counts), then the page's value fails with the failure `message` names.
func nqWaitFails(t *testing.T, host Host, page *Reference, message string) {
	t.Helper()
	flushed(host)
	deliveredOn(host)
	if _, err := page.Member("sessions").Value(); err == nil || !strings.Contains(err.Error(), message) {
		t.Fatalf("the page's value: error = %v, want one containing %q", err, message)
	}
}

func TestNamedQueryAPageFailsWhenItsSetIsUnboundAndHealsWhenItIsBoundAgain(t *testing.T) {
	// Mine (Pi has no case of its own): the failed run is the member's failure, and a set bound again compiles and runs the
	// read once more, so the member's next value is whole.
	chordStore := memoryStore(t)
	club := nqClubPlugin(&nqProbe{})
	host, _ := nqHostedWith(t, chordStore, club)
	page := mustGet(t, host, nqTeamPage, jsonObject("team", "u12"))
	qrWant(t, "rows", nqPageRows(t, page), `{"rows":[{"session":"s1","title":"t1","touched":1},{"session":"s0","title":"t0","touched":0}],"more":false}`)
	reloadMust(t)(host.Reload([]HostFacet{nqBareSessions(), club}, background, ReloadOptions{}))
	_, err := mustGet(t, host, nqSessions, jsonObject()).Call("touch", jsonObject("id", "s0", "at", float64(1)), background)
	must(t, err)
	nqWaitFails(t, host, page, `column set "sessions.columns" is not served`)
	reloadMust(t)(host.Reload([]HostFacet{nqPublisher(nqRows), club}, background, ReloadOptions{}))
	_, err = mustGet(t, host, nqSessions, jsonObject()).Call("touch", jsonObject("id", "s0", "at", float64(7)), background)
	must(t, err)
	flushed(host)
	deliveredOn(host)
	qrWant(t, "rows", nqPageRows(t, page), `{"rows":[{"session":"s0","title":"t0","touched":7},{"session":"s1","title":"t1","touched":1}],"more":false}`)
}

func TestNamedQueryAClubThatRestartsWithTheSessionsGivesItsPagesWatchTheUnservedSet(t *testing.T) {
	// Mine: a new club plugin value is new code: the club and the page restart with the sessions, and which of three ends
	// comes first is not an ordering of Pi's design. The sessions' successor drops the set when it activates (attempt.ts
	// #bindSets, after its old attempt closed); the page's old attempt seals (its reference then holds the last rows) at the
	// same step of its own restart, a few awaits before; Pi's microtasks give that one order each time (200 of 200 runs: the
	// value holds its rows, the watch gets the failure), and a page with a call still running (its seal waits for the call to
	// drain) the other (200 of 200: the value throws), with no case of Pi's naming either. Here the restarts run on tasks:
	// either the old page's live query fails first (its reference's value is that failure) or the reference holds its last
	// rows first and the new page's attempt, reading the unserved set, is shown failed. What both give, and what is
	// asserted: the watch gets exactly one call, the failure (no value after the set was dropped: no rows shown), and the
	// value is that failure or the page's last rows, never other rows.
	chordStore := memoryStore(t)
	host, _ := nqHosted(t, chordStore, &nqProbe{})
	page := mustGet(t, host, nqTeamPage, jsonObject("team", "u12"))
	last := nqPageRows(t, page)
	watch, err := page.Member("sessions").Watch(background)
	must(t, err)
	calls := nexttest.ChangesOf(watch)
	reloadMust(t)(host.Reload([]HostFacet{nqBareSessions(), nqClubPlugin(&nqProbe{})}, background, ReloadOptions{}))
	flushed(host)
	deliveredOn(host)
	const unserved = `column set "sessions.columns" is not served`
	if got := nqFailedMessages(calls.Items()); calls.Len() != 1 || len(got) != 1 || !strings.Contains(got[0], unserved) {
		t.Fatalf("the watch got %d calls, failures %v; want exactly one, %q", calls.Len(), got, unserved)
	}
	value, err := page.Member("sessions").Value()
	switch {
	case err != nil:
		if !strings.Contains(err.Error(), unserved) {
			t.Fatalf("the page's value failed with %v, want %q", err, unserved)
		}
	case nexttest.JSONText(t, value) != last:
		t.Fatalf("the page's value = %s, want its last rows %s or the failure", nexttest.JSONText(t, value), last)
	}
	_, _ = watch.Stop()
}

// nqFailedMessages is the failure messages of the failed calls of a watch.
func nqFailedMessages(calls []chord.WatchCall[delta.JsonValue]) []string {
	var out []string
	for _, call := range calls {
		if call.Kind == chord.WatchCallFailed {
			out = append(out, call.Error.Error())
		}
	}
	return out
}

func TestNamedQueryAnUnknownQueryOfAMemberIsInvalidAndARemoteObjectHasNone(t *testing.T) {
	// Mine (Pi has no case of its own): a reference's member resolves to the object's own query; none of that name is
	// `invalid/member`, and a remote object has no `queries` member at all.
	host, _ := nqHosted(t, memoryStore(t), &nqProbe{})
	_, err := mustGet(t, host, nqClub, jsonObject()).binding.queryMember("teams", "nope")
	wantChordError(t, err, data.CodeInvalid, data.ReasonMember, `app.club has no query "teams.nope"`)
	_, err = (&remoteObject{name: "app.far"}).queryMember("teams", "ofTeam", nil)
	wantChordError(t, err, data.CodeInvalid, data.ReasonMember, `app.far has no queries member "teams": named queries are local`)
}

func TestNamedQueryAPageChecksItsArgumentsByTheContractsSchemaAndWhatTheBuilderReturns(t *testing.T) {
	// Mine (Pi has no case of its own): the arguments are a trust boundary (`invalid/args`), and the builder is plugin code.
	built := store.NewQuery
	for _, each := range []struct {
		what    string
		args    delta.JsonValue
		table   *store.TableDefinition
		message string
	}{
		{"arguments that are not the schema's", jsonObject("team", float64(3)), nqTeamRows, ""},
		{"a builder returning a query of another table", jsonObject("team", "u12"), nqPlain, `app.club: query "ofTeam": the builder returns a query of table "team-rows"`},
	} {
		t.Run(each.what, func(t *testing.T) {
			host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
				return h.Serve(nqClub, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
					self := plain.(StoreSelf)
					teams := self.Queries(self.Table(nqTeamRows), QueriesOptions{}, map[string]QueryBuilder{"ofTeam": {Build: func(delta.JsonValue) *store.Query { return built(each.table) }}})
					noop := func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }
					return contracts.MembersImplementation{
						Queries: map[string]nqNamed{"teams": teams}, Methods: map[string]contracts.MethodFunction{"add": noop},
					}, nil
				}, ServeOptions{})
			}, loadedOptions{Store: memoryStore(t)})
			_, err := mustGet(t, host, nqClub, jsonObject()).Queries("teams")["ofTeam"].Page(each.args, 1, background, contracts.QueryPageOptions{})
			if each.message == "" {
				wantChordError(t, err, data.CodeInvalid, data.ReasonArgs, "")
				return
			}
			if err == nil || err.Error() != each.message {
				t.Fatalf("err = %v, want %q", err, each.message)
			}
		})
	}
}

func TestNamedQueryATargetIsWhatReadingItReadsAndDecodesAStoredRowAsItsPageDoes(t *testing.T) {
	// Mine: the reader's contract (Pi's query-reader.ts takes `provided().target(args, context)`): its spec names the query,
	// the table's SQL name and its owner, and its decode is the page's.
	host, _ := nqHosted(t, memoryStore(t), &nqProbe{})
	own, err := mustGet(t, host, nqClub, jsonObject()).binding.queryMember("teams", "ofTeam")
	must(t, err)
	target, err := own.Target(jsonObject("team", "u12"), background)
	must(t, err)
	spec := target.Spec
	if spec.Table != own.Handle.Sql.Table || spec.Owner != own.Handle || spec.Query.Table != nqTeamRows || spec.Query.Joined == nil {
		t.Fatalf("spec = %+v", spec)
	}
	title, touch := store.PartnerName(nqSessionColumns.Name, "title"), store.PartnerName(nqSessionColumns.Name, "lastTouch")
	item, err := target.Decode(store.StoredRead{"team": "u12", "session": nqSession("s1").Key, title: "t1", touch: int64(1)}, background)
	must(t, err)
	if got := nqItems([]delta.JsonValue{item}); !slices.Equal(got, []string{"s1/t1/1"}) {
		t.Fatalf("decoded = %v", got)
	}
	_, err = own.Target(jsonObject("team", float64(3)), background)
	wantChordError(t, err, data.CodeInvalid, data.ReasonArgs, "")
}

func TestNamedQueryAReferenceTellsItsFollowersOfEveryOtherInstanceOfItsObject(t *testing.T) {
	// Mine (Pi's reference.ts `followInstance` is covered through query-reader.test.ts): a restart's successor is told of
	// once, and a released follower is told no more. The caller waits on each new instance being live: a call to the club
	// parks until it is.
	host, _ := nqHosted(t, memoryStore(t), &nqProbe{})
	club := mustGet(t, host, nqClub, jsonObject())
	var moved atomic.Int32
	release := club.Queries("teams")["ofTeam"].Follow(func() { moved.Add(1) })
	restart := func() {
		t.Helper()
		reloadMust(t)(host.Reload([]HostFacet{nqPublisher(nqRows), nqClubPlugin(&nqProbe{})}, background, ReloadOptions{}))
		_, err := club.Call("add", jsonObject("team", "u13", "session", "s0"), background)
		must(t, err)
	}
	restart()
	if got := moved.Load(); got != 1 {
		t.Fatalf("told %d times after one restart, want 1", got)
	}
	restart()
	if got := moved.Load(); got != 2 {
		t.Fatalf("told %d times after two restarts, want 2", got)
	}
	release()
	restart()
	if got := moved.Load(); got != 2 {
		t.Fatalf("told %d times after the release, want 2", got)
	}
}

func TestNamedQueryASetMovedToAnotherPluginsTableInOneReloadIsBoundToTheNewTable(t *testing.T) {
	// Opus S6, round 2 S3: the old owner's binding goes at its seal, not at the end of its teardown. Its native's close ends
	// only when the new owner is live, so its teardown is still running when the new owner binds.
	chordStore := memoryStore(t)
	club := nqClubPlugin(&nqProbe{})
	host, _ := nqHostedWith(t, chordStore, club)
	page := mustGet(t, host, nqTeamPage, jsonObject("team", "u12"))
	closing := make(chan struct{})
	slow := HostFacet{Name: "@test/sessions", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return nqServeSessions(h, nqRows, func(self StoreSelf) {
			self.Open(func(context.Context) (any, error) { return nil, nil },
				func(_ any, _ NativeCloseReason, ctx context.Context) error {
					_, _ = chordctx.Receive(context.WithoutCancel(ctx), closing)
					return nil
				})
		})
	})}
	reloadMust(t)(host.Reload([]HostFacet{slow, club}, background, ReloadOptions{}))
	mustGet(t, host, nqSessions, jsonObject())
	moved := exLocal("app.moved", contracts.Params{}, contracts.Mem("put", contracts.NewMethod(exStr())))
	movedRows := nqMust(store.DefineTable("moved-rows", store.TableOptions{
		Version: 1, Columns: slices.Clone(nqSessionColumns.Schemas), Key: []string{"id"}, Exposes: nqSessionColumns,
	}))
	movedPlugin := HostFacet{Name: "@test/moved", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(moved, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
			self := plain.(StoreSelf)
			rows := self.Table(movedRows)
			return exImpl(nil, map[string]contracts.MethodFunction{
				"put": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					_, err := self.Tx(func(tx documents.Tx) (any, error) {
						return nil, tx.Put(rows, store.Row{"id": nqSession(id.(string)), "app": "club", "title": "moved", "lastTouch": int64(5)})
					}, ctx)
					return nil, err
				},
			}), nil
		}, ServeOptions{})
	})}
	// The sessions' plugin is gone, another plugin exposes the set from its own table.
	report := reloadMust(t)(host.Reload([]HostFacet{club, movedPlugin}, background, ReloadOptions{}))
	if len(report.Failed) != 0 {
		t.Fatalf("failed = %+v", report.Failed)
	}
	rows := mustGet(t, host, moved, jsonObject())
	close(closing)
	_, err := rows.Call("put", "s0", background)
	must(t, err)
	bound, ok := chordStore.Reads.Get(nqSessionColumns.Name)
	if !ok || bound.Table != "app.moved:moved-rows" {
		t.Fatalf("the set is bound to %+v (%v), want app.moved:moved-rows", bound, ok)
	}
	flushed(host)
	qrWant(t, "rows", nqPageRows(t, page), `{"rows":[{"session":"s0","title":"moved","touched":5}],"more":false}`)
}

func TestNamedQueryAHostThatClosedServesNoSetAnymore(t *testing.T) {
	// Go's own check of `Host.close`'s `unbindSets` (the objects' sets are dropped when every object is torn down).
	chordStore := memoryStore(t)
	host, _ := nqHosted(t, chordStore, &nqProbe{})
	if _, bound := chordStore.Reads.Get(nqSessionColumns.Name); !bound {
		t.Fatal("the set is not bound while its publisher serves it")
	}
	must(t, host.Close(background))
	if bound, ok := chordStore.Reads.Get(nqSessionColumns.Name); ok {
		t.Fatalf("the set is still bound after the host closed: %+v", bound)
	}
}

func TestNamedQueryTwoTablesOfOneObjectExposingOneSetAreRefusedWhereTheyAreDeclaredNamingBoth(t *testing.T) {
	twice := exLocal("app.twice", contracts.Params{}, contracts.Mem("n", contracts.Int()))
	second := nqMust(store.DefineTable("second", store.TableOptions{
		Version: 1, Columns: slices.Clone(nqSessionColumns.Schemas), Key: []string{"id"}, Exposes: nqSessionColumns,
	}))
	serve := func(h FacetHost) error {
		return h.Serve(twice, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
			self := plain.(StoreSelf)
			self.Table(nqRows)
			self.Table(second)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"n": self.Derive(nil, func([]any) (delta.JsonValue, error) { return float64(1), nil }),
			}, nil), nil
		}, ServeOptions{})
	}
	const want = `is exposed by tables "rows" and "second"`
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error { return serve(h) }, loadedOptions{Store: memoryStore(t)})
	_, _ = host.Get(background, twice, jsonObject(), GetOptions{})
	all := errs.All()
	if len(all) == 0 || all[len(all)-1].Failure.Error == nil || !strings.Contains(all[len(all)-1].Failure.Error.Message, want) {
		t.Fatalf("the attempt's errors = %+v, want the last to contain %q", all, want)
	}
	// Staging refuses them too, where the reload declares them (Pi's recording `table`).
	bare, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(twice, func(*delta.JsonObject, Self) (contracts.MembersImplementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"n": constant(float64(1))}, nil), nil
		}, ServeOptions{})
	}, loadedOptions{Store: memoryStore(t)})
	report := reloadMust(t)(reloadApp(bare, func(h FacetHost) error { return serve(h) }))
	var staged string
	for _, failure := range report.Failed {
		staged += failure.Error.Error()
	}
	if !strings.Contains(staged, want) {
		t.Fatalf("staging refused with %q, want %q", staged, want)
	}
}

func TestNamedQueryStagingAcceptsTheOwnNamedQueryCountedAsAnAttemptDoes(t *testing.T) {
	// What an attempt accepts, staging accepts: the object's own named query, counted (Pi: `self.count(named(self).all, {})`
	// is the one declaration of that test that is no refusal).
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return nqServeCounted(h, func(StoreSelf) {}) }, loadedOptions{Store: memoryStore(t)})
	counted := mustGet(t, host, nqCounted, jsonObject())
	report := reloadMust(t)(reloadApp(host, func(h FacetHost) error {
		return h.Serve(nqCounted, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
			self := plain.(StoreSelf)
			named := self.Queries(self.Table(nqPlain), QueriesOptions{}, map[string]QueryBuilder{"all": {Args: contracts.Obj(), Build: func(delta.JsonValue) *store.Query { return store.NewQuery(nqPlain) }}})
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"n": self.Count(named["all"], jsonObject())}, nil), nil
		}, ServeOptions{})
	}))
	if len(report.Failed) != 0 {
		t.Fatalf("failed = %+v", report.Failed)
	}
	// The successor counts the rows of an empty table: the member shows 0 once its read ran.
	watch, err := counted.Member("n").Watch(background)
	must(t, err)
	values := make(chan delta.JsonValue, 8)
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			values <- call.Value
		}
		return nil
	})
	value, err := counted.Member("n").Value()
	must(t, err)
	for value != float64(0) {
		value = <-values
	}
	_, _ = watch.Stop()
}

// nqTitles is the titles of the page's rows, in order.
func nqTitles(t *testing.T, page *Reference) []string {
	t.Helper()
	value, err := page.Member("sessions").Value()
	if err != nil {
		return nil
	}
	var titles []string
	for _, row := range value.(*delta.JsonObject).Value("rows").([]delta.JsonValue) {
		titles = append(titles, row.(*delta.JsonObject).Value("title").(string))
	}
	return titles
}

func TestNamedQueryAUsedColumnDroppedFailsExactlyTheReadsUsingItBeforeAnySQLRestoredTheyHeal(t *testing.T) {
	club := nqClubPlugin(&nqProbe{})
	host, _ := nqHostedWith(t, memoryStore(t), club)
	page := mustGet(t, host, nqTeamPage, jsonObject("team", "u12"))
	watch, err := page.Member("sessions").Watch(background)
	must(t, err)
	calls := nexttest.ChangesOf(watch)
	narrow := nqMust(store.DefineColumns("sessions.columns", store.ColumnSetOptions{
		Columns: []store.ColumnProp{store.Col("id", store.Host(Session)), store.Col("app", store.Str()), store.Col("lastTouch", store.Int())},
		Key:     []string{"id"},
	}))
	dropped := nqMust(store.EvolveTable(nqRows, func(e *store.TableEditor) store.TableTailEditor { return e.Drop("title") }, store.EvolveOptions{Exposes: narrow}))
	reloadMust(t)(host.Reload([]HostFacet{nqPublisher(dropped), club}, background, ReloadOptions{}))
	// The read fails when the narrower set is bound (its query compiles no more): the watch gets that failure, once.
	flushed(host)
	deliveredOn(host)
	want := `column "title" of column set "sessions.columns" is not what this read was written for`
	if got := nqFailedMessages(calls.Items()); len(got) != 1 || got[0] != want || calls.Len() != 1 {
		t.Fatalf("the watch got %v (%d calls), want one failure %q", got, calls.Len(), want)
	}
	// The store is usable: another owner still commits. Restored, the read heals.
	_, err = mustGet(t, host, nqClub, jsonObject()).Call("add", jsonObject("team", "u14", "session", "s0"), background)
	must(t, err)
	restored := nqMust(store.EvolveTable(dropped, func(e *store.TableEditor) store.TableTailEditor {
		return e.Add("title", store.Str(contracts.Default("restored")))
	}, store.EvolveOptions{Exposes: nqSessionColumns}))
	reloadMust(t)(host.Reload([]HostFacet{nqPublisher(restored), club}, background, ReloadOptions{}))
	flushed(host)
	deliveredOn(host)
	if got := nqTitles(t, page); !slices.Equal(got, []string{"restored", "restored"}) {
		t.Fatalf("the page's titles = %v, want restored twice", got)
	}
	_, _ = watch.Stop()
}

func TestNamedQueryASetExposedNoMoreIsNotServedTwoTablesExposingOneNameAreRefusedNamingBoth(t *testing.T) {
	club := nqClubPlugin(&nqProbe{})
	host, errs := nqHostedWith(t, memoryStore(t), club)
	page := mustGet(t, host, nqTeamPage, jsonObject("team", "u12"))
	hidden := nqMust(store.EvolveTable(nqRows, func(e *store.TableEditor) store.TableTailEditor { return e.Add("note", store.Opt(store.Str())) }, store.EvolveOptions{ExposesNone: true}))
	reloadMust(t)(host.Reload([]HostFacet{nqPublisher(hidden), club}, background, ReloadOptions{}))
	nqWaitFails(t, host, page, `column set "sessions.columns" is not served`)
	exposed := nqMust(store.EvolveTable(hidden, func(e *store.TableEditor) store.TableTailEditor { return e.Add("more", store.Opt(store.Str())) }, store.EvolveOptions{Exposes: nqSessionColumns}))
	reloadMust(t)(host.Reload([]HostFacet{nqPublisher(exposed), club}, background, ReloadOptions{}))
	flushed(host)
	deliveredOn(host)
	value, err := page.Member("sessions").Value()
	if err != nil || nexttest.JSONText(t, value) != `{"rows":[{"session":"s1","title":"t1","touched":1},{"session":"s0","title":"t0","touched":0}],"more":false}` {
		t.Fatalf("the page's value = %v, %v; want its rows back", value, err)
	}
	// Another owner exposing the same name: its activation fails, the binding stays.
	thief := exLocal("app.thief", contracts.Params{}, contracts.Mem("n", exInt()))
	stolen := nqMust(store.DefineTable("stolen", store.TableOptions{
		Version: 1, Columns: slices.Clone(nqSessionColumns.Schemas), Key: []string{"id"}, Exposes: nqSessionColumns,
	}))
	thieves := HostFacet{Name: "@test/thief", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(thief, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
			self := plain.(StoreSelf)
			self.Table(stolen)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"n": self.Derive(nil, func([]any) (delta.JsonValue, error) { return float64(1), nil })}, nil), nil
		}, ServeOptions{})
	})}
	reloadMust(t)(host.Reload([]HostFacet{nqPublisher(exposed), club, thieves}, background, ReloadOptions{}))
	_, _ = host.Get(background, thief, jsonObject(), GetOptions{})
	deliveredOn(host)
	if all := failures(errs.All()); len(all) == 0 || all[len(all)-1] != "app.thief: broken" {
		t.Fatalf("the failures = %v, want the thief's activation failed last", all)
	}
	last := errs.All()[len(errs.All())-1]
	if last.Failure.Error == nil || !strings.Contains(last.Failure.Error.Message, `exposed by tables "app.sessions:rows" and "app.thief:stolen"`) {
		t.Fatalf("the last error = %+v, want the two tables named", last)
	}
	if got := nqTitles(t, page); len(got) != 2 {
		t.Fatalf("the page's rows after the refusal = %v, want 2", got)
	}
}

func TestNamedQueryAReaderNeedsAStore(t *testing.T) {
	// Pi: `reading a named query needs a store` (`readerSite`); no Pi case reaches it.
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(nqCounted, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"n": self.Count(nqForeign{}, jsonObject())}, nil), nil
		}, ServeOptions{})
	}, loadedOptions{})
	_, _ = host.Get(background, nqCounted, jsonObject(), GetOptions{})
	nqExpectOne(t, errMessages(errs), "app.counted: reading a named query needs a store")
}

// errMessages is the messages of the host's reported errors.
func errMessages(errs *hostErrors) []string {
	var messages []string
	for _, err := range errs.All() {
		if err.Failure.Error != nil {
			messages = append(messages, err.Failure.Error.Message)
		} else {
			messages = append(messages, string(err.Kind))
		}
	}
	return messages
}

// nqPageRows is the rows of the team page's value, as JSON text.
func nqPageRows(t *testing.T, page *Reference) string {
	t.Helper()
	return qrValue(t, page, "sessions")
}

func TestNamedQueryAJoinedQueryMapsEachRowWithItsPartnersProjectedColumnsInThePartnersOrder(t *testing.T) {
	probe := &nqProbe{}
	host, _ := nqHosted(t, memoryStore(t), probe)
	page := mustGet(t, host, nqTeamPage, jsonObject("team", "u12"))
	// The family's session is no club session: its team row is dropped.
	qrWant(t, "rows", nqPageRows(t, page), `{"rows":[{"session":"s1","title":"t1","touched":1},{"session":"s0","title":"t0","touched":0}],"more":false}`)
	if got := probe.last(); len(got) != 2 || got[0] != "lastTouch" || got[1] != "title" {
		t.Fatalf("partner columns = %v, want [lastTouch title]", got)
	}
	// The sessions' owner commits: the club's read over its table runs in that owner's step.
	_, err := mustGet(t, host, nqSessions, jsonObject()).Call("touch", jsonObject("id", "s0", "at", float64(9)), background)
	must(t, err)
	flushed(host)
	qrWant(t, "rows", nqPageRows(t, page), `{"rows":[{"session":"s0","title":"t0","touched":9},{"session":"s1","title":"t1","touched":1}],"more":false}`)
	closeHosts(t, host)
}

func TestNamedQueryAColumnSetAcrossItsPublishersReloadsUnchangedCodeAndAnUnusedColumnAddedNoReaderRestartsEqualRowsSendNothing(t *testing.T) {
	club := nqClubPlugin(&nqProbe{})
	host, errs := nqHostedWith(t, memoryStore(t), club)
	page := mustGet(t, host, nqTeamPage, jsonObject("team", "u12"))
	watch, err := page.Member("sessions").Watch(background)
	must(t, err)
	calls := nexttest.ChangesOf(watch)
	reloadMust(t)(host.Reload([]HostFacet{nqPublisher(nqRows), club}, background, ReloadOptions{}))
	flushed(host)
	wider := nqMust(store.EvolveTable(nqRows, func(e *store.TableEditor) store.TableTailEditor { return e.Add("note", store.Opt(store.Str())) }))
	reloadMust(t)(host.Reload([]HostFacet{nqPublisher(wider), club}, background, ReloadOptions{}))
	flushed(host)
	deliveredOn(host)
	if calls.Len() != 0 || len(failures(errs.All())) != 0 {
		t.Fatalf("calls = %d, failures = %v; want none", calls.Len(), failures(errs.All()))
	}
	// The migrated table still feeds the reader.
	_, err = mustGet(t, host, nqSessions, jsonObject()).Call("touch", jsonObject("id", "s0", "at", float64(9)), background)
	must(t, err)
	deliveredOn(host)
	qrWant(t, "rows", nqPageRows(t, page), `{"rows":[{"session":"s0","title":"t0","touched":9},{"session":"s1","title":"t1","touched":1}],"more":false}`)
	if calls.Len() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Len())
	}
	_, _ = watch.Stop()
	closeHosts(t, host)
}
