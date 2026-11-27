package host

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/internal/storeseam"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/test/host/tiling.test.ts (Pi 0db9861ec018, 762 lines): 29 cases (commit group x4, one commit several
// pages x2, a row another owner's commit lets in x1, publisher migration x1, switches and failures x5, invariant x16).
// Subtest names are Pi's `it` texts. Support: host_list_support_test.go.
//
// Go forms (Pi's one thread is gone, the order of what Pi starts "in one tick" is made explicit):
//   - `store.commit = (...) => ...` (a wrapper around every commit, run when it is queued) is `storeseam.WrapAdopt`, called
//     as a commit takes its place in the queue, which returns the adoption the commit uses. `commitLog` counts
//     the enlisted commits: `startInOrder` starts each call on a goroutine and waits until its commit enlisted before it
//     starts the next, so the commits queue in the order Pi's calls are made and `probe.pending` (shifted at enlist) meets
//     the commit it was queued for.
//   - "the filler's runs alone, the other two as one group": the filler's commit is held in its backend transaction
//     (faultyStore's holdNextCommit) until the two others enlisted behind it, then released: no timing.
//   - calls Pi makes in one tick (the rollback case's three) enlist in call order and form one group: the commit queue is held
//     (ChordStore.Take) while they start, each once its commit took its place behind the hold, then released (`inOneGroup`).
//   - `store.reads.on(set)[i].run = ...` (a run that fails) is `tlReadFaults.failRuns(read, fail)`: the faulty backend fails the
//     SELECT that read runs in a commit's step with the error `fail` returns (`restore` lets it run for real). The read's run
//     returns a failed run as it does for any backend error (Pi's `run` catches the executor's throw, read.ts:83-87): the
//     cause is a real one, no store seam is needed.
//   - `await flushed()` after a group is `flushed(host)` then waiting for the store's OnCommitted listener to report the
//     last seq (`commitLog.reported`): the listener runs on a goroutine of the store. The same holds for the `await` of a
//     commit before a group, after which Pi has its announcement delivered (a microtask of that commit's turn): `group` waits
//     for the last seq to be reported before it counts.
//   - `screen.list.value` thrown is the error of Member("list").Value().

func mustColumns(name string, options store.ColumnSetOptions) *store.ColumnSet {
	set, err := store.DefineColumns(name, options)
	if err != nil {
		panic(err)
	}
	return set
}

var (
	tlSessionProps   = []store.ColumnProp{store.Col("id", store.Str()), store.Col("title", store.Str()), store.Col("touched", store.Int())}
	tlSessionColumns = mustColumns("sessions.columns", store.ColumnSetOptions{Columns: tlSessionProps, Key: []string{"id"}})
	tlSessionRows    = mustTableDef("session-rows", store.TableOptions{Version: 1, Columns: tlSessionProps, Key: []string{"id"}, Exposes: tlSessionColumns})
	tlSession        = contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("title", contracts.Str()), contracts.Prop("touched", contracts.Int()))
	// One transaction of the sessions' owner: rows put, touched, renamed and dropped; `overflow` adds past the integer range:
	// refused in the commit step (the column's check), not at staging.
	tlSessionWrites = contracts.Obj(
		contracts.Prop("put", contracts.Opt(contracts.Arr(tlSession))),
		contracts.Prop("touch", contracts.Opt(contracts.Arr(contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("touched", contracts.Int()))))),
		contracts.Prop("rename", contracts.Opt(contracts.Arr(contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("title", contracts.Str()))))),
		contracts.Prop("drop", contracts.Opt(contracts.Arr(contracts.Str()))),
		contracts.Prop("overflow", contracts.Opt(contracts.Arr(contracts.Str()))),
	)
	tlSessions = mustContract(contracts.Local("app.sessions", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("write", contracts.NewMethod(tlSessionWrites))}}))

	tlTeamRows = mustTableDef("team-rows", store.TableOptions{Version: 1,
		Columns: []store.ColumnProp{store.Col("team", store.Str()), store.Col("session", store.Str())}, Key: []string{"team", "session"}})
	tlMembership = contracts.Obj(contracts.Prop("team", contracts.Str()), contracts.Prop("session", contracts.Str()))
	tlClub       = mustContract(contracts.Local("app.club", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("teams", contracts.Queries(tlSession, []contracts.Property{contracts.Prop("ofTeam", contracts.Obj(contracts.Prop("team", contracts.Str())))})),
			contracts.Mem("write", contracts.NewMethod(contracts.Obj(
				contracts.Prop("add", contracts.Opt(contracts.Arr(tlMembership))), contracts.Prop("remove", contracts.Opt(contracts.Arr(tlMembership)))))),
		}}))

	tlFillRows = mustTableDef("fill-rows", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Int())}, Key: []string{"id"}})
	// A third owner: its commit runs first, so the two that queue behind it form one group.
	tlFiller = mustContract(contracts.Local("app.filler", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("write", contracts.NewMethod(contracts.Int()))}}))

	tlScreen = mustContract(contracts.Local("app.screen", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{contracts.Prop("view", contracts.Str())},
		Members: []contracts.LocalEntry{
			contracts.Mem("list", contracts.Obj(contracts.Prop("rows", contracts.Arr(tlSession)), contracts.Prop("moreAfter", contracts.Bool()))),
			contracts.Mem("more", contracts.NewMethod()),
			contracts.Mem("show", contracts.NewMethod(contracts.Str())),
		}}))
)

// tlArray is `args.<key> ?? []`.
func tlArray(args *delta.JsonObject, key string) []*delta.JsonObject {
	out := []*delta.JsonObject{}
	if value, ok := args.Get(key); ok && value != nil {
		for _, item := range value.([]any) {
			out = append(out, item.(*delta.JsonObject))
		}
	}
	return out
}

func tlStrings(args *delta.JsonObject, key string) []string {
	out := []string{}
	if value, ok := args.Get(key); ok && value != nil {
		for _, item := range value.([]any) {
			out = append(out, item.(string))
		}
	}
	return out
}

// tlTx runs write as one transaction of self (Pi's `await self.tx(async (tx) => ..., ctx)`).
func tlTx(self Self, ctx context.Context, write func(tx documents.Tx) error) (delta.JsonValue, error) {
	_, err := self.Tx(func(tx documents.Tx) (any, error) { return nil, write(tx) }, ctx)
	return nil, err
}

func tlServeSessions(h FacetHost) error {
	return h.Serve(tlSessions, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		self := plain.(StoreSelf)
		rows := self.Table(tlSessionRows)
		return contracts.MembersImplementation{Methods: map[string]contracts.MethodFunction{
			"write": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
				return tlTx(self, ctx, func(tx documents.Tx) error {
					for _, id := range tlStrings(args, "overflow") {
						if err := tx.Update(rows, store.Row{"id": id}, func(r store.ColumnRefs) map[string]any {
							return map[string]any{"touched": store.Add(r["touched"], int64(9007199254740991))}
						}); err != nil {
							return err
						}
					}
					for _, row := range tlArray(args, "put") {
						if err := tx.Put(rows, store.Row{"id": row.Value("id"), "title": row.Value("title"), "touched": int64(row.Value("touched").(float64))}); err != nil {
							return err
						}
					}
					for _, row := range tlArray(args, "touch") {
						if err := tx.Update(rows, store.Row{"id": row.Value("id")}, store.Row{"touched": int64(row.Value("touched").(float64))}); err != nil {
							return err
						}
					}
					for _, row := range tlArray(args, "rename") {
						if err := tx.Update(rows, store.Row{"id": row.Value("id")}, store.Row{"title": row.Value("title")}); err != nil {
							return err
						}
					}
					for _, id := range tlStrings(args, "drop") {
						if err := tx.Delete(rows, store.Row{"id": id}); err != nil {
							return err
						}
					}
					return nil
				})
			}),
		}}, nil
	}, ServeOptions{})
}

