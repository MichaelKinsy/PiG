package host

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/test/store/live-query.test.ts (Pi main 2439933cf, 8889e623d): live queries over an object's own
// tables (decision "Live queries over an object's own tables"): `self.query(table, q, limit)` runs before its object goes
// live, again in the commit step of every commit of the object that writes its table, and publishes the diff in that
// commit's batch; a commit writing another table runs none; a reload restarts the object; the limit is required; a failing
// run is a failed member; an ended attempt's query is collected. The last case (a refresh applied again) is in
// chord/next/store/live_query_test.go, where QueryRefresh lives.
//
// Forced differences: a `limit` is an int64, so Pi's fractional and absent limits cannot be written (designed out; 0 and a
// negative one are the Go cases of "a limit is required").

// reloadedKindOfABoard is the kind a reload reports for the board (Pi main's restart model, a5d209afd).
const reloadedKindOfABoard = ReloadRestarted

var (
	lqItems = mustTableDef("items", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{
			store.Col("id", store.Int()), store.Col("text", store.Str()),
			store.Col("meta", store.Opt(store.Json(contracts.Obj(), store.JsonColumnOptions{Version: 1}))),
		},
		Key: []string{"id"},
	})
	lqOther = mustTableDef("other", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Int())}, Key: []string{"id"}})
	// lqNewest is the newest items first.
	lqNewest = store.NewQuery(lqItems).OrderBy(func(item store.ColumnRefs) *store.ColumnRef { return item["id"] }, "desc")

	lqItem  = contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("text", contracts.Str()), contracts.Prop("meta", contracts.Opt(contracts.Obj())))
	lqBoard = mustContract(contracts.Local("app.board", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("items", contracts.Arr(lqItem)),
			contracts.Mem("put", contracts.NewMethod(lqItem)),
			contracts.Mem("rename", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("text", contracts.Str())))),
			contracts.Mem("drop", contracts.NewMethod(contracts.Int())),
			contracts.Mem("other", contracts.NewMethod(contracts.Int())),
		},
	}))
)

func mustTableDef(name string, options store.TableOptions) *store.TableDefinition {
	definition, err := store.DefineTable(name, options)
	if err != nil {
		panic(err)
	}
	return definition
}

// lqQueries are the live queries collected weakly as they are declared (Pi's `instancesOf(LiveQuery, "load")`).
type lqQueries struct {
	refs []weak.Pointer[store.LiveQuery]
	// live are the queries themselves, for a test that waits for one to detach (the memory test holds weak ones only).
	live []*store.LiveQuery
}

// serveBoard serves `Board` with a live query of the newest `limit` items; declare runs first in its factory.
func (q *lqQueries) serveBoard(h FacetHost, limit int64, declare func(self StoreSelf)) error {
	return h.Serve(lqBoard, func(_ *delta.JsonObject, plain Self) (contracts.Implementation, error) {
		self := plain.(StoreSelf)
		if declare != nil {
			declare(self)
		}
		items := self.Table(lqItems)
		other := self.Table(lqOther)
		query := self.Query(items, lqNewest, limit)
		// The recording that stages a reload declares inert queries: only an attempt's own is collected.
		if live, ok := unwrapMember(query).(*store.LiveQuery); ok && q != nil {
			q.refs = append(q.refs, weak.Make(live))
			q.live = append(q.live, live)
		}
		tx := func(ctx context.Context, write func(tx documents.Tx) error) (delta.JsonValue, error) {
			_, err := self.Tx(func(tx documents.Tx) (any, error) { return nil, write(tx) }, ctx)
			return nil, err
		}
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"items": query},
			Methods: map[string]contracts.MethodFunction{
				"put": exMethod(func(row *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					stored := store.Row{"id": int64(row.Value("id").(float64)), "text": row.Value("text")}
					if meta, ok := row.Get("meta"); ok {
						stored["meta"] = meta
					}
					return tx(ctx, func(tx documents.Tx) error { return tx.Put(items, stored) })
				}),
				"rename": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					id := int64(args.Value("id").(float64))
					return tx(ctx, func(tx documents.Tx) error {
						return tx.UpdateWhere(items, store.Eq(store.NewColumnRef("id"), id), store.Row{"text": args.Value("text")})
					})
				}),
				"drop": func(arg delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return tx(ctx, func(tx documents.Tx) error {
						return tx.DeleteWhere(items, store.Eq(store.NewColumnRef("id"), int64(arg.(float64))))
					})
				},
				"other": func(arg delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return tx(ctx, func(tx documents.Tx) error { return tx.Put(other, store.Row{"id": int64(arg.(float64))}) })
				},
			},
		}}, nil
	}, ServeOptions{})
}