func tlServeClub(h FacetHost) error {
	return h.Serve(tlClub, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		self := plain.(StoreSelf)
		rows := self.Table(tlTeamRows)
		teams := self.Queries(
			rows,
			QueriesOptions{
				Map: func(_ store.Row, session PartnerRow) (delta.JsonValue, error) {
					return jsonObject("id", session["id"], "title", session["title"], "touched", float64(session["touched"].(int64))), nil
				},
				Reads: []string{},
			},
			map[string]QueryBuilder{
				// The key (team, session): driven from the team's rows, ordered by the sessions' last touch.
				"ofTeam": {Build: func(args delta.JsonValue) *store.Query {
					team := args.(*delta.JsonObject).Value("team").(string)
					if team == "broken" {
						panic(errors.New("no such team"))
					}
					return store.NewQuery(tlTeamRows).
						Where(func(t, _ store.ColumnRefs) store.Condition { return store.Eq(t["team"], team) }).
						Join(tlSessionColumns, func(t store.ColumnRefs) store.Match { return store.Match{{Column: "id", Ref: t["session"]}} }).
						OrderBy(func(_, session store.ColumnRefs) *store.ColumnRef { return session["touched"] }, "desc")
				}},
			})
		return contracts.MembersImplementation{
			Queries: map[string]map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]{"teams": teams},
			Methods: map[string]contracts.MethodFunction{
				"write": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return tlTx(self, ctx, func(tx documents.Tx) error {
						for _, row := range tlArray(args, "remove") {
							if err := tx.Delete(rows, store.Row{"team": row.Value("team"), "session": row.Value("session")}); err != nil {
								return err
							}
						}
						for _, row := range tlArray(args, "add") {
							if err := tx.Put(rows, store.Row{"team": row.Value("team"), "session": row.Value("session")}); err != nil {
								return err
							}
						}
						return nil
					})
				}),
			},
		}, nil
	}, ServeOptions{})
}

func tlServeFiller(h FacetHost) error {
	return h.Serve(tlFiller, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		self := plain.(StoreSelf)
		rows := self.Table(tlFillRows)
		return contracts.MembersImplementation{Methods: map[string]contracts.MethodFunction{
			"write": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return tlTx(self, ctx, func(tx documents.Tx) error { return tx.Put(rows, store.Row{"id": int64(id.(float64))}) })
			},
		}}, nil
	}, ServeOptions{})
}

// tlServeScreen serves a `Screen` over `contract`'s list of `name` from `source` (the team's sessions, or a test's own).
func tlServeScreen(h FacetHost, screen *contracts.Contract, name string, first int, initial any, source func(self Self, args delta.JsonValue) ListSource) error {
	return h.Serve(screen, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
		team := self.Writable(initial)
		args := self.Derive([]any{team}, func(values []any) (delta.JsonValue, error) { return jsonObject("team", values[0]), nil })
		handle := self.List(name, []ListSource{source(self, args)}, ListOptions{First: first})
		return contracts.MembersImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"list": handle.Value()},
			Methods: map[string]contracts.MethodFunction{
				"more": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return nil, handle.More(ctx) },
				"show": func(shown delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					return nil, team.Set(shown.(string))
				},
			},
		}, nil
	}, ServeOptions{})
}

func tlFacetOf(first int) Facet {
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return firstError(tlServeSessions(h), tlServeClub(h), tlServeFiller(h),
			tlServeScreen(h, tlScreen, "team", first, "t", func(self Self, args delta.JsonValue) ListSource {
				return ListSource{Query: self.Get(tlClub, jsonObject()).Queries("teams")["ofTeam"], Args: args}
			}))
	})
}

// tlProbe is what the store's wrapper does around every commit's adoption: `pending` is what each commit wrote (taken when it
// enlists), applied to the truth before the adoption; `adopted` runs after it.
type tlProbe struct {
	mu      sync.Mutex
	pending []func()
	adopted func()
}

func (p *tlProbe) push(wrote func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending = append(p.pending, wrote)
}
func (p *tlProbe) shift() func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.pending) == 0 {
		return nil
	}
	wrote := p.pending[0]
	p.pending = p.pending[1:]
	return wrote
}

// tlReadFaults is a backend whose commit steps can fail the SELECT of one read: what Pi's tests get by assigning `read.run`
// (a page's run in a commit's step fails while another page's runs well, "a failed page ..."). The statement fails as a storage
// error in the step does: the read's run is a failed run, the commit and the store go on. A read's first load runs outside a
// commit's step and is not touched.
type tlReadFaults struct {
	sqlite.SqliteDatabase
	mu   sync.Mutex
	fail func(sql string, params []sqlite.SqliteValue) error
}

func (db *tlReadFaults) Transaction(callback func(transaction sqlite.SqliteExecutor) error) error {
	return db.SqliteDatabase.Transaction(func(transaction sqlite.SqliteExecutor) error {
		return callback(&tlFaultingExecutor{SqliteExecutor: transaction, db: db})
	})
}

func (db *tlReadFaults) set(fail func(sql string, params []sqlite.SqliteValue) error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.fail = fail
}

func (db *tlReadFaults) asked(sql string, params []sqlite.SqliteValue) error {
	db.mu.Lock()
	fail := db.fail
	db.mu.Unlock()
	if fail == nil {
		return nil
	}
	return fail(sql, params)
}

// failRuns makes the statement read runs in a commit's step fail with the error fail returns (nil: it runs for real), each
// time it is asked; fail may restore. The statement is the one `RegisteredRead.Fetch` asks of its plan: one row more than the
// range's limit.
func (db *tlReadFaults) failRuns(read *store.RegisteredRead, fail func() error) {
	rng := read.What.Range
	rng.Limit++
	want := read.Plan.Select(rng)
	db.set(func(sql string, params []sqlite.SqliteValue) error {
		if sql != want.SQL || !reflect.DeepEqual(params, want.Params) {
			return nil
		}
		return fail()
	})
}

// restore lets every run go for real again.
func (db *tlReadFaults) restore() { db.set(nil) }

type tlFaultingExecutor struct {
	sqlite.SqliteExecutor
	db *tlReadFaults
}

func (e *tlFaultingExecutor) All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	if err := e.db.asked(sql, params); err != nil {
		return nil, err
	}
	return e.SqliteExecutor.All(sql, params...)
}

// tlFaultyStore is faultyStore (host_next_support_test.go) with the read faults of tlReadFaults: its holdNextCommit, and the
// faults a test sets on a page's read.
func tlFaultyStore(t testing.TB) (*store.ChordStore, func() *transactionGate, *tlReadFaults) {
	t.Helper()
	var db *faultyDatabase
	faults := &tlReadFaults{}
	chordStore := openMemoryStore(t, func(inner sqlite.SqliteDatabase) sqlite.SqliteDatabase {
		faults.SqliteDatabase = inner
		db = &faultyDatabase{SqliteDatabase: faults, failing: map[string]bool{}}
		return db
	}, nil)
	return chordStore, func() *transactionGate {
		gate := newTransactionGate()
		db.mu.Lock()
		db.gate = gate
		db.mu.Unlock()
		return gate
	}, faults
}

type tlWorld struct {
	t        *testing.T
	store    *store.ChordStore
	host     Host
	sessions *Reference
	club     *Reference
	filler   *Reference
	screen   *Reference
	probe    *tlProbe
	commits  *commitLog
	hold     func() *transactionGate
	faults   *tlReadFaults
}

// tlHosted is `hosted(first, seed)`: a host with the three owners and a screen of team "t" in pages of `first`; sessions s1 to
// s`seed`, touched at their number, all in the team. Every commit's adoption is followed by `probe.adopted()`, after the first
// of `probe.pending` (what that commit wrote) ran.
func tlHosted(t *testing.T, first, seed int) *tlWorld {
	t.Helper()
	chordStore, holdNextCommit, faults := tlFaultyStore(t)
	host := newHost(t, HostOptions{Store: chordStore, OnError: (&hostErrors{}).push})
	_, err := host.Load([]HostFacet{{Name: "@test/team", Facet: tlFacetOf(first)}}, background)
	must(t, err)
	w := &tlWorld{t: t, store: chordStore, host: host, probe: &tlProbe{adopted: func() {}}, hold: holdNextCommit, faults: faults,
		sessions: mustGet(t, host, tlSessions, jsonObject()), club: mustGet(t, host, tlClub, jsonObject()), filler: mustGet(t, host, tlFiller, jsonObject())}
	ids := []string{}
	for index := range seed {
		ids = append(ids, fmt.Sprintf("s%d", index+1))
	}
	puts := []any{}
	for index, id := range ids {
		puts = append(puts, tlSessionRow(id, "old "+id, index+1))
	}
	mustCall(t, w.sessions, "write", jsonObject("put", puts))
	mustCall(t, w.club, "write", jsonObject("add", tlMembers("t", ids...)))
	w.screen = mustGet(t, host, tlScreen, jsonObject("view", "v"))
	// From here on: a commit takes what it wrote from `pending` when it is queued, and tells the probe once adopted.
	w.install()
	return w
}

func (w *tlWorld) close() { closeHosts(w.t, w.host) }

func (w *tlWorld) call(ref *Reference, args *delta.JsonObject) error {
	_, err := ref.Call("write", args, background)
	return err
}
func (w *tlWorld) sessionsWrite(args *delta.JsonObject) {
	w.t.Helper()
	mustCall(w.t, w.sessions, "write", args)
}
func (w *tlWorld) clubWrite(args *delta.JsonObject) {
	w.t.Helper()
	mustCall(w.t, w.club, "write", args)
}
func (w *tlWorld) more()            { w.t.Helper(); mustCall(w.t, w.screen, "more", nil) }
func (w *tlWorld) show(team string) { w.t.Helper(); mustCall(w.t, w.screen, "show", team) }

// startInOrder is commitLog.startInOrder over the world's commits.
func (w *tlWorld) startInOrder(calls ...func()) (wait func()) {
	return w.commits.startInOrder(calls...)
}

// queued is `await Promise.all([filler.write(1), ...calls])`: the filler's commit runs alone (held in its backend
// transaction), the calls' commits queue behind it as one group, then it is released.
func (w *tlWorld) queued(calls ...func()) {
	w.t.Helper()
	held := w.hold()
	wait := w.startInOrder(func() { mustCall(w.t, w.filler, "write", 1.0) })
	<-held.Entered()
	wait2 := w.startInOrder(calls...)
	held.Release()
	wait()
	wait2()
	flushed(w.host)
	w.commits.waitReported(w.store.LastSeq())
}

// inOneGroup is what Pi's calls of one tick are: their commits enlist in call order before the commit queue's turn comes, so
// they form ONE group (a refused one in it rolls the group back). Go: the commit queue is taken (ChordStore.Take, an export's
// hold: the commit of no owner stands in for it), the calls start in order, each once its commit took its place behind the
// hold, then the queue is released.
func (w *tlWorld) inOneGroup(calls ...func()) {
	w.t.Helper()
	release := <-w.store.Take(func() {})
	defer release()
	wait := w.startInOrder(calls...)
	release()
	wait()
	flushed(w.host)
	w.commits.waitReported(w.store.LastSeq())
}

// group queues the two owners' commits behind the filler's and checks they formed one group. Its baseline is taken once the
// listener reported every commit made before it (a case's `sessions.write(put)`): Pi's `await` of such a commit returns after
// its announcement ran (`#committed` queues the microtask in the turn that settles the commit, ahead of the later hops of the
// awaiting chain), Go's runs on a goroutine of the store, and the filler's commit adopted before it started is announced WITH
// it: one notification over the earlier commit and the filler, a jump of two that is no group of the commit queue's.
func (w *tlWorld) group(first, second func()) {
	w.t.Helper()
	w.commits.waitReported(w.store.LastSeq())
	before := w.commits.grouped()
	w.queued(first, second)
	if got := w.commits.grouped(); got != before+1 {
		w.t.Fatalf("groups = %d, want %d: the two commits formed one group", got, before+1)
	}
}

func (w *tlWorld) value() (rows []any, moreAfter bool) {
	w.t.Helper()
	return mustListValue(w.t, w.screen)
}
func (w *tlWorld) ids() []string { rows, _ := w.value(); return sessionIDs(rows) }
func (w *tlWorld) shown() string { rows, _ := w.value(); return tlShown(rows) }

// tlShown is `list.rows.map((row) => `${row.id}:${row.title}`).join(" ")`.
func tlShown(rows []any) string {
	parts := []string{}
	for _, row := range rows {
		object := row.(*delta.JsonObject)
		parts = append(parts, object.Value("id").(string)+":"+object.Value("title").(string))
	}
	return strings.Join(parts, " ")
}

// throws is `expect(() => screen.list.value).toThrow(message)`.
func (w *tlWorld) throws(message string) {
	w.t.Helper()
	if _, _, err := listMemberValue(w.screen); err == nil || !strings.Contains(err.Error(), message) {
		w.t.Fatalf("list.value error = %v, want %q", err, message)
	}
}

func tlSessionRow(id, title string, touched int) *delta.JsonObject {
	return jsonObject("id", id, "title", title, "touched", float64(touched))
}

// tlMembers are `sessions.map((session) => ({ team, session }))`.
func tlMembers(team string, sessions ...string) []any {
	out := []any{}
	for _, session := range sessions {
		out = append(out, jsonObject("team", team, "session", session))
	}
	return out
}

func tlNumbered(ns ...int) []string {
	out := []string{}
	for _, n := range ns {
		out = append(out, fmt.Sprintf("s%d", n))
	}
	return out
}

func tlStringsAny(ids ...string) []any {
	out := []any{}
	for _, id := range ids {
		out = append(out, id)
	}
	return out
}