// board is a host serving `Board` on chordStore, metered, and a reference to the board.
type lqBoardHost struct {
	host   Host
	errors *hostErrors
	board  *Reference
	ctx    context.Context
	counts func() map[string]float64
}

func newBoard(t *testing.T, chordStore *store.ChordStore, limit int64, queries *lqQueries) *lqBoardHost {
	t.Helper()
	telemetry, ctx, counts := meteredHost(t)
	// The board lives as long as its host: it activates during `load`, under the load's context.
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error { return queries.serveBoard(h, limit, nil) },
		loadedOptions{Store: chordStore, Telemetry: telemetry, Context: ctx})
	return &lqBoardHost{host: host, errors: errs, board: mustGet(t, host, lqBoard, jsonObject()), ctx: ctx, counts: counts}
}

func (b *lqBoardHost) items(t *testing.T) []any {
	t.Helper()
	value, err := b.board.Member("items").Value()
	must(t, err)
	return value.([]any)
}

func (b *lqBoardHost) call(t *testing.T, method string, args delta.JsonValue) {
	t.Helper()
	if _, err := b.board.Call(method, args, b.ctx); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

func (b *lqBoardHost) put(t *testing.T, id int, text string, extra ...any) {
	t.Helper()
	b.call(t, "put", jsonObject(append([]any{"id", float64(id), "text", text}, extra...)...))
}

// ids are the ids of rows.
func idsOf(rows []any) []int {
	ids := []int{}
	for _, row := range rows {
		ids = append(ids, int(row.(*delta.JsonObject).Value("id").(float64)))
	}
	return ids
}

func equalInts(t *testing.T, what string, got []int, want ...int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

func lqJSON(t *testing.T, value any) string {
	t.Helper()
	out, err := json.Marshal(value)
	must(t, err)
	return string(out)
}

func TestLiveQueryActivationRunsTheQueryBeforeTheObjectGoesLiveOverTheRowsAlreadyStored(t *testing.T) {
	chordStore := memoryStore(t)
	first := newBoard(t, chordStore, 3, nil)
	for _, id := range []int{1, 2} {
		first.put(t, id, "t"+string(rune('0'+id)))
	}
	closeHosts(t, first.host)
	again := newBoard(t, chordStore, 3, nil)
	if got := lqJSON(t, again.items(t)); got != `[{"id":2,"text":"t2"},{"id":1,"text":"t1"}]` {
		t.Fatalf("items = %s", got)
	}
	if runs := again.counts()["chord.query.runs"]; runs != 1 {
		t.Fatalf("chord.query.runs = %v, want 1", runs)
	}
	closeHosts(t, again.host)
}

func TestLiveQueryActivationARunThatFailsAtActivationFailsTheActivationBroken(t *testing.T) {
	chordStore := memoryStore(t)
	first := newBoard(t, chordStore, 3, nil)
	first.put(t, 1, "t1", "meta", jsonObject())
	closeHosts(t, first.host)
	if _, err := store.Read(chordStore, func(executor sqlite.SqliteExecutor) (struct{}, error) {
		return struct{}{}, executor.Run(`UPDATE "app.board:items" SET "meta$v" = 9`)
	}, background); err != nil {
		t.Fatal(err)
	}
	errs := &hostErrors{}
	host := newHost(t, HostOptions{Store: chordStore, OnError: errs.push})
	_, err := host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return (&lqQueries{}).serveBoard(h, 3, nil)
	})}}, background)
	must(t, err)
	_, err = host.Get(background, lqBoard, jsonObject(), GetOptions{})
	if err == nil || !strings.Contains(err.Error(), "stored at version 9") {
		t.Fatalf("Get = %v, want the stored-version refusal", err)
	}
	if got := failures(errs.All()); len(got) != 1 || got[0] != "app.board: broken" {
		t.Fatalf("failures = %v", got)
	}
	closeHosts(t, host)
}