func tlRenames(title string, ids ...string) []any {
	out := []any{}
	for _, id := range ids {
		out = append(out, jsonObject("id", id, "title", title))
	}
	return out
}

func tlEqual(t *testing.T, what string, got, want any) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// Pi: describe("a commit group of two owners")
func TestTilingACommitGroupOfTwoOwners(t *testing.T) {
	t.Run("a run patched into a merged page: the page knows its rows, so a later rename and delete reach it", func(t *testing.T) {
		// Review round 1 (Fable B1, Opus S2): pages [s5 s4] [s3 s2] [s1].
		w := tlHosted(t, 2, 5)
		w.more()
		w.more()
		tlEqual(t, "ids", w.ids(), []string{"s5", "s4", "s3", "s2", "s1"})
		// The club removes s3 (its adoption merges the second and third page); the sessions' owner moves s5 to the end
		// (its run for the old third page lands in the merged one).
		w.group(
			func() { w.clubWrite(jsonObject("remove", tlMembers("t", "s3"))) },
			func() { w.sessionsWrite(jsonObject("touch", []any{jsonObject("id", "s5", "touched", 0.0)})) },
		)
		tlEqual(t, "ids", w.ids(), []string{"s4", "s2", "s1", "s5"})
		w.sessionsWrite(jsonObject("rename", tlRenames("NEW", "s5")))
		if !strings.Contains(w.shown(), "s5:NEW") {
			t.Fatalf("shown = %q, want it to contain s5:NEW", w.shown())
		}
		w.sessionsWrite(jsonObject("drop", tlStringsAny("s5")))
		tlEqual(t, "ids", w.ids(), []string{"s4", "s2", "s1"})
		w.close()
	})

	t.Run("C1 merges P and Q, C2's run for Q's old interval changes its row and nothing else; then both orders", func(t *testing.T) {
		for _, clubFirst := range []bool{true, false} {
			// Pages P [s6 s5 s4 s3], Q [s2 s1] (first 4): C1 leaves P with [s6] and Q with [s2 s1]; C2 renames s1.
			w := tlHosted(t, 4, 6)
			w.more()
			c1 := func() { w.clubWrite(jsonObject("remove", tlMembers("t", "s5", "s4", "s3"))) }
			c2 := func() { w.sessionsWrite(jsonObject("rename", tlRenames("renamed", "s1"))) }
			if clubFirst {
				w.group(c1, c2)
			} else {
				w.group(c2, c1)
			}
			rows, moreAfter := w.value()
			tlEqual(t, "list", nexttest.JSONText(t, jsonObject("rows", rows, "moreAfter", moreAfter)),
				`{"rows":[{"id":"s6","title":"old s6","touched":6},{"id":"s2","title":"old s2","touched":2},{"id":"s1","title":"renamed","touched":1}],"moreAfter":false}`)
			// The merged page is one read that knows all three rows: a touch of each moves it, a delete removes it.
			w.sessionsWrite(jsonObject("touch", []any{jsonObject("id", "s1", "touched", 9.0)}))
			w.sessionsWrite(jsonObject("drop", tlStringsAny("s2")))
			tlEqual(t, "ids", w.ids(), []string{"s1", "s6"})
			w.close()
		}
	})

	t.Run("a page cut by the first commit's adoption takes the second commit's run for the uncut page", func(t *testing.T) {
		// One page [s3 s2 s1] of first 2 (open, short of a row behind); C1 adds five sessions' team rows (the page is
		// cut), C2 renames s2 (its run read the page as it was before the cut).
		w := tlHosted(t, 2, 3)
		w.more()
		tlEqual(t, "ids", w.ids(), []string{"s3", "s2", "s1"})
		puts := []any{}
		for _, n := range []int{4, 5, 6, 7, 8} {
			puts = append(puts, tlSessionRow(fmt.Sprintf("s%d", n), fmt.Sprintf("old s%d", n), n))
		}
		w.sessionsWrite(jsonObject("put", puts))
		w.group(
			func() { w.clubWrite(jsonObject("add", tlMembers("t", tlNumbered(4, 5, 6, 7, 8)...))) },
			func() { w.sessionsWrite(jsonObject("rename", tlRenames("renamed", "s2"))) },
		)
		for range 6 {
			if _, moreAfter := w.value(); !moreAfter {
				break
			}
			w.more()
		}
		tlEqual(t, "shown", w.shown(), "s8:old s8 s7:old s7 s6:old s6 s5:old s5 s4:old s4 s3:old s3 s2:renamed s1:old s1")
		// Every page knows its rows: each is still reached.
		w.sessionsWrite(jsonObject("drop", tlStringsAny("s8", "s5", "s2")))
		tlEqual(t, "ids", w.ids(), []string{"s7", "s6", "s4", "s3", "s1"})
		w.close()
	})

	t.Run("a group that rolled back and ran again alone leaves the same pages", func(t *testing.T) {
		w := tlHosted(t, 2, 5)
		w.more()
		w.more()
		// The sessions' commit refuses in its step (an integer past its range): the group rolls back, each commit runs
		// again alone, and the club's stands. Pi makes the three calls in one tick: the refused one first, then the filler's,
		// then the club's, and their commits enlist in that order and form one group (Pi's probe: three enlists, three
		// admissions, the refused one rejected, the other two adopted one by one). Go: `inOneGroup`.
		var refused error
		w.inOneGroup(
			func() { refused = w.call(w.sessions, jsonObject("overflow", tlStringsAny("s1"))) },
			func() { mustCall(w.t, w.filler, "write", 1.0) },
			func() { w.clubWrite(jsonObject("remove", tlMembers("t", "s3"))) },
		)
		if refused == nil {
			t.Fatal("the overflow commit was not refused")
		}
		tlEqual(t, "ids", w.ids(), []string{"s5", "s4", "s2", "s1"})
		w.sessionsWrite(jsonObject("touch", []any{jsonObject("id", "s1", "touched", 50.0)}))
		tlEqual(t, "ids", w.ids(), []string{"s1", "s5", "s4", "s2"})
		w.close()
	})
}

// Pi: describe("one commit, several pages")
func TestTilingOneCommitSeveralPages(t *testing.T) {
	t.Run("a merge by the first page's run and the second page's run after it: the merged page knows the new row", func(t *testing.T) {
		// Review round 1 (Astra B1): pages [s6 s5 s4 s3] [s2 s1]; one commit of the club drops four rows and adds s7
		// (touched between s3 and s1).
		w := tlHosted(t, 4, 6)
		w.more()
		w.sessionsWrite(jsonObject("put", []any{tlSessionRow("s7", "old s7", 2)}))
		w.clubWrite(jsonObject("remove", tlMembers("t", "s6", "s5", "s4", "s2"), "add", tlMembers("t", "s7")))
		tlEqual(t, "ids", w.ids(), []string{"s3", "s7", "s1"})
		w.clubWrite(jsonObject("remove", tlMembers("t", "s7")))
		tlEqual(t, "ids", w.ids(), []string{"s3", "s1"})
		w.close()
	})

	t.Run("a cut run patched into a merged page keeps its continuation: more rows are behind, and more loads them", func(t *testing.T) {
		// Review round 1 (Astra B2): the second page's run returns four of ten new rows, cut by its limit; the page
		// merged meanwhile must not claim the list is complete. With rows left in the first page, and with none.
		for _, emptied := range [][]string{{"s6", "s5", "s4", "s2"}, {"s6", "s5", "s4", "s3", "s2"}} {
			w := tlHosted(t, 4, 6)
			w.more()
			added, puts := []string{}, []any{}
			for index := range 10 {
				id := fmt.Sprintf("n%d", index)
				added = append(added, id)
				puts = append(puts, tlSessionRow(id, id, 2))
			}
			w.sessionsWrite(jsonObject("put", puts))
			w.clubWrite(jsonObject("remove", tlMembers("t", emptied...), "add", tlMembers("t", added...)))
			all := []string{}
			if !slices.Contains(emptied, "s3") {
				all = append(all, "s3")
			}
			reversed := slices.Clone(added) // `added.toReversed()`
			slices.Reverse(reversed)
			all = append(all, reversed...)
			all = append(all, "s1")
			rows, moreAfter := w.value()
			shown := sessionIDs(rows)
			tlEqual(t, "a prefix", shown, all[:len(shown)])
			if !moreAfter && len(shown) != len(all) {
				t.Fatalf("moreAfter = false with %d of %d rows", len(shown), len(all))
			}
			for range 6 {
				if _, more := w.value(); !more {
					break
				}
				w.more()
			}
			tlEqual(t, "ids", w.ids(), all)
			w.close()
		}
	})
}

// tlBare is a world of `facet` on a store of its own, without the sessions' fixture: the filler (when the facet serves it),
// and the hold of the next commit. The commit bookkeeping (`install`) is the test's to start, where Pi's test registers its
// `store.onCommitted`: after its setup.
func tlBare(t *testing.T, facet Facet) *tlWorld {
	t.Helper()
	chordStore, holdNextCommit, faults := tlFaultyStore(t)
	host := newHost(t, HostOptions{Store: chordStore, OnError: (&hostErrors{}).push})
	_, err := host.Load([]HostFacet{{Name: "@test/facet", Facet: facet}}, background)
	must(t, err)
	w := &tlWorld{t: t, store: chordStore, host: host, probe: &tlProbe{adopted: func() {}}, hold: holdNextCommit, faults: faults,
		filler: mustGet(t, host, tlFiller, jsonObject())}
	return w
}

// install starts the commit bookkeeping from the store's current seq on (Pi: the lines after the seeding in `hosted`). The
// store's listener runs on a goroutine of its own and one notification covers every commit made since the last, so a
// notification may still be on its way for commits made before install: it carries the baseline seq (or a later one that also
// covers commits made after install), a jump over no commit of the past. Only commits made after install can form a group.
func (w *tlWorld) install() {
	w.commits = newCommitLog(w.store.LastSeq())
	w.store.OnCommitted(w.commits.onCommitted, func(error) {})
	storeseam.WrapAdopt(w.store, func(adopt storeseam.Adopt) storeseam.Adopt {
		wrote := w.probe.shift()
		w.commits.enlist()
		return func(seq int64, ids []int64, rows int) error {
			if wrote != nil {
				wrote()
			}
			err := adopt(seq, ids, rows)
			w.probe.adopted()
			return err
		}
	})
}

// Pi: describe("a row another owner's commit lets in, in the group that merged its pages")
func TestTilingARowAnotherOwnersCommitLetsInInTheGroupThatMergedItsPages(t *testing.T) {
	// Code review round 1 (Sol 7): pages of two; row 35 is hidden by an `exists` in the gates' set. One group: the rows'
	// owner deletes 20 and 40 (the pages merge), the gates' owner lets 35 in (the run for the second page's old interval
	// brings it). The merged page must know 35, or its delete is skipped.
	gateSet := mustColumns("gate.columns", store.ColumnSetOptions{Columns: []store.ColumnProp{store.Col("id", store.Int())}, Key: []string{"id"}})
	gate := mustTableDef("gate", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Int())}, Key: []string{"id"}, Exposes: gateSet})
	data := mustTableDef("data", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Int())}, Key: []string{"id"}})
	item := contracts.Obj(contracts.Prop("id", contracts.Int()))
	ids := func(values ...int) []any {
		out := []any{}
		for _, v := range values {
			out = append(out, float64(v))
		}
		return out
	}
	rowsContract := mustContract(contracts.Local("app.rows", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
		contracts.Mem("rows", contracts.Queries(item, []contracts.Property{contracts.Prop("all", contracts.Obj())})),
		contracts.Mem("write", contracts.NewMethod(contracts.Obj(contracts.Prop("put", contracts.Opt(contracts.Arr(contracts.Int()))), contracts.Prop("drop", contracts.Opt(contracts.Arr(contracts.Int())))))),
	}}))
	gatesContract := mustContract(contracts.Local("app.gates", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
		contracts.Mem("put", contracts.NewMethod(contracts.Arr(contracts.Int()))),
	}}))
	gatedContract := mustContract(contracts.Local("app.gated", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
		contracts.Mem("list", contracts.Obj(contracts.Prop("rows", contracts.Arr(item)), contracts.Prop("moreAfter", contracts.Bool()))),
		contracts.Mem("more", contracts.NewMethod()),
	}}))
	ints := func(args *delta.JsonObject, key string) []int64 {
		out := []int64{}
		if value, ok := args.Get(key); ok && value != nil {
			for _, v := range value.([]any) {
				out = append(out, int64(v.(float64)))
			}
		}
		return out
	}
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return firstError(
			h.Serve(rowsContract, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
				self := plain.(StoreSelf)
				table := self.Table(data)
				rows := self.Queries(table,
					QueriesOptions{Map: func(row store.Row, _ PartnerRow) (delta.JsonValue, error) {
						return jsonObject("id", float64(row["id"].(int64))), nil
					}},
					map[string]QueryBuilder{"all": {Build: func(delta.JsonValue) *store.Query {
						return store.NewQuery(data).Where(func(r, _ store.ColumnRefs) store.Condition {
							return store.Exists(gateSet, store.Match{{Column: "id", Ref: r["id"]}}, nil)
						})
					}}})
				return contracts.MembersImplementation{
					Queries: map[string]map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]{"rows": rows},
					Methods: map[string]contracts.MethodFunction{"write": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						return tlTx(self, ctx, func(tx documents.Tx) error {
							for _, id := range ints(args, "put") {
								if err := tx.Put(table, store.Row{"id": id}); err != nil {
									return err
								}
							}
							for _, id := range ints(args, "drop") {
								if err := tx.Delete(table, store.Row{"id": id}); err != nil {
									return err
								}
							}
							return nil
						})
					})},
				}, nil
			}, ServeOptions{}),
			h.Serve(gatesContract, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
				self := plain.(StoreSelf)
				table := self.Table(gate)
				return contracts.MembersImplementation{Methods: map[string]contracts.MethodFunction{
					"put": func(values delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return tlTx(self, ctx, func(tx documents.Tx) error {
							for _, id := range values.([]any) {
								if err := tx.Put(table, store.Row{"id": int64(id.(float64))}); err != nil {
									return err
								}
							}
							return nil
						})
					},
				}}, nil
			}, ServeOptions{}),
			tlServeFiller(h),
			h.Serve(gatedContract, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
				source := ListSource{Query: self.Get(rowsContract, jsonObject()).Queries("rows")["all"], Args: jsonObject()}
				handle := self.List("rows", []ListSource{source}, ListOptions{First: 2})
				return contracts.MembersImplementation{
					State:   map[string]chord.Watchable[delta.JsonValue]{"list": handle.Value()},
					Methods: map[string]contracts.MethodFunction{"more": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return nil, handle.More(ctx) }},
				}, nil
			}, ServeOptions{}),
		)
	})

	t.Run("is known to the merged page: its delete reaches the list", func(t *testing.T) {
		w := tlBare(t, facet)
		rows, gates := mustGet(t, w.host, rowsContract, jsonObject()), mustGet(t, w.host, gatesContract, jsonObject())
		mustCall(t, rows, "write", jsonObject("put", ids(10, 20, 30, 35, 40, 50, 60)))
		mustCall(t, gates, "put", ids(10, 20, 30, 40, 50, 60))
		w.screen = mustGet(t, w.host, gatedContract, jsonObject())
		w.more()
		listIDs := func() []int { shown, _ := w.value(); return rowIDs(shown) }
		tlEqual(t, "ids", listIDs(), []int{10, 20, 30, 40})
		// Commits queued behind a running one are one group: one notification for several sequence numbers. Pi registers its
		// listener here, after the setup; so does the count (the setup's commits, made one after the other, may be announced
		// as one notification, which is no group of the commit queue's).
		w.install()
		before := w.store.LastSeq()
		w.queued(
			func() { mustCall(t, rows, "write", jsonObject("drop", ids(20, 40))) },
			func() { mustCall(t, gates, "put", ids(35)) },
		)
		if got := w.store.LastSeq(); got != before+3 { // the filler's, and the two
			t.Fatalf("lastSeq = %d, want %d: the three commits", got, before+3)
		}
		if w.commits.grouped() != 1 {
			t.Fatalf("groups = %d, want 1: the two commits formed one group", w.commits.grouped())
		}
		tlEqual(t, "ids", listIDs(), []int{10, 30, 35})
		mustCall(t, rows, "write", jsonObject("drop", ids(35)))
		flushed(w.host)
		got := listIDs()
		tlEqual(t, "first two", got[:2], []int{10, 30})
		if slices.Contains(got, 35) {
			t.Fatalf("ids = %v, want 35 gone", got)
		}
		w.close()
	})
}