func TestLiveQueryPublicationAnInsertAnUpdateAndADeleteReachAWatchAsOpsInTheCommitsBatch(t *testing.T) {
	b := newBoard(t, memoryStore(t), 3, nil)
	watch, frames := exWatch(t, background, b.board.Member("items"))
	b.put(t, 1, "a")
	// Published in the commit's batch: the value is current once the call returned.
	equalInts(t, "ids", idsOf(b.items(t)), 1)
	// Each call's frame is delivered before the next call: a JavaScript caller's `await` lets the delivery run, while a Go
	// watcher is fed by its own goroutine, which folds the frames of calls made faster than it takes them.
	turn(b.host)
	b.put(t, 2, "b")
	turn(b.host)
	b.call(t, "rename", jsonObject("id", float64(1), "text", "A"))
	turn(b.host)
	b.call(t, "drop", float64(2))
	turn(b.host)
	// Splices of the rows at the root (`["p", path, at, deleted, inserted]`) and a set inside one row.
	var ops []string
	for _, frame := range frames.all() {
		ops = append(ops, lqJSON(t, frame.Ops))
	}
	want := []string{
		`[["p",[],0,0,[{"id":1,"text":"a"}]]]`,
		`[["p",[],0,0,[{"id":2,"text":"b"}]]]`,
		`[["s",[1,"text"],"A"]]`,
		`[["p",[],0,1,[]]]`,
	}
	if strings.Join(ops, "\n") != strings.Join(want, "\n") {
		t.Fatalf("frames = %v, want %v", ops, want)
	}
	if got := lqJSON(t, b.items(t)); got != `[{"id":1,"text":"A"}]` {
		t.Fatalf("items = %s", got)
	}
	_, _ = watch.Stop()
	closeHosts(t, b.host)
}

func TestLiveQueryPublicationKeepsTheWindowAtItsLimitANewerRowPushesTheOldestOut(t *testing.T) {
	b := newBoard(t, memoryStore(t), 2, nil)
	for _, id := range []int{1, 2} {
		b.put(t, id, "t"+string(rune('0'+id)))
	}
	watch, frames := exWatch(t, background, b.board.Member("items"))
	b.put(t, 3, "t3")
	turn(b.host)
	equalInts(t, "ids", idsOf(b.items(t)), 3, 2)
	var ops []string
	for _, frame := range frames.all() {
		ops = append(ops, lqJSON(t, frame.Ops))
	}
	if len(ops) != 1 || ops[0] != `[["p",[],0,0,[{"id":3,"text":"t3"}]],["p",[],2,1,[]]]` {
		t.Fatalf("frames = %v", ops)
	}
	_, _ = watch.Stop()
	closeHosts(t, b.host)
}

func TestLiveQueryPublicationKeepsAnUnchangedRowTheSameObjectAcrossRunsAChangedOneNew(t *testing.T) {
	b := newBoard(t, memoryStore(t), 3, nil)
	for _, id := range []int{1, 2} {
		b.put(t, id, "t"+string(rune('0'+id)), "meta", jsonObject())
	}
	rows := b.items(t)
	two, one := rows[0], rows[1]
	b.call(t, "rename", jsonObject("id", float64(2), "text", "T2"))
	after := b.items(t)
	twoAfter, oneAfter := after[0], after[1]
	if oneAfter != one || twoAfter == two {
		t.Fatalf("same objects: one %v, two %v; want true, false", oneAfter == one, twoAfter == two)
	}
	if got := lqJSON(t, twoAfter); got != `{"id":2,"text":"T2","meta":{}}` {
		t.Fatalf("row = %s", got)
	}
	closeHosts(t, b.host)
}