// Pi: describe("a publisher's migration that changes the order (code review round 2, Sol)")
func TestTilingAPublishersMigrationThatChangesTheOrder(t *testing.T) {
	// The list joins another plugin's set and orders by its `touched`. Only the publisher reloads: its migration turns the
	// order around. The provider and the list's object keep their code and their attempts.
	touchProps := []store.ColumnProp{store.Col("id", store.Int()), store.Col("touched", store.Int())}
	touches := mustColumns("touch.columns", store.ColumnSetOptions{Columns: touchProps, Key: []string{"id"}})
	touchRows := mustTableDef("touches", store.TableOptions{Version: 1, Columns: touchProps, Key: []string{"id"}, Exposes: touches})
	turned, err := store.EvolveTable(touchRows, func(e *store.TableEditor) store.TableTailEditor {
		return e.Rows(func(row store.Row) (any, error) { return store.Row{"touched": 10 - row["touched"].(int64)}, nil })
	})
	must(t, err)
	data := mustTableDef("data", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Int())}, Key: []string{"id"}})
	item := contracts.Obj(contracts.Prop("id", contracts.Int()))
	rowsContract := mustContract(contracts.Local("app.rows", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
		contracts.Mem("rows", contracts.Queries(item, []contracts.Property{contracts.Prop("all", contracts.Obj())})),
		contracts.Mem("put", contracts.NewMethod(contracts.Int())),
	}}))
	toucherContract := mustContract(contracts.Local("app.toucher", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
		contracts.Mem("put", contracts.NewMethod(contracts.Int())),
	}}))
	listedContract := mustContract(contracts.Local("app.listed", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
		contracts.Mem("list", contracts.Obj(contracts.Prop("rows", contracts.Arr(item)), contracts.Prop("moreAfter", contracts.Bool()))),
		contracts.Mem("more", contracts.NewMethod()),
	}}))
	readers := HostFacet{Name: "@test/readers", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return firstError(
			h.Serve(rowsContract, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
				self := plain.(StoreSelf)
				table := self.Table(data)
				rows := self.Queries(table,
					QueriesOptions{Map: func(row store.Row, _ PartnerRow) (delta.JsonValue, error) {
						return jsonObject("id", float64(row["id"].(int64))), nil
					}},
					map[string]QueryBuilder{"all": {Build: func(delta.JsonValue) *store.Query {
						return store.NewQuery(data).
							Join(touches, func(r store.ColumnRefs) store.Match { return store.Match{{Column: "id", Ref: r["id"]}} }).
							OrderBy(func(_, touch store.ColumnRefs) *store.ColumnRef { return touch["touched"] }, "desc")
					}}})
				return contracts.MembersImplementation{
					Queries: map[string]map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]{"rows": rows},
					Methods: map[string]contracts.MethodFunction{"put": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return tlTx(self, ctx, func(tx documents.Tx) error { return tx.Put(table, store.Row{"id": int64(id.(float64))}) })
					}},
				}, nil
			}, ServeOptions{}),
			h.Serve(listedContract, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
				source := ListSource{Query: self.Get(rowsContract, jsonObject()).Queries("rows")["all"], Args: jsonObject()}
				handle := self.List("rows", []ListSource{source}, ListOptions{First: 2})
				return contracts.MembersImplementation{
					State:   map[string]chord.Watchable[delta.JsonValue]{"list": handle.Value()},
					Methods: map[string]contracts.MethodFunction{"more": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return nil, handle.More(ctx) }},
				}, nil
			}, ServeOptions{}),
		)
	})}
	publisher := func(table *store.TableDefinition) HostFacet {
		return HostFacet{Name: "@test/publisher", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(toucherContract, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
				self := plain.(StoreSelf)
				rows := self.Table(table)
				return contracts.MembersImplementation{Methods: map[string]contracts.MethodFunction{
					"put": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return tlTx(self, ctx, func(tx documents.Tx) error {
							return tx.Put(rows, store.Row{"id": int64(id.(float64)), "touched": int64(id.(float64))})
						})
					},
				}}, nil
			}, ServeOptions{})
		})}
	}

	t.Run("switches the list in one frame: every row once, in the new order, no page reloaded alone", func(t *testing.T) {
		// Each page was loaded again on its own: the first adoption cut and merged the others with the rows they had and
		// closed their loads, and the list ended as [1 2 3 4 5 6 1] for good.
		chordStore := memoryStore(t)
		host := newHost(t, HostOptions{Store: chordStore, OnError: (&hostErrors{}).push})
		_, err := host.Load([]HostFacet{readers, publisher(touchRows)}, background)
		must(t, err)
		rows, toucher := mustGet(t, host, rowsContract, jsonObject()), mustGet(t, host, toucherContract, jsonObject())
		for id := 1; id <= 6; id++ {
			mustCall(t, rows, "put", float64(id))
			mustCall(t, toucher, "put", float64(id))
		}
		screen := mustGet(t, host, listedContract, jsonObject())
		mustCall(t, screen, "more", nil)
		mustCall(t, screen, "more", nil)
		shown, _ := mustListValue(t, screen)
		tlEqual(t, "ids", rowIDs(shown), []int{6, 5, 4, 3, 2, 1})
		watch, err := screen.Member("list").Watch(background)
		must(t, err)
		frames := nexttest.ChangesOf(watch)
		_, err = host.Reload([]HostFacet{readers, publisher(turned)}, background, ReloadOptions{})
		must(t, err)
		// The reload returns before the publisher's successor migrates its table (it starts once the old attempt's work
		// ended, a task of the host's loop): deliveredOn joins it, then every read the switch started.
		lsSettled(host)
		tlEqual(t, "frames", callsIDs(frames.Items()), []any{[]int{1, 2, 3, 4, 5, 6}})
		if _, moreAfter := mustListValue(t, screen); moreAfter {
			t.Fatal("moreAfter = true, want false")
		}
		// The pages know their rows under the new plan: a write of the publisher still reaches each.
		mustCall(t, toucher, "put", 3.0)
		lsSettled(host)
		shown, _ = mustListValue(t, screen)
		tlEqual(t, "ids", rowIDs(shown), []int{1, 2, 4, 5, 6, 3})
		_, _ = watch.Stop()
		closeHosts(t, host)
	})
}