func TestLiveQueryPublicationACommitWritingAnotherTableRunsNoQueryAndPublishesNothing(t *testing.T) {
	b := newBoard(t, memoryStore(t), 3, nil)
	watch, frames := exWatch(t, background, b.board.Member("items"))
	statements := func() float64 { return b.counts()["chord.store.statements"] }
	runs := func() float64 { return b.counts()["chord.query.runs"] }
	beforeStatements, beforeRuns := statements(), runs()
	b.call(t, "other", float64(1))
	otherStatements, otherRuns := statements()-beforeStatements, runs()-beforeRuns
	beforeStatements, beforeRuns = statements(), runs()
	b.put(t, 1, "a")
	touchingStatements, touchingRuns := statements()-beforeStatements, runs()-beforeRuns
	turn(b.host)
	// The same one-row put: the touching commit costs one query, one statement more.
	if otherRuns != 0 || touchingRuns != 1 || touchingStatements != otherStatements+1 {
		t.Fatalf("other: %v statements %v runs; touching: %v statements %v runs", otherStatements, otherRuns, touchingStatements, touchingRuns)
	}
	if got := len(frames.all()); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
	_, _ = watch.Stop()
	closeHosts(t, b.host)
}

func TestLiveQueryReloadTheNewAttemptsQueryRunsAtItsActivationAndTakesOver(t *testing.T) {
	queries := &lqQueries{}
	b := newBoard(t, memoryStore(t), 3, queries)
	for _, id := range []int{1, 2, 3} {
		b.put(t, id, "t"+string(rune('0'+id)))
	}
	report := reloadMust(t)(reloadApp(b.host, func(h FacetHost) error { return (&lqQueries{}).serveBoard(h, 2, nil) }))
	if len(report.Objects) != 1 || report.Objects[0].Object != "app.board" || report.Objects[0].Kind != reloadedKindOfABoard {
		t.Fatalf("objects = %+v, failed = %+v, plugins = %+v", report.Objects, report.Failed, report.Plugins)
	}
	turn(b.host)
	// The restart's old attempt ends first and the successor, a new instance, runs its factory then: `await flushed()`
	// is the wait for the new attempt's query to take over.
	eventually(t, "the new attempt's query to take over", func() bool {
		value, err := b.board.Member("items").Value()
		return err == nil && reflect.DeepEqual(idsOf(value.([]any)), []int{3, 2})
	})
	equalInts(t, "ids", idsOf(b.items(t)), 3, 2)
	runs := b.counts()["chord.query.runs"]
	b.put(t, 4, "t4")
	equalInts(t, "ids", idsOf(b.items(t)), 4, 3)
	// Only the new attempt's query runs: the old one left with its attempt.
	if got := b.counts()["chord.query.runs"] - runs; got != 1 {
		t.Fatalf("runs after the reload = %v, want 1", got)
	}
	closeHosts(t, b.host)
}

func TestLiveQueryDeclarationRefusals(t *testing.T) {
	cases := []struct {
		what    string
		declare func(self StoreSelf)
		message string
	}{
		{"a limit of 0", func(self StoreSelf) { self.Query(self.Table(lqItems), lqNewest, 0) }, "a limit is required"},
		{"a negative limit", func(self StoreSelf) { self.Query(self.Table(lqItems), lqNewest, -1) }, "a limit is required"},
		{"a query of another table", func(self StoreSelf) { self.Query(self.Table(lqOther), lqNewest, 3) }, `a query of table "items" over "other"`},
		{"a table this factory did not declare", func(self StoreSelf) { self.Query(&store.TableHandle{}, lqNewest, 3) }, "the table is not one this factory declared"},
	}
	for _, each := range cases {
		t.Run("refuses "+each.what, func(t *testing.T) {
			chordStore := memoryStore(t)
			host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
				return h.Serve(lqBoard, func(_ *delta.JsonObject, plain Self) (contracts.Implementation, error) {
					each.declare(plain.(StoreSelf))
					panic("unreachable")
				}, ServeOptions{})
			}, loadedOptions{Store: chordStore})
			_, err := host.Get(background, lqBoard, jsonObject(), GetOptions{})
			if err == nil || !strings.Contains(err.Error(), each.message) {
				t.Fatalf("Get = %v, want %q", err, each.message)
			}
			closeHosts(t, host)
		})
	}
}