// Pi: describe("switches and failures")
func TestTilingSwitchesAndFailures(t *testing.T) {
	t.Run("a switch waits for every page of the new arguments: no page that has not run is shown", func(t *testing.T) {
		// Review round 1 (Sol 5): three pages, the joined table held (its owner migrates it), then other arguments.
		w := tlHosted(t, 2, 6)
		w.clubWrite(jsonObject("add", tlMembers("u", "s1", "s2")))
		w.more()
		w.more()
		before := w.ids()
		if len(before) != 6 {
			t.Fatalf("rows = %d, want 6", len(before))
		}
		w.store.Reads.Hold("app.sessions:session-rows")
		w.show("u")
		lsSettled(w.host)
		// The pages of team "u" have not run: the list still shows what it had.
		tlEqual(t, "ids", w.ids(), before)
		w.store.Reads.Ready("app.sessions:session-rows")
		lsSettled(w.host)
		tlEqual(t, "ids", w.ids(), []string{"s2", "s1"})
		w.close()
	})

	t.Run("a more called while a switch is pending waits for it, and loads behind the new arguments' pages", func(t *testing.T) {
		// Code review round 3 (Sol): the new arguments' pages were held, so their start had settled without a run; `more`
		// then loaded behind the old arguments' pages and the page asked for was lost with them.
		w := tlHosted(t, 2, 6)
		w.clubWrite(jsonObject("add", tlMembers("u", "s1", "s2", "s3")))
		w.store.Reads.Hold("app.sessions:session-rows")
		w.show("u")
		lsSettled(w.host)
		settled := make(chan struct{})
		go func() { w.more(); close(settled) }()
		deliveredOn(w.host)
		select {
		case <-settled:
			t.Fatal("more settled while the switch was pending")
		default:
		}
		tlEqual(t, "ids", w.ids(), []string{"s6", "s5"})
		w.store.Reads.Ready("app.sessions:session-rows")
		<-settled
		lsSettled(w.host)
		// The new arguments' rows lie behind the bookmark the list had (s5's position): the switch alone shows none of
		// them, and the page the call asked for is the first that does.
		rows, moreAfter := w.value()
		tlEqual(t, "list", []any{sessionIDs(rows), moreAfter}, []any{[]string{"s3", "s2"}, true})
		w.close()
	})

	t.Run("a failed page fails the list until that page runs well: another page's run does not heal it", func(t *testing.T) {
		// Review round 1 (Sol 6): three pages; one write renames a session of each, and the first page's run fails.
		w := tlHosted(t, 2, 6)
		w.more()
		w.more()
		first := w.store.Reads.On("app.club:team-rows")[0]
		w.faults.failRuns(first, func() error { return errors.New("page failure") })
		rename := func(title string) { w.sessionsWrite(jsonObject("rename", tlRenames(title, "s6", "s4", "s2"))) }
		rename("x")
		w.throws("page failure")
		// A run of the other pages alone changes nothing about that.
		w.sessionsWrite(jsonObject("rename", tlRenames("y", "s1")))
		w.throws("page failure")
		w.faults.restore()
		rename("z")
		tlEqual(t, "shown", w.shown(), "s6:z s5:old s5 s4:z s3:old s3 s2:z s1:y")
		w.close()
	})

	t.Run("a failed page a retired page's later run is patched into stays without a result: the next commit runs it", func(t *testing.T) {
		// Code review round 2 (Astra): pages [s6 s5 s4 s3] and [s2 s1]. One group: C1 removes four sessions from the team
		// (the pages merge; the second page's run fails), C2 renames s1 (the second page's run, for its old interval,
		// succeeds). The merged page is failed, and must not skip the next write as if it knew its rows.
		w := tlHosted(t, 4, 6)
		w.more()
		tail := w.store.Reads.On("app.club:team-rows")[1]
		w.faults.failRuns(tail, func() error {
			w.faults.restore() // fails once: the next run is the real one
			return errors.New("page failure")
		})
		w.group(
			func() { w.clubWrite(jsonObject("remove", tlMembers("t", "s6", "s5", "s4", "s2"))) },
			func() { w.sessionsWrite(jsonObject("rename", tlRenames("renamed", "s1"))) },
		)
		w.throws("page failure")
		// A write that changes no row of the list: a page that knew its rows would ask one probe and skip.
		w.sessionsWrite(jsonObject("touch", []any{jsonObject("id", "s999", "touched", 999.0)}))
		tlEqual(t, "shown", w.shown(), "s3:old s3 s1:renamed")
		w.close()
	})

	t.Run("arguments the builder refuses fail the list; a write to the previous arguments' rows does not publish them", func(t *testing.T) {
		// Review round 1 (Sol 4, for lists).
		w := tlHosted(t, 2, 4)
		w.show("broken")
		lsSettled(w.host)
		w.throws("no such team")
		w.sessionsWrite(jsonObject("rename", tlRenames("edited", "s4")))
		w.throws("no such team")
		w.show("t")
		lsSettled(w.host)
		tlEqual(t, "shown", w.shown(), "s4:edited s3:old s3")
		w.close()
	})
}

// tlRow is Pi's `Row`.
type tlRow struct {
	id, title string
	touched   int
}

// tlTruth is the test's model: `rows` (a Map: insertion order matters for `pick`) and `team` (a Set).
type tlTruth struct {
	order []string
	rows  map[string]tlRow
	team  map[string]bool
}

func (m *tlTruth) set(row tlRow) {
	if _, ok := m.rows[row.id]; !ok {
		m.order = append(m.order, row.id)
	}
	m.rows[row.id] = row
}

func (m *tlTruth) delete(id string) {
	if _, ok := m.rows[id]; ok {
		delete(m.rows, id)
		m.order = slices.DeleteFunc(m.order, func(other string) bool { return other == id })
	}
}