func TestLiveQueryDeclarationStagingRefusesAQueryWithoutAValidLimitThePluginKeepsItsOldCode(t *testing.T) {
	b := newBoard(t, memoryStore(t), 3, nil)
	report := reloadMust(t)(reloadApp(b.host, func(h FacetHost) error { return (&lqQueries{}).serveBoard(h, 0, nil) }))
	if len(report.Failed) != 1 || !strings.Contains(report.Failed[0].Error.Error(), "a limit is required") {
		t.Fatalf("failed = %+v", report.Failed)
	}
	if state := b.board.State().Value(); state.Kind != RefStateLive {
		t.Fatalf("state = %v, want live", state.Kind)
	}
	closeHosts(t, b.host)
}

func TestLiveQueryFailureAFailingRunIsAFailedMemberReportedOnceTheNextGoodRunHealsIt(t *testing.T) {
	chordStore := memoryStore(t)
	b := newBoard(t, chordStore, 3, nil)
	b.put(t, 1, "a", "meta", jsonObject())
	watch, frames := exWatch(t, background, b.board.Member("items"))
	if _, err := store.Read(chordStore, func(executor sqlite.SqliteExecutor) (struct{}, error) {
		return struct{}{}, executor.Run(`UPDATE "app.board:items" SET "meta$v" = 9`)
	}, background); err != nil {
		t.Fatal(err)
	}
	b.put(t, 2, "b")
	b.put(t, 3, "c")
	if _, err := b.board.Member("items").Value(); err == nil || !strings.Contains(err.Error(), "stored at version 9") {
		t.Fatalf("Value = %v, want the stored-version failure", err)
	}
	b.call(t, "drop", float64(1))
	turn(b.host)
	var kinds []string
	for _, frame := range frames.all() {
		kinds = append(kinds, frame.Kind)
	}
	if strings.Join(kinds, ",") != "failed,value" {
		t.Fatalf("calls = %v, want failed,value", kinds)
	}
	if got := lqJSON(t, frames.all()[1].Ops); got != `[["r",[{"id":3,"text":"c"},{"id":2,"text":"b"}]]]` {
		t.Fatalf("healing ops = %s", got)
	}
	var reported []string
	for _, err := range b.errors.All() {
		reported = append(reported, string(err.Kind)+":"+err.Table)
	}
	if strings.Join(reported, ",") != "query_failed:items" {
		t.Fatalf("errors = %v", reported)
	}
	// The object stays live: only the member failed.
	if state := b.board.State().Value(); state.Kind != RefStateLive {
		t.Fatalf("state = %v, want live", state.Kind)
	}
	_, _ = watch.Stop()
	closeHosts(t, b.host)
}

func TestLiveQueryMemoryCollectsARestartedAttemptsQuery(t *testing.T) {
	queries := &lqQueries{}
	telemetry, ctx, _ := meteredHost(t)
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return queries.serveBoard(h, 3, nil) },
		loadedOptions{Store: memoryStore(t), Telemetry: telemetry, Context: ctx})
	mustGet(t, host, lqBoard, jsonObject())
	from := len(queries.refs) - 1
	reloadMust(t)(reloadApp(host, func(h FacetHost) error { return (&lqQueries{}).serveBoard(h, 2, nil) }))
	turn(host)
	collectedWithin(t, queries.refs[from])
	closeHosts(t, host)
}