// truth is the true order: the team's sessions that exist, newest touch first, ties by id (the key's order).
func (m *tlTruth) truth() []tlRow {
	out := []tlRow{}
	for id := range m.team {
		if row, ok := m.rows[id]; ok {
			out = append(out, row)
		}
	}
	slices.SortFunc(out, func(a, b tlRow) int {
		if a.touched != b.touched {
			return b.touched - a.touched
		}
		return strings.Compare(b.id, a.id)
	})
	return out
}

func (r tlRow) json() string {
	return fmt.Sprintf(`{"id":%q,"title":%q,"touched":%d}`, r.id, r.title, r.touched)
}

// Pi: describe("the invariant, with two owners and commit groups")
func TestTilingTheInvariantWithTwoOwnersAndCommitGroups(t *testing.T) {
	for seed := uint32(1); seed <= 16; seed++ {
		t.Run(fmt.Sprintf("after every adoption the value is a gapless prefix of the true order, row by row (seed %d)", seed), func(t *testing.T) {
			first := 2 + int(seed%2)
			w := tlHosted(t, first, 9)
			r := newRandom(seed)
			m := &tlTruth{rows: map[string]tlRow{}, team: map[string]bool{}}
			for index := 1; index <= 9; index++ {
				id := fmt.Sprintf("s%d", index)
				m.set(tlRow{id, "old " + id, index})
				m.team[id] = true
			}
			clock, made := 100, 9
			var stepName atomic.Value
			stepName.Store("start")
			var checks atomic.Int32
			// check compares the value with the truth as of the commit just adopted. It runs on the store's goroutine inside
			// the commit (Pi: inside `adopt`), so it reports with Errorf and returns.
			check := func() {
				rows, moreAfter, err := listMemberValue(w.screen)
				if err != nil {
					t.Errorf("%s: list.value: %v", stepName.Load(), err)
					return
				}
				expected := m.truth()
				checks.Add(1)
				if len(rows) > len(expected) {
					t.Errorf("%s: a prefix: %d rows shown of %d", stepName.Load(), len(rows), len(expected))
					return
				}
				for index, row := range rows {
					if got := nexttest.JSONText(t, row); got != expected[index].json() {
						t.Errorf("%s: a prefix: row %d = %s, want %s", stepName.Load(), index, got, expected[index].json())
						return
					}
				}
				if !moreAfter && len(rows) != len(expected) {
					t.Errorf("%s: the whole order: %d rows, want %d", stepName.Load(), len(rows), len(expected))
				}
			}
			// After every commit's adoption: the frame it published is checked against the truth as of that commit.
			w.probe.adopted = check
			// A session to write: mostly one the list shows now (a stale page shows there), else any that exists.
			pick := func() string {
				rows, _ := w.value()
				shownIDs := sessionIDs(rows)
				from := m.order
				if len(shownIDs) > 0 && r.chance(0.8) {
					from = shownIDs
				}
				if len(from) == 0 {
					return "s1"
				}
				return from[r.int(len(from))]
			}
			newTouched := func() int {
				if r.chance(0.7) {
					clock++
					return clock
				}
				return r.int(50)
			}
			// One owner's commit; what it wrote reaches the truth when its commit is adopted. It returns the call to make.
			sessionsWrite := func() func() {
				kind := r.int(8)
				id := pick()
				switch {
				case kind < 2:
					touched := newTouched()
					w.probe.push(func() {
						if row, ok := m.rows[id]; ok {
							row.touched = touched
							m.set(row)
						}
					})
					return func() { w.sessionsWrite(jsonObject("touch", []any{jsonObject("id", id, "touched", float64(touched))})) }
				case kind < 5:
					clock++
					title := fmt.Sprintf("t%d", clock)
					w.probe.push(func() {
						if row, ok := m.rows[id]; ok {
							row.title = title
							m.set(row)
						}
					})
					return func() { w.sessionsWrite(jsonObject("rename", tlRenames(title, id))) }
				case kind < 7:
					w.probe.push(func() { m.delete(id) })
					return func() { w.sessionsWrite(jsonObject("drop", tlStringsAny(id))) }
				}
				fresh := []tlRow{}
				for range 1 + r.int(4*first) {
					made++
					at := 0
					if r.chance(0.5) {
						clock++
						at = clock
					} else {
						at = r.int(50)
					}
					fresh = append(fresh, tlRow{fmt.Sprintf("s%d", made), fmt.Sprintf("new %d", made), at})
				}
				w.probe.push(func() {
					for _, row := range fresh {
						m.set(row)
					}
				})
				puts := []any{}
				for _, row := range fresh {
					puts = append(puts, tlSessionRow(row.id, row.title, row.touched))
				}
				return func() { w.sessionsWrite(jsonObject("put", puts)) }
			}
			clubWrite := func() func() {
				// Sessions leave the team (mostly shown ones: pages empty and merge) and others join, also many at once (a run
				// cut by its limit) and ones that do not exist yet; often both in one commit.
				leaving := []string{}
				if r.chance(0.6) {
					for range 1 + r.int(3) {
						leaving = append(leaving, pick())
					}
				}
				joining := []string{}
				if len(leaving) == 0 || r.chance(0.5) {
					for range 1 + r.int(4*first) {
						joining = append(joining, fmt.Sprintf("s%d", 1+r.int(made+2)))
					}
				}
				w.probe.push(func() {
					for _, session := range leaving {
						delete(m.team, session)
					}
					for _, session := range joining {
						m.team[session] = true
					}
				})
				// The transaction removes, then adds: a session in both stays in the team.
				return func() {
					w.clubWrite(jsonObject("remove", tlMembers("t", leaving...), "add", tlMembers("t", joining...)))
				}
			}
			for index := range 400 {
				stepName.Store(fmt.Sprintf("seed %d, step %d", seed, index))
				switch kind := r.int(10); {
				case kind < 3:
					// A real group: the filler's commit runs alone (held), the two owners' commits behind it in one transaction.
					w.probe.push(func() {})
					fill := func() { mustCall(t, w.filler, "write", float64(index)) }
					var a, b func()
					if r.chance(0.5) {
						a = sessionsWrite()
						b = clubWrite()
					} else {
						a = clubWrite()
						b = sessionsWrite()
					}
					held := w.hold()
					waitFill := w.startInOrder(fill)
					<-held.Entered()
					waitRest := w.startInOrder(a, b)
					held.Release()
					waitFill()
					waitRest()
				case kind < 6:
					w.startInOrder(sessionsWrite())()
				case kind < 9:
					w.startInOrder(clubWrite())()
				default:
					w.more()
				}
				flushed(w.host)
				w.commits.waitReported(w.store.LastSeq())
				check()
			}
			if g := w.commits.grouped(); g <= 50 {
				t.Fatalf("groups = %d, want > 50", g)
			}
			if n := checks.Load(); n <= 600 {
				t.Fatalf("checks = %d, want > 600", n)
			}
			for range 200 {
				if _, moreAfter := w.value(); !moreAfter {
					break
				}
				w.more()
			}
			rows, _ := w.value()
			expected := m.truth()
			if len(rows) != len(expected) {
				t.Fatalf("rows = %d, want %d", len(rows), len(expected))
			}
			for index, row := range rows {
				if got := nexttest.JSONText(t, row); got != expected[index].json() {
					t.Fatalf("row %d = %s, want %s", index, got, expected[index].json())
				}
			}
			w.close()
		})
	}
}
